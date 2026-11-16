package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/jackc/pgx/v5"
)

// Linked services are pull integrations: saved credentials and a schedule. Each sync is
// an ordinary import run, so it only adds or moves things forward and never repeats a play.

// LinkKinds lists the services that can be linked, with what each needs.
var LinkKinds = map[string]struct {
	Name    string
	Fields  []string // url, token, username, user_id, steam_id
	Auth    string   // "", device (Trakt), pin (Simkl)
	Creds   func(Credentials) bool
	Private bool // usually on the local network
}{
	"trakt":          {"Trakt", nil, "device", func(c Credentials) bool { return c.TraktClientID != "" && c.TraktClientSecret != "" }, false},
	"simkl":          {"Simkl", nil, "pin", func(c Credentials) bool { return c.SimklClientID != "" }, false},
	"mal":            {"MyAnimeList", []string{"username"}, "", func(c Credentials) bool { return c.MALClientID != "" }, false},
	"kitsu":          {"Kitsu", []string{"username"}, "", nil, false},
	"anilist":        {"AniList", []string{"username"}, "", nil, false},
	"hardcover":      {"Hardcover", []string{"token"}, "", nil, false},
	"steam":          {"Steam", []string{"steam_id", "token"}, "", nil, false},
	"bgg":            {"BoardGameGeek", []string{"username"}, "", func(c Credentials) bool { return c.BGGToken != "" }, false},
	"jellyfin":       {"Jellyfin", []string{"url", "token", "username"}, "", nil, true},
	"emby":           {"Emby", []string{"url", "token", "username"}, "", nil, true},
	"plex":           {"Plex", []string{"url", "token", "user_id"}, "", nil, true},
	"audiobookshelf": {"Audiobookshelf", []string{"url", "token"}, "", nil, true},
	"lastfm":         {"Last.fm", []string{"username"}, "", func(c Credentials) bool { return c.LastfmKey != "" }, false},
	"listenbrainz":   {"ListenBrainz", []string{"username", "token"}, "", nil, false},
	"koito":          {"Koito", []string{"url", "username", "token"}, "", nil, true},
	"gpodder":        {"gPodder", []string{"url", "username", "token"}, "", nil, false},
	"pocketcasts":    {"Pocket Casts", []string{"username", "token"}, "", nil, false},
	"stremio":        {"Stremio", []string{"username", "token"}, "", nil, false},
	"psn":            {"PlayStation", []string{"token"}, "", nil, false},
	"xbox":           {"Xbox", []string{"token"}, "", nil, false},
	"storyteller":    {"Storyteller", []string{"url"}, "device", nil, true},
	"radarr":         {"Radarr", []string{"url", "token"}, "", nil, true},
	"mylar3":         {"Mylar3", []string{"url", "token"}, "", nil, true},
	"komga":          {"Komga", []string{"url", "token"}, "", nil, true},
	"kavita":         {"Kavita", []string{"url", "token"}, "", nil, true},
	"kapowarr":       {"Kapowarr", []string{"url", "token"}, "", nil, true},
	"sonarr":         {"Sonarr", []string{"url", "token"}, "", nil, true},
}

type Link struct {
	Health      string     `json:"health"`
	CheckedAt   *time.Time `json:"checked_at"`
	PushStatus  string     `json:"push_status"`
	ID          int64      `json:"id,string"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Schedule    string     `json:"schedule"`
	NextRunAt   *time.Time `json:"next_run_at"`
	LastRunID   *string    `json:"last_run_id"`
	LastRunAt   *time.Time `json:"last_run_at"`
	LastStatus  string     `json:"last_status"`
	CreatedAt   time.Time  `json:"created_at"`
}

type LinkInput struct {
	Kind     string     `json:"kind"`
	Name     string     `json:"name"`
	Schedule string     `json:"schedule"`
	Config   LinkConfig `json:"config"`
}

// LinkKind describes one service for the interface.
type LinkKind struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name"`
	Fields    []string `json:"fields"`
	Auth      string   `json:"auth"`
	Available bool     `json:"available"`
}

func linkPurpose(userID int64) string { return "service-link:" + strconv.FormatInt(userID, 10) }

func (s Store) linksReady() error {
	if !s.Box.Available() {
		return &fault.Error{Status: 503, Code: "secret_key_missing", Message: "Linked services need SECRET_KEY to be set on the server."}
	}
	return nil
}

