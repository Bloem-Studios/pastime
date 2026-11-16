// Package connections manages per-account links to media servers and turns their
// webhook deliveries into scrobble references.
package connections

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kinds are the media servers that can deliver webhooks.
var Kinds = []string{"plex", "jellyfin", "emby", "generic", "seerr"}

// Settings are non-secret delivery filters. Username limits a shared media server to
// events from one of its accounts; empty accepts every account on that server.
type Settings struct {
	Username string `json:"username"`
}

type Connection struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Settings       Settings   `json:"settings"`
	CreatedAt      time.Time  `json:"created_at"`
	LastEventAt    *time.Time `json:"last_event_at"`
	LastOutcome    string     `json:"last_outcome"`
	EventsRecorded int        `json:"events_recorded"`
	ProfileID      int64      `json:"-"`
}

type Store struct{ Pool *pgxpool.Pool }

func valid(kind string) bool {
	for _, k := range Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func digest(raw string) []byte { h := sha256.Sum256([]byte(raw)); return h[:] }

const columns = "id::text,kind,settings,created_at,last_event_at,last_outcome,events_recorded,profile_id"

func scan(row pgx.Row) (Connection, error) {
	var c Connection
	var settings []byte
	err := row.Scan(&c.ID, &c.Kind, &settings, &c.CreatedAt, &c.LastEventAt, &c.LastOutcome, &c.EventsRecorded, &c.ProfileID)
	if err == nil {
		_ = json.Unmarshal(settings, &c.Settings)
	}
	return c, err
}

func (s Store) List(ctx context.Context, a identity.Actor) ([]Connection, error) {
	if !a.Browser() {
		return nil, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+columns+" FROM connections WHERE profile_id=$1 ORDER BY kind", a.TrackingID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func cleanSettings(in Settings) (Settings, error) {
	in.Username = strings.TrimSpace(in.Username)
	if len([]rune(in.Username)) > 100 {
		return in, fault.Invalid("The media server username can have at most 100 characters.")
	}
	return in, nil
}

// Connect creates or replaces the caller's connection of a kind and returns its secret
// once. Replacing it invalidates the previous webhook URL.
func (s Store) Connect(ctx context.Context, a identity.Actor, kind string, settings Settings) (Connection, string, error) {
	if !a.Browser() {
		return Connection{}, "", fault.Forbidden
	}
	if !valid(kind) {
		return Connection{}, "", fault.Invalid("Choose Plex, Jellyfin or Emby.")
	}
	settings, err := cleanSettings(settings)
	if err != nil {
		return Connection{}, "", err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return Connection{}, "", err
	}
	raw := "fn_w_" + base64.RawURLEncoding.EncodeToString(b)
	data, _ := json.Marshal(settings)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Connection{}, "", err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, ""); err != nil {
		return Connection{}, "", err
	}
	c, err := scan(tx.QueryRow(ctx, `INSERT INTO connections(profile_id,kind,token_hash,settings) VALUES($1,$2,$3,$4)
		ON CONFLICT(profile_id,kind) DO UPDATE SET token_hash=EXCLUDED.token_hash,settings=EXCLUDED.settings,created_at=clock_timestamp(),last_outcome='',events_recorded=0,last_event_at=NULL
		RETURNING `+columns, a.TrackingID(), kind, digest(raw), data))
	if err != nil {
		return Connection{}, "", err
	}
	if err = identity.Audit(ctx, tx, a, "connection.create", "connection", c.ID, map[string]any{"kind": kind}); err != nil {
		return Connection{}, "", err
	}
	return c, raw, tx.Commit(ctx)
}

func (s Store) Update(ctx context.Context, a identity.Actor, id string, settings Settings) (Connection, error) {
	return identity.Write(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) (Connection, error) {
		if !a.Browser() || !identity.ValidUUID(id) {
			return Connection{}, fault.NotFound
		}
		settings, err := cleanSettings(settings)
		if err != nil {
			return Connection{}, err
		}
		data, _ := json.Marshal(settings)
		c, err := scan(tx.QueryRow(ctx, "UPDATE connections SET settings=$3,created_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING "+columns, a.TrackingID(), id, data))
		if errors.Is(err, pgx.ErrNoRows) {
			return c, fault.NotFound
		}
		return c, err
	})
}

func (s Store) Disconnect(ctx context.Context, a identity.Actor, id string) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() || !identity.ValidUUID(id) {
			return fault.NotFound
		}
		tag, err := tx.Exec(ctx, "DELETE FROM connections WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			return fault.NotFound
		}
		return err
	})
}

// Authenticate resolves a webhook secret to its connection for an active account.
func (s Store) Authenticate(ctx context.Context, kind, raw string) (Connection, error) {
	if !valid(kind) || len(raw) != 48 || !strings.HasPrefix(raw, "fn_w_") {
		return Connection{}, fault.Unauthenticated
	}
	c, err := scan(s.Pool.QueryRow(ctx, "SELECT c."+strings.ReplaceAll(columns, ",", ",c.")+" FROM connections c JOIN tracking_profiles tracking ON tracking.id=c.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE c.token_hash=$1 AND c.kind=$2 AND u.active", digest(raw), kind))
	if err != nil {
		return Connection{}, fault.Unauthenticated
	}
	return c, nil
}

// Outcome records the last delivery result for display; it is not an audit trail.
func (s Store) Outcome(ctx context.Context, id, outcome string) {
	if r := []rune(outcome); len(r) > 200 {
		outcome = string(r[:200])
	}
	_, _ = s.Pool.Exec(ctx, "UPDATE connections SET last_outcome=$2,last_event_at=clock_timestamp() WHERE id=$1", id, outcome)
}
