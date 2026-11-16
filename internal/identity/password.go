package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"golang.org/x/crypto/argon2"
)

const hashPrefix = "$argon2id$v=19$m=32768,t=2,p=1$"

type Passwords struct {
	slots chan struct{}
	dummy string
}

func NewPasswords() *Passwords {
	p := &Passwords{slots: make(chan struct{}, 4)}
	p.dummy = encodePassword("nonexistent-account-dummy-password")
	return p
}
func encodePassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic("secure randomness unavailable")
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 32768, 1, 32)
	return hashPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
}

// acquire waits briefly for a hashing slot so a short burst queues instead of failing.
// Sustained saturation is still refused rather than growing an unbounded backlog.
func (p *Passwords) acquire(ctx context.Context) error {
	wait := time.NewTimer(2 * time.Second)
	defer wait.Stop()
	select {
	case p.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fault.Unavailable
	case <-wait.C:
		return fault.Limited
	}
}
func (p *Passwords) Hash(ctx context.Context, password string) (string, error) {
	n := utf8.RuneCountInString(password)
	if n < 12 || n > 128 || len(password) > 512 {
		return "", fault.Invalid("Use a password with 12 to 128 characters.")
	}
	if err := p.acquire(ctx); err != nil {
		return "", err
	}
	defer func() { <-p.slots }()
	return encodePassword(password), nil
}
func (p *Passwords) Verify(ctx context.Context, password, encoded string) (bool, error) {
	if err := p.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-p.slots }()
	if len(password) > 512 {
		return false, nil
	}
	hasPassword := encoded != ""
	if !hasPassword {
		encoded = p.dummy
	}
	if !strings.HasPrefix(encoded, hashPrefix) || len(encoded) > 200 {
		return false, nil
	}
	parts := strings.Split(strings.TrimPrefix(encoded, hashPrefix), "$")
	if len(parts) != 2 {
		return false, nil
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[0])
	hash, e2 := base64.RawStdEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(hash) != 32 {
		return false, nil
	}
	candidate := argon2.IDKey([]byte(password), salt, 2, 32768, 1, 32)
	return subtle.ConstantTimeCompare(hash, candidate) == 1 && hasPassword, nil
}
