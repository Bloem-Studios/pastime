// Package notify delivers release notices to the destinations people configure:
// generic webhooks, ntfy, Discord, Gotify and Apprise. Destination URLs and tokens are
// stored encrypted; delivery refuses private network addresses unless the instance
// allows them.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/netguard"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job is the durable job kind that sends one day's release notices.
const Job = "release_notices"

type Store struct {
	Pool         *pgxpool.Pool
	Box          *sealed.Box
	AllowPrivate bool
	Client       *http.Client // nil builds a guarded client
}

// Config is the decrypted destination. Which fields apply depends on the kind.
type Config struct {
	URL    string `json:"url,omitempty"`    // webhook, discord, apprise; ntfy and gotify server
	Topic  string `json:"topic,omitempty"`  // ntfy
	Token  string `json:"token,omitempty"`  // ntfy (optional), gotify
	Secret string `json:"secret,omitempty"` // webhook signing secret (optional)
}

type Target struct {
	ID          int64      `json:"id,string"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
	LastStatus  string     `json:"last_status"`
	LastSentAt  *time.Time `json:"last_sent_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type TargetInput struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
	Config  Config `json:"config"`
}

type Settings struct {
	Releases   bool     `json:"releases"`
	DaysAhead  int      `json:"days_ahead"`
	MediaTypes []string `json:"media_types"`
	Statuses   []string `json:"statuses"`
	Digest     bool     `json:"digest"`
	Version    int      `json:"version"`
}

var kinds = map[string]bool{"webhook": true, "ntfy": true, "discord": true, "gotify": true, "apprise": true}

func purpose(userID int64) string { return "notify-target:" + strconv.FormatInt(userID, 10) }

// validate checks the destination and returns a redacted description safe to show.
func (in *TargetInput) validate() (string, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !kinds[in.Kind] || len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 80 {
		return "", fault.Invalid("Choose a destination type and a name of 1 to 80 characters.")
	}
	c := &in.Config
	if in.Kind == "ntfy" && c.URL == "" {
		c.URL = "https://ntfy.sh"
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(c.URL) > 1000 {
		return "", fault.Invalid("Enter the destination's full http(s) address.")
	}
	switch in.Kind {
	case "ntfy":
		if c.Topic == "" || len(c.Topic) > 64 || strings.ContainsAny(c.Topic, "/?#") {
			return "", fault.Invalid("An ntfy topic is 1 to 64 characters without slashes.")
		}
		return u.Host + "/" + c.Topic, nil
	case "gotify":
		if c.Token == "" {
			return "", fault.Invalid("Gotify needs an application token.")
		}
	case "discord":
		if u.Host != "discord.com" && u.Host != "discordapp.com" {
			return "", fault.Invalid("Use a Discord webhook address.")
		}
	}
	if len(c.Token) > 500 || len(c.Secret) > 500 {
		return "", fault.Invalid("Token or secret is too long.")
	}
	return u.Host, nil
}

func (s Store) available() error {
	if !s.Box.Available() {
		return &fault.Error{Status: 503, Code: "secret_key_missing", Message: "Notifications need SECRET_KEY to be set on the server."}
	}
	return nil
}

func (s Store) Targets(ctx context.Context, a identity.Actor) ([]Target, error) {
	out := []Target{}
	if !a.Browser() {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,kind,name,description,enabled,last_status,last_sent_at,created_at FROM notification_targets WHERE profile_id=$1 ORDER BY id", a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Target
		if err = rows.Scan(&t.ID, &t.Kind, &t.Name, &t.Description, &t.Enabled, &t.LastStatus, &t.LastSentAt, &t.CreatedAt); err != nil {
			return out, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SaveTarget creates a destination (id 0) or replaces one. The configuration is never
// returned; replacing a destination requires sending it again.
func (s Store) SaveTarget(ctx context.Context, a identity.Actor, id int64, in TargetInput) (Target, error) {
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Target, error) {
		var t Target
		if !a.Browser() {
			return t, fault.Forbidden
		}
		if err := s.available(); err != nil {
			return t, err
		}
		desc, err := in.validate()
		if err != nil {
			return t, err
		}
		raw, _ := json.Marshal(in.Config)
		box, err := s.Box.Seal(raw, purpose(a.User.ID))
		if err != nil {
			return t, err
		}
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pastime.notify.config.'||$1::bigint::text,0))", a.TrackingID()); err != nil {
			return t, err
		}
		enabled := in.Enabled == nil || *in.Enabled
		if id == 0 {
			var n int
			if err = tx.QueryRow(ctx, "SELECT count(*) FROM notification_targets WHERE profile_id=$1", a.TrackingID()).Scan(&n); err != nil {
				return t, err
			}
			if n >= 10 {
				return t, fault.Invalid("You can keep up to 10 destinations.")
			}
			err = tx.QueryRow(ctx, `INSERT INTO notification_targets(profile_id,kind,name,description,sealed_config,enabled) VALUES($1,$2,$3,$4,$5,$6)
			RETURNING id,kind,name,description,enabled,last_status,last_sent_at,created_at`, a.TrackingID(), in.Kind, in.Name, desc, box, enabled).
				Scan(&t.ID, &t.Kind, &t.Name, &t.Description, &t.Enabled, &t.LastStatus, &t.LastSentAt, &t.CreatedAt)
		} else {
			err = tx.QueryRow(ctx, `UPDATE notification_targets SET kind=$3,name=$4,description=$5,sealed_config=$6,enabled=$7,last_status='' WHERE profile_id=$1 AND id=$2
			RETURNING id,kind,name,description,enabled,last_status,last_sent_at,created_at`, a.TrackingID(), id, in.Kind, in.Name, desc, box, enabled).
				Scan(&t.ID, &t.Kind, &t.Name, &t.Description, &t.Enabled, &t.LastStatus, &t.LastSentAt, &t.CreatedAt)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return t, fault.NotFound
		}
		return t, err
	})
}

func (s Store) DeleteTarget(ctx context.Context, a identity.Actor, id int64) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		tag, err := tx.Exec(ctx, "DELETE FROM notification_targets WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return err
	})
}

