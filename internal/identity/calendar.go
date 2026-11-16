package identity

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// FeedStatus reports whether the caller has a calendar subscription secret.
func (s *Store) FeedStatus(ctx context.Context, a Actor) (*time.Time, error) {
	if !a.Browser() {
		return nil, fault.Forbidden
	}
	var created time.Time
	err := s.Pool.QueryRow(ctx, "SELECT created_at FROM calendar_feeds WHERE profile_id=$1", a.TrackingID()).Scan(&created)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// RotateFeed issues a new read-only calendar secret, replacing any previous one.
func (s *Store) RotateFeed(ctx context.Context, a Actor) (string, error) {
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	raw := randomToken("fn_c_")
	if _, err = tx.Exec(ctx, `INSERT INTO calendar_feeds(profile_id,token_hash) VALUES($1,$2)
		ON CONFLICT(profile_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,created_at=clock_timestamp()`, a.TrackingID(), Digest(raw)); err != nil {
		return "", err
	}
	if err = Audit(ctx, tx, a, "calendar_feed.rotate", "user", strconv.FormatInt(a.User.ID, 10), nil); err != nil {
		return "", err
	}
	return raw, tx.Commit(ctx)
}

func (s *Store) RevokeFeed(ctx context.Context, a Actor) error {
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM calendar_feeds WHERE profile_id=$1", a.TrackingID()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FeedActor authenticates a calendar secret as a read-only actor. It is checked against
// the current account state on every fetch, so disabling the account stops the feed.
func (s *Store) FeedActor(ctx context.Context, raw string) (Actor, error) {
	if len(raw) != 48 || !strings.HasPrefix(raw, "fn_c_") {
		return Actor{}, fault.Unauthenticated
	}
	return authenticateFeed(ctx, s.Pool, Digest(raw), false)
}

func authenticateFeed(ctx context.Context, q queryer, hash []byte, lock bool) (Actor, error) {
	var profileID int64
	err := q.QueryRow(ctx, "SELECT profile_id FROM calendar_feeds WHERE token_hash=$1", hash).Scan(&profileID)
	if err != nil {
		return Actor{}, fault.Unauthenticated
	}
	a, err := ResolveWorkerProfile(ctx, q, profileID, "feed", []string{"library:read"}, lock)
	if err != nil {
		return Actor{}, err
	}
	if lock {
		var present int
		if err = q.QueryRow(ctx, "SELECT 1 FROM calendar_feeds WHERE profile_id=$1 AND token_hash=$2 FOR SHARE", profileID, hash).Scan(&present); err != nil {
			return Actor{}, fault.Unauthenticated
		}
	}
	a.Hash = hash
	return a, nil
}
