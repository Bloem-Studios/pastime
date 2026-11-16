package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/jobs"
)

func TestAdministrationConsoleContracts(t *testing.T) {
	h := application(t)
	ctx := context.Background()
	origin := "http://127.0.0.1:8080"
	call := func(method, path string) map[string]any {
		t.Helper()
		r := h.request(method, path, nil, h.OwnerRaw, h.Owner.CSRF, origin, "")
		out := map[string]any{}
		if r.Code == 200 {
			_ = json.Unmarshal(r.Body.Bytes(), &out)
		}
		out["_code"] = float64(r.Code)
		return out
	}
	code := func(v map[string]any) int { return int(v["_code"].(float64)) }

	// A reporting process with provider traffic appears on the dashboard.
	if _, err := h.Pool.Exec(ctx, `INSERT INTO runtime_nodes(id,role,hostname,version,started_at,stats) VALUES(gen_random_uuid(),'worker','worker-a','test',now(),
		'{"goroutines":12,"heap_mb":4.5,"go_version":"go1.26","pool":{"max":8,"total":2,"idle":1,"acquired":1},"providers":[{"host":"api.themoviedb.org","requests":10,"failures":1,"rate_limited":0,"total_latency_ms":500,"last_status":200,"last_at":"2026-09-21T10:00:00Z","last_failure_at":"2026-09-21T09:00:00Z"}]}')`); err != nil {
		t.Fatal(err)
	}
	var failedID, queuedID string
	var err error
	if failedID, err = jobs.Enqueue(ctx, h.Pool, "catalog_refresh", "admin-test-failed", "idempotent", json.RawMessage(`{"user":1}`), 3); err != nil {
		t.Fatal(err)
	}
	if queuedID, err = jobs.Enqueue(ctx, h.Pool, "scrobble", "admin-test-queued", "idempotent", json.RawMessage(`{}`), 3); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(ctx, "UPDATE jobs SET status='failed', attempts=3, last_error_code='provider_unavailable' WHERE id=$1", failedID); err != nil {
		t.Fatal(err)
	}

	overview := call("GET", "/api/admin/v1/overview")
	if code(overview) != 200 {
		t.Fatalf("overview %v", overview)
	}
	if nodes := overview["nodes"].([]any); len(nodes) != 1 {
		t.Fatalf("nodes %v", nodes)
	}
	jobHealth := overview["jobs"].(map[string]any)
	if jobHealth["failed"].(float64) != 1 || jobHealth["queued"].(float64) != 1 || len(jobHealth["throughput"].([]any)) != 24 {
		t.Fatalf("job health %v", jobHealth)
	}
	for _, p := range overview["providers"].([]any) {
		if p := p.(map[string]any); p["name"] == "tmdb" && (p["requests"].(float64) != 10 || p["mean_latency_ms"].(float64) != 50) {
			t.Fatalf("tmdb health %v", p)
		}
	}

	page := call("GET", "/api/admin/v1/jobs?status=failed")
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["last_error"] != "provider_unavailable" {
		t.Fatalf("failed jobs %v", page)
	}
	raw, _ := json.Marshal(items[0])
	if strings.Contains(string(raw), "admin-test") || strings.Contains(string(raw), "payload") {
		t.Fatal("job listing exposed its dedupe key or payload")
	}
	first := call("GET", "/api/admin/v1/jobs?limit=1")
	if first["next_cursor"] == nil {
		t.Fatal("expected a second page")
	}
	second := call("GET", "/api/admin/v1/jobs?limit=1&cursor="+first["next_cursor"].(string))
	if len(second["items"].([]any)) != 1 || second["items"].([]any)[0].(map[string]any)["id"] == first["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatalf("second page %v", second)
	}
	if code(call("GET", "/api/admin/v1/jobs?status=failed&cursor="+first["next_cursor"].(string))) != 422 {
		t.Fatal("cursor reused across filters")
	}

	retried := call("POST", "/api/admin/v1/jobs/"+failedID+"/retry")
	if retried["status"] != "queued" || retried["attempts"].(float64) != 0 {
		t.Fatalf("retry %v", retried)
	}
	if code(call("POST", "/api/admin/v1/jobs/"+failedID+"/retry")) != 409 {
		t.Fatal("queued job retried")
	}
	cancelled := call("POST", "/api/admin/v1/jobs/"+queuedID+"/cancel")
	if cancelled["status"] != "failed" || cancelled["last_error"] != "cancelled_by_admin" {
		t.Fatalf("cancel %v", cancelled)
	}
	if code(call("POST", "/api/admin/v1/jobs/00000000-0000-4000-8000-000000000000/cancel")) != 404 || code(call("POST", "/api/admin/v1/jobs/not-a-uuid/cancel")) != 404 {
		t.Fatal("unknown job")
	}
	var audited int
	if err = h.Pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action IN ('job.retry','job.cancel')").Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("audit %d %v", audited, err)
	}

	for _, p := range []string{"/api/admin/v1/job-kinds", "/api/admin/v1/catalog", "/api/admin/v1/imports", "/api/admin/v1/checks"} {
		if code(call("GET", p)) != 200 {
			t.Fatal(p)
		}
	}
	if code(call("POST", "/api/admin/v1/catalog/refresh-stale")) != 503 {
		t.Fatal("refresh without a catalog service")
	}

	// Members see none of it.
	h.User(t, "console_member", "member")
	_, memberRaw := h.Login(t, "console_member")
	for _, p := range []string{"/api/admin/v1/overview", "/api/admin/v1/jobs", "/api/admin/v1/imports"} {
		if r := h.request("GET", p, nil, memberRaw, "", origin, ""); r.Code != 403 {
			t.Fatalf("member reached %s: %d", p, r.Code)
		}
	}
}
