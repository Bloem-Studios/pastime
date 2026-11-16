package identity

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// Device sign-in lets an app on a TV or phone get an API token without a keyboard
// login: the app starts a request, a person signed in elsewhere approves its short
// code, and the app's next poll receives the token once.

const (
	deviceLifetime = 10 * time.Minute
	deviceInterval = 5
	// Token lifetime for a device: long enough not to nag, revocable at any time.
	deviceTokenDays = 365
)

// userCodeAlphabet avoids vowels (no words) and look-alike characters.
const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

// DeviceStart is what the app shows: the code, where to enter it, and how often to poll.
type DeviceStart struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

// DeviceRequest is a pending request as the approving person sees it.
type DeviceRequest struct {
	ClientName string    `json:"client_name"`
	Scopes     []string  `json:"scopes"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// DevicePoll is the app's answer: pending, slow_down, approved (with the token, once),
// denied or expired.
type DevicePoll struct {
	Status   string   `json:"status"`
	Interval int      `json:"interval"`
	Token    *string  `json:"access_token,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

func newUserCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	out := make([]byte, 0, 9)
	for i, v := range b {
		if i == 4 {
			out = append(out, '-')
		}
		// 256 is not a multiple of 20; reject the biased tail rather than skew codes.
		for v >= 240 {
			var one [1]byte
			if _, err := rand.Read(one[:]); err != nil {
				panic("secure randomness unavailable")
			}
			v = one[0]
		}
		out = append(out, userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return string(out)
}

// normalizeUserCode accepts the code in any case, with or without its dash or spaces.
func normalizeUserCode(code string) string {
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	if len(code) != 8 || strings.Trim(code, userCodeAlphabet) != "" {
		return ""
	}
	return code[:4] + "-" + code[4:]
}

// StartDevice opens a request for an app. Scopes default to every member scope.
func (s *Store) StartDevice(ctx context.Context, clientName string, scopes []string) (DeviceStart, error) {
	clientName = strings.TrimSpace(clientName)
	if n := len([]rune(clientName)); n < 1 || n > 60 {
		return DeviceStart{}, fault.Invalid("Name the app with 1 to 60 characters.")
	}
	if len(scopes) == 0 {
		scopes = Scopes
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		known := false
		for _, allowed := range Scopes {
			known = known || scope == allowed
		}
		if !known || seen[scope] {
			return DeviceStart{}, fault.Invalid("Choose unique supported scopes.")
		}
		seen[scope] = true
	}
	// Expired requests are removed as new ones arrive, so the table stays small.
	if _, err := s.Pool.Exec(ctx, "DELETE FROM device_authorizations WHERE expires_at < clock_timestamp() - interval '1 hour'"); err != nil {
		return DeviceStart{}, err
	}
	out := DeviceStart{DeviceCode: randomToken("pd_"), ExpiresIn: int(deviceLifetime.Seconds()), Interval: deviceInterval}
	for range 3 {
		out.UserCode = newUserCode()
		_, err := s.Pool.Exec(ctx, `INSERT INTO device_authorizations(device_code_hash,user_code_hash,client_name,scopes,expires_at) VALUES($1::bytea,$2::bytea,$3::text,$4::text[],clock_timestamp()+$5::int*interval '1 second')`,
			Digest(out.DeviceCode), Digest(out.UserCode), clientName, scopes, out.ExpiresIn)
		if err == nil {
			return out, nil
		}
		if !strings.Contains(err.Error(), "device_authorizations_user_code_hash_key") {
			return DeviceStart{}, err
		}
	}
	return DeviceStart{}, fault.Unavailable
}

var deviceCodeInvalid = &fault.Error{Status: 404, Code: "device_code_invalid", Message: "That code is not valid or has expired."}

// LookupDevice shows a pending request to the signed-in person entering its code.
func (s *Store) LookupDevice(ctx context.Context, a Actor, userCode string) (DeviceRequest, error) {
	var out DeviceRequest
	if !a.Browser() {
		return out, fault.Forbidden
	}
	code := normalizeUserCode(userCode)
	if code == "" {
		return out, deviceCodeInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT client_name,scopes,expires_at FROM device_authorizations WHERE user_code_hash=$1::bytea AND status='pending' AND expires_at>clock_timestamp()`, Digest(code)).Scan(&out.ClientName, &out.Scopes, &out.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, deviceCodeInvalid
	}
	return out, err
}

// DecideDevice approves or denies a pending request for the signed-in account.
func (s *Store) DecideDevice(ctx context.Context, a Actor, userCode string, approve bool) error {
	code := normalizeUserCode(userCode)
	if code == "" {
		return deviceCodeInvalid
	}
	tx, a, err := s.browserTx(ctx, a)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// A profile person's approval yields a token for their profile; the
	// account's keeps the default profile.
	var profile *int64
	if !a.Household() {
		id := a.TrackingID()
		profile = &id
	}
	status := "denied"
	if approve {
		status = "approved"
	}
	var id, client string
	err = tx.QueryRow(ctx, `UPDATE device_authorizations SET status=$2::text,user_id=CASE WHEN $2::text='approved' THEN $3::bigint END,
		approved_epoch=CASE WHEN $2::text='approved' THEN (SELECT auth_epoch FROM users WHERE id=$3::bigint) END,
		profile_id=CASE WHEN $2::text='approved' THEN $4::bigint END
		WHERE user_code_hash=$1::bytea AND status='pending' AND expires_at>clock_timestamp() RETURNING id::text,client_name`, Digest(code), status, a.User.ID, profile).Scan(&id, &client)
	if errors.Is(err, pgx.ErrNoRows) {
		return deviceCodeInvalid
	}
	if err != nil {
		return err
	}
	if err = Audit(ctx, tx, a, "device."+status, "device", id, map[string]any{"client": client}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PollDevice answers the app. After approval the first poll mints the token and later
// polls see the request as expired: the token is handed out exactly once.
func (s *Store) PollDevice(ctx context.Context, deviceCode string) (DevicePoll, error) {
	if !strings.HasPrefix(deviceCode, "pd_") || len(deviceCode) > 100 {
		return DevicePoll{}, deviceCodeInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return DevicePoll{}, err
	}
	defer tx.Rollback(ctx)
	var id, status, client string
	var scopes []string
	var userID, epoch, profile *int64
	var interval int
	var expired, tooSoon bool
	err = tx.QueryRow(ctx, `SELECT id::text,status,client_name,scopes,user_id,approved_epoch,profile_id,poll_interval,expires_at<=clock_timestamp(),
		COALESCE(last_poll_at > clock_timestamp() - poll_interval*interval '1 second', false)
		FROM device_authorizations WHERE device_code_hash=$1::bytea FOR UPDATE`, Digest(deviceCode)).Scan(&id, &status, &client, &scopes, &userID, &epoch, &profile, &interval, &expired, &tooSoon)
	if errors.Is(err, pgx.ErrNoRows) {
		return DevicePoll{}, deviceCodeInvalid
	}
	if err != nil {
		return DevicePoll{}, err
	}
	out := DevicePoll{Interval: interval}
	switch {
	case status == "issued" || expired:
		out.Status = "expired"
	case status == "denied":
		out.Status = "denied"
	case tooSoon:
		// Polling faster than asked: back off by five seconds, as RFC 8628 describes.
		out.Status, out.Interval = "slow_down", min(interval+5, 60)
	case status == "pending":
		out.Status = "pending"
	case status == "approved":
		raw := randomToken("fn_k_")
		var tokenID string
		var username string
		err = tx.QueryRow(ctx, `INSERT INTO api_tokens(user_id,name,token_hash,scopes,auth_epoch,expires_at,profile_id)
			SELECT u.id,left('Device: '||$1::text,80),$2::bytea,$3::text[],u.auth_epoch,clock_timestamp()+$6::int*interval '1 day',$7::bigint FROM users u
			WHERE u.id=$4::bigint AND u.active AND u.auth_epoch=$5::bigint
			  AND (SELECT count(*) FROM api_tokens t WHERE t.user_id=u.id AND t.revoked_at IS NULL AND t.expires_at>clock_timestamp())<20
			RETURNING id::text,(SELECT username FROM users WHERE id=$4::bigint)`, client, Digest(raw), scopes, *userID, *epoch, deviceTokenDays, profile).Scan(&tokenID, &username)
		if errors.Is(err, pgx.ErrNoRows) {
			// Signed out everywhere, deactivated, or at the token limit since approval.
			out.Status = "denied"
			status = "denied"
			break
		}
		if err != nil {
			return DevicePoll{}, err
		}
		if err = Audit(ctx, tx, Actor{Kind: "device", User: User{ID: *userID, Username: username}}, "token.created", "token", tokenID, map[string]any{"device": id}); err != nil {
			return DevicePoll{}, err
		}
		out.Status, out.Token, out.Scopes, status = "approved", &raw, scopes, "issued"
	}
	if _, err = tx.Exec(ctx, "UPDATE device_authorizations SET last_poll_at=clock_timestamp(),poll_interval=$2::int,status=$3::text WHERE id=$1::uuid", id, out.Interval, status); err != nil {
		return DevicePoll{}, err
	}
	return out, tx.Commit(ctx)
}
