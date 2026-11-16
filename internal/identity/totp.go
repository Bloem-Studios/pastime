package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// Authenticator recovery lets a person who forgot their password set a new one with a
// code from an authenticator app or a one-use recovery code. It is not a second sign-in
// factor. Secrets are encrypted with SECRET_KEY; recovery codes are kept as digests.

const totpStep = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

type TOTPStatus struct {
	Enabled           bool       `json:"enabled"`
	Pending           bool       `json:"pending"`
	ConfirmedAt       *time.Time `json:"confirmed_at"`
	RecoveryRemaining int        `json:"recovery_remaining"`
}

type TOTPSetup struct {
	Secret string `json:"secret"`
	URI    string `json:"otpauth_uri"`
}

func totpPurpose(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

func (s *Store) secretsReady() error {
	if !s.Secrets.Available() {
		return &fault.Error{Status: 503, Code: "secret_key_missing", Message: "Authenticator recovery needs SECRET_KEY to be set on the server."}
	}
	return nil
}

func (s *Store) TOTPStatus(ctx context.Context, a Actor) (TOTPStatus, error) {
	var out TOTPStatus
	if !a.Household() {
		return out, fault.Forbidden
	}
	var codes int
	err := s.Pool.QueryRow(ctx, "SELECT confirmed_at,cardinality(recovery_digests) FROM user_totp WHERE user_id=$1", a.User.ID).Scan(&out.ConfirmedAt, &codes)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	out.Enabled, out.Pending, out.RecoveryRemaining = out.ConfirmedAt != nil, out.ConfirmedAt == nil, codes
	return out, err
}

// BeginTOTP creates a new, unconfirmed secret after checking the current password. It
// replaces any earlier setup only once confirmed.
func (s *Store) BeginTOTP(ctx context.Context, a Actor, password, issuer string) (TOTPSetup, error) {
	var out TOTPSetup
	if !a.Household() {
		return out, fault.Forbidden
	}
	if err := s.secretsReady(); err != nil {
		return out, err
	}
	if err := s.checkPassword(ctx, a.User.ID, password); err != nil {
		return out, err
	}
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return out, err
	}
	box, err := s.Secrets.Seal(secret, totpPurpose(a.User.ID))
	if err != nil {
		return out, err
	}
	// A confirmed authenticator stays in force until the new one is confirmed.
	if _, err = s.Pool.Exec(ctx, `INSERT INTO user_totp(user_id,sealed_secret) VALUES($1,$2)
		ON CONFLICT (user_id) DO UPDATE SET sealed_secret=EXCLUDED.sealed_secret, confirmed_at=NULL, last_step=0, recovery_digests='{}'
		WHERE user_totp.confirmed_at IS NULL`, a.User.ID, box); err != nil {
		return out, err
	}
	var confirmed *time.Time
	if err = s.Pool.QueryRow(ctx, "SELECT confirmed_at FROM user_totp WHERE user_id=$1", a.User.ID).Scan(&confirmed); err != nil {
		return out, err
	}
	if confirmed != nil {
		return out, &fault.Error{Status: 409, Code: "totp_enabled", Message: "Turn off the current authenticator before setting up a new one."}
	}
	out.Secret = b32.EncodeToString(secret)
	label := url.PathEscape(issuer + ":" + a.User.Username)
	out.URI = "otpauth://totp/" + label + "?secret=" + out.Secret + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
	return out, nil
}

// ConfirmTOTP proves the app is set up and returns ten recovery codes, shown once.
func (s *Store) ConfirmTOTP(ctx context.Context, a Actor, code string) ([]string, error) {
	if !a.Household() {
		return nil, fault.Forbidden
	}
	if err := s.secretsReady(); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	secret, lastStep, confirmed, err := s.loadTOTP(ctx, tx, a.User.ID)
	if err != nil {
		return nil, err
	}
	if confirmed {
		return nil, &fault.Error{Status: 409, Code: "totp_enabled", Message: "The authenticator is already confirmed."}
	}
	step, ok := verifyTOTP(secret, code, time.Now(), lastStep)
	if !ok {
		return nil, fault.Invalid("That code is not valid. Check the time on your device and try again.")
	}
	codes := make([]string, 10)
	digests := make([][]byte, 10)
	for i := range codes {
		codes[i] = recoveryCode()
		digests[i] = Digest(normalizeCode(codes[i]))
	}
	if _, err = tx.Exec(ctx, "UPDATE user_totp SET confirmed_at=clock_timestamp(),last_step=$2,recovery_digests=$3 WHERE user_id=$1", a.User.ID, step, digests); err != nil {
		return nil, err
	}
	if err = Audit(ctx, tx, a, "totp.enabled", "user", strconv.FormatInt(a.User.ID, 10), nil); err != nil {
		return nil, err
	}
	return codes, tx.Commit(ctx)
}

