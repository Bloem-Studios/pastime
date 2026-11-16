package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/testdb"
)

func setup(t *testing.T) (Store, context.Context) {
	t.Helper()
	pool := testdb.Open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if _, err := pool.Exec(ctx, "TRUNCATE jobs"); err != nil {
		t.Fatal(err)
	}
	return Store{Pool: pool}, ctx
}
func add(t *testing.T, s Store, ctx context.Context, key, policy string, max int) string {
	t.Helper()
	id, err := Enqueue(ctx, s.Pool, "runtime_probe", key, policy, json.RawMessage(`{"value":1}`), max)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func claim(t *testing.T, s Store, ctx context.Context) Job {
	t.Helper()
	j, err := s.Claim(ctx, 30*time.Second)
	if err != nil || j == nil {
		t.Fatalf("claim: %v, %v", j, err)
	}
	return *j
}
func status(t *testing.T, s Store, ctx context.Context, id string) string {
	t.Helper()
	var v string
	if err := s.Pool.QueryRow(ctx, "SELECT status FROM jobs WHERE id=$1", id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDedupeAndTransactionalEnqueue(t *testing.T) {
	s, ctx := setup(t)
	id := add(t, s, ctx, "same", "idempotent", 3)
	if again := add(t, s, ctx, "same", "idempotent", 3); again != id {
		t.Fatal("dedupe produced another job")
	}
	_, err := Enqueue(ctx, s.Pool, "runtime_probe", "same", "idempotent", json.RawMessage(`{"value":2}`), 3)
	if !errors.Is(err, ErrDedupeConflict) {
		t.Fatalf("conflict: %v", err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := Enqueue(ctx, tx, "runtime_probe", "rollback", "idempotent", json.RawMessage(`{}`), 3); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&n); err != nil || n != 1 {
		t.Fatalf("rolled-back intent persisted: %d %v", n, err)
	}
}

func TestConcurrentClaims(t *testing.T) {
	s, ctx := setup(t)
	for i := range 32 {
		add(t, s, ctx, testingKey(i), "idempotent", 3)
	}
	var seen sync.Map
	var completed atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for {
				j, err := s.Claim(ctx, 30*time.Second)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				if _, loaded := seen.LoadOrStore(j.ID, true); loaded {
					t.Error("duplicate concurrent ownership")
				}
				if j.Attempts != 1 {
					t.Error("unexpected replay")
				}
				if err := s.Complete(ctx, *j); err != nil {
					t.Error(err)
					return
				}
				completed.Add(1)
			}
		})
	}
	wg.Wait()
	if completed.Load() != 32 {
		t.Fatalf("completed %d jobs", completed.Load())
	}
}
func testingKey(i int) string { return time.Unix(int64(i), 0).UTC().Format(time.RFC3339) }

func TestLostLeaseIsFenced(t *testing.T) {
	s, ctx := setup(t)
	id := add(t, s, ctx, "lease", "idempotent", 3)
	old := claim(t, s, ctx)
	if err := s.Heartbeat(ctx, old, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(ctx, old); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired completion accepted: %v", err)
	}
	if n, err := s.Recover(ctx); err != nil || n != 1 {
		t.Fatalf("recover: %d %v", n, err)
	}
	fresh := claim(t, s, ctx)
	if fresh.ID != old.ID || fresh.LeaseToken == old.LeaseToken || fresh.Attempts != 2 {
		t.Fatal("recovery did not fence old owner")
	}
	for _, err := range []error{s.Heartbeat(ctx, old, 30*time.Second), s.Complete(ctx, old), s.Fail(ctx, old, "handler_failed")} {
		if !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("old owner retained authority: %v", err)
		}
	}
	if err := s.Complete(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if status(t, s, ctx, id) != "succeeded" {
		t.Fatal("new owner did not complete")
	}
}

func TestRecoveryPolicyAndAttemptLimit(t *testing.T) {
	for _, tc := range []struct {
		policy string
		max    int
		want   string
	}{{"reconcile", 3, "blocked"}, {"idempotent", 1, "failed"}, {"idempotent", 3, "queued"}} {
		t.Run(tc.want, func(t *testing.T) {
			s, ctx := setup(t)
			id := add(t, s, ctx, "recovery", tc.policy, tc.max)
			_ = claim(t, s, ctx)
			if _, err := s.Pool.Exec(ctx, "UPDATE jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Recover(ctx); err != nil {
				t.Fatal(err)
			}
			if got := status(t, s, ctx, id); got != tc.want {
				t.Fatalf("status %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFailureBackoffAndValidation(t *testing.T) {
	s, ctx := setup(t)
	add(t, s, ctx, "retry", "idempotent", 3)
	j := claim(t, s, ctx)
	if err := s.Fail(ctx, j, "https://secret@example.com"); err == nil {
		t.Fatal("raw error accepted")
	}
	if err := s.Fail(ctx, j, "temporary_failure"); err != nil {
		t.Fatal(err)
	}
	if next, err := s.Claim(ctx, 30*time.Second); err != nil || next != nil {
		t.Fatalf("backoff not enforced: %v %v", next, err)
	}
	if _, err := Enqueue(ctx, s.Pool, "runtime_probe", "array", "idempotent", json.RawMessage(`[]`), 3); err == nil {
		t.Fatal("array payload accepted")
	}
	if _, err := Enqueue(ctx, s.Pool, "runtime_probe", "null", "idempotent", json.RawMessage(`null`), 3); err == nil {
		t.Fatal("null payload accepted")
	}
	if _, err := s.Claim(ctx, 0); err == nil {
		t.Fatal("zero lease accepted")
	}
}

func TestWorkerRunsAndStops(t *testing.T) {
	s, ctx := setup(t)
	id := add(t, s, ctx, "worker", "idempotent", 3)
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		Run(work, s, 2, map[string]Handler{"runtime_probe": func(ctx context.Context, p json.RawMessage) error { return nil }}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(finished)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for status(t, s, ctx, id) != "succeeded" {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("worker did not finish")
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestUnknownHandlerIsBlocked(t *testing.T) {
	s, ctx := setup(t)
	id := add(t, s, ctx, "unknown", "idempotent", 3)
	j := claim(t, s, ctx)
	execute(ctx, s, j, map[string]Handler{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if status(t, s, ctx, id) != "blocked" {
		t.Fatal("unknown work was retried or reported successful")
	}
}

func TestWorkerShutdownWaitsForHandlerAndRetainsRecoverableLease(t *testing.T) {
	s, ctx := setup(t)
	id := add(t, s, ctx, "drain", "idempotent", 3)
	work, cancel := context.WithCancel(ctx)
	entered := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		Run(work, s, 1, map[string]Handler{"runtime_probe": func(ctx context.Context, _ json.RawMessage) error {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return ctx.Err()
		}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler not started")
	}
	cancel()
	<-canceled
	select {
	case <-done:
		t.Fatal("worker returned before handler stopped")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not drain")
	}
	if status(t, s, ctx, id) != "running" {
		t.Fatal("canceled work falsely acknowledged")
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if status(t, s, ctx, id) != "queued" {
		t.Fatal("interrupted work not recoverable")
	}
}
