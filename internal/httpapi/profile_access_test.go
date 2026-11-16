package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

// profileSessionAccess is what a profile session gets from every route:
//
//	ok   the route served it (2xx, or a 4xx/503 about the request itself)
//	401  the route ignores browser sessions (public, machine-key, feed and
//	     compatibility routes) or needs a different credential
//	403  refused: household-only, administration, or another profile
//	404  refused as not found, or the fixture's path IDs don't exist
//
// Every registered route must be listed. Adding a route means deciding what a
// profile session may do with it.
var profileSessionAccess = map[string]string{
	"DELETE /api/admin/v1/invitations/{invitationID}":                        "403",
	"DELETE /api/v1/collab-lists/{collabID}":                                 "403",
	"DELETE /api/v1/collab-lists/{collabID}/items/{catalogID}":               "403",
	"DELETE /api/v1/collab-lists/{collabID}/members/{username}":              "403",
	"DELETE /api/v1/collection-fields/{fieldID}":                             "404",
	"DELETE /api/v1/connections/{connectionID}":                              "404",
	"DELETE /api/v1/discover/dismissals":                                     "ok",
	"DELETE /api/v1/export-files/{fileID}":                                   "404",
	"DELETE /api/v1/history/{playID}":                                        "404",
	"DELETE /api/v1/integrations/silo/profiles/{profileID}/credential":       "ok",
	"DELETE /api/v1/library/{entryID}":                                       "ok",
	"DELETE /api/v1/library/{entryID}/copies/{copyID}":                       "404",
	"DELETE /api/v1/library/{entryID}/plays/{playID}":                        "ok",
	"DELETE /api/v1/lists/{listID}":                                          "ok",
	"DELETE /api/v1/lists/{listID}/source":                                   "404",
	"DELETE /api/v1/me/calendar-feed":                                        "ok",
	"DELETE /api/v1/me/export-schedule":                                      "ok",
	"DELETE /api/v1/me/koreader":                                             "ok",
	"DELETE /api/v1/me/notification-targets/{targetID}":                      "404",
	"DELETE /api/v1/me/oidc":                                                 "403",
	"DELETE /api/v1/me/services/{serviceID}":                                 "404",
	"DELETE /api/v1/me/sessions/{credentialID}":                              "404",
	"DELETE /api/v1/me/tokens/{credentialID}":                                "404",
	"DELETE /api/v1/me/totp":                                                 "403",
	"DELETE /api/v1/profiles/{profileID}/login":                              "403",
	"DELETE /api/v1/saved-views/{viewID}":                                    "ok",
	"DELETE /api/v1/tags/{tagID}":                                            "404",
	"GET /api/admin/v1/audit":                                                "403",
	"GET /api/admin/v1/bloem/instances":                                      "403",
	"GET /api/admin/v1/bloem/instances/{instanceID}/handoff":                 "403",
	"GET /api/admin/v1/catalog":                                              "403",
	"GET /api/admin/v1/checks":                                               "403",
	"GET /api/admin/v1/imports":                                              "403",
	"GET /api/admin/v1/invitations":                                          "403",
	"GET /api/admin/v1/job-kinds":                                            "403",
	"GET /api/admin/v1/jobs":                                                 "403",
	"GET /api/admin/v1/overview":                                             "403",
	"GET /api/admin/v1/users":                                                "403",
	"GET /api/admin/v1/users/{userID}":                                       "403",
	"GET /api/openapi.json":                                                  "403",
	"GET /api/openapi.yaml":                                                  "403",
	"GET /api/v1/activity":                                                   "ok",
	"GET /api/v1/ai-recommendations":                                         "ok",
	"GET /api/v1/ai-recommendations/feedback":                                "ok",
	"GET /api/v1/auth/methods":                                               "ok",
	"GET /api/v1/bloem/library/mutations/{requestID}":                        "401",
	"GET /api/v1/bloem/library/queries/{requestID}":                          "404",
	"GET /api/v1/bloem/library/sources":                                      "ok",
	"GET /api/v1/calendar":                                                   "ok",
	"GET /api/v1/catalog/browse":                                             "ok",
	"GET /api/v1/catalog/providers":                                          "ok",
	"GET /api/v1/catalog/search":                                             "ok",
	"GET /api/v1/catalog/{catalogID}":                                        "ok",
	"GET /api/v1/catalog/{catalogID}/bloem-availability":                     "ok",
	"GET /api/v1/catalog/{catalogID}/children":                               "ok",
	"GET /api/v1/catalog/{catalogID}/critic":                                 "ok",
	"GET /api/v1/catalog/{catalogID}/editions":                               "ok",
	"GET /api/v1/catalog/{catalogID}/episode-orders":                         "ok",
	"GET /api/v1/catalog/{catalogID}/episode-orders/{orderID}":               "ok",
	"GET /api/v1/catalog/{catalogID}/personal-state":                         "404",
	"GET /api/v1/changes":                                                    "ok",
	"GET /api/v1/checkins":                                                   "ok",
	"GET /api/v1/checkins/{checkinID}":                                       "404",
	"GET /api/v1/collab-lists":                                               "403",
	"GET /api/v1/collab-lists/{collabID}":                                    "403",
	"GET /api/v1/collection":                                                 "ok",
	"GET /api/v1/collection-fields":                                          "ok",
	"GET /api/v1/connections":                                                "ok",
	"GET /api/v1/device":                                                     "404",
	"GET /api/v1/discover":                                                   "ok",
	"GET /api/v1/discover/dismissals":                                        "ok",
	"GET /api/v1/export-files":                                               "ok",
	"GET /api/v1/export-files/{fileID}":                                      "404",
	"GET /api/v1/export/letterboxd.csv":                                      "ok",
	"GET /api/v1/export/library.csv":                                         "ok",
	"GET /api/v1/export/library.jsonl":                                       "ok",
	"GET /api/v1/history":                                                    "ok",
	"GET /api/v1/history/":                                                   "401",
	"GET /api/v1/home":                                                       "ok",
	"GET /api/v1/import-unmatched":                                           "ok",
	"GET /api/v1/imports":                                                    "ok",
	"GET /api/v1/imports/{importID}":                                         "ok",
	"GET /api/v1/info":                                                       "ok",
	"GET /api/v1/integrations/silo/connect":                                  "ok",
	"GET /api/v1/library":                                                    "ok",
	"GET /api/v1/library-stats":                                              "ok",
	"GET /api/v1/library/facets":                                             "ok",
	"GET /api/v1/library/{entryID}":                                          "404",
	"GET /api/v1/library/{entryID}/copies":                                   "ok",
	"GET /api/v1/library/{entryID}/derived-rating":                           "404",
	"GET /api/v1/library/{entryID}/episode-ratings":                          "404",
	"GET /api/v1/library/{entryID}/lists":                                    "404",
	"GET /api/v1/library/{entryID}/notifications":                            "404",
	"GET /api/v1/library/{entryID}/play-history":                             "404",
	"GET /api/v1/library/{entryID}/plays":                                    "404",
	"GET /api/v1/library/{entryID}/skipped":                                  "ok",
	"GET /api/v1/library/{entryID}/status-history":                           "404",
	"GET /api/v1/library/{entryID}/tags":                                     "ok",
	"GET /api/v1/lists":                                                      "ok",
	"GET /api/v1/lists/{listID}":                                             "404",
	"GET /api/v1/lists/{listID}/tiers":                                       "404",
	"GET /api/v1/me":                                                         "ok",
	"GET /api/v1/me/calendar-feed":                                           "ok",
	"GET /api/v1/me/export-schedule":                                         "ok",
	"GET /api/v1/me/koreader":                                                "ok",
	"GET /api/v1/me/notification-settings":                                   "ok",
	"GET /api/v1/me/notification-targets":                                    "ok",
	"GET /api/v1/me/oidc":                                                    "403",
	"GET /api/v1/me/preferences":                                             "ok",
	"GET /api/v1/me/service-kinds":                                           "ok",
	"GET /api/v1/me/services":                                                "ok",
	"GET /api/v1/me/sessions":                                                "ok",
	"GET /api/v1/me/tokens":                                                  "ok",
	"GET /api/v1/me/totp":                                                    "403",
	"GET /api/v1/people/{provider}/{personID}":                               "ok",
	"GET /api/v1/playback/progress/":                                         "401",
	"GET /api/v1/profile-logins":                                             "403",
	"GET /api/v1/profiles":                                                   "ok",
	"GET /api/v1/public/{username}/lists":                                    "404",
	"GET /api/v1/public/{username}/lists/{slug}":                             "404",
	"GET /api/v1/recommendation-recipients":                                  "403",
	"GET /api/v1/recommendations":                                            "403",
	"GET /api/v1/review/{year}":                                              "ok",
	"GET /api/v1/saved-views":                                                "ok",
	"GET /api/v1/session":                                                    "ok",
	"GET /api/v1/stats":                                                      "ok",
	"GET /api/v1/stats/years":                                                "ok",
	"GET /api/v1/studios/{provider}/{studioKind}/{studioID}":                 "ok",
	"GET /api/v1/tags":                                                       "ok",
	"GET /apis/listenbrainz/1/validate-token":                                "ok",
	"GET /auth/oidc/callback":                                                "ok",
	"GET /auth/oidc/start":                                                   "ok",
	"GET /calendar/{secret}.ics":                                             "401",
	"GET /docs":                                                              "403",
	"GET /docs/":                                                             "403",
	"GET /docs/assets/docs-theme.css":                                        "ok",
	"GET /docs/assets/docs-theme.js":                                         "ok",
	"GET /docs/assets/swagger-bootstrap.js":                                  "ok",
	"GET /docs/assets/swagger-ui-bundle.js":                                  "ok",
	"GET /docs/assets/swagger-ui-bundle.js.LICENSE.txt":                      "ok",
	"GET /docs/assets/swagger-ui.css":                                        "ok",
	"GET /docs/licenses.txt":                                                 "ok",
	"GET /healthz":                                                           "ok",
	"GET /kosync/healthcheck":                                                "ok",
	"GET /kosync/syncs/progress/{document}":                                  "401",
	"GET /kosync/users/auth":                                                 "401",
	"GET /lists/{username}/{slug}.csv":                                       "404",
	"GET /lists/{username}/{slug}.rss":                                       "404",
	"GET /readyz":                                                            "ok",
	"PATCH /api/admin/v1/users/{userID}":                                     "403",
	"PATCH /api/v1/collab-lists/{collabID}":                                  "403",
	"PATCH /api/v1/connections/{connectionID}":                               "404",
	"PATCH /api/v1/me":                                                       "403",
	"POST /api/admin/v1/catalog/refresh-stale":                               "403",
	"POST /api/admin/v1/invitations":                                         "403",
	"POST /api/admin/v1/jobs/{jobID}/cancel":                                 "403",
	"POST /api/admin/v1/jobs/{jobID}/retry":                                  "403",
	"POST /api/admin/v1/users":                                               "403",
	"POST /api/admin/v1/users/{userID}/reset-link":                           "403",
	"POST /api/admin/v1/users/{userID}/revoke-access":                        "403",
	"POST /api/v1/bloem/library/copies:import":                               "ok",
	"POST /api/v1/bloem/library/mutations":                                   "ok",
	"POST /api/v1/bloem/library/queries":                                     "ok",
	"POST /api/v1/catalog/resolve":                                           "ok",
	"POST /api/v1/checkins":                                                  "ok",
	"POST /api/v1/checkins/{checkinID}/cancel":                               "404",
	"POST /api/v1/checkins/{checkinID}/finish":                               "404",
	"POST /api/v1/collab-lists":                                              "403",
	"POST /api/v1/collab-lists/{collabID}/items":                             "403",
	"POST /api/v1/collection-fields":                                         "ok",
	"POST /api/v1/connections":                                               "ok",
	"POST /api/v1/device/authorize":                                          "ok",
	"POST /api/v1/device/decision":                                           "404",
	"POST /api/v1/device/token":                                              "404",
	"POST /api/v1/discover/dismissals":                                       "ok",
	"POST /api/v1/export-files":                                              "ok",
	"POST /api/v1/import-unmatched/{unmatchedID}/ignore":                     "404",
	"POST /api/v1/import-unmatched/{unmatchedID}/resolve":                    "ok",
	"POST /api/v1/imports":                                                   "ok",
	"POST /api/v1/imports/{importID}/cancel":                                 "ok",
	"POST /api/v1/imports/{importID}/rollback":                               "404",
	"POST /api/v1/integrations/bloem/availability:apply":                     "401",
	"POST /api/v1/integrations/bloem/availability:pending":                   "401",
	"POST /api/v1/integrations/bloem/calendar/invalidate":                    "401",
	"POST /api/v1/integrations/bloem/calendar/leases":                        "401",
	"POST /api/v1/integrations/bloem/calendar/pages":                         "401",
	"POST /api/v1/integrations/bloem/calendar/{snapshotID}/publish":          "401",
	"POST /api/v1/integrations/bloem/credentials:inspect":                    "401",
	"POST /api/v1/integrations/bloem/events:apply":                           "401",
	"POST /api/v1/integrations/bloem/library/mutations:apply":                "401",
	"POST /api/v1/integrations/bloem/library/mutations:pending":              "401",
	"POST /api/v1/integrations/bloem/library:apply":                          "401",
	"POST /api/v1/integrations/bloem/library:pending":                        "401",
	"POST /api/v1/integrations/bloem/library:reject":                         "401",
	"POST /api/v1/integrations/bloem/lists:attest":                           "401",
	"POST /api/v1/integrations/bloem/lists:page":                             "401",
	"POST /api/v1/integrations/bloem/lists:resolve":                          "401",
	"POST /api/v1/integrations/bloem/profiles/{profileID}/credentials:issue": "401",
	"POST /api/v1/integrations/bloem/profiles:provision":                     "401",
	"POST /api/v1/integrations/bloem/recommendations":                        "401",
	"POST /api/v1/integrations/bloem/recommendations/feedback:ack":           "401",
	"POST /api/v1/integrations/bloem/recommendations/feedback:pending":       "401",
	"POST /api/v1/integrations/bloem/snapshots:complete":                     "401",
	"POST /api/v1/integrations/bloem/snapshots:page":                         "401",
	"POST /api/v1/integrations/bloem/state/events:apply":                     "401",
	"POST /api/v1/integrations/bloem/state:read":                             "401",
	"POST /api/v1/integrations/silo/profiles/{profileID}/credential":         "ok",
	"POST /api/v1/invitations/claim":                                         "404",
	"POST /api/v1/invitations/preview":                                       "404",
	"POST /api/v1/library":                                                   "ok",
	"POST /api/v1/library/bulk":                                              "ok",
	"POST /api/v1/library/track":                                             "ok",
	"POST /api/v1/library/{entryID}/copies":                                  "ok",
	"POST /api/v1/library/{entryID}/plays":                                   "ok",
	"POST /api/v1/library/{entryID}/progress":                                "ok",
	"POST /api/v1/library/{entryID}/remap":                                   "ok",
	"POST /api/v1/library/{entryID}/remap/preview":                           "ok",
	"POST /api/v1/library/{entryID}/restart":                                 "ok",
	"POST /api/v1/library/{entryID}/skips":                                   "ok",
	"POST /api/v1/lists":                                                     "ok",
	"POST /api/v1/lists/{listID}/trakt-export":                               "ok",
	"POST /api/v1/login":                                                     "401",
	"POST /api/v1/logout":                                                    "ok",
	"POST /api/v1/me/calendar-feed":                                          "ok",
	"POST /api/v1/me/notification-targets":                                   "ok",
	"POST /api/v1/me/notification-targets/{targetID}/test":                   "ok",
	"POST /api/v1/me/oidc/link":                                              "403",
	"POST /api/v1/me/password":                                               "ok",
	"POST /api/v1/me/service-authorizations":                                 "ok",
	"POST /api/v1/me/service-authorizations/{authorizationID}/poll":          "ok",
	"POST /api/v1/me/services":                                               "ok",
	"POST /api/v1/me/services/{serviceID}/sync":                              "404",
	"POST /api/v1/me/tokens":                                                 "ok",
	"POST /api/v1/me/totp":                                                   "403",
	"POST /api/v1/me/totp/confirm":                                           "403",
	"POST /api/v1/password-recovery":                                         "ok",
	"POST /api/v1/recommendations":                                           "403",
	"POST /api/v1/recommendations/{recommendationID}/accept":                 "403",
	"POST /api/v1/recommendations/{recommendationID}/dismiss":                "403",
	"POST /api/v1/register":                                                  "403",
	"POST /api/v1/saved-views":                                               "ok",
	"POST /api/v1/scrobble/":                                                 "401",
	"POST /api/v1/tags":                                                      "ok",
	"POST /apis/listenbrainz/1/submit-listens":                               "401",
	"POST /kosync/users/create":                                              "ok",
	"POST /mcp":                                                              "ok",
	"POST /webhooks/{kind}/{secret}":                                         "401",
	"PUT /api/admin/v1/bloem/instances/{instanceID}/handoff":                 "403",
	"PUT /api/v1/ai-recommendations/feedback":                                "ok",
	"PUT /api/v1/catalog/{catalogID}/personal-state":                         "ok",
	"PUT /api/v1/collab-lists/{collabID}/members":                            "403",
	"PUT /api/v1/collab-lists/{collabID}/order":                              "403",
	"PUT /api/v1/collection-fields/{fieldID}":                                "ok",
	"PUT /api/v1/library/{entryID}":                                          "ok",
	"PUT /api/v1/library/{entryID}/artwork":                                  "ok",
	"PUT /api/v1/library/{entryID}/copies/{copyID}":                          "ok",
	"PUT /api/v1/library/{entryID}/edition":                                  "ok",
	"PUT /api/v1/library/{entryID}/episode-order":                            "ok",
	"PUT /api/v1/library/{entryID}/episode-ratings/{catalogID}":              "404",
	"PUT /api/v1/library/{entryID}/notifications":                            "ok",
	"PUT /api/v1/library/{entryID}/tags":                                     "404",
	"PUT /api/v1/lists/{listID}":                                             "ok",
	"PUT /api/v1/lists/{listID}/entries/{entryID}":                           "ok",
	"PUT /api/v1/lists/{listID}/entries/{entryID}/note":                      "ok",
	"PUT /api/v1/lists/{listID}/order":                                       "ok",
	"PUT /api/v1/lists/{listID}/source":                                      "ok",
	"PUT /api/v1/lists/{listID}/tiers":                                       "ok",
	"PUT /api/v1/me/export-schedule":                                         "ok",
	"PUT /api/v1/me/koreader":                                                "ok",
	"PUT /api/v1/me/koreader/documents/{document}":                           "404",
	"PUT /api/v1/me/notification-settings":                                   "ok",
	"PUT /api/v1/me/notification-targets/{targetID}":                         "ok",
	"PUT /api/v1/me/preferences":                                             "ok",
	"PUT /api/v1/me/services/{serviceID}/schedule":                           "ok",
	"PUT /api/v1/profiles/{profileID}/login":                                 "403",
	"PUT /api/v1/saved-views/order":                                          "ok",
	"PUT /api/v1/saved-views/{viewID}":                                       "ok",
	"PUT /api/v1/tags/{tagID}":                                               "ok",
	"PUT /kosync/syncs/progress":                                             "401",
}

