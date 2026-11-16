package kosync

import (
	"context"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestReaderRejectsStalePositionAndNonReadingLink(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool, Library: library.Store{Pool: f.Pool}}
	if err := s.SetPassword(ctx, f.Owner, "synthetic-reader-password"); err != nil {
		t.Fatal(err)
	}
	p := Progress{Document: "0123456789abcdef0123456789abcdef", Progress: "page20", Percentage: .5, Timestamp: time.Now().Unix()}
	if _, err := s.Push(ctx, f.Owner.User.ID, p); err != nil {
		t.Fatal(err)
	}
	p.Timestamp--
	p.Progress = "page1"
	p.Percentage = .01
	if _, err := s.Push(ctx, f.Owner.User.ID, p); err == nil {
		t.Error("stale push accepted")
	}
	got, err := s.Pull(ctx, f.Owner.User.ID, p.Document)
	if err != nil || got.Progress != "page20" {
		t.Fatal("stale position replaced current", got, err)
	}
	m, err := s.Library.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Movie", MediaType: "movie", Unit: "minutes", Status: "planned"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Link(ctx, f.Owner, p.Document, fmt.Sprint(m.ID)); err == nil {
		t.Fatal("reader linked to movie")
	}
}

func TestReaderEqualTimestampConflictAndRotatedCredential(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool, Library: library.Store{Pool: f.Pool}}
	if err := s.SetPassword(ctx, f.Owner, "synthetic-reader-password"); err != nil {
		t.Fatal(err)
	}
	p := Progress{Document: "same-time", Progress: "page20", Percentage: .5, Timestamp: time.Now().Unix()}
	if _, err := s.Push(ctx, f.Owner.User.ID, p); err != nil {
		t.Fatal(err)
	}
	p.Progress = "page1"
	p.Percentage = .01
	if _, err := s.Push(ctx, f.Owner.User.ID, p); err == nil {
		t.Fatal("conflicting same-timestamp update accepted")
	}
	if _, err := s.Push(ctx, f.Owner.User.ID, p, "old-credential"); err != ErrUnauthorized {
		t.Fatalf("rotated key accepted: %v", err)
	}
	p.Timestamp++
	if _, err := s.Push(ctx, f.Owner.User.ID, p); err != nil {
		t.Fatalf("explicit newer rewind rejected: %v", err)
	}
}
