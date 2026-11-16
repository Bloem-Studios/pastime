package library_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type memoryCache struct {
	mu   sync.Mutex
	data map[string][]byte
	hits int
}

func (m *memoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if ok {
		m.hits++
	}
	return v, ok
}
func (m *memoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
}

// Statistics are served from cache until the library changes, then recomputed.
func TestInsightsCacheFollowsChanges(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cache := &memoryCache{data: map[string][]byte{}}
	lib := library.Store{Pool: f.Pool, Cache: cache}
	from, to := time.Now().AddDate(-1, 0, 0), time.Now().Add(time.Hour)
	first, err := lib.Insights(ctx, f.Owner, from, to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lib.Insights(ctx, f.Owner, from, to); err != nil || cache.hits != 1 {
		t.Fatal("second read not cached", cache.hits, err)
	}
	if _, err = lib.Create(ctx, f.Owner, testkit.Key(t, f), library.EntryInput{Title: "Cache Novel", MediaType: "book", Unit: "pages", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	after, err := lib.Insights(ctx, f.Owner, from, to)
	total := func(in library.Insights) (n int) {
		for _, c := range in.Statuses {
			n += c.Count
		}
		return n
	}
	if err != nil || cache.hits != 1 || total(after) != total(first)+1 {
		t.Fatalf("stale statistics after a change: %+v %v", after.Statuses, err)
	}
}

func TestStatisticsWarmingResumesAndSkipsInactive(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	cache := &memoryCache{data: map[string][]byte{}}
	lib := library.Store{Pool: f.Pool, Cache: cache}
	if err := lib.EnqueueWarm(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := f.Pool.QueryRow(ctx, "SELECT payload FROM jobs WHERE kind=$1 LIMIT 1", library.WarmJob).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if err := lib.WarmHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if len(cache.data) != 2 {
		t.Fatal("windows not warmed", len(cache.data))
	}
	if err := lib.EnqueueWarm(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind=$1", library.WarmJob).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if _, err := f.Pool.Exec(ctx, "UPDATE users SET active=false WHERE id=$1", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	before := cache.hits
	if err := lib.WarmHandler(ctx, payload); err != nil {
		t.Fatal(err)
	}
	if cache.hits != before {
		t.Fatal("inactive account warmed")
	}
}