// DisableTOTP removes the authenticator after checking the current password.
func (s *Store) DisableTOTP(ctx context.Context, a Actor, password string) error {
	if !a.Household() {
		return fault.Forbidden
	}
	if err := s.checkPassword(ctx, a.User.ID, password); err != nil {
		return err
	}
	tx, a, err := s.identityTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM user_totp WHERE user_id=$1", a.User.ID); err != nil {
		return err
	}
	if err = Audit(ctx, tx, a, "totp.disabled", "user", strconv.FormatInt(a.User.ID, 10), nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var recoveryFailed = &fault.Error{Status: 401, Code: "recovery_failed", Message: "The username or code is not valid."}

// RecoverPassword sets a new password with an authenticator or recovery code, then
// revokes every session, token and pending link, exactly like a password change.
// Unknown accounts, accounts without an authenticator and wrong codes fail alike. The
// new password is hashed only after the code is accepted, so wrong guesses cost no
// hashing work.
func (s *Store) RecoverPassword(ctx context.Context, username, code, password string) error {
	if err := s.secretsReady(); err != nil {
		return err
	}
	if n := utf8.RuneCountInString(password); n < 12 || n > 128 || len(password) > 512 {
		return fault.Invalid("Use a password with 12 to 128 characters.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID int64
	err = tx.QueryRow(ctx, "SELECT id FROM users WHERE username=$1 AND active FOR UPDATE", strings.ToLower(strings.TrimSpace(username))).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return recoveryFailed
	}
	if err != nil {
		return err
	}
	secret, lastStep, confirmed, err := s.loadTOTP(ctx, tx, userID)
	if errors.Is(err, fault.NotFound) || (err == nil && !confirmed) {
		return recoveryFailed
	}
	if err != nil {
		return err
	}
	method := "authenticator"
	if step, ok := verifyTOTP(secret, code, time.Now(), lastStep); ok {
		if _, err = tx.Exec(ctx, "UPDATE user_totp SET last_step=$2 WHERE user_id=$1", userID, step); err != nil {
			return err
		}
	} else {
		// A recovery code works once: it is removed as it is used.
		tag, err := tx.Exec(ctx, "UPDATE user_totp SET recovery_digests=array_remove(recovery_digests,$2) WHERE user_id=$1 AND $2=ANY(recovery_digests)", userID, Digest(normalizeCode(code)))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return recoveryFailed
		}
		method = "recovery_code"
	}
	hash, err := s.Passwords.Hash(ctx, password)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE users SET password_hash=$2,auth_epoch=auth_epoch+1,version=version+1 WHERE id=$1", userID, hash); err != nil {
		return err
	}
	if err = revokeAll(ctx, tx, userID); err != nil {
		return err
	}
	u, err := ScanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id=$1", userID))
	if err != nil {
		return err
	}
	if err = Audit(ctx, tx, Actor{Kind: "recovery", User: u}, "password.recovered", "user", strconv.FormatInt(userID, 10), map[string]any{"method": method}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) checkPassword(ctx context.Context, userID int64, password string) error {
	var hash string
	if err := s.Pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1", userID).Scan(&hash); err != nil {
		return fault.Unauthenticated
	}
	ok, err := s.Passwords.Verify(ctx, password, hash)
	if err != nil {
		return err
	}
	if !ok {
		return fault.Invalid("The current password is incorrect.")
	}
	return nil
}

func (s *Store) loadTOTP(ctx context.Context, tx pgx.Tx, userID int64) ([]byte, int64, bool, error) {
	var box []byte
	var lastStep int64
	var confirmed *time.Time
	err := tx.QueryRow(ctx, "SELECT sealed_secret,last_step,confirmed_at FROM user_totp WHERE user_id=$1 FOR UPDATE", userID).Scan(&box, &lastStep, &confirmed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, false, fault.NotFound
	}
	if err != nil {
		return nil, 0, false, err
	}
	secret, err := s.Secrets.Open(box, totpPurpose(userID))
	if err != nil {
		return nil, 0, false, fmt.Errorf("open totp secret: %w", err)
	}
	return secret, lastStep, confirmed != nil, nil
}

// verifyTOTP accepts a code from the previous, current or next step that is later than
// the last accepted step, so each code works once.
func verifyTOTP(secret []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / totpStep
	for _, step := range []int64{current - 1, current, current + 1} {
		if step <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

func recoveryCode() string {
	raw := make([]byte, 10)
	_, _ = rand.Read(raw)
	s := strings.ToLower(b32.EncodeToString(raw))
	return s[:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

func normalizeCode(code string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}