func (s Store) Kinds() []LinkKind {
	out := []LinkKind{}
	for _, k := range []string{"trakt", "simkl", "anilist", "mal", "kitsu", "hardcover", "steam", "bgg", "jellyfin", "emby", "plex", "audiobookshelf", "lastfm", "listenbrainz", "koito", "gpodder", "pocketcasts", "stremio", "psn", "xbox", "storyteller", "radarr", "sonarr", "mylar3", "komga", "kavita", "kapowarr"} {
		d := LinkKinds[k]
		fields := d.Fields
		if fields == nil {
			fields = []string{}
		}
		out = append(out, LinkKind{Kind: k, Name: d.Name, Fields: fields, Auth: d.Auth, Available: s.Box.Available() && (d.Creds == nil || d.Creds(s.Creds))})
	}
	return out
}

func describe(kind string, c LinkConfig) string {
	switch {
	case c.URL != "":
		if u, err := url.Parse(c.URL); err == nil {
			return u.Host
		}
	case c.Username != "":
		return c.Username
	case c.SteamID != "":
		return c.SteamID
	}
	return LinkKinds[kind].Name
}

func nextRun(schedule string, from time.Time) *time.Time {
	var t time.Time
	switch schedule {
	case "hourly":
		t = from.Add(time.Hour)
	case "two_hourly":
		t = from.Add(2 * time.Hour)
	case "daily":
		t = from.Add(24 * time.Hour)
	case "weekly":
		t = from.AddDate(0, 0, 7)
	default:
		return nil
	}
	return &t
}

const linkColumns = `l.id,l.kind,l.name,l.description,l.schedule,l.next_run_at,l.last_run::text,l.last_run_at,COALESCE(r.status,''),l.created_at,COALESCE((SELECT j.status FROM jobs j WHERE j.dedupe_key='playback_push:'||l.last_run::text),''),l.health,l.checked_at
FROM service_links l LEFT JOIN import_runs r ON r.id=l.last_run`

func scanLink(row pgx.Row) (Link, error) {
	var l Link
	err := row.Scan(&l.ID, &l.Kind, &l.Name, &l.Description, &l.Schedule, &l.NextRunAt, &l.LastRunID, &l.LastRunAt, &l.LastStatus, &l.CreatedAt, &l.PushStatus, &l.Health, &l.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.NotFound
	}
	return l, err
}

