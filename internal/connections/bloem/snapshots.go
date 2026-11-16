package bloem

import (
	"bytes"
	"context"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type SnapshotPage struct {
	SnapshotID string           `json:"snapshot_id"`
	Cursor     string           `json:"cursor"`
	NextCursor string           `json:"next_cursor"`
	Complete   bool             `json:"complete"`
	Profiles   []ProvisionInput `json:"profiles"`
}
type PageReceipt struct {
	Receipt string `json:"receipt"`
}

func lockInstance(ctx context.Context, tx pgx.Tx, raw string) (string, error) {
	var instance string
	err := tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR UPDATE`, identity.Digest(raw)).Scan(&instance)
	return instance, authorityError(err)
}
func (s Store) AcceptPage(ctx context.Context, raw string, in SnapshotPage) (PageReceipt, error) {
	var out PageReceipt
	if !opaque(in.SnapshotID) || in.Cursor != "" && !opaque(in.Cursor) || in.NextCursor != "" && !opaque(in.NextCursor) || in.Complete != (in.NextCursor == "") || len(in.Profiles) > 100 || !in.Complete && len(in.Profiles) == 0 || in.NextCursor != "" && in.NextCursor == in.Cursor {
		return out, fault.Invalid("Invalid managed snapshot page.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	instance, err := lockInstance(ctx, tx, raw)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_snapshots WHERE instance_id=$1 AND expires_at<clock_timestamp()`, instance); err != nil {
		return out, err
	}
	var next string
	var pages int
	var complete bool
	err = tx.QueryRow(ctx, `SELECT next_cursor,pages,complete FROM bloem_snapshots WHERE instance_id=$1 AND snapshot_id=$2 FOR UPDATE`, instance, in.SnapshotID).Scan(&next, &pages, &complete)
	if errors.Is(err, pgx.ErrNoRows) {
		if in.Cursor != "" {
			return out, fault.Conflict
		}
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_snapshots WHERE instance_id=$1`, instance).Scan(&count); err != nil {
			return out, err
		}
		if count >= 16 {
			return out, fault.Conflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_snapshots(instance_id,snapshot_id) VALUES($1,$2)`, instance, in.SnapshotID); err != nil {
			return out, err
		}
	} else if err != nil {
		return out, err
	}
	digest := fingerprint(in)
	var before []byte
	err = tx.QueryRow(ctx, `SELECT receipt::text,payload_digest FROM bloem_snapshot_pages WHERE instance_id=$1 AND snapshot_id=$2 AND cursor=$3`, instance, in.SnapshotID, in.Cursor).Scan(&out.Receipt, &before)
	if err == nil {
		if !bytes.Equal(before, digest) {
			return out, fault.Conflict
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if complete || next != in.Cursor || pages >= 1000 {
		return out, fault.Conflict
	}
	var cycle bool
	if in.NextCursor != "" {
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_snapshot_pages WHERE instance_id=$1 AND snapshot_id=$2 AND cursor=$3)`, instance, in.SnapshotID, in.NextCursor).Scan(&cycle); err != nil {
			return out, err
		}
		if cycle {
			return out, fault.Conflict
		}
	}
	var maximum uint64
	for _, p := range in.Profiles {
		if err = validate(p); err != nil {
			return out, err
		}
		if p.InstanceID != instance {
			return out, fault.Forbidden
		}
		var ar, pr uint64
		var accountPrint, profilePrint []byte
		err = tx.QueryRow(ctx, `SELECT a.source_revision,p.source_revision,a.source_fingerprint,p.source_fingerprint FROM bloem_accounts a JOIN bloem_profiles p USING(instance_id,source_account_id) WHERE a.instance_id=$1 AND a.source_account_id=$2 AND p.source_profile_id=$3`, instance, p.AccountID, p.ProfileID).Scan(&ar, &pr, &accountPrint, &profilePrint)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, fault.Conflict
		}
		if err != nil {
			return out, err
		}
		ap := fingerprint(struct {
			Default, Name, Email, Issuer, Subject string
			Active                                bool
		}{p.DefaultProfileID, p.AccountName, p.Email, p.IdentityIssuer, p.IdentitySubject, p.AccountActive})
		pp := fingerprint(struct {
			Name   string
			Active bool
		}{p.Name, p.Active})
		if ar != p.AccountRevision || pr != p.Revision || !bytes.Equal(ap, accountPrint) || !bytes.Equal(pp, profilePrint) {
			return out, fault.Conflict
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_snapshot_profiles VALUES($1,$2,$3,$4)`, instance, in.SnapshotID, p.AccountID, p.ProfileID); err != nil {
			return out, constraint(err)
		}
		maximum = max(maximum, p.Revision, p.AccountRevision)
	}
	err = tx.QueryRow(ctx, `INSERT INTO bloem_snapshot_pages(instance_id,snapshot_id,cursor,payload_digest) VALUES($1,$2,$3,$4) RETURNING receipt::text`, instance, in.SnapshotID, in.Cursor, digest).Scan(&out.Receipt)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE bloem_snapshots SET next_cursor=$3,complete=$4,final_cursor=CASE WHEN $4 THEN $6 ELSE final_cursor END,pages=pages+1,max_revision=greatest(max_revision,$5) WHERE instance_id=$1 AND snapshot_id=$2`, instance, in.SnapshotID, in.NextCursor, in.Complete, maximum, in.Cursor)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s Store) CompleteSnapshot(ctx context.Context, raw, snapshot, receipt string) error {
	if !opaque(snapshot) || !opaque(receipt) {
		return fault.Invalid("Invalid snapshot receipt.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	instance, err := lockInstance(ctx, tx, raw)
	if err != nil {
		return err
	}
	var complete, finalized bool
	var maximum int64
	err = tx.QueryRow(ctx, `SELECT s.complete,s.finalized,s.max_revision FROM bloem_snapshots s JOIN bloem_snapshot_pages p USING(instance_id,snapshot_id) WHERE s.instance_id=$1 AND s.snapshot_id=$2 AND p.receipt::text=$3 AND s.expires_at>clock_timestamp() FOR UPDATE OF s`, instance, snapshot, receipt).Scan(&complete, &finalized, &maximum)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict
	}
	if err != nil {
		return err
	}
	if !complete {
		return fault.Conflict
	}
	// The receipt must belong to the final page, not an earlier traversal page.
	var finalReceipt string
	var finalCursor string
	if err = tx.QueryRow(ctx, `SELECT final_cursor FROM bloem_snapshots WHERE instance_id=$1 AND snapshot_id=$2`, instance, snapshot).Scan(&finalCursor); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, `SELECT receipt::text FROM bloem_snapshot_pages WHERE instance_id=$1 AND snapshot_id=$2 AND cursor=$3`, instance, snapshot, finalCursor).Scan(&finalReceipt); err != nil {
		return err
	}
	if finalReceipt != receipt {
		return fault.Conflict
	}
	if finalized {
		return tx.Commit(ctx)
	}
	// Keep mappings published after this source revision. Empty snapshots cannot
	// establish a revision ceiling and deliberately do not archive unknown rows.
	// Bindings created on the Connect Silo page are not the snapshot's to archive.
	_, err = tx.Exec(ctx, `WITH missing AS (UPDATE bloem_profiles p SET active=false WHERE p.instance_id=$1 AND p.origin='provisioned' AND p.source_revision<=$3 AND $3>0 AND NOT EXISTS(SELECT 1 FROM bloem_snapshot_profiles x WHERE x.instance_id=p.instance_id AND x.snapshot_id=$2 AND x.source_account_id=p.source_account_id AND x.source_profile_id=p.source_profile_id) RETURNING profile_id,owns_profile), revoke AS (UPDATE bloem_sync_credentials c SET revoked_at=LEAST(revoked_at,clock_timestamp()) FROM missing m WHERE c.instance_id=$1 AND c.profile_id=m.profile_id) UPDATE tracking_profiles t SET archived_at=coalesce(t.archived_at,clock_timestamp()) FROM missing m WHERE t.id=m.profile_id AND m.owns_profile AND NOT t.is_default`, instance, snapshot, maximum)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE bloem_snapshots SET finalized=true WHERE instance_id=$1 AND snapshot_id=$2`, instance, snapshot); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