// requestBodies replaces the empty JSON object for routes that answer it with
// an unrelated server error.
var requestBodies = map[string]string{
	// An omitted statuses list reaches a NOT NULL column (a separate, existing bug).
	"PUT /api/v1/me/notification-settings": `{"statuses":[]}`,
}

var routeParam = regexp.MustCompile(`\{([^}:]+)(:[^}]*)?\}`)

func TestEveryRouteDecidesProfileSessions(t *testing.T) {
	h := application(t)
	sam, a, raw := h.ProfileLogin(t, "Sam", "sam")
	values := map[string]string{"profileID": itoa(sam.ID), "username": "sam", "year": "2026", "slug": "synthetic", "secret": strings.Repeat("s", 48)}
	uuid := "00000000-0000-4000-8000-000000000001"
	var routes []string
	if err := chi.Walk(h.server.Router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, method+" "+route)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(routes)
	actual := map[string]string{}
	for _, key := range routes {
		method, route, _ := strings.Cut(key, " ")
		path := routeParam.ReplaceAllStringFunc(route, func(p string) string {
			name := routeParam.FindStringSubmatch(p)[1]
			if v, ok := values[name]; ok {
				return v
			}
			if strings.HasSuffix(name, "ID") && (name == "credentialID" || name == "invitationID" || name == "connectionID" || name == "importID" || name == "jobID" || name == "fileID" || name == "authorizationID") {
				return uuid
			}
			return "1"
		})
		path = strings.ReplaceAll(path, "*", "synthetic")
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		var body *strings.Reader
		if method == "GET" || method == "HEAD" {
			body = strings.NewReader("")
		} else if b, ok := requestBodies[key]; ok {
			body = strings.NewReader(b)
		} else {
			body = strings.NewReader("{}")
		}
		req := httptest.NewRequestWithContext(ctx, method, path, body)
		req.RemoteAddr = "127.0.0.1:51234"
		if method != "GET" && method != "HEAD" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", a.CSRF)
			req.Header.Set("Origin", testOrigin)
			req.Header.Set("Idempotency-Key", uuid)
		}
		req.AddCookie(&http.Cookie{Name: "pastime_session", Value: raw})
		res := httptest.NewRecorder()
		h.server.Router.ServeHTTP(res, req)
		cancel()
		class := "ok"
		switch {
		case res.Code == 401 || res.Code == 403 || res.Code == 404:
			class = fmt.Sprint(res.Code)
		case res.Code >= 500 && res.Code != 503:
			t.Errorf("%s: %d %s", key, res.Code, res.Body.String())
		}
		actual[key] = class
		// Signing out, a password change and the like end the session; open a
		// fresh one so the rest of the walk still runs as Sam.
		if _, err := h.Accounts.Authenticate(t.Context(), raw, identity.KindSession); err != nil {
			a, raw = h.Login(t, "sam")
		}
	}
	if os.Getenv("PASTIME_PRINT_ROUTE_ACCESS") == "1" {
		for _, key := range routes {
			fmt.Printf("\t%q: %q,\n", key, actual[key])
		}
	}
	for _, key := range routes {
		want, ok := profileSessionAccess[key]
		if !ok {
			t.Errorf("%s has no decision for profile sessions (it gave %s)", key, actual[key])
			continue
		}
		if want != actual[key] {
			t.Errorf("%s: profile session got %s, want %s", key, actual[key], want)
		}
	}
	for key := range profileSessionAccess {
		if _, ok := actual[key]; !ok {
			t.Errorf("stale profile-session decision for %s", key)
		}
	}
}
