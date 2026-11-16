package jobs

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/testdb"
	"log/slog"
	"testing"
	"time"
)

func TestPriorityClaimSkipsBulkQueue(t *testing.T) {
	pool := testdb.Isolated(t)
	s := Store{Pool: pool}
	ctx := context.Background()
	if _, err := Enqueue(ctx, pool, "import", "synthetic-import", "idempotent", json.RawMessage(`{}`), 3); err != nil {
		t.Fatal(err)
	}
	want, err := Enqueue(ctx, pool, "scrobble", "synthetic-play", "idempotent", json.RawMessage(`{}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimKinds(ctx, time.Minute, []string{"scrobble"})
	if err != nil || got == nil || got.ID != want {
		t.Fatal("priority lane claimed bulk work", got, err)
	}
}

func TestUnsupportedJobVersionIsQuarantined(t *testing.T) {
	pool := testdb.Isolated(t)
	s := Store{Pool: pool}
	ctx := context.Background()
	id, err := Enqueue(ctx, pool, "runtime_probe", "future-payload", "idempotent", json.RawMessage(`{}`), 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE jobs SET payload_version=2 WHERE id=$1::uuid", id); err != nil {
		t.Fatal(err)
	}
	job, err := s.Claim(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	execute(ctx, s, *job, map[string]Handler{"runtime_probe": func(context.Context, json.RawMessage) error { called = true; return nil }}, slog.Default())
	var status string
	if err = pool.QueryRow(ctx, "SELECT status FROM jobs WHERE id=$1::uuid", id).Scan(&status); err != nil || called || status != "blocked" {
		t.Fatal("unsupported payload executed", called, status, err)
	}
}
