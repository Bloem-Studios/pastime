package bloem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
)

// PlaybackEvent carries explicit source authority and ordering. Catalog IDs are
// intentionally absent: the HTTP adapter must resolve external media references.
type PlaybackEvent struct {
	Version         int       `json:"version"`
	InstanceID      string    `json:"instance_id"`
	AccountID       string    `json:"account_id"`
	ProfileID       string    `json:"profile_id"`
	EntityID        string    `json:"entity_id"`
	EventID         string    `json:"event_id"`
	Revision        uint64    `json:"revision"`
	Operation       string    `json:"operation"`
	OccurredAt      time.Time `json:"occurred_at"`
	Completed       bool      `json:"completed"`
	ConsumptionID   string    `json:"consumption_id,omitempty"`
	PositionSeconds int       `json:"position_seconds"`
	DurationSeconds int       `json:"duration_seconds"`
}
type EventResult struct {
	Status       string `json:"status"`
	PlayRecorded bool   `json:"play_recorded"`
}

// ValidateEvent rejects unsupported payloads before catalog lookups.
func ValidateEvent(e PlaybackEvent) error {
	if e.Version != 1 || !opaque(e.InstanceID) || !opaque(e.AccountID) || !opaque(e.ProfileID) || !opaque(e.EntityID) || !opaque(e.EventID) || e.Revision == 0 || e.Revision > math.MaxInt64 || e.OccurredAt.IsZero() || e.OccurredAt.Before(time.Unix(0, 0)) || e.OccurredAt.After(time.Now().Add(5*time.Minute)) || e.PositionSeconds < 0 || e.PositionSeconds > 10000000 || e.DurationSeconds < 1 || e.DurationSeconds > 10000000 {
		return fault.Invalid("Invalid managed playback event.")
	}
	if e.Operation != "start" && e.Operation != "pause" && e.Operation != "stop" {
		return fault.Invalid("Unsupported managed playback operation.")
	}
	if e.Completed && (e.Operation != "stop" || !opaque(e.ConsumptionID)) || !e.Completed && e.ConsumptionID != "" {
		return fault.Invalid("Invalid managed playback completion.")
	}
	return nil
}

