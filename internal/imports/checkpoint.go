package imports

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/jackc/pgx/v5"
	"time"
)

// Persist the fetched records and their cursor in the same transaction. Retrying a
// crashed run consumes this batch, never fetches past unapplied history.
func (s Store) stageLinked(ctx context.Context, runID string, userID, linkID int64, cfg LinkConfig, state map[string]any, records []Record) error {
	if records == nil {
		records = []Record{}
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if cfg.Push {
		if start, _ := state["push_started"].(string); start == "" {
			if legacy, _ := state["pushed_after"].(string); legacy != "" {
				state["push_started"] = legacy
			} else {
				state["push_started"] = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if err = s.saveLinkStateTx(ctx, tx, userID, linkID, cfg, state); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO import_batches(run_id,profile_id,link_id,generation,records) VALUES($1,$2,$3,$4,$5) ON CONFLICT(run_id) DO NOTHING`, runID, userID, linkID, cfg.Generation, data)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	if cfg.Push {
		payload, _ := json.Marshal(pushPayload{ProfileID: userID, LinkID: linkID, Generation: cfg.Generation})
		if _, err = jobs.Enqueue(ctx, tx, PushJob, "playback_push:"+runID, "idempotent", payload, 8); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s Store) saveLinkStateTx(ctx context.Context, tx pgx.Tx, userID, linkID int64, cfg LinkConfig, state map[string]any) error {
	var active bool
	if err := tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u", userID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return fault.Unauthenticated
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	owner, err := identity.ResolveWorkerProfile(ctx, tx, userID, "import", nil, true)
	if err != nil {
		return err
	}
	box, err := s.Box.Seal(raw, linkPurpose(owner.User.ID))
	if err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "UPDATE service_links SET sealed_config=$3,state=$4 WHERE profile_id=$1 AND id=$2 AND generation=$5", userID, linkID, box, data, cfg.Generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return nil
}
