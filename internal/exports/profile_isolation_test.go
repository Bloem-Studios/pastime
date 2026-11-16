package exports

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestExportsAndWorkersUseProfileNotAccountID(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	s := Store{Pool: f.Pool, Library: lib}
	child, err := f.Accounts.CreateProfile(ctx, f.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	if child.ID == f.Owner.User.ID {
		t.Fatal("fixture needs different account/profile IDs")
	}
	selected, err := f.Accounts.SelectProfile(ctx, f.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []struct {
		child bool
		title string
	}{{false, "Main private book"}, {true, "Child private book"}} {
		a := f.Owner
		if in.child {
			a = selected
		}
		if _, err = lib.Create(ctx, a, testkit.Key(t, f), library.EntryInput{Title: in.title, MediaType: "book", Unit: "pages", Status: "planned"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, format := range []string{"csv", "jsonl"} {
		var out bytes.Buffer
		n, err := Write(ctx, lib, selected, format, &out)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || !strings.Contains(out.String(), "Child private book") || strings.Contains(out.String(), "Main private book") {
			t.Fatalf("%s export scope: %d %s", format, n, out.String())
		}
	}
	file, err := s.Snapshot(ctx, child.ID, "jsonl", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Open(ctx, f.Owner, file.ID); !errors.Is(err, fault.NotFound) {
		t.Fatalf("main downloaded child snapshot: %v", err)
	}
	_, body, err := s.Open(ctx, selected, file.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Child private book") || strings.Contains(string(data), "Main private book") {
		t.Fatal("worker exported account instead of profile")
	}
}
