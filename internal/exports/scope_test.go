package exports

import (
	"context"
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// A stored snapshot needs the scopes its format needs when exported directly: a
// library-only token cannot list or download a native snapshot, which holds the
// profile's private lists, but keeps its CSV snapshots.
func TestStoredSnapshotsRequireTheirFormatScopes(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool, Library: library.Store{Pool: f.Pool}}
	native, err := s.SnapshotNow(ctx, f.Owner, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	sheet, err := s.SnapshotNow(ctx, f.Owner, "csv")
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Library only", Scopes: []string{"library:read"}, ExpiresInDays: 30, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	token, err := f.Accounts.Authenticate(ctx, raw, "token")
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Files(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].ID != sheet.ID {
		t.Fatalf("library-only token listed %+v", files)
	}
	if _, body, err := s.Open(ctx, token, native.ID); !errors.Is(err, fault.Forbidden) {
		if body != nil {
			body.Close()
		}
		t.Fatal("library-only token opened a native snapshot", err)
	}
	_, body, err := s.Open(ctx, token, sheet.ID)
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
	if files, err = s.Files(ctx, f.Owner); err != nil || len(files) != 2 {
		t.Fatal("owner session snapshots", len(files), err)
	}
	if _, body, err = s.Open(ctx, f.Owner, native.ID); err != nil {
		t.Fatal(err)
	}
	body.Close()
}
