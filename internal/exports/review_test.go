package exports

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestMonthlyScheduleClampsAndRestoresDay(t *testing.T) {
	jan := time.Date(2024, 1, 31, 12, 0, 0, 0, time.UTC)
	feb := next("monthly", jan, 31)
	march := next("monthly", feb, 31)
	if feb.Month() != time.February || feb.Day() != 29 || march.Month() != time.March || march.Day() != 31 {
		t.Fatal(feb, march)
	}
}
func TestScheduledSnapshotReplay(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := Store{Pool: f.Pool, Library: library.Store{Pool: f.Pool}}
	due := time.Now().UTC()
	first, err := s.snapshot(ctx, f.Owner.User.ID, "jsonl", "schedule", 5, &due)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.snapshot(ctx, f.Owner.User.ID, "jsonl", "schedule", 5, &due)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatal("retry created a second snapshot")
	}
}
