package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://user:test-secret@localhost/pastime_test"}
	get := func(key string) string { return base[key] }
	c, err := Load(get)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:8080" || c.DatabaseMaxConn != 8 || c.WorkerCount != 2 || c.SiloSyncRequestsPerMinute != 600 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_URL", "postgres://user:test-secret@localhost"},
		{"DATABASE_URL", "broken:test-secret"},
		{"DATABASE_MAX_CONNECTIONS", "500"},
		{"DATABASE_MAX_CONNECTIONS", "1"},
		{"WORKER_COUNT", "8"},
		{"WORKER_COUNT", "-1"},
		{"SILO_SYNC_REQUESTS_PER_MINUTE", "59"},
		{"SILO_SYNC_REQUESTS_PER_MINUTE", "100001"},
		{"SILO_SYNC_REQUESTS_PER_MINUTE", "fast"},
		{"LISTEN_ADDR", "not-an-address"},
		{"VALKEY_URL", "https://test-secret@example.com"},
		{"REVIEW_NOTICES_TOKEN", "test-secret"},
		{"REVIEW_NOTICES_URL", "https://notices.example.test/api/v1/hooks/trackers/x/notices"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			m := map[string]string{"DATABASE_URL": base["DATABASE_URL"]}
			m[tc.key] = tc.value
			_, err := Load(func(k string) string { return m[k] })
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if strings.Contains(err.Error(), "test-secret") {
				t.Fatal("secret leaked in error")
			}
		})
	}
}

func TestReviewNotices(t *testing.T) {
	load := func(url string) (Config, error) {
		m := map[string]string{"DATABASE_URL": "postgres://user:x@localhost/pastime_test", "REVIEW_NOTICES_URL": url, "REVIEW_NOTICES_TOKEN": "bgtrk_test-secret"}
		return Load(func(k string) string { return m[k] })
	}
	c, err := load("https://notices.example.test/api/v1/hooks/trackers/x/notices")
	if err != nil || c.ReviewNoticesURL == "" || c.ReviewNoticesToken != "bgtrk_test-secret" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{"http://notices.example.test/notices", "https://user:test-secret@notices.example.test/notices", "https://notices.example.test/notices?x=1", "notices"} {
		if _, err := load(bad); err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestDefaultOriginOnlyForLoopback(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080", "localhost:9000": "http://localhost:9000",
		":8080": "", "0.0.0.0:8080": "", "10.0.0.5:8080": "",
	} {
		m := map[string]string{"DATABASE_URL": "postgres://u@localhost/db", "LISTEN_ADDR": listen}
		c, err := Load(func(k string) string { return m[k] })
		if err != nil {
			t.Fatalf("%s: non-serve commands must load without an origin: %v", listen, err)
		}
		if c.PublicOrigin != want {
			t.Fatalf("%s: origin %q, want %q", listen, c.PublicOrigin, want)
		}
	}
}
