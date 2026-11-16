package library

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// A check-in is an activity a profile has started and not finished yet. Finishing a
// "play" check-in records a play with the real time spent. Finishing a "session"
// check-in (reading or playing a game) records the time and how much was read, moves
// progress on, and completes the work only when it is done, because Pastime counts any
// play of a single work as finishing it.

// sessionKinds are the catalog kinds whose check-ins are sessions.
var sessionKinds = map[string]bool{"book": true, "audiobook": true, "comic": true, "comic_issue": true, "manga": true, "game": true}

// autoFinishKinds complete on their own once their runtime has passed.
var autoFinishKinds = map[string]bool{"movie": true, "episode": true}

// checkinParts are the descendant kinds a check-in may name instead of a title.
var checkinParts = map[string]bool{"episode": true, "comic_issue": true, "podcast_episode": true, "track": true}

// CheckinSource marks plays recorded by finishing a check-in.
const CheckinSource = "checkin"

const maxCheckinMinutes = 100000

type Checkin struct {
	ID            int64          `json:"id,string"`
	EntryID       int64          `json:"entry_id,string"`
	CatalogID     int64          `json:"catalog_id,string"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	MediaKind     string         `json:"media_kind"`
	Title         string         `json:"title"`
	ItemTitle     string         `json:"item_title,omitempty"`
	SeasonNumber  *int           `json:"season_number,omitempty"`
	EpisodeNumber *int           `json:"episode_number,omitempty"`
	ImageURL      string         `json:"image_url"`
	Unit          string         `json:"unit"`
	StartedAt     time.Time      `json:"started_at"`
	ExpectedEndAt *time.Time     `json:"expected_end_at,omitempty"`
	FinishedAt    *time.Time     `json:"finished_at,omitempty"`
	Minutes       *int           `json:"minutes,omitempty"`
	Amount        *int           `json:"amount,omitempty"`
	Completed     bool           `json:"completed"`
	Details       map[string]any `json:"details"`
}

type CheckinInput struct {
	CatalogID int64      `json:"catalog_id,string"`
	StartedAt *time.Time `json:"started_at"`
}

// BoardGamePlayer is one seat at a finished board game.
type BoardGamePlayer struct {
	Name   string   `json:"name"`
	Score  *float64 `json:"score,omitempty"`
	Winner bool     `json:"winner"`
}

// FinishInput describes how a check-in ended. Every field is optional: by default it
// finishes now and the time spent is the time since it started.
type FinishInput struct {
	FinishedAt *time.Time        `json:"finished_at"`
	Minutes    *int              `json:"minutes"`
	Amount     *int              `json:"amount"`
	Completed  bool              `json:"completed"`
	Players    []BoardGamePlayer `json:"players"`
	Location   string            `json:"location"`
	Platform   string            `json:"platform"`
}

func validateFinish(in FinishInput, now time.Time) error {
	if in.FinishedAt != nil && (in.FinishedAt.After(now.Add(5*time.Minute)) || in.FinishedAt.Year() < 1900) {
		return fault.Invalid("A check-in cannot finish in the future.")
	}
	if in.Minutes != nil && (*in.Minutes < 0 || *in.Minutes > maxCheckinMinutes) || in.Amount != nil && (*in.Amount < 0 || *in.Amount > 1000000) {
		return fault.Invalid("Time and amount must be zero or more.")
	}
	if len(in.Players) > 20 || utf8.RuneCountInString(in.Location) > 120 || utf8.RuneCountInString(in.Platform) > 60 {
		return fault.Invalid("A board game has at most 20 players; keep the location and platform short.")
	}
	for _, p := range in.Players {
		name := strings.TrimSpace(p.Name)
		if name == "" || utf8.RuneCountInString(name) > 80 || p.Score != nil && (math.IsNaN(*p.Score) || math.IsInf(*p.Score, 0) || math.Abs(*p.Score) > 1e9) {
			return fault.Invalid("Each player needs a name and, if scored, a finite score.")
		}
	}
	return nil
}

// CheckIn starts a check-in on a title or one of its parts, adding the title to the
// library when needed and moving a planned, paused or dropped title to in progress.
func (s Store) CheckIn(ctx context.Context, a identity.Actor, key string, in CheckinInput) (Mutation, error) {
	now := time.Now().UTC()
	if in.CatalogID < 1 || in.StartedAt != nil && (in.StartedAt.After(now.Add(5*time.Minute)) || in.StartedAt.Before(now.Add(-7*24*time.Hour))) {
		return Mutation{}, fault.Invalid("Choose a catalog record and a start time within the last week.")
	}
	return s.mutate(ctx, a, "library:write", key, "checkin.start", in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		var kind, rootKind string
		var rootID int64
		var parent *int64
		var runtime *int
		err := tx.QueryRow(ctx, `SELECT c.kind,r.kind,c.root_id,c.parent_id,COALESCE(c.runtime_minutes,r.runtime_minutes)
			FROM catalog_items c JOIN catalog_items r ON r.id=c.root_id WHERE c.id=$1`, in.CatalogID).Scan(&kind, &rootKind, &rootID, &parent, &runtime)
		if errors.Is(err, pgx.ErrNoRows) {
			return Mutation{}, fault.NotFound
		}
		if err != nil {
			return Mutation{}, err
		}
		if _, ok := catalogKinds[rootKind]; !ok || parent != nil && !checkinParts[kind] {
			return Mutation{}, fault.Invalid("Check in to a title, an episode, an issue or a track.")
		}
		entryID, err := s.entryForCheckin(ctx, tx, a, rootID)
		if err != nil {
			return Mutation{}, err
		}
		started := now
		if in.StartedAt != nil {
			started = in.StartedAt.UTC()
		}
		started = started.Truncate(time.Microsecond)
		var expected *time.Time
		if autoFinishKinds[kind] && runtime != nil && *runtime > 0 {
			end := started.Add(time.Duration(*runtime) * time.Minute)
			expected = &end
		}
		checkinKind := "play"
		if sessionKinds[kind] {
			checkinKind = "session"
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO checkins(profile_id,entry_id,catalog_item_id,kind,started_at,expected_end_at) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
			a.TrackingID(), entryID, in.CatalogID, checkinKind, started, expected).Scan(&id)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Mutation{}, &fault.Error{Status: 409, Code: "already_checked_in", Message: "You are already checked in to this."}
		}
		if err != nil {
			return Mutation{}, err
		}
		return Mutation{ID: id, Version: 1}, nil
	})
}

// entryForCheckin returns the caller's entry for a top-level record, adding it as in
// progress when absent and resuming a planned, paused or dropped one.
func (s Store) entryForCheckin(ctx context.Context, tx pgx.Tx, a identity.Actor, rootID int64) (int64, error) {
	var id int64
	var status string
	err := tx.QueryRow(ctx, "SELECT id,status FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2 FOR UPDATE", a.TrackingID(), rootID).Scan(&id, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		m, err := s.trackTx(ctx, tx, a, TrackInput{CatalogID: rootID, Status: "in_progress"})
		return m.ID, err
	}
	if err != nil {
		return 0, err
	}
	if status == "planned" || status == "paused" || status == "dropped" {
		if _, err = tx.Exec(ctx, "UPDATE library_entries SET status='in_progress',version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2", a.TrackingID(), id); err != nil {
			return 0, err
		}
		err = event(ctx, tx, a, id, "progress")
	}
	return id, err
}

// FinishCheckin finishes the caller's active check-in.
func (s Store) FinishCheckin(ctx context.Context, a identity.Actor, id int64, key string, in FinishInput) (Mutation, error) {
	if err := validateFinish(in, time.Now().UTC()); err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "library:write", key, "checkin.finish/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		return s.finishTx(ctx, tx, a, id, in, false)
	})
}

func (s Store) finishTx(ctx context.Context, tx pgx.Tx, a identity.Actor, id int64, in FinishInput, auto bool) (Mutation, error) {
	var entryID, catalogID int64
	var kind, itemKind string
	var started time.Time
	var expected *time.Time
	var runtime *int
	err := tx.QueryRow(ctx, `SELECT k.entry_id,k.catalog_item_id,k.kind,c.kind,k.started_at,k.expected_end_at,COALESCE(c.runtime_minutes,r.runtime_minutes)
		FROM checkins k JOIN catalog_items c ON c.id=k.catalog_item_id JOIN catalog_items r ON r.id=c.root_id
		WHERE k.profile_id=$1 AND k.id=$2 AND k.status='active' FOR UPDATE OF k`, a.TrackingID(), id).Scan(&entryID, &catalogID, &kind, &itemKind, &started, &expected, &runtime)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mutation{}, fault.NotFound
	}
	if err != nil {
		return Mutation{}, err
	}
	finished := time.Now().UTC()
	if auto && expected != nil {
		finished = *expected
	}
	if in.FinishedAt != nil {
		finished = in.FinishedAt.UTC()
	}
	finished = finished.Truncate(time.Microsecond)
	if finished.Before(started) {
		return Mutation{}, fault.Invalid("A check-in cannot finish before it started.")
	}
	minutes := int(math.Round(finished.Sub(started).Minutes()))
	if auto && runtime != nil {
		minutes = *runtime
	}
	if in.Minutes != nil {
		minutes = *in.Minutes
	}
	minutes = min(max(minutes, 0), maxCheckinMinutes)
	details := map[string]any{}
	switch {
	case len(in.Players) > 0 || in.Location != "":
		if itemKind != "boardgame" {
			return Mutation{}, fault.Invalid("Players and location belong to board games.")
		}
		if len(in.Players) > 0 {
			players := make([]BoardGamePlayer, len(in.Players))
			for i, p := range in.Players {
				players[i] = BoardGamePlayer{Name: strings.TrimSpace(p.Name), Score: p.Score, Winner: p.Winner}
			}
			details["players"] = players
		}
		if loc := strings.TrimSpace(in.Location); loc != "" {
			details["location"] = loc
		}
	case in.Platform != "":
		if itemKind != "game" {
			return Mutation{}, fault.Invalid("A platform belongs to games.")
		}
		details["platform"] = strings.TrimSpace(in.Platform)
	}
	if (in.Amount != nil || in.Completed) && kind != "session" {
		return Mutation{}, fault.Invalid("Amount and completion belong to reading and game sessions.")
	}
	var version int
	if err = tx.QueryRow(ctx, "SELECT version FROM library_entries WHERE profile_id=$1 AND id=$2", a.TrackingID(), entryID).Scan(&version); err != nil {
		return Mutation{}, err
	}
	e, err := current(ctx, tx, a, entryID, version)
	if err != nil {
		return Mutation{}, err
	}
	var playDetails []byte
	if len(details) > 0 {
		if playDetails, err = json.Marshal(details); err != nil {
			return Mutation{}, err
		}
	}
	var playID *int64
	recordPlay := func(target int64) error {
		var pid int64
		err := tx.QueryRow(ctx, `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,minutes,source,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			a.TrackingID(), entryID, target, e.Cycle, finished, minutes, CheckinSource, playDetails).Scan(&pid)
		playID = &pid
		return err
	}
	amount := 0
	if in.Amount != nil {
		amount = *in.Amount
	} else if kind == "session" && e.Unit == "minutes" {
		// Games and audiobooks measure progress in minutes, as the Steam import does, so
		// a session's time is its progress.
		amount = minutes
	}
	switch {
	case kind == "play":
		if err = recordPlay(catalogID); err == nil {
			_, err = s.reproject(ctx, tx, a, e, "played")
		}
	case itemKind == "comic_issue":
		// A comic's progress counts its issues; a finished issue is a play of it.
		if in.Completed {
			if err = recordPlay(catalogID); err == nil {
				_, err = s.reproject(ctx, tx, a, e, "played")
			}
		}
	case amount > 0 || in.Completed:
		progress := e.Progress + amount
		if e.Total != nil && progress > *e.Total {
			progress = *e.Total
		}
		if (in.Completed || e.Total != nil && progress >= *e.Total) && e.Status != "completed" {
			// The work is done: one completion play carrying this session's time, after
			// which reproject marks it completed exactly once.
			if err = recordPlay(catalogID); err == nil {
				_, err = s.reproject(ctx, tx, a, e, "played")
			}
			break
		}
		status := e.Status
		if status != "completed" {
			status = "in_progress"
		}
		_, err = tx.Exec(ctx, `UPDATE library_entries SET progress=$3,status=$4,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2`,
			a.TrackingID(), entryID, progress, status)
		if err == nil {
			err = event(ctx, tx, a, entryID, "progress")
		}
	}
	if err != nil {
		return Mutation{}, err
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return Mutation{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE checkins SET status='finished',finished_at=$3,minutes=$4,amount=$5,completed=$6,details=$7,play_id=$8,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2`,
		a.TrackingID(), id, finished, minutes, in.Amount, in.Completed || kind == "play", detailsJSON, playID)
	if err != nil {
		return Mutation{}, err
	}
	return Mutation{ID: id, Version: 1}, nil
}

// CancelCheckin drops the caller's active check-in without recording anything.
func (s Store) CancelCheckin(ctx context.Context, a identity.Actor, id int64, key string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "checkin.cancel/"+strconv.FormatInt(id, 10), struct{}{}, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		tag, err := tx.Exec(ctx, "UPDATE checkins SET status='cancelled',finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 AND status='active'", a.TrackingID(), id)
		if err != nil {
			return Mutation{}, err
		}
		if tag.RowsAffected() == 0 {
			return Mutation{}, fault.NotFound
		}
		return Mutation{ID: id, Version: 1}, nil
	})
}

const checkinColumns = `k.id,k.entry_id,k.catalog_item_id,k.kind,k.status,c.kind,r.title,CASE WHEN c.id<>r.id THEN c.title ELSE '' END,
	c.season_number,c.episode_number,COALESCE(NULLIF(c.image_url,''),r.image_url),e.unit,k.started_at,k.expected_end_at,k.finished_at,k.minutes,k.amount,k.completed,k.details
	FROM checkins k JOIN catalog_items c ON c.id=k.catalog_item_id JOIN catalog_items r ON r.id=c.root_id
	JOIN library_entries e ON e.profile_id=k.profile_id AND e.id=k.entry_id`

func scanCheckin(row pgx.Row) (Checkin, error) {
	var c Checkin
	err := row.Scan(&c.ID, &c.EntryID, &c.CatalogID, &c.Kind, &c.Status, &c.MediaKind, &c.Title, &c.ItemTitle, &c.SeasonNumber, &c.EpisodeNumber,
		&c.ImageURL, &c.Unit, &c.StartedAt, &c.ExpectedEndAt, &c.FinishedAt, &c.Minutes, &c.Amount, &c.Completed, &c.Details)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, fault.NotFound
	}
	if c.Details == nil {
		c.Details = map[string]any{}
	}
	return c, err
}

// ActiveCheckins lists the caller's active check-ins, newest first.
func (s Store) ActiveCheckins(ctx context.Context, a identity.Actor) ([]Checkin, error) {
	if !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+checkinColumns+" WHERE k.profile_id=$1 AND k.status='active' ORDER BY k.started_at DESC,k.id DESC LIMIT 50", a.TrackingID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Checkin{}
	for rows.Next() {
		c, err := scanCheckin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCheckin returns one of the caller's check-ins.
func (s Store) GetCheckin(ctx context.Context, a identity.Actor, id int64) (Checkin, error) {
	if !a.Allows("library:read") {
		return Checkin{}, fault.Forbidden
	}
	return scanCheckin(s.Pool.QueryRow(ctx, "SELECT "+checkinColumns+" WHERE k.profile_id=$1 AND k.id=$2", a.TrackingID(), id))
}

// FinishDueCheckins completes film and episode check-ins whose runtime has passed, as
// if they had been finished at the end of the runtime. A profile that has lost access
// is skipped and retried later.
func (s Store) FinishDueCheckins(ctx context.Context, limit int) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,profile_id FROM checkins WHERE status='active' AND expected_end_at<=clock_timestamp() ORDER BY expected_end_at,id LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type due struct{ id, profile int64 }
	var list []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.id, &d.profile); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	finished := 0
	for _, d := range list {
		ok, err := s.finishDue(ctx, d.id, d.profile)
		if err != nil {
			return finished, err
		}
		if ok {
			finished++
		}
	}
	return finished, nil
}

func (s Store) finishDue(ctx context.Context, id, profileID int64) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	a, err := identity.ResolveWorkerProfile(ctx, tx, profileID, "worker", []string{"library:write"}, true)
	if errors.Is(err, fault.Unauthenticated) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, "SELECT set_config('pastime.change_source','system:checkin.auto_finish',true)"); err != nil {
		return false, err
	}
	if _, err = s.finishTx(ctx, tx, a, id, FinishInput{}, true); errors.Is(err, fault.NotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
