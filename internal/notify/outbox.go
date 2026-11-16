package notify

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// Delivery is at least once: a destination may receive a repeat if the process dies
// after its HTTP acknowledgement but before PostgreSQL records success. Completed
// destinations never repeat merely because another destination failed.
func (s Store) deliverPending(ctx context.Context, userID int64) error {
	var failures error
	var after int64
	for range 200 {
		var id, target int64
		var attempt int
		var raw []byte
		err := s.Pool.QueryRow(ctx, `WITH candidate AS (
   SELECT d.id FROM notification_deliveries d JOIN notification_targets t ON t.id=d.target_id JOIN tracking_profiles tracking ON tracking.id=d.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
   WHERE d.profile_id=$1 AND d.id>$2 AND d.delivered_at IS NULL AND (d.leased_until IS NULL OR d.leased_until<clock_timestamp())
    AND t.enabled AND u.active
   ORDER BY d.id FOR UPDATE OF d SKIP LOCKED LIMIT 1)
   UPDATE notification_deliveries d SET leased_until=clock_timestamp()+interval '2 minutes',attempts=attempts+1
   FROM candidate c WHERE d.id=c.id RETURNING d.id,d.target_id,d.notice,d.attempts`, userID, after).Scan(&id, &target, &raw, &attempt)
		if errors.Is(err, pgx.ErrNoRows) {
			// A crashed sender may still own an unexpired lease. Keep the job
			// retryable until every eligible destination has acknowledged delivery.
			var pending bool
			err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries d JOIN notification_targets t ON t.id=d.target_id JOIN tracking_profiles tracking ON tracking.id=d.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id WHERE d.profile_id=$1 AND d.delivered_at IS NULL AND t.enabled AND u.active)`, userID).Scan(&pending)
			if pending {
				return errors.Join(failures, err, fault.Unavailable)
			}
			return errors.Join(failures, err)
		}
		if err != nil {
			return errors.Join(failures, err)
		}
		after = id
		var n Notice
		err = json.Unmarshal(raw, &n)
		if err == nil {
			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err = s.deliver(sendCtx, userID, target, n)
			cancel()
		}
		_, recordErr := s.Pool.Exec(ctx, `UPDATE notification_deliveries SET leased_until=NULL,
   delivered_at=CASE WHEN $3 THEN clock_timestamp() ELSE delivered_at END WHERE id=$1 AND attempts=$2`, id, attempt, err == nil)
		failures = errors.Join(failures, err, recordErr)
		if ctx.Err() != nil {
			return errors.Join(failures, ctx.Err())
		}
	}
	// Yield the bounded batch and ask the durable job to continue.
	return errors.Join(failures, fault.Unavailable)
}
