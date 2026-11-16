package bloem

import (
	"bytes"
	"context"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
)

type PersonalEvent struct {
	Version    int       `json:"version"`
	InstanceID string    `json:"instance_id"`
	AccountID  string    `json:"account_id"`
	ProfileID  string    `json:"profile_id"`
	EntityID   string    `json:"entity_id"`
	EventID    string    `json:"event_id"`
	Revision   uint64    `json:"revision"`
	Operation  string    `json:"operation"`
	OccurredAt time.Time `json:"occurred_at"`
	Rating     *int      `json:"rating,omitempty"`
	Position   *int32    `json:"position,omitempty"`
}

func ValidatePersonalEvent(e PersonalEvent) (library.PersonalState, error) {
	in := library.PersonalState{Rating: e.Rating, Position: e.Position}
	if e.Version != 1 || !opaque(e.InstanceID) || !opaque(e.AccountID) || !opaque(e.ProfileID) || !opaque(e.EntityID) || !opaque(e.EventID) || e.Revision == 0 || e.Revision > math.MaxInt64 || e.OccurredAt.IsZero() || e.OccurredAt.Before(time.Unix(0, 0)) || e.OccurredAt.After(time.Now().Add(5*time.Minute)) {
		return in, fault.Invalid("Invalid managed personal state.")
	}
	switch e.Operation {
	case "favorite_add":
		in.Kind = "favorite"
		in.Present = true
	case "favorite_remove":
		in.Kind = "favorite"
	case "watchlist_add":
		in.Kind = "watchlist"
		in.Present = true
	case "watchlist_remove":
		in.Kind = "watchlist"
	case "rating_set":
		in.Kind = "rating"
		in.Present = true
	case "rating_remove":
		in.Kind = "rating"
	default:
		return in, fault.Invalid("Unsupported managed personal state.")
	}
	// Bloem exports whole stars as even values on the provider's ten-point scale.
	if in.Kind == "rating" && (in.Present && (e.Rating == nil || *e.Rating < 2 || *e.Rating > 10 || *e.Rating%2 != 0) || !in.Present && e.Rating != nil) || in.Kind != "rating" && e.Rating != nil || e.Position != nil && (in.Kind != "watchlist" || !in.Present || *e.Position < 0) {
		return in, fault.Invalid("Unrepresentable managed personal state.")
	}
	return in, nil
}
func (s Store) ApplyMatchedPersonalEvent(ctx context.Context, raw string, e PersonalEvent, titleID int64) (EventResult, error) {
	out := EventResult{Status: "applied"}
	in, err := ValidatePersonalEvent(e)
	if err != nil {
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
		Event PersonalEvent
		Title int64
	}{e, titleID})
	var old []byte
	var result string
	err = tx.QueryRow(ctx, `SELECT payload_digest,result FROM bloem_event_receipts WHERE instance_id=$1 AND profile_id=$2 AND event_id=$3`, authority.InstanceID, authority.ProfileID, e.EventID).Scan(&old, &result)
	if err == nil {
		if !bytes.Equal(old, digest) {
			return out, fault.Conflict
		}
		out.Status = result
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	statePrint := fingerprint(struct {
		State library.PersonalState
		Title int64
	}{in, titleID})
	var revision int64
	var oldKind string
	err = tx.QueryRow(ctx, `SELECT source_revision,payload_digest,kind FROM bloem_personal_entity_states WHERE instance_id=$1 AND profile_id=$2 AND entity_id=$3 FOR UPDATE`, authority.InstanceID, authority.ProfileID, e.EntityID).Scan(&revision, &old, &oldKind)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if revision > 0 && oldKind != in.Kind || e.Revision == uint64(revision) && !bytes.Equal(old, statePrint) {
		return out, fault.Conflict
	}
	if olderRevision(e.Revision, revision) {
		out.Status = "stale"
	} else {
		a, err := identity.ResolveWorkerProfile(ctx, tx, authority.ProfileID, "connection", []string{"library:write"}, true)
		if err != nil {
			return out, err
		}
		if in.Kind == "rating" {
			var current *int
			err = tx.QueryRow(ctx, `SELECT rating FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2 FOR SHARE`, authority.ProfileID, titleID).Scan(&current)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return out, err
			}
			if current != nil && (*current == 0 || *current%2 != 0) {
				return out, &fault.Error{Status: 409, Code: "rating_precision", Message: "This Pastime rating has no exact Silo equivalent; change it explicitly in Pastime before synchronizing."}
			}
		}
		var newerNative bool
		if in.Kind == "rating" {
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2 AND scored_at>$3)`, authority.ProfileID, titleID, e.OccurredAt).Scan(&newerNative)
		} else {
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2 AND kind=$3 AND source<>'bloem' AND changed_at>=$4)`, authority.ProfileID, titleID, in.Kind, e.OccurredAt).Scan(&newerNative)
		}
		if err != nil {
			return out, err
		}
		changed := false
		if newerNative {
			out.Status = "stale"
		} else {
			changed, err = (library.Store{Pool: s.Pool}).ApplyPersonalStateTx(ctx, tx, a, titleID, in, e.OccurredAt, "bloem")
		}

		if err != nil {
			return out, err
		}
		if !changed && !newerNative {
			out.Status = "no_change"
		}
		_, err = tx.Exec(ctx, `INSERT INTO bloem_personal_entity_states(instance_id,profile_id,entity_id,kind,target_catalog_id,source_revision,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(instance_id,profile_id,entity_id) DO UPDATE SET target_catalog_id=EXCLUDED.target_catalog_id,source_revision=EXCLUDED.source_revision,payload_digest=EXCLUDED.payload_digest`, authority.InstanceID, authority.ProfileID, e.EntityID, in.Kind, titleID, int64(e.Revision), statePrint)
		if err != nil {
			return out, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_event_receipts(instance_id,profile_id,event_id,payload_digest,result) VALUES($1,$2,$3,$4,$5)`, authority.InstanceID, authority.ProfileID, e.EventID, digest, out.Status)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
