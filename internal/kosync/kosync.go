// Package kosync is a KOReader progress sync server. Devices sign in with a username
// and a separate KOReader password; a document's position is stored as reported and,
// once the document is linked to a title in the library, moves that title forward.
package kosync

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool    *pgxpool.Pool
	Library library.Store
}

// Progress is one document's position as KOReader reports it.
type Progress struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp"`
}

// Document is a synced document as the owner sees it.
type Document struct {
	Document   string    `json:"document"`
	Percentage float64   `json:"percentage"`
	Device     string    `json:"device"`
	EntryID    *string   `json:"entry_id"`
	EntryTitle *string   `json:"entry_title"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Status struct {
	Configured bool       `json:"configured"`
	Username   string     `json:"username"`
	Documents  []Document `json:"documents"`
}

// ErrUnauthorized is returned for an unknown username or wrong key.
var ErrUnauthorized = errors.New("kosync: unauthorized")

// keyDigest stores what KOReader sends: the MD5 of the password, as lowercase hex.
func keyDigest(key string) []byte {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(key))))
	return h[:]
}

func ValidDocument(d string) bool {
	if len(d) == 0 || len(d) > 128 {
		return false
	}
	for _, r := range d {
		if r <= ' ' || r > '~' || r == '/' {
			return false
		}
	}
	return true
}

func (s Store) Status(ctx context.Context, a identity.Actor) (Status, error) {
	out := Status{Username: a.User.Username, Documents: []Document{}}
	var isDefault bool
	if err := s.Pool.QueryRow(ctx, "SELECT is_default FROM tracking_profiles WHERE id=$1 AND account_id=$2", a.TrackingID(), a.User.ID).Scan(&isDefault); err != nil {
		return out, err
	}
	if !isDefault {
		out.Username += "#" + strconv.FormatInt(a.TrackingID(), 10)
	}
	if !a.Browser() {
		return out, fault.Forbidden
	}
	if err := s.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM kosync_accounts k JOIN users u ON u.id=k.user_id WHERE k.profile_id=$1 AND "+liveKey+")", a.TrackingID()).Scan(&out.Configured); err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT d.document,d.percentage,d.device,d.entry_id::text,e.title,d.updated_at FROM kosync_documents d
		LEFT JOIN library_entries e ON e.profile_id=d.profile_id AND e.id=d.entry_id WHERE d.profile_id=$1 ORDER BY d.updated_at DESC LIMIT 200`, a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.Document, &d.Percentage, &d.Device, &d.EntryID, &d.EntryTitle, &d.UpdatedAt); err != nil {
			return out, err
		}
		out.Documents = append(out.Documents, d)
	}
	return out, rows.Err()
}

// SetPassword sets the password KOReader signs in with. It is separate from the account
// password, since KOReader stores and sends it with weak hashing.
func (s Store) SetPassword(ctx context.Context, a identity.Actor, password string) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		if len([]rune(password)) < 8 || len(password) > 200 {
			return fault.Invalid("Use a KOReader password of 8 to 200 characters, different from your account password.")
		}
		sum := md5.Sum([]byte(password))
		_, err := tx.Exec(ctx, `INSERT INTO kosync_accounts(profile_id,key_digest) VALUES($1,$2) ON CONFLICT (profile_id) DO UPDATE SET key_digest=EXCLUDED.key_digest,created_at=clock_timestamp()`,
			a.TrackingID(), keyDigest(hex.EncodeToString(sum[:])))
		return err
	})
}

func (s Store) Disable(ctx context.Context, a identity.Actor) error {
	return identity.WriteOnly(ctx, s.Pool, a, "", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Browser() {
			return fault.Forbidden
		}
		_, err := tx.Exec(ctx, "DELETE FROM kosync_accounts WHERE profile_id=$1", a.TrackingID())
		return err
	})
}

// liveKey holds while a KOReader key may sign its account (u) in: an active account
// with tracking access under the service-access policy. Password change, recovery,
// revoke-access and role or status changes delete the key (identity.revokeAll).
const liveKey = "(u.active AND pastime_account_has_service_access(u.id))"

// Authenticate checks KOReader's x-auth-user and x-auth-key headers.
func (s Store) Authenticate(ctx context.Context, username, key string) (int64, error) {
	var id int64
	var selectedID int64
	username = strings.TrimSpace(username)
	if base, suffix, ok := strings.Cut(username, "#"); ok {
		parsed, err := identity.ParseID(suffix)
		if err != nil {
			return 0, ErrUnauthorized
		}
		username = base
		selectedID = parsed
	}
	var digest []byte
	err := s.Pool.QueryRow(ctx, `SELECT p.id,k.key_digest FROM users u JOIN tracking_profiles p ON p.account_id=u.id AND (($2::bigint=0 AND p.is_default) OR p.id=$2) AND p.archived_at IS NULL JOIN kosync_accounts k ON k.profile_id=p.id WHERE lower(u.username)=lower($1) AND `+liveKey, username, selectedID).Scan(&id, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		keyDigest(key) // Comparable work for unknown names.
		return 0, ErrUnauthorized
	}
	if err != nil {
		return 0, err
	}
	if subtle.ConstantTimeCompare(digest, keyDigest(key)) != 1 {
		return 0, ErrUnauthorized
	}
	return id, nil
}