func (s Store) Links(ctx context.Context, a identity.Actor) ([]Link, error) {
	out := []Link{}
	if !a.Browser() {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+linkColumns+" WHERE l.profile_id=$1 ORDER BY l.id", a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return out, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SaveLink stores a service with typed-in credentials (not the Trakt or Simkl sign-in
// flows) and queues a first sync. One link per service and person.
func (s Store) SaveLink(ctx context.Context, a identity.Actor, in LinkInput) (Link, error) {
	if !a.Browser() {
		return Link{}, fault.Forbidden
	}
	if err := s.linksReady(); err != nil {
		return Link{}, err
	}
	d, ok := LinkKinds[in.Kind]
	if !ok || d.Auth != "" {
		return Link{}, fault.Invalid("Choose a service that is linked with credentials.")
	}
	if d.Creds != nil && !d.Creds(s.Creds) {
		return Link{}, &fault.Error{Status: 503, Code: "service_unavailable", Message: d.Name + " is not set up on this server."}
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = d.Name
	}
	if len([]rune(in.Name)) > 80 || (in.Schedule != "two_hourly" && in.Schedule != "manual" && in.Schedule != "hourly" && in.Schedule != "daily" && in.Schedule != "weekly") {
		return Link{}, fault.Invalid("Use a name up to 80 characters and a supported schedule.")
	}
	c := in.Config
	for _, f := range d.Fields {
		v := map[string]string{"url": c.URL, "token": c.Token, "username": c.Username, "user_id": c.UserID, "steam_id": c.SteamID}[f]
		optional := (f == "token" && (in.Kind == "steam" || in.Kind == "listenbrainz" || in.Kind == "koito")) || (f == "url" && in.Kind == "gpodder") || (f == "username" && (in.Kind == "jellyfin" || in.Kind == "emby")) || f == "user_id"
		if strings.TrimSpace(v) == "" && !optional {
			return Link{}, fault.Invalid("Fill in every field for " + d.Name + ".")
		}
		if len(v) > 1000 {
			return Link{}, fault.Invalid("A field is too long.")
		}
	}
	if c.URL != "" {
		if _, err := baseURL(c.URL); err != nil {
			return Link{}, fault.Invalid("Enter the server's full address, such as http://192.0.2.10:8096.")
		}
	}
	return s.storeLink(ctx, a.TrackingID(), in.Kind, in.Name, in.Schedule, c, a)
}

func (s Store) storeLink(ctx context.Context, userID int64, kind, name, schedule string, c LinkConfig, actors ...identity.Actor) (Link, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Link{}, err
	}
	defer tx.Rollback(ctx)
	if err = authorizeLinkTx(ctx, tx, userID, actors); err != nil {
		return Link{}, err
	}
	raw, _ := json.Marshal(c)
	owner, err := identity.ResolveWorkerProfile(ctx, tx, userID, "import", nil, true)
	if err != nil {
		return Link{}, err
	}
	box, err := s.Box.Seal(raw, linkPurpose(owner.User.ID))
	if err != nil {
		return Link{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO service_links(profile_id,kind,name,description,sealed_config,schedule,next_run_at,state) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp(),'{}')
		ON CONFLICT (profile_id,kind) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,sealed_config=EXCLUDED.sealed_config,schedule=EXCLUDED.schedule,next_run_at=clock_timestamp(),state='{}',generation=service_links.generation+1
		RETURNING id`, userID, kind, name, describe(kind, c), box, schedule).Scan(&id)
	if err != nil {
		return Link{}, err
	}
	out, err := scanLink(tx.QueryRow(ctx, "SELECT "+linkColumns+" WHERE l.profile_id=$1 AND l.id=$2", userID, id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s Store) UpdateSchedule(ctx context.Context, a identity.Actor, id int64, schedule string) (Link, error) {
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Link, error) {
		if !a.Browser() {
			return Link{}, fault.Forbidden
		}
		if schedule != "two_hourly" && schedule != "manual" && schedule != "hourly" && schedule != "daily" && schedule != "weekly" {
			return Link{}, fault.Invalid("Unsupported schedule.")
		}
		tag, err := tx.Exec(ctx, "UPDATE service_links SET schedule=$3,next_run_at=CASE WHEN $3='manual' THEN NULL ELSE COALESCE(next_run_at,clock_timestamp()) END WHERE profile_id=$1 AND id=$2", a.TrackingID(), id, schedule)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		if err != nil {
			return Link{}, err
		}
		return scanLink(tx.QueryRow(ctx, "SELECT "+linkColumns+" WHERE l.profile_id=$1 AND l.id=$2", a.TrackingID(), id))
	})
}

func (s Store) DeleteLink(ctx context.Context, a identity.Actor, id int64) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		var kind string
		if err := tx.QueryRow(ctx, "SELECT kind FROM service_links WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), id).Scan(&kind); err != nil {
			if err == pgx.ErrNoRows {
				return fault.NotFound
			}
			return err
		}
		// Unlinking an ownership index removes the copies it recorded.
		if _, err := tx.Exec(ctx, `DELETE FROM owned_copies WHERE profile_id=$1 AND source=$2 AND $2=ANY($3)`, a.TrackingID(), kind, ownershipSources); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, "DELETE FROM service_links WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return err
	})
}

// SyncNow starts a sync of one linked service for its owner.
func (s Store) SyncNow(ctx context.Context, a identity.Actor, id int64) (Run, error) {
	if !a.Allows("library:write") {
		return Run{}, fault.Forbidden
	}
	return s.startLinked(ctx, a.TrackingID(), id, a)
}

var importBusy = &fault.Error{Status: 409, Code: "import_in_progress", Message: "Another import is running. Try again when it finishes."}

func (s Store) startLinked(ctx context.Context, userID, linkID int64, actors ...identity.Actor) (Run, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback(ctx)
	if err = authorizeLinkTx(ctx, tx, userID, actors); err != nil {
		return Run{}, err
	}
	var kind, schedule string
	err = tx.QueryRow(ctx, "SELECT kind,schedule FROM service_links WHERE profile_id=$1 AND id=$2 FOR UPDATE", userID, linkID).Scan(&kind, &schedule)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, fault.NotFound
	}
	if err != nil {
		return Run{}, err
	}
	options, _ := json.Marshal(map[string]int64{"link_id": linkID})
	var id string
	err = tx.QueryRow(ctx, "INSERT INTO import_runs(profile_id,source,options,upload_name) VALUES($1,$2,$3,$4) RETURNING id::text", userID, kind, options, LinkKinds[kind].Name).Scan(&id)
	if isUnique(err) {
		return Run{}, importBusy
	}
	if err != nil {
		return Run{}, err
	}
	payload, _ := json.Marshal(map[string]string{"run_id": id})
	if _, err = jobs.Enqueue(ctx, tx, Job, "import:"+id, "idempotent", payload, 5); err != nil {
		return Run{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE service_links SET last_run=$3::uuid,last_run_at=clock_timestamp(),next_run_at=$4 WHERE profile_id=$1 AND id=$2", userID, linkID, id, nextRun(schedule, time.Now())); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Run{}, err
	}
	return scanRun(s.Pool.QueryRow(ctx, "SELECT "+runColumns+" FROM import_runs WHERE id=$1", id))
}

func isUnique(err error) bool {
	var pg interface{ SQLState() string }
	return errors.As(err, &pg) && pg.SQLState() == "23505"
}

// EnqueueDue starts runs for linked services whose next sync is due. A person with an
// import already running is tried again at the next pass.
func (s Store) EnqueueDue(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT l.profile_id,l.id FROM service_links l JOIN tracking_profiles tracking ON tracking.id=l.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
		WHERE l.schedule<>'manual' AND l.next_run_at <= clock_timestamp() AND u.active ORDER BY l.next_run_at LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type due struct{ user, link int64 }
	var list []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.user, &d.link); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	started := 0
	for _, d := range list {
		if _, err = s.startLinked(ctx, d.user, d.link); err == nil {
			started++
		} else if !errors.Is(err, importBusy) {
			return started, err
		}
	}
	return started, nil
}

// loadLink decrypts a link for its run. allowPrivate reflects the owner's role and the
// instance setting.
func (s Store) loadLink(ctx context.Context, userID, linkID int64) (string, LinkConfig, map[string]any, bool, error) {
	var kind, role string
	var accountID int64
	var box, rawState []byte
	var generation int64
	err := s.Pool.QueryRow(ctx, "SELECT l.kind,l.sealed_config,l.state,u.role,l.generation,u.id FROM service_links l JOIN tracking_profiles tracking ON tracking.id=l.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE l.profile_id=$1 AND l.id=$2 AND u.active", userID, linkID).Scan(&kind, &box, &rawState, &role, &generation, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", LinkConfig{}, nil, false, fmt.Errorf("%w: this service was unlinked", ErrFormat)
	}
	if err != nil {
		return "", LinkConfig{}, nil, false, err
	}
	var c LinkConfig
	raw, err := s.Box.Open(box, linkPurpose(accountID))
	if err != nil {
		return "", c, nil, false, fmt.Errorf("%w: the saved credentials cannot be read; link the service again", ErrFormat)
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return "", c, nil, false, err
	}
	c.Generation = generation
	state := map[string]any{}
	_ = json.Unmarshal(rawState, &state)
	allow := s.AllowPrivate || role == "admin" || role == "owner"
	return kind, c, state, allow, nil
}

// saveLinkState keeps refreshed tokens and the new sync position after a successful fetch.
func (s Store) saveLinkState(ctx context.Context, userID, linkID int64, c LinkConfig, state map[string]any) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.saveLinkStateTx(ctx, tx, userID, linkID, c, state); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------------------------------------------------------------- Trakt and Simkl sign-in

// Authorization is a pending device or PIN sign-in the person completes on the service.
type Authorization struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
	Status          string `json:"status"` // pending, linked, expired
	Link            *Link  `json:"link,omitempty"`
}

type authState struct {
	DeviceCode string `json:"device_code,omitempty"`
	UserCode   string `json:"user_code"`
	URL        string `json:"url,omitempty"` // the person's own server, for Storyteller
}

func (s Store) BeginAuthorization(ctx context.Context, a identity.Actor, kind, address string) (Authorization, error) {
	var out Authorization
	if !a.Browser() {
		return out, fault.Forbidden
	}
	if err := s.linksReady(); err != nil {
		return out, err
	}
	d, ok := LinkKinds[kind]
	if !ok || d.Auth == "" {
		return out, fault.Invalid("That service is linked with credentials instead.")
	}
	if d.Creds != nil && !d.Creds(s.Creds) {
		return out, &fault.Error{Status: 503, Code: "service_unavailable", Message: d.Name + " is not set up on this server."}
	}
	c := s.httpClient(false)
	var st authState
	switch kind {
	case "storyteller":
		base, err := baseURL(address)
		if err != nil {
			return out, fault.Invalid("Enter your Storyteller server's address.")
		}
		c = s.httpClient(s.AllowPrivate || a.IsAdmin())
		var r struct {
			DeviceCode      string `json:"device_code"`
			UserCode        string `json:"user_code"`
			VerificationURI string `json:"verification_uri"`
			Complete        string `json:"verification_uri_complete"`
			ExpiresIn       int    `json:"expires_in"`
			Interval        int    `json:"interval"`
		}
		if _, err := getJSON(ctx, c, "POST", base+"/api/v2/device/start", http.Header{"Content-Type": {"application/json"}}, strings.NewReader("{}"), &r); err != nil {
			var fe *fault.Error
			if errors.As(err, &fe) {
				return out, err
			}
			return out, fault.Invalid("That server did not answer as Storyteller does. Check the address.")
		}
		st = authState{DeviceCode: r.DeviceCode, UserCode: r.UserCode, URL: base}
		out = Authorization{Kind: kind, UserCode: r.UserCode, VerificationURL: onServer(firstNonEmpty(r.Complete, r.VerificationURI, base), base), Interval: max(r.Interval, 5), ExpiresIn: max(r.ExpiresIn, 300), Status: "pending"}
	case "trakt":
		body, _ := json.Marshal(map[string]string{"client_id": s.Creds.TraktClientID})
		var r struct {
			DeviceCode      string `json:"device_code"`
			UserCode        string `json:"user_code"`
			VerificationURL string `json:"verification_url"`
			ExpiresIn       int    `json:"expires_in"`
			Interval        int    `json:"interval"`
		}
		if _, err := getJSON(ctx, c, "POST", traktAPI+"/oauth/device/code", http.Header{"Content-Type": {"application/json"}}, strings.NewReader(string(body)), &r); err != nil {
			return out, fault.Unavailable
		}
		st = authState{DeviceCode: r.DeviceCode, UserCode: r.UserCode}
		out = Authorization{Kind: kind, UserCode: r.UserCode, VerificationURL: r.VerificationURL, Interval: max(r.Interval, 5), ExpiresIn: r.ExpiresIn, Status: "pending"}
	case "simkl":
		var r struct {
			UserCode        string `json:"user_code"`
			VerificationURL string `json:"verification_url"`
			ExpiresIn       int    `json:"expires_in"`
			Interval        int    `json:"interval"`
		}
		if _, err := getJSON(ctx, c, "GET", "https://api.simkl.com/oauth/pin?client_id="+url.QueryEscape(s.Creds.SimklClientID), nil, nil, &r); err != nil {
			return out, fault.Unavailable
		}
		st = authState{UserCode: r.UserCode}
		out = Authorization{Kind: kind, UserCode: r.UserCode, VerificationURL: firstNonEmpty(r.VerificationURL, "https://simkl.com/pin/"), Interval: max(r.Interval, 5), ExpiresIn: max(r.ExpiresIn, 300), Status: "pending"}
	}
	raw, _ := json.Marshal(st)
	box, err := s.Box.Seal(raw, "service-auth:"+strconv.FormatInt(a.User.ID, 10))
	if err != nil {
		return out, err
	}
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Authorization, error) {
		_, err := tx.Exec(ctx, "DELETE FROM service_authorizations WHERE id IN(SELECT id FROM service_authorizations WHERE expires_at<clock_timestamp() LIMIT 500)")
		if err != nil {
			return out, err
		}
		err = tx.QueryRow(ctx, "INSERT INTO service_authorizations(profile_id,kind,sealed_state,expires_at) VALUES($1,$2,$3,clock_timestamp()+make_interval(secs=>$4)) RETURNING id::text", a.TrackingID(), kind, box, out.ExpiresIn).Scan(&out.ID)
		return out, err
	})
}

// PollAuthorization checks whether the person approved the sign-in; once approved, the
// tokens are saved as a linked service and the pending sign-in is removed.
func (s Store) PollAuthorization(ctx context.Context, a identity.Actor, id string) (Authorization, error) {
	out := Authorization{ID: id, Status: "pending"}
	if !a.Browser() {
		return out, fault.Forbidden
	}
	if err := s.linksReady(); err != nil {
		return out, err
	}
	if !identity.ValidUUID(id) {
		return out, fault.NotFound
	}
	var box []byte
	var expired bool
	err := s.Pool.QueryRow(ctx, "SELECT kind,sealed_state,expires_at < clock_timestamp() FROM service_authorizations WHERE profile_id=$1 AND id=$2::uuid", a.TrackingID(), id).Scan(&out.Kind, &box, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	if expired {
		out.Status = "expired"
		return out, nil
	}
	raw, err := s.Box.Open(box, "service-auth:"+strconv.FormatInt(a.User.ID, 10))
	if err != nil {
		return out, err
	}
	var st authState
	_ = json.Unmarshal(raw, &st)
	out.UserCode = st.UserCode
	c := s.httpClient(false)
	var cfg LinkConfig
	switch out.Kind {
	case "trakt":
		body, _ := json.Marshal(map[string]string{"code": st.DeviceCode, "client_id": s.Creds.TraktClientID, "client_secret": s.Creds.TraktClientSecret})
		var tok struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
			CreatedAt    int64  `json:"created_at"`
		}
		if _, err = getJSON(ctx, c, "POST", traktAPI+"/oauth/device/token", http.Header{"Content-Type": {"application/json"}}, strings.NewReader(string(body)), &tok); err != nil || tok.AccessToken == "" {
			return out, nil // Still waiting (Trakt answers 400 until approved).
		}
		cfg = LinkConfig{Token: tok.AccessToken, RefreshToken: tok.RefreshToken, Expires: time.Unix(tok.CreatedAt+tok.ExpiresIn, 0)}
	case "storyteller":
		body, _ := json.Marshal(map[string]string{"device_code": st.DeviceCode})
		var tok struct {
			AccessToken string `json:"access_token"`
		}
		sc := s.httpClient(s.AllowPrivate || a.IsAdmin())
		if _, err = getJSON(ctx, sc, "POST", st.URL+"/api/v2/device/token", http.Header{"Content-Type": {"application/json"}}, strings.NewReader(string(body)), &tok); err != nil || tok.AccessToken == "" {
			return out, nil // Still waiting for approval.
		}
		cfg = LinkConfig{URL: st.URL, Token: tok.AccessToken}
	case "simkl":
		var r struct {
			Result      string `json:"result"`
			AccessToken string `json:"access_token"`
		}
		if _, err = getJSON(ctx, c, "GET", "https://api.simkl.com/oauth/pin/"+url.PathEscape(st.UserCode)+"?client_id="+url.QueryEscape(s.Creds.SimklClientID), nil, nil, &r); err != nil || r.Result != "OK" || r.AccessToken == "" {
			return out, nil
		}
		cfg = LinkConfig{Token: r.AccessToken}
	}
	link, err := s.storeLink(ctx, a.TrackingID(), out.Kind, LinkKinds[out.Kind].Name, "daily", cfg, a)
	if err != nil {
		return out, err
	}
	_, _ = s.Pool.Exec(ctx, "DELETE FROM service_authorizations WHERE id=$1::uuid", id)
	out.Status, out.Link = "linked", &link
	return out, nil
}

// onServer points a verification link at the address the person gave, since servers
// behind a proxy may report their internal host.
func onServer(link, base string) string {
	u, err := url.Parse(link)
	b, err2 := url.Parse(base)
	if err != nil || err2 != nil || u.Host == "" {
		return base
	}
	u.Scheme, u.Host = b.Scheme, b.Host
	return u.String()
}

func authorizeLinkTx(ctx context.Context, tx pgx.Tx, userID int64, actors []identity.Actor) error {
	if len(actors) > 0 {
		fresh, err := identity.AuthorizeTx(ctx, tx, actors[0], "library:write")
		if err == nil && fresh.TrackingID() != userID {
			return fault.Forbidden
		}
		return err
	}
	var active bool
	err := tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u", userID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Unauthenticated
	}
	if err != nil {
		return err
	}
	if !active {
		return fault.Unauthenticated
	}
	return nil
}