// ApplyMatchedEvent is backend-only. titleID/targetID come from trusted catalog
// resolution, never from JSON. The authority, receipt and effect share one tx.
func (s Store) ApplyMatchedEvent(ctx context.Context, raw string, e PlaybackEvent, titleID, targetID int64) (EventResult, error) {
	out := EventResult{Status: "applied"}
	if err := ValidateEvent(e); err != nil {
		return out, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	authority, err := authenticateSyncTx(ctx, tx, raw)
	if err != nil {
		return out, err
	}
	if e.InstanceID != authority.InstanceID || e.AccountID != authority.SourceAccountID || e.ProfileID != authority.SourceProfileID {
		return out, fault.Forbidden
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.'||$1::bigint::text,0))`, authority.ProfileID); err != nil {
		return out, err
	}
	digest := fingerprint(struct {
		Event         PlaybackEvent
		Title, Target int64
	}{e, titleID, targetID})
	var previous []byte
	var previousResult string
	err = tx.QueryRow(ctx, `SELECT payload_digest,result FROM bloem_event_receipts WHERE instance_id=$1 AND profile_id=$2 AND event_id=$3`, authority.InstanceID, authority.ProfileID, e.EventID).Scan(&previous, &previousResult)
	if err == nil {
		if !bytes.Equal(previous, digest) {
			return out, fault.Conflict
		}
		out.Status = previousResult
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND root_id=$2)`, targetID, titleID).Scan(&valid); err != nil {
		return out, err
	}
	if !valid {
		return out, fault.NotFound
	}
	statePrint := fingerprint(struct {
		Operation          string
		Completed          bool
		Position, Duration int
		Target             int64
	}{e.Operation, e.Completed, e.PositionSeconds, e.DurationSeconds, targetID})
	var revision int64
	var statePrintBefore []byte
	var previousTarget int64
	err = tx.QueryRow(ctx, `SELECT source_revision,payload_digest,target_catalog_id FROM bloem_entity_states WHERE instance_id=$1 AND profile_id=$2 AND entity_id=$3 FOR UPDATE`, authority.InstanceID, authority.ProfileID, e.EntityID).Scan(&revision, &statePrintBefore, &previousTarget)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	// statePrint includes the target, so one revision cannot name two targets. A
	// newer revision may move the entity: a metadata correction can resolve the
	// same source item to a different catalog target.
	// Two viewings stamped with the same moment (an imported play count whose plays
	// share the last played date) are both plays: the second leaves the state as the
	// first set it and is still counted below. Anything else at the same revision with
	// a different state is a conflict.
	sameMoment := e.Revision == uint64(revision) && !bytes.Equal(statePrint, statePrintBefore)
	if sameMoment && !e.Completed {
		return out, fault.Conflict
	}
	if sameMoment || olderRevision(e.Revision, revision) {
		out.Status = "stale"
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO bloem_entity_states(instance_id,profile_id,entity_id,target_catalog_id,source_revision,payload_digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(instance_id,profile_id,entity_id) DO UPDATE SET target_catalog_id=EXCLUDED.target_catalog_id,source_revision=EXCLUDED.source_revision,payload_digest=EXCLUDED.payload_digest`, authority.InstanceID, authority.ProfileID, e.EntityID, targetID, int64(e.Revision), statePrint)
		if err != nil {
			return out, err
		}
		if revision > 0 && previousTarget != targetID {
			// The entity left its previous target. Drop that target's integration
			// resume point unless another integration entity of this profile still
			// resolves there; progress from other sources is never touched.
			_, err = tx.Exec(ctx, `DELETE FROM playback_progress WHERE profile_id=$1 AND catalog_item_id=$2 AND source='bloem' AND NOT EXISTS(SELECT 1 FROM bloem_entity_states s JOIN bloem_profiles p ON p.instance_id=s.instance_id AND p.profile_id=s.profile_id WHERE p.profile_id=$1 AND s.target_catalog_id=$2)`, authority.ProfileID, previousTarget)
			if err != nil {
				return out, err
			}
		}
		if e.Completed {
			// A completion clears the resume point it finished, but not one updated
			// well after it: an old history export that reaches Pastime late, or the
			// first plugin 0.2.0 event on an entity, must not remove the progress of a
			// later viewing. The margin absorbs clock differences between hosts.
			_, err = tx.Exec(ctx, `DELETE FROM playback_progress WHERE profile_id=$1 AND catalog_item_id=$2 AND updated_at<=$3::timestamptz+make_interval(secs=>$4::double precision)`, authority.ProfileID, targetID, e.OccurredAt, completionClockMargin.Seconds())
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO playback_progress(profile_id,catalog_item_id,position_seconds,duration_seconds,source) VALUES($1,$2,$3,$4,'bloem') ON CONFLICT(profile_id,catalog_item_id) DO UPDATE SET position_seconds=EXCLUDED.position_seconds,duration_seconds=EXCLUDED.duration_seconds,source=EXCLUDED.source,updated_at=clock_timestamp()`, authority.ProfileID, targetID, e.PositionSeconds, e.DurationSeconds)
		}
		if err != nil {
			return out, err
		}
	}
	if e.Completed {
		// The stable consumption ID alone identifies one viewing. Its live stop and
		// history export carry different timestamps and may resolve to a corrected
		// target, so any later delivery is an idempotent replay of the recorded
		// play. The digest records the first delivery's provenance only.
		playPrint := fingerprint(struct {
			Title, Target int64
			At            time.Time
		}{titleID, targetID, e.OccurredAt})
		var recorded bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_consumption_receipts WHERE instance_id=$1 AND profile_id=$2 AND consumption_id=$3)`, authority.InstanceID, authority.ProfileID, e.ConsumptionID).Scan(&recorded)
		if err != nil {
			return out, err
		}
		if !recorded {
			a, err := identity.ResolveWorkerProfile(ctx, tx, authority.ProfileID, "connection", []string{"library:write"}, true)
			if err != nil {
				return out, err
			}
			// Source names retain Pastime's existing schema format. Instance and stable
			// consumption provenance live in the external ID, shared with history import.
			externalID := fmt.Sprintf("%x", fingerprint([]string{authority.InstanceID, e.ConsumptionID}))
			out.PlayRecorded, err = (library.Store{Pool: s.Pool}).ScrobbleTx(ctx, tx, a, "bloem", titleID, targetID, e.OccurredAt, externalID)
			if err != nil {
				return out, err
			}
			_, err = tx.Exec(ctx, `INSERT INTO bloem_consumption_receipts(instance_id,profile_id,consumption_id,payload_digest) VALUES($1,$2,$3,$4)`, authority.InstanceID, authority.ProfileID, e.ConsumptionID, playPrint)
			if err != nil {
				return out, err
			}
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_event_receipts(instance_id,profile_id,event_id,payload_digest,result) VALUES($1,$2,$3,$4,$5)`, authority.InstanceID, authority.ProfileID, e.EventID, digest, out.Status)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
