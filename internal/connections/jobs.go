package connections

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// ScrobbleJob matches and records one delivered viewing. Matching can take several
// provider calls, so webhooks acknowledge after the durable enqueue instead of waiting.
const ScrobbleJob = "scrobble"

type scrobblePayload struct {
	ConfigurationAt time.Time         `json:"configuration_at"`
	EventID         string            `json:"event_id,omitempty"`
	ConnectionID    string            `json:"connection_id"`
	ProfileID       string            `json:"profile_id"`
	Source          string            `json:"source"`
	Reference       catalog.Reference `json:"reference"`
	PlayedAt        time.Time         `json:"played_at"`
	Plan            bool              `json:"plan,omitempty"`
}

// Enqueue stores the event durably. Repeated deliveries are harmless: the scrobble
// window suppresses a second play of the same item.
func (s Store) Enqueue(ctx context.Context, c Connection, ev Event) error {
	if len(ev.EventID) > 200 {
		return fault.Invalid("Event identifiers must be at most 200 characters.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var authorized bool
	err = tx.QueryRow(ctx, "SELECT u.active FROM connections c JOIN tracking_profiles tracking ON tracking.id=c.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE c.id=$1 AND c.profile_id=$2 AND c.kind=$3 AND c.created_at=$4 FOR SHARE OF c,u", c.ID, c.ProfileID, c.Kind, c.CreatedAt).Scan(&authorized)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !authorized) {
		return fault.Unauthenticated
	}
	if err != nil {
		return err
	}
	eventKey := ""
	if ev.EventID != "" {
		digest := sha256.Sum256([]byte(c.ID + "\x00" + ev.EventID))
		intentRaw, _ := json.Marshal(struct {
			Reference catalog.Reference
			Plan      bool
		}{ev.Reference, ev.Plan})
		intent := sha256.Sum256(intentRaw)
		tag, err := tx.Exec(ctx, "INSERT INTO webhook_receipts(connection_id,event_digest,intent_digest) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", c.ID, digest[:], intent[:])
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var prior []byte
			if err = tx.QueryRow(ctx, "SELECT intent_digest FROM webhook_receipts WHERE connection_id=$1 AND event_digest=$2", c.ID, digest[:]).Scan(&prior); err != nil {
				return err
			}
			if !bytes.Equal(prior, intent[:]) {
				return fault.Conflict
			}
			return tx.Commit(ctx)
		}
		eventKey = "webhook:" + hex.EncodeToString(digest[:])
	}
	dedupe := eventKey
	if dedupe == "" {
		b := make([]byte, 12)
		if _, err = rand.Read(b); err != nil {
			return err
		}
		dedupe = "scrobble:" + hex.EncodeToString(b)
	}
	payload, err := json.Marshal(scrobblePayload{ConfigurationAt: c.CreatedAt, EventID: eventKey, ConnectionID: c.ID, ProfileID: strconv.FormatInt(c.ProfileID, 10), Source: c.Kind, Reference: ev.Reference, PlayedAt: ev.PlayedAt, Plan: ev.Plan})
	if err != nil {
		return err
	}
	if _, err = jobs.Enqueue(ctx, tx, ScrobbleJob, dedupe, "idempotent", payload, 5); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Handler returns the worker handler. Provider outages fail the attempt so the queue
// retries; a reference that cannot be matched is recorded on the connection and dropped.
// An episode the provider does not list yet counts as unmatched: the queue's retries
// end within minutes, long before a catalog refresh could list it.
func (s Store) Handler(cat *catalog.Service, lib library.Store) func(context.Context, json.RawMessage) error {
	return func(ctx context.Context, raw json.RawMessage) error {
		var p scrobblePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return jobs.ErrInvalidPayload
		}
		userID, err := strconv.ParseInt(p.ProfileID, 10, 64)
		if err != nil || userID < 1 || p.ConfigurationAt.IsZero() {
			return jobs.ErrInvalidPayload
		}
		m, err := cat.MatchVideo(ctx, p.Reference)
		if err != nil {
			var fe *fault.Error
			if errors.As(err, &fe) && fe.Status == 404 || catalog.NotListedYet(err) {
				s.Outcome(ctx, p.ConnectionID, "Not matched: "+describe(p.Reference))
				return nil
			}
			return err
		}
		if p.Plan {
			added, err := lib.Plan(ctx, userID, p.ConnectionID, m.Title.ID, p.ConfigurationAt)
			if errors.Is(err, fault.Unauthenticated) {
				return nil
			}
			if err != nil {
				return err
			}
			outcome := "Planned " + describe(p.Reference)
			if !added {
				outcome = "Already tracking " + describe(p.Reference)
			}
			s.Outcome(ctx, p.ConnectionID, outcome)
			return nil
		}
		target := m.Title.ID
		if m.Episode != nil {
			target = m.Episode.ID
		}
		recorded, err := lib.ScrobbleEvent(ctx, userID, p.ConnectionID, p.Source, m.Title.ID, target, p.PlayedAt, p.EventID, p.ConfigurationAt)
		if errors.Is(err, fault.Unauthenticated) {
			return nil // The account or connection is gone; nothing may be recorded.
		}
		if err != nil {
			return err
		}
		outcome := "Recorded " + describe(p.Reference)
		if !recorded {
			outcome = "Already recorded " + describe(p.Reference)
		}
		s.Outcome(ctx, p.ConnectionID, outcome)
		return nil
	}
}

func describe(r catalog.Reference) string {
	if r.Kind == "episode" {
		out := r.ShowTitle
		if r.Season != nil && r.Episode != nil {
			out += " S" + strconv.Itoa(*r.Season) + "E" + strconv.Itoa(*r.Episode)
		}
		return out
	}
	return r.Title
}