// Test sends a sample notice to one destination and records the outcome.
func (s Store) Test(ctx context.Context, a identity.Actor, id int64) (Target, error) {
	if !a.Browser() {
		return Target{}, fault.Forbidden
	}
	if err := s.available(); err != nil {
		return Target{}, err
	}
	notice := Notice{Title: "Pastime test notice", Body: "This destination is set up correctly.", Items: []Item{}}
	err := s.deliver(ctx, a.TrackingID(), id, notice)
	var t Target
	e := s.Pool.QueryRow(ctx, "SELECT id,kind,name,description,enabled,last_status,last_sent_at,created_at FROM notification_targets WHERE profile_id=$1 AND id=$2", a.TrackingID(), id).
		Scan(&t.ID, &t.Kind, &t.Name, &t.Description, &t.Enabled, &t.LastStatus, &t.LastSentAt, &t.CreatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return t, fault.NotFound
	}
	if err != nil && e == nil {
		return t, &fault.Error{Status: 502, Code: "delivery_failed", Message: t.LastStatus}
	}
	return t, e
}

func (s Store) Settings(ctx context.Context, a identity.Actor) (Settings, error) {
	out := Settings{Releases: true, Statuses: []string{"planned", "in_progress"}, MediaTypes: []string{}, Digest: true}
	if !a.Browser() {
		return out, fault.Forbidden
	}
	err := s.Pool.QueryRow(ctx, "SELECT releases,days_ahead,media_types,statuses,digest,version FROM notification_settings WHERE profile_id=$1", a.TrackingID()).
		Scan(&out.Releases, &out.DaysAhead, &out.MediaTypes, &out.Statuses, &out.Digest, &out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	return out, err
}

func (s Store) SaveSettings(ctx context.Context, a identity.Actor, in Settings) (Settings, error) {
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Settings, error) {
		if !a.Browser() {
			return in, fault.Forbidden
		}
		if in.DaysAhead < 0 || in.DaysAhead > 14 || len(in.MediaTypes) > 11 || len(in.Statuses) > 5 {
			return in, fault.Invalid("Notify from 0 to 14 days ahead.")
		}
		for _, st := range in.Statuses {
			if st != "planned" && st != "in_progress" && st != "paused" && st != "completed" && st != "dropped" {
				return in, fault.Invalid("Unsupported status.")
			}
		}
		if in.MediaTypes == nil {
			in.MediaTypes = []string{}
		}
		var version int
		var err error
		if in.Version == 0 {
			err = tx.QueryRow(ctx, `INSERT INTO notification_settings(profile_id,releases,days_ahead,media_types,statuses,digest) VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT DO NOTHING RETURNING version`, a.TrackingID(), in.Releases, in.DaysAhead, in.MediaTypes, in.Statuses, in.Digest).Scan(&version)
		} else {
			err = tx.QueryRow(ctx, `UPDATE notification_settings SET releases=$2,days_ahead=$3,media_types=$4,statuses=$5,digest=$6,version=version+1
			WHERE profile_id=$1 AND version=$7 RETURNING version`, a.TrackingID(), in.Releases, in.DaysAhead, in.MediaTypes, in.Statuses, in.Digest, in.Version).Scan(&version)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return in, fault.Conflict
		}
		in.Version = version
		return in, err
	})
}

