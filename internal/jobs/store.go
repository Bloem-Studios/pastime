// Package jobs provides durable, fenced work ownership. Cache state is never authoritative.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidPayload = errors.New("invalid job payload")
	ErrLeaseLost      = errors.New("job lease is no longer owned")
	ErrDedupeConflict = errors.New("dedupe key was used for different work")
	codePattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Store struct{ Pool *pgxpool.Pool }
type Job struct {
	ID             string
	Kind           string
	Payload        json.RawMessage
	PayloadVersion int
	ReplayPolicy   string
	LeaseToken     string
	Attempts       int
	MaxAttempts    int
}

// Enqueue can participate in a caller's domain transaction. The caller must
// keep receipt keys for the required replay horizon; deleting jobs loses deduplication.
func Enqueue(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, kind, key, policy string, payload json.RawMessage, maxAttempts int) (string, error) {
	if !codePattern.MatchString(kind) || len(key) < 1 || len(key) > 200 || maxAttempts < 1 || maxAttempts > 20 || (policy != "idempotent" && policy != "reconcile") {
		return "", errors.New("invalid job definition")
	}
	var obj map[string]json.RawMessage
	if len(payload) > 64<<10 || json.Unmarshal(payload, &obj) != nil || obj == nil {
		return "", errors.New("job payload must be an object of at most 64 KiB")
	}
	var id string
	err := q.QueryRow(ctx, `INSERT INTO jobs (kind, dedupe_key, replay_policy, payload, max_attempts)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (dedupe_key) DO UPDATE SET dedupe_key = jobs.dedupe_key
		WHERE jobs.kind = EXCLUDED.kind AND jobs.replay_policy = EXCLUDED.replay_policy
		AND jobs.payload = EXCLUDED.payload AND jobs.max_attempts = EXCLUDED.max_attempts
		RETURNING id::text`, kind, key, policy, payload, maxAttempts).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrDedupeConflict
	}
	return id, err
}

// Recover handles a bounded batch. Uncertain non-idempotent work is blocked,
// never blindly replayed after a worker disappears.
func (s Store) Recover(ctx context.Context) (int64, error) {
	r, err := s.Pool.Exec(ctx, `WITH expired AS (
		SELECT id FROM jobs WHERE status='running' AND lease_until <= clock_timestamp()
		ORDER BY lease_until, id LIMIT 100 FOR UPDATE SKIP LOCKED
	) UPDATE jobs j SET
		status = CASE WHEN replay_policy='reconcile' THEN 'blocked'
			WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
		lease_token=NULL, lease_until=NULL, run_after=clock_timestamp(),
		last_error_code='lease_expired', updated_at=clock_timestamp()
		FROM expired e WHERE j.id=e.id`)
	return r.RowsAffected(), err
}

func (s Store) Claim(ctx context.Context, lease time.Duration) (*Job, error) {
	return s.ClaimKinds(ctx, lease, nil)
}

// ClaimKinds reserves capacity for latency-sensitive work; nil accepts any kind.
func (s Store) ClaimKinds(ctx context.Context, lease time.Duration, kinds []string) (*Job, error) {
	if lease < time.Second || lease > 5*time.Minute {
		return nil, errors.New("lease must be between one second and five minutes")
	}
	j := &Job{}
	err := s.Pool.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM jobs WHERE status='queued' AND run_after <= clock_timestamp() AND attempts < max_attempts AND ($2::text[] IS NULL OR kind=ANY($2))
		ORDER BY run_after, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	) UPDATE jobs j SET status='running', attempts=attempts+1,
		lease_token=gen_random_uuid(), lease_until=clock_timestamp()+($1 * interval '1 millisecond'),
		updated_at=clock_timestamp() FROM candidate c WHERE j.id=c.id
		RETURNING j.id::text, kind, payload, replay_policy, lease_token::text, attempts, max_attempts, payload_version`, lease.Milliseconds(), kinds).
		Scan(&j.ID, &j.Kind, &j.Payload, &j.ReplayPolicy, &j.LeaseToken, &j.Attempts, &j.MaxAttempts, &j.PayloadVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return j, err
}

func (s Store) Heartbeat(ctx context.Context, j Job, lease time.Duration) error {
	if lease < time.Second || lease > 5*time.Minute {
		return errors.New("invalid lease duration")
	}
	r, err := s.Pool.Exec(ctx, `UPDATE jobs SET lease_until=clock_timestamp()+($3 * interval '1 millisecond'), updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until > clock_timestamp()`, j.ID, j.LeaseToken, lease.Milliseconds())
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s Store) Complete(ctx context.Context, j Job) error {
	r, err := s.Pool.Exec(ctx, `UPDATE jobs SET status='succeeded', lease_token=NULL, lease_until=NULL, last_error_code=NULL, updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until > clock_timestamp()`, j.ID, j.LeaseToken)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s Store) Fail(ctx context.Context, j Job, code string) error {
	if !codePattern.MatchString(code) {
		return errors.New("failure details must be a non-secret error code")
	}
	r, err := s.Pool.Exec(ctx, `UPDATE jobs SET
		status=CASE WHEN replay_policy='reconcile' THEN 'blocked'
			WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
		run_after=clock_timestamp() + (LEAST(300, attempts * attempts) * interval '1 second'),
		lease_token=NULL, lease_until=NULL, last_error_code=$3, updated_at=clock_timestamp()
		WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until > clock_timestamp()`, j.ID, j.LeaseToken, code)
	if err != nil {
		return err
	}
	if r.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}
