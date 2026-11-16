package identity_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func code(secret string, at time.Time) string {
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, raw)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	o := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[o:o+4])&0x7fffffff)%1000000)
}

func TestAuthenticatorRecoveryResetsPasswordAndRevokesAccess(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	f.Accounts.Secrets = box
	member := f.User(t, "totp_member", "member")
	_, raw := f.Login(t, "totp_member")
	if _, err := f.Accounts.BeginTOTP(ctx, member, "wrong password", "Pastime"); err == nil {
		t.Fatal("setup without the current password")
	}
	setup, err := f.Accounts.BeginTOTP(ctx, member, testkit.Password, "Pastime")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.ConfirmTOTP(ctx, member, "000000"); err == nil {
		t.Fatal("wrong code confirmed")
	}
	codes, err := f.Accounts.ConfirmTOTP(ctx, member, code(setup.Secret, time.Now().Add(-30*time.Second)))
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirm %v %v", codes, err)
	}
	status, _ := f.Accounts.TOTPStatus(ctx, member)
	if !status.Enabled || status.RecoveryRemaining != 10 {
		t.Fatalf("status %+v", status)
	}
	// A current code resets the password and ends existing sessions.
	if err = f.Accounts.RecoverPassword(ctx, "totp_member", code(setup.Secret, time.Now()), "a-new-synthetic-password"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(ctx, raw, "session"); err == nil {
		t.Fatal("old session survived recovery")
	}
	// The same code cannot be used twice.
	if err = f.Accounts.RecoverPassword(ctx, "totp_member", code(setup.Secret, time.Now()), "another-synthetic-password"); err == nil {
		t.Fatal("authenticator code replayed")
	}
	// Recovery codes work once each.
	if err = f.Accounts.RecoverPassword(ctx, "totp_member", codes[0], "third-synthetic-password"); err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.RecoverPassword(ctx, "totp_member", codes[0], "fourth-synthetic-password"); err == nil {
		t.Fatal("recovery code reused")
	}
	if _, _, err = f.Accounts.Login(ctx, "totp_member", "third-synthetic-password"); err != nil {
		t.Fatal("recovered password does not sign in", err)
	}
	// Accounts without an authenticator fail exactly like wrong codes.
	f.User(t, "plain_member", "member")
	if err = f.Accounts.RecoverPassword(ctx, "plain_member", "123456", "fifth-synthetic-password"); err == nil {
		t.Fatal("recovered an account without an authenticator")
	}
}