// SetExcluded stops or resumes notices for one title.
func (s Store) SetExcluded(ctx context.Context, a identity.Actor, entryID int64, excluded bool) error {
	return identity.WriteOnly(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Allows("library:write") {
			return fault.Forbidden
		}
		var err error
		if excluded {
			var tag interface{ RowsAffected() int64 }
			tag, err = tx.Exec(ctx, "INSERT INTO notification_exclusions(profile_id,entry_id) SELECT $1,id FROM library_entries WHERE profile_id=$1 AND id=$2 ON CONFLICT DO NOTHING", a.TrackingID(), entryID)
			if err == nil && tag.RowsAffected() == 0 {
				var exists bool
				if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_entries WHERE profile_id=$1 AND id=$2)", a.TrackingID(), entryID).Scan(&exists); err == nil && !exists {
					err = fault.NotFound
				}
			}
		} else {
			_, err = tx.Exec(ctx, "DELETE FROM notification_exclusions WHERE profile_id=$1 AND entry_id=$2", a.TrackingID(), entryID)
		}
		return err
	})
}

func (s Store) Excluded(ctx context.Context, a identity.Actor, entryID int64) (bool, error) {
	var out bool
	err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notification_exclusions WHERE profile_id=$1 AND entry_id=$2)", a.TrackingID(), entryID).Scan(&out)
	return out, err
}

// ---------------------------------------------------------------- sending

type Item struct {
	Title       string `json:"title"`
	Episode     string `json:"episode,omitempty"`
	MediaType   string `json:"media_type"`
	ReleaseDate string `json:"release_date"`
}

type Notice struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Items []Item `json:"items"`
}

