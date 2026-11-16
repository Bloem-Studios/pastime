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

// TrackingProfile owns personal tracking data. AccountID owns login authority.
type TrackingProfile struct {
	ID             int64      `json:"id,string"`
	AccountID      int64      `json:"account_id,string"`
	Name           string     `json:"name"`
	Default        bool       `json:"default"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	BloemAvailable bool       `json:"bloem_available"`
	BloemLinked    bool       `json:"bloem_linked"`
	BloemActive    bool       `json:"bloem_active"`
}

// TrackingID is deliberately zero for an unresolved actor: callers must resolve
// a profile, rather than reinterpret an account ID as tracking ownership.
func (a Actor) TrackingID() int64 { return a.profileID }

func resolveProfile(ctx context.Context, q queryer, a Actor, id int64, lock bool) (Actor, error) {
	sql := `SELECT id FROM tracking_profiles WHERE account_id=$1 AND archived_at IS NULL`
	args := []any{a.User.ID}
	if id == 0 {
		sql += ` AND is_default`
	} else {
		sql += ` AND id=$2`
		args = append(args, id)
	}
	if lock {
		sql += ` FOR SHARE`
	}
	if err := q.QueryRow(ctx, sql, args...).Scan(&a.profileID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Actor{}, fault.NotFound
		}
		return Actor{}, err
	}
	return a, nil
}

func (s *Store) SelectProfile(ctx context.Context, a Actor, id int64) (Actor, error) {
	if id < 1 {
		return Actor{}, fault.NotFound
	}
	if !a.Household() && id != a.profileID {
		return Actor{}, fault.Forbidden
	}
	return resolveProfile(ctx, s.Pool, a, id, false)
}

func (s *Store) Profiles(ctx context.Context, a Actor) ([]TrackingProfile, error) {
	if !a.Allows("profile:read") {
		return nil, fault.Forbidden
	}
	// A profile credential must not disclose the household's other profiles.
	boundID := int64(0)
	if !a.Household() {
		boundID = a.TrackingID()
		if boundID == 0 {
			return nil, fault.Forbidden
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.account_id,t.name,t.is_default,t.archived_at,t.created_at,
 EXISTS(SELECT 1 FROM bloem_instances WHERE active),
 EXISTS(SELECT 1 FROM bloem_profiles p JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE p.profile_id=t.id AND b.account_id=t.account_id),
 EXISTS(SELECT 1 FROM bloem_profiles p JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id JOIN bloem_instances i ON i.id=p.instance_id WHERE p.profile_id=t.id AND b.account_id=t.account_id AND p.active AND NOT p.excluded AND b.active AND i.active)
 FROM tracking_profiles t WHERE t.account_id=$1 AND t.archived_at IS NULL AND ($2::bigint=0 OR t.id=$2) ORDER BY t.is_default DESC,t.created_at,t.id`, a.User.ID, boundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrackingProfile{}
	for rows.Next() {
		var p TrackingProfile
		if err = rows.Scan(&p.ID, &p.AccountID, &p.Name, &p.Default, &p.ArchivedAt, &p.CreatedAt, &p.BloemAvailable, &p.BloemLinked, &p.BloemActive); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProfile(ctx context.Context, a Actor, name string) (TrackingProfile, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return TrackingProfile{}, fault.Invalid("Give the profile a name of 1 to 80 characters.")
	}
	tx, fresh, err := s.identityTx(ctx, a)
	if err != nil {
		return TrackingProfile{}, err
	}
	defer tx.Rollback(ctx)
	var out TrackingProfile
	err = tx.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,$2) RETURNING id,account_id,name,is_default,archived_at,created_at`, fresh.User.ID, name).Scan(&out.ID, &out.AccountID, &out.Name, &out.Default, &out.ArchivedAt, &out.CreatedAt)
	if err != nil {
		return out, err
	}
	if err = Audit(ctx, tx, fresh, "profile.created", "profile", strconv.FormatInt(out.ID, 10), map[string]any{"name": name}); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// ResolveWorkerProfile resolves only a server-side trusted profile identifier.
// HTTP callers must authenticate an account and use SelectProfile instead.
func ResolveWorkerProfile(ctx context.Context, q queryer, id int64, kind string, scopes []string, lock bool) (Actor, error) {
	sql := `SELECT ` + UserColumns + ` FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL AND u.active AND pastime_account_has_service_access(u.id)`
	if lock {
		sql += ` FOR SHARE OF p,u`
	}
	u, err := ScanUser(q.QueryRow(ctx, sql, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Actor{}, fault.Unauthenticated
	}
	if err != nil {
		return Actor{}, err
	}
	return Actor{User: u, Kind: kind, Scopes: scopes, profileID: id}, nil
}
