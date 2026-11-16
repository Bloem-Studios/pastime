package kosync

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestKOReaderProfileCredentialIsolation(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Reader")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	const password = "synthetic-reader-profile-password"
	if err = s.SetPassword(ctx, selected, password); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, selected)
	if err != nil {
		t.Fatal(err)
	}
	hash := md5.Sum([]byte(password))
	key := hex.EncodeToString(hash[:])
	actual, err := s.Authenticate(ctx, status.Username, key)
	if err != nil || actual != child.ID {
		t.Fatal("reader credential failed to resolve child", actual, err)
	}
	if _, err = s.Authenticate(ctx, f.Owner.User.Username, key); err == nil {
		t.Fatal("child credential accepted for default profile")
	}
}
