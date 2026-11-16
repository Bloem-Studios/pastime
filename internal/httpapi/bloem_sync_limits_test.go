package httpapi

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
)

// Each Silo sync credential has its own request budget: one busy profile connection
// cannot starve the others that share the Silo server's address.
func TestSiloSyncLimitIsPerCredential(t *testing.T) {
	f := newSyncFixture(t)
	f.server.syncPerMinute = 60
	_, busy := f.profile(t, "main", "Main")
	_, quiet := f.profile(t, "kid", "Kid")
	inspect := "/api/v1/integrations/bloem/credentials:inspect"
	for range 60 {
		requireCode(t, f.call(t, "POST", inspect, busy, map[string]any{}), 200)
	}
	w := f.call(t, "POST", inspect, busy, map[string]any{})
	requireCode(t, w, 429)
	if w.Header().Get("Retry-After") != "60" || problemCode(t, w) != "rate_limited" {
		t.Fatal("a limited sync credential must be told when to retry", w.Header(), w.Body.String())
	}
	requireCode(t, f.call(t, "POST", inspect, quiet, map[string]any{}), 200)
}

// Keys Pastime never issued are counted per address and refused before any lookup once
// the address used its failure budget; guessed keys never create rate buckets of their
// own, and valid keys from another address are unaffected. Known keys that were revoked
// or expired do not count, so a Silo server holding one dead key keeps syncing the rest.
func TestSiloSyncFailuresAreLimitedPerAddress(t *testing.T) {
	f := newSyncFixture(t)
	_, key := f.profile(t, "main", "Main")
	_, dead := f.profile(t, "gone", "Gone")
	execSQL(t, f, `UPDATE bloem_sync_credentials SET revoked_at=clock_timestamp() WHERE token_digest=$1`, identity.Digest(dead))
	inspect := "/api/v1/integrations/bloem/credentials:inspect"
	for range syncFailuresPerMinute + 10 {
		requireCode(t, f.call(t, "POST", inspect, dead, map[string]any{}), 401)
	}
	requireCode(t, f.call(t, "POST", inspect, key, map[string]any{}), 200)
	bucketsBefore := countRows(t, f, `SELECT count(*) FROM rate_buckets`)
	for i := range syncFailuresPerMinute {
		requireCode(t, f.call(t, "POST", inspect, fmt.Sprintf("ptbs_guess-%d", i), map[string]any{}), 401)
	}
	if n := countRows(t, f, `SELECT count(*) FROM rate_buckets`) - bucketsBefore; n != 1 {
		t.Fatalf("guessed keys created %d rate buckets; want only the address's failure bucket", n)
	}
	w := f.call(t, "POST", inspect, "ptbs_guess-again", map[string]any{})
	requireCode(t, w, 429)
	if w.Header().Get("Retry-After") != "60" {
		t.Fatal(w.Header())
	}
	// The same address is refused even with a valid key until the window ends.
	requireCode(t, f.call(t, "POST", inspect, key, map[string]any{}), 429)
	other := f.callFrom(t, "198.51.100.7:40000", "POST", inspect, key, map[string]any{})
	requireCode(t, other, 200)
}

// The per-credential bucket is keyed by the credential's ID, so the number of buckets
// is bounded by the keys Pastime issued, not by what clients send.
func TestSiloSyncBucketsAreKeyedByCredential(t *testing.T) {
	f := newSyncFixture(t)
	_, key := f.profile(t, "main", "Main")
	before := countRows(t, f, `SELECT count(*) FROM rate_buckets`)
	for range 3 {
		requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", key, map[string]any{}), 200)
	}
	if n := countRows(t, f, `SELECT count(*) FROM rate_buckets`) - before; n != 2 {
		t.Fatalf("one key created %d buckets; want the address ceiling and the credential", n)
	}
}

func countRows(t *testing.T, f *syncFixture, query string) int {
	t.Helper()
	var n int
	if err := f.Pool.QueryRow(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Refusals on sync routes are counted by route and code only, never with credentials
// or identifiers, and logged once per window.
func TestSiloSyncRefusalsAreCountedWithoutSecrets(t *testing.T) {
	var out bytes.Buffer
	c := &faultCounter{logger: slog.New(slog.NewTextHandler(&out, nil))}
	c.add("/api/v1/integrations/bloem/events:apply", "no_match")
	c.add("/api/v1/integrations/bloem/events:apply", "no_match")
	c.add("/api/v1/integrations/bloem/credentials:inspect", "unauthenticated")
	c.mu.Lock()
	c.timer.Stop()
	c.mu.Unlock()
	c.flush()
	log := out.String()
	if !strings.Contains(log, "route=/api/v1/integrations/bloem/events:apply code=no_match count=2") ||
		!strings.Contains(log, "code=unauthenticated count=1") || strings.Count(log, "\n") != 2 {
		t.Fatalf("unexpected refusal log:\n%s", log)
	}
	f := newSyncFixture(t)
	f.server.syncFaults = &faultCounter{logger: slog.New(slog.NewTextHandler(&out, nil))}
	out.Reset()
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", "ptbs_unknown", map[string]any{}), 401)
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/profiles:provision", "ptbs_unknown", map[string]any{}), 401)
	c = f.server.syncFaults
	c.mu.Lock()
	counts := c.counts
	if c.timer != nil {
		c.timer.Stop()
	}
	c.mu.Unlock()
	if len(counts) != 1 || counts[faultKey{"/api/v1/integrations/bloem/credentials:inspect", "unauthenticated"}] != 1 {
		t.Fatalf("only sync routes are counted: %v", counts)
	}
}
