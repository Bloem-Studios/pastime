package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

const accountLock int64 = 7351942001

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,47}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var Scopes = []string{"profile:read", "library:read", "library:write", "lists:read", "lists:write"}

type User struct {
	ID            int64      `json:"id,string"`
	Username      string     `json:"username"`
	DisplayName   string     `json:"display_name"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	Active        bool       `json:"active"`
	PasswordReady bool       `json:"password_ready"`
	Version       int        `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at"`
}

// Actor kinds. Compare through Browser and Household rather than with these
// strings, so every call site states which of the two trust levels it means.
const (
	// KindSession is a browser session of the account itself: the household's
	// primary user, trusted with every profile and household setting.
	KindSession = "session"
	// KindProfileSession is a browser session opened with a profile login. It
	// acts only as that one profile.
	KindProfileSession = "profile_session"
	KindToken          = "token"
	KindFeed           = "feed"
)

type Actor struct {
	profileID int64
	// lastUsed is when a session was last marked used; see Store.Authenticate.
	lastUsed *time.Time
	// profileLoginID, loginName and profileName are set only for a profile session.
	profileLoginID int64
	loginName      string
	profileName    string
	User           User
	Kind           string
	CredentialID   string
	Hash           []byte
	CSRF           string
	Scopes         []string
}

// Browser reports a cookie session of either kind: CSRF-protected, and trusted
// with everything its profile may do.
func (a Actor) Browser() bool { return a.Kind == KindSession || a.Kind == KindProfileSession }

// Household reports the account's own session, which may act for every
// profile and for the household as a whole.
func (a Actor) Household() bool { return a.Kind == KindSession }

// ProfileLoginID is the profile login a profile session was opened with, or 0.
func (a Actor) ProfileLoginID() int64 {
	if a.Kind != KindProfileSession {
		return 0
	}
	return a.profileLoginID
}

// PublicUser is the account as this actor may see it. A profile session sees
// its own login and profile name, never the household's email or role.
func (a Actor) PublicUser() User {
	if a.Kind != KindProfileSession {
		return a.User
	}
	u := a.User
	u.Username = a.loginName
	u.DisplayName = a.profileName
	u.Email = ""
	u.Role = "member"
	u.PasswordReady = true
	// When the primary user last signed in is theirs, not the profile's.
	u.LastLoginAt = nil
	return u
}

func (a Actor) Allows(scope string) bool {
	if a.Browser() {
		return true
	}
	for _, s := range a.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}
func (a Actor) IsAdmin() bool {
	return a.Household() && (a.User.Role == "owner" || a.User.Role == "admin")
}
func (a Actor) CanManage(role string) bool {
	return a.IsAdmin() && (a.User.Role == "owner" || role == "member")
}

type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
	// UserAgent is what the browser sent when it signed in; empty for older sessions.
	UserAgent string `json:"user_agent"`
	// LastUsedAt is accurate to a few minutes; null until a session issued before it was
	// recorded is used again.
	LastUsedAt *time.Time `json:"last_used_at"`
}
type Token struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Revoked   bool      `json:"revoked"`
	// ProfileID is the profile the token acts for.
	ProfileID int64 `json:"profile_id,string"`
}
type Invitation struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Role           string    `json:"role"`
	RecipientEmail string    `json:"recipient_email"`
	Username       string    `json:"username"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type InvitePreview struct {
	Kind      string    `json:"kind"`
	Role      string    `json:"role"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}
type AuditEvent struct {
	ID         int64  `json:"id,string"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	TargetKind string `json:"target_kind"`
	TargetID   string `json:"target_id"`
	// TargetLabel names the target for people: the current username of an account target.
	TargetLabel string    `json:"target_label"`
	CreatedAt   time.Time `json:"created_at"`
}

func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}
func Digest(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func csrf(token string) string {
	h := hmac.New(sha256.New, []byte(token))
	h.Write([]byte("pastime/csrf/v1"))
	return hex.EncodeToString(h.Sum(nil))
}
func ValidateUsername(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if !usernamePattern.MatchString(v) {
		return "", fault.Invalid("Username must contain 3 to 48 letters, numbers, dots, underscores or hyphens.")
	}
	return v, nil
}
func ValidateEmail(v string) error {
	if v == "" {
		return nil
	}
	p, e := mail.ParseAddress(v)
	if e != nil || p.Address != v || len(v) > 254 {
		return fault.Invalid("Enter a valid email address.")
	}
	return nil
}

// ValidateLoginName accepts a username or an email address for a profile
// login, lowercased. The address is only a name: it is never verified or mailed.
func ValidateLoginName(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if usernamePattern.MatchString(v) {
		return v, nil
	}
	if strings.Contains(v, "@") && len(v) <= 254 && ValidateEmail(v) == nil {
		return v, nil
	}
	return "", fault.Invalid("Use 3 to 48 letters, numbers, dots, underscores or hyphens, or an email address.")
}
func ValidRole(v string) bool { return v == "member" || v == "admin" || v == "owner" }
func ValidUUID(v string) bool { return uuidPattern.MatchString(v) }
func ParseID(v string) (int64, error) {
	id, e := strconv.ParseInt(v, 10, 64)
	if e != nil || id < 1 || strconv.FormatInt(id, 10) != v {
		return 0, fault.NotFound
	}
	return id, nil
}