// Handler sends notices for releases on the job's date. Each release is logged per person
// with its destination outbox records. Failed destinations remain retryable.
func (s Store) Handler(ctx context.Context, raw json.RawMessage) error {
	if !s.Box.Available() {
		return nil
	}
	var p struct {
		Date string `json:"date"`
	}
	day, err := time.Parse("2006-01-02", func() string { _ = json.Unmarshal(raw, &p); return p.Date }())
	if err != nil {
		return jobs.ErrInvalidPayload
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT t.profile_id FROM notification_targets t JOIN tracking_profiles tracking ON tracking.id=t.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
		LEFT JOIN notification_settings n ON n.profile_id=t.profile_id WHERE t.enabled AND u.active AND COALESCE(n.releases,true)`)
	if err != nil {
		return err
	}
	var users []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var failures error
	for _, userID := range users {
		failures = errors.Join(failures, s.notifyUser(ctx, userID, day))
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return failures
}

func (s Store) notifyUser(ctx context.Context, userID int64, day time.Time) error {
	if err := s.queueUser(ctx, userID, day); err != nil {
		return err
	}
	return s.deliverPending(ctx, userID)
}

func (s Store) queueUser(ctx context.Context, userID int64, day time.Time) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u", userID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return fault.Unauthenticated
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pastime.notify.'||$1::bigint::text,0))", userID); err != nil {
		return err
	}
	// Claim releases and freeze every intended destination in the same transaction.
	rows, err := tx.Query(ctx, `WITH settings AS (
			SELECT COALESCE(n.days_ahead,0) AS ahead, COALESCE(n.media_types,'{}') AS types, COALESCE(n.statuses,'{planned,in_progress}') AS statuses
			FROM tracking_profiles p JOIN users u ON u.id=p.account_id LEFT JOIN notification_settings n ON n.profile_id=p.id WHERE p.id=$1 AND p.archived_at IS NULL),
		due AS (
			SELECT DISTINCT ON (x.id) x.id, e.title, e.media_type, x.release_date, x.kind, x.season_number, x.episode_number, x.title AS item_title
			FROM settings, library_entries e JOIN catalog_items x ON x.root_id=e.catalog_item_id
			WHERE e.profile_id=$1 AND e.status=ANY(settings.statuses) AND (cardinality(settings.types)=0 OR e.media_type=ANY(settings.types))
			AND x.release_date = $2::date + settings.ahead AND x.kind IN ('movie','episode','podcast_episode','comic_issue')
			AND NOT EXISTS(SELECT 1 FROM notification_exclusions n WHERE n.profile_id=e.profile_id AND n.entry_id=e.id)
			ORDER BY x.id),
		claimed AS (
			INSERT INTO notification_log(profile_id,catalog_item_id) SELECT $1,id FROM due ON CONFLICT DO NOTHING RETURNING catalog_item_id)
		SELECT d.title,d.media_type,d.release_date,d.kind,d.season_number,d.episode_number,d.item_title FROM due d JOIN claimed c ON c.catalog_item_id=d.id
		ORDER BY d.title, d.season_number NULLS FIRST, d.episode_number NULLS FIRST`, userID, day)
	if err != nil {
		return err
	}
	items := []Item{}
	for rows.Next() {
		var it Item
		var released time.Time
		var kind, itemTitle string
		var season, episode *int
		if err = rows.Scan(&it.Title, &it.MediaType, &released, &kind, &season, &episode, &itemTitle); err != nil {
			rows.Close()
			return err
		}
		it.ReleaseDate = released.Format("2006-01-02")
		if season != nil && episode != nil {
			it.Episode = fmt.Sprintf("S%d E%d", *season, *episode)
			if itemTitle != "" {
				it.Episode += " · " + itemTitle
			}
		}
		items = append(items, it)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return tx.Commit(ctx)
	}
	var digest bool
	if err = tx.QueryRow(ctx, "SELECT COALESCE((SELECT digest FROM notification_settings WHERE profile_id=$1),true)", userID).Scan(&digest); err != nil {
		return err
	}
	var notices []Notice
	if digest || len(items) == 1 {
		notices = []Notice{summarize(items)}
	} else {
		for _, it := range items {
			notices = append(notices, summarize([]Item{it}))
		}
	}
	targets, err := tx.Query(ctx, "SELECT id FROM notification_targets WHERE profile_id=$1 AND enabled", userID)
	if err != nil {
		return err
	}
	var ids []int64
	for targets.Next() {
		var id int64
		if err = targets.Scan(&id); err != nil {
			targets.Close()
			return err
		}
		ids = append(ids, id)
	}
	targets.Close()
	if err = targets.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		for _, notice := range notices {
			raw, err := json.Marshal(notice)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "INSERT INTO notification_deliveries(profile_id,target_id,notice) VALUES($1,$2,$3)", userID, id, raw); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func summarize(items []Item) Notice {
	if len(items) == 1 {
		it := items[0]
		body := it.Title
		if it.Episode != "" {
			body += " — " + it.Episode
		}
		return Notice{Title: "Out now: " + it.Title, Body: body, Items: items}
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString("• " + it.Title)
		if it.Episode != "" {
			b.WriteString(" — " + it.Episode)
		}
		b.WriteString("\n")
	}
	return Notice{Title: fmt.Sprintf("%d releases today", len(items)), Body: strings.TrimSpace(b.String()), Items: items}
}

func (s Store) deliver(ctx context.Context, userID, targetID int64, n Notice) error {
	var kind string
	var accountID int64
	var box []byte
	err := s.Pool.QueryRow(ctx, "SELECT t.kind,t.sealed_config,t.user_id FROM notification_targets t JOIN tracking_profiles tracking ON tracking.id=t.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE t.profile_id=$1 AND t.id=$2 AND t.enabled AND u.active", userID, targetID).Scan(&kind, &box, &accountID)
	if err != nil {
		return err
	}
	raw, err := s.Box.Open(box, purpose(accountID))
	if err != nil {
		if recordErr := s.record(ctx, targetID, "Could not read the destination; save it again.", false); recordErr != nil {
			return recordErr
		}
		return fault.Unavailable
	}
	var c Config
	if err = json.Unmarshal(raw, &c); err != nil {
		return err
	}
	sendErr := s.send(ctx, kind, c, n)
	status := "Delivered"
	if sendErr != nil {
		status = sendErr.Error()
	}
	if err = s.record(ctx, targetID, status, sendErr == nil); err != nil {
		return err
	}
	return sendErr
}

func (s Store) record(ctx context.Context, id int64, status string, ok bool) error {
	if len(status) > 200 {
		status = status[:200]
	}
	if ok {
		_, err := s.Pool.Exec(ctx, "UPDATE notification_targets SET last_status=$2,last_sent_at=clock_timestamp() WHERE id=$1", id, status)
		return err
	}
	_, err := s.Pool.Exec(ctx, "UPDATE notification_targets SET last_status=$2 WHERE id=$1", id, status)
	return err
}

func (s Store) send(ctx context.Context, kind string, c Config, n Notice) error {
	var req *http.Request
	var err error
	jsonReq := func(target string, body any) (*http.Request, error) {
		data, _ := json.Marshal(body)
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(data))
		if err == nil {
			r.Header.Set("Content-Type", "application/json")
		}
		return r, err
	}
	switch kind {
	case "webhook":
		payload := map[string]any{"event": "releases", "title": n.Title, "body": n.Body, "items": n.Items, "sent_at": time.Now().UTC()}
		if req, err = jsonReq(c.URL, payload); err == nil && c.Secret != "" {
			data, _ := json.Marshal(payload)
			req.Body = io.NopCloser(bytes.NewReader(data))
			mac := hmac.New(sha256.New, []byte(c.Secret))
			mac.Write(data)
			req.Header.Set("X-Pastime-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
	case "ntfy":
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/"+url.PathEscape(c.Topic), strings.NewReader(n.Body))
		if err == nil {
			req.Header.Set("Title", n.Title)
			req.Header.Set("Tags", "clapper")
			if c.Token != "" {
				req.Header.Set("Authorization", "Bearer "+c.Token)
			}
		}
	case "discord":
		req, err = jsonReq(c.URL, map[string]any{"content": "**" + n.Title + "**\n" + n.Body, "allowed_mentions": map[string]any{"parse": []string{}}})
	case "gotify":
		req, err = jsonReq(strings.TrimRight(c.URL, "/")+"/message", map[string]any{"title": n.Title, "message": n.Body, "priority": 5})
		if err == nil {
			req.Header.Set("X-Gotify-Key", c.Token)
		}
	case "apprise":
		req, err = jsonReq(c.URL, map[string]any{"title": n.Title, "body": n.Body, "type": "info"})
	default:
		return errors.New("unsupported destination")
	}
	if err != nil {
		return errors.New("invalid destination address")
	}
	req.Header.Set("User-Agent", "Pastime-Notify/1")
	client := s.Client
	if client == nil {
		client = guardedClient(s.AllowPrivate)
	}
	resp, err := client.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), errPrivate.Error()) {
			return errPrivate
		}
		return errors.New("could not reach the destination")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("the destination answered %d", resp.StatusCode)
	}
	return nil
}

var errPrivate = netguard.ErrPrivate

func guardedClient(allowPrivate bool) *http.Client {
	return netguard.Client(allowPrivate, 15*time.Second)
}
