package bloem

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type listGrant struct {
	Instance, Account, Profile string
	Locator                    ListLocator
	UID                        string
}
type nativeListSource struct {
	ID, ProfileID           int64
	UID, Family, Name, Kind string
}

func (s Store) sealList(value any, purpose string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sealed, err := s.Box.Seal(raw, "bloem.list."+purpose+".v1")
	return base64.RawURLEncoding.EncodeToString(sealed), err
}
func (s Store) openList(token, purpose string, value any) error {
	if len(token) > 16384 {
		return fault.Invalid("Invalid list authority token.")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return fault.Forbidden
	}
	plain, err := s.Box.Open(raw, "bloem.list."+purpose+".v1")
	if err != nil {
		return fault.Forbidden
	}
	if json.Unmarshal(plain, value) != nil {
		return fault.Forbidden
	}
	return nil
}

func nativeListTx(ctx context.Context, tx pgx.Tx, a SyncAuthority, g listGrant) (nativeListSource, identity.Actor, error) {
	var out nativeListSource
	var uid any
	if g.UID != "" {
		uid = g.UID
	}
	var err error
	switch g.Locator.Kind {
	case "personal":
		err = tx.QueryRow(ctx, `SELECT l.id,l.profile_id,l.source_uid::text,l.name,l.kind FROM personal_lists l
		WHERE l.profile_id=$1 AND (($3::uuid IS NULL AND l.id=$2::bigint) OR l.source_uid=$3) FOR SHARE`, a.ProfileID, nativeLocatorID(g.Locator), uid).Scan(&out.ID, &out.ProfileID, &out.UID, &out.Name, &out.Kind)
		out.Family = "personal"
	case "public":
		err = tx.QueryRow(ctx, `SELECT l.id,l.profile_id,l.source_uid::text,l.name,l.kind FROM personal_lists l
		JOIN tracking_profiles p ON p.id=l.profile_id AND p.archived_at IS NULL JOIN users u ON u.id=p.account_id AND u.active
		WHERE (($3::uuid IS NULL AND u.username=$1 AND (l.slug=$2 OR l.share_token=$2)) OR l.source_uid=$3)
		AND (l.visibility='public' OR (l.visibility='unlisted' AND l.share_token=$2 AND l.share_expires_at>clock_timestamp())) FOR SHARE OF l,p,u`, g.Locator.Username, g.Locator.Slug, uid).Scan(&out.ID, &out.ProfileID, &out.UID, &out.Name, &out.Kind)
		out.Family = "personal"
	case "collaborative":
		err = tx.QueryRow(ctx, `SELECT l.id,l.source_uid::text,l.name FROM collab_lists l
		JOIN collab_members m ON m.list_id=l.id AND m.user_id=$1 JOIN users u ON u.id=l.owner_id AND u.active
		WHERE (($3::uuid IS NULL AND l.id=$2::bigint) OR l.source_uid=$3) FOR SHARE OF l,m,u`, a.AccountID, nativeLocatorID(g.Locator), uid).Scan(&out.ID, &out.UID, &out.Name)
		out.Family = "collaborative"
		out.Kind = "collaborative"
		out.ProfileID = a.ProfileID
	default:
		return out, identity.Actor{}, fault.Forbidden
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, identity.Actor{}, fault.NotFound
	}
	if err != nil {
		return out, identity.Actor{}, err
	}
	reader, err := identity.ResolveWorkerProfile(ctx, tx, out.ProfileID, "bloem-list-export", []string{"library:read", "lists:read"}, true)
	return out, reader, err
}
func nativeLocatorID(l ListLocator) string {
	if l.NativeID == "" {
		return "0"
	}
	return l.NativeID
}

// Initial signer creation may race another preview under repeatable read.
// Retry only PostgreSQL's serialization/deadlock refusal, with fresh authority
// and snapshot each time; retain the one committed instance signing identity.
func (s Store) ResolveList(ctx context.Context, raw, origin string, in ListResolveInput) (ListResolution, error) {
	for attempt := 0; attempt < 4; attempt++ {
		out, err := s.resolveListOnce(ctx, raw, origin, in)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "40001" && pgerr.Code != "40P01" {
			return out, err
		}
		if ctx.Err() != nil {
			return ListResolution{}, ctx.Err()
		}
	}
	return ListResolution{}, fault.Conflict
}

func (s Store) resolveListOnce(ctx context.Context, raw, origin string, in ListResolveInput) (ListResolution, error) {
	var out ListResolution
	locator, err := ParseListLocator(origin, in.URL)
	if err != nil {
		return out, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	a, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		return out, err
	}
	g := listGrant{Instance: in.InstanceID, Account: in.AccountID, Profile: in.ProfileID, Locator: locator}
	native, reader, err := nativeListTx(ctx, tx, a, g)
	if err != nil {
		return out, err
	}
	entries, err := (library.Store{Pool: s.Pool}).ExportListTx(ctx, tx, reader, native.ID, native.Family == "collaborative")
	if err != nil {
		return out, err
	}
	g.UID = native.UID
	out.SourceID = native.Family + ":" + native.UID
	out.Name = native.Name
	out.Kind = native.Kind
	out.Count = len(entries)
	signingKey, err := s.listSigningKeyTx(ctx, tx, in.InstanceID)
	if err != nil {
		return out, err
	}
	out.SigningKey = base64.RawURLEncoding.EncodeToString(signingKey[ed25519.SeedSize:])
	out.ResolutionToken, err = s.sealList(g, "grant")
	if err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()+interval '120 seconds'`).Scan(&out.ExpiresAt); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s Store) listGrantTx(ctx context.Context, tx pgx.Tx, raw string, in ListSnapshotInput) (SyncAuthority, listGrant, nativeListSource, identity.Actor, error) {
	var g listGrant
	if err := s.openList(in.ResolutionToken, "grant", &g); err != nil {
		return SyncAuthority{}, g, nativeListSource{}, identity.Actor{}, err
	}
	if g.Instance != in.InstanceID || g.Account != in.AccountID || g.Profile != in.ProfileID {
		return SyncAuthority{}, g, nativeListSource{}, identity.Actor{}, fault.Forbidden
	}
	a, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		return a, g, nativeListSource{}, identity.Actor{}, err
	}
	native, reader, err := nativeListTx(ctx, tx, a, g)
	if err == nil && in.SourceID != native.Family+":"+native.UID {
		err = fault.Forbidden
	}
	return a, g, native, reader, err
}

type listCursor struct {
	ID, Instance, Account, Profile, Source string
	Position                               int
	ExpiresAt                              time.Time
}