// Push stores a position and moves a linked title forward.
func (s Store) Push(ctx context.Context, userID int64, p Progress, credential ...string) (Progress, error) {
	if !ValidDocument(p.Document) || p.Percentage < 0 || p.Percentage > 1 || math.IsNaN(p.Percentage) || p.Timestamp < 0 || p.Timestamp > time.Now().Add(5*time.Minute).Unix() || len(p.Progress) > 500 || len(p.Device) > 100 || len(p.DeviceID) > 100 {
		return p, fault.Invalid("Invalid progress document.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return p, err
	}
	defer tx.Rollback(ctx)
	var active bool
	var digest []byte
	if err = tx.QueryRow(ctx, "SELECT "+liveKey+",k.key_digest FROM tracking_profiles p JOIN users u ON u.id=p.account_id JOIN kosync_accounts k ON k.profile_id=p.id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u,k", userID).Scan(&active, &digest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return p, ErrUnauthorized
		}
		return p, err
	}
	if !active || (len(credential) > 0 && subtle.ConstantTimeCompare(digest, keyDigest(credential[0])) != 1) {
		return p, ErrUnauthorized
	}
	var entryID *int64
	var at time.Time
	err = tx.QueryRow(ctx, `INSERT INTO kosync_documents(profile_id,document,progress,percentage,device,device_id,source_timestamp) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (profile_id,document) DO UPDATE SET progress=EXCLUDED.progress,percentage=EXCLUDED.percentage,device=EXCLUDED.device,device_id=EXCLUDED.device_id,updated_at=clock_timestamp(),source_timestamp=GREATEST(kosync_documents.source_timestamp,EXCLUDED.source_timestamp)
		WHERE EXCLUDED.source_timestamp=0 OR EXCLUDED.source_timestamp>kosync_documents.source_timestamp
 OR (EXCLUDED.source_timestamp=kosync_documents.source_timestamp AND EXCLUDED.progress=kosync_documents.progress AND EXCLUDED.percentage=kosync_documents.percentage)
		RETURNING entry_id,updated_at`, userID, p.Document, p.Progress, p.Percentage, p.Device, p.DeviceID, p.Timestamp).Scan(&entryID, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, fault.Conflict
	}
	if err != nil {
		return p, err
	}
	if entryID != nil {
		if _, err = s.Library.ReaderProgressTx(ctx, tx, userID, *entryID, p.Percentage); err != nil {
			return p, err
		}
	}
	p.Timestamp = at.Unix()
	return p, tx.Commit(ctx)
}

// Pull returns the last stored position, or nil when the document is unknown.
func (s Store) Pull(ctx context.Context, userID int64, document string, credential ...string) (*Progress, error) {
	if !ValidDocument(document) {
		return nil, nil
	}
	var p Progress
	var digest []byte
	var at time.Time
	err := s.Pool.QueryRow(ctx, "SELECT d.document,d.progress,d.percentage,d.device,d.device_id,d.updated_at,k.key_digest FROM kosync_documents d JOIN tracking_profiles tracking ON tracking.id=d.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id JOIN kosync_accounts k ON k.profile_id=d.profile_id WHERE d.profile_id=$1 AND d.document=$2 AND "+liveKey, userID, document).
		Scan(&p.Document, &p.Progress, &p.Percentage, &p.Device, &p.DeviceID, &at, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err == nil && len(credential) > 0 && subtle.ConstantTimeCompare(digest, keyDigest(credential[0])) != 1 {
		return nil, ErrUnauthorized
	}
	p.Timestamp = at.Unix()
	return &p, err
}

// Link ties a document to a title in the library, or unlinks it when entryID is empty,
// and applies the latest position right away.
func (s Store) Link(ctx context.Context, a identity.Actor, document, entryID string) error {
	if !a.Browser() {
		return fault.Forbidden
	}
	var id *int64
	if entryID != "" {
		n, err := strconv.ParseInt(entryID, 10, 64)
		if err != nil {
			return fault.NotFound
		}
		id = &n
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, ""); err != nil {
		return err
	}
	var percentage float64
	err = tx.QueryRow(ctx, `UPDATE kosync_documents d SET entry_id=$3 WHERE d.profile_id=$1 AND d.document=$2
 AND ($3::bigint IS NULL OR EXISTS(SELECT 1 FROM library_entries e WHERE e.profile_id=$1 AND e.id=$3 AND e.media_type IN ('book','audiobook','comic','manga'))) RETURNING percentage`, a.TrackingID(), document, id).Scan(&percentage)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	if id != nil {
		if _, err = s.Library.ReaderProgressTx(ctx, tx, a.TrackingID(), *id, percentage); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
