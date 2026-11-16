package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/go-chi/chi/v5"
)

// Retryable catalog answers tell clients when to come back; definite answers do not.
func TestRetryAfterForRetryableProblems(t *testing.T) {
	for _, c := range []struct {
		problem *fault.Error
		want    string
	}{
		{fault.Limited, "60"},
		{&fault.Error{Status: 503, Code: "catalog_loading"}, "60"},
		{&fault.Error{Status: 503, Code: "catalog_pending"}, "21600"},
		{&fault.Error{Status: 503, Code: "provider_unavailable"}, ""},
		{fault.Unavailable, ""},
		{&fault.Error{Status: 404, Code: "no_match"}, ""},
		{fault.Conflict, ""},
	} {
		if got := retryAfter(c.problem); got != c.want {
			t.Errorf("%d %s: Retry-After %q, want %q", c.problem.Status, c.problem.Code, got, c.want)
		}
	}
}

// A Silo play of an episode the show does not list yet asks for a retry instead of
// the terminal no_match, and says when to come back.
func TestBloemPlaybackOfAnUnlistedEpisodeIsRetryable(t *testing.T) {
	f := newSyncFixture(t)
	_, key := f.profile(t, "main", "Main")
	event := func(episode int) map[string]any {
		return map[string]any{"version": 1, "instance_id": f.instance.ID, "account_id": f.account, "profile_id": "main", "entity_id": "media_episode", "event_id": "pevt_" + strconv.Itoa(episode),
			"revision": episode, "operation": "start", "occurred_at": time.Now().UTC().Add(-time.Minute), "completed": false, "position_seconds": 1, "duration_seconds": 100,
			"media": map[string]any{"kind": "episode", "external_ids": map[string]string{}, "series_external_ids": map[string]string{"tmdb": "100"}, "season": 1, "episode": episode}}
	}
	path := "/api/v1/integrations/bloem/events:apply"
	requireCode(t, f.call(t, "POST", path, key, event(2)), 200)
	w := f.call(t, "POST", path, key, event(4))
	requireCode(t, w, 503)
	if problemCode(t, w) != "catalog_pending" || w.Header().Get("Retry-After") != "21600" {
		t.Fatal("unlisted episode", w.Header(), w.Body.String())
	}
	// Plugin 0.1.14 (bloem_evt_ event IDs) retries every later event of its batch
	// after one retryable answer, so it keeps the definite answer it had before.
	legacy := event(4)
	legacy["event_id"] = "bloem_evt_4"
	w = f.call(t, "POST", path, key, legacy)
	requireCode(t, w, 404)
	if problemCode(t, w) != "no_match" || w.Header().Get("Retry-After") != "" {
		t.Fatal("unlisted episode for 0.1.14", w.Header(), w.Body.String())
	}
	// Far outside what the show lists is a definite refusal.
	w = f.call(t, "POST", path, key, event(400))
	requireCode(t, w, 404)
	if problemCode(t, w) != "no_match" || w.Header().Get("Retry-After") != "" {
		t.Fatal("far-off episode", w.Header(), w.Body.String())
	}
}

// Catalog retries are routine for Silo sync and webhooks, so they are not logged as
// failed requests; other 503s and 500s still are.
func TestExpectedRetriesAreNotLoggedAsErrors(t *testing.T) {
	var out bytes.Buffer
	s := &Server{logger: slog.New(slog.NewTextHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}))}
	for _, c := range []struct {
		problem error
		logged  bool
	}{
		{&fault.Error{Status: 503, Code: "catalog_loading", Message: "x"}, false},
		{&fault.Error{Status: 503, Code: "catalog_pending", Message: "x"}, false},
		{fault.Unavailable, true},
		{errors.New("database down"), true},
	} {
		out.Reset()
		r := httptest.NewRequest("POST", "/api/v1/integrations/bloem/events:apply", nil)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
		s.problem(httptest.NewRecorder(), r, c.problem)
		if got := strings.Contains(out.String(), "level=ERROR"); got != c.logged {
			t.Errorf("%v: logged as error %v:\n%s", c.problem, got, out.String())
		}
	}
}
