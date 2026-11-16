package httpapi

import (
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/library"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Silo calendar rows left by the earlier plugin are never shown. Favourite and
// watchlist titles reach the native calendar, its feed and Home on their own.
func TestCalendarEndpointsShowNativeFavouritesAndNotSiloRows(t *testing.T) {
	h := application(t)
	h.User(t, "calendar_viewer", "member")
	member, session := h.Login(t, "calendar_viewer")
	const issuer = "https://sso.example.test/bloem"
	const subject = "calendar-viewer"
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, member.User.ID); err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: h.Pool}
	instance, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "calendar", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Provision(t.Context(), raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", DefaultProfileID: "main", AccountName: "Household", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: issuer, IdentitySubject: subject})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	var titleID int64
	if err = h.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,image_url,release_date) VALUES(0,'movie','tmdb','42','Verified film','minutes','https://image.tmdb.org/t/p/w500/synthetic.jpg',$1) RETURNING id`, now).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(t.Context(), `UPDATE catalog_items SET root_id=id WHERE id=$1`, titleID); err != nil {
		t.Fatal(err)
	}
	// A live snapshot the earlier plugin published for the same title and another.
	page := bloem.CalendarInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", SnapshotID: testkit.Key(t, h.Fixture), Start: day, End: day, VisibilityDigest: "scope", Revision: 1, Complete: true, ExpiresAt: time.Now().Add(time.Minute), Events: []bloem.CalendarEvent{
		{EventID: "event", ContentID: "content", Kind: "movie", Title: "Imported release", Date: day, Favourite: true, Watchlist: true, Followed: true, Playable: true, ExternalIDs: map[string]string{"tmdb": "42"}},
		{EventID: "other", ContentID: "other", Kind: "movie", Title: "Source-only release", Date: day, Favourite: true},
	}}
	if _, err = store.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err = store.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	feed, err := h.server.accounts.RotateFeed(t.Context(), member)
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, credential string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if credential != "" {
			req.AddCookie(&http.Cookie{Name: "pastime_session", Value: credential})
		}
		rec := httptest.NewRecorder()
		h.server.Router.ServeHTTP(rec, req)
		return rec
	}
	apiPath := "/api/v1/calendar?from=" + day + "&to=" + day
	items := func(path string) []map[string]any {
		t.Helper()
		rec := request(path, session)
		var response struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != 200 {
			t.Fatalf("calendar %s: %d %s", path, rec.Code, rec.Body.String())
		}
		validateResponse(t, h.server, "/api/v1/calendar", rec)
		return response.Items
	}
	for _, path := range []string{apiPath, "/calendar/" + feed + ".ics", "/api/v1/home"} {
		rec := request(path, session)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), "Imported release") || strings.Contains(rec.Body.String(), "Source-only release") || strings.Contains(rec.Body.String(), "Verified film") {
			t.Fatalf("Silo calendar row shown: %s %d %s", path, rec.Code, rec.Body.String())
		}
	}

	// A favourite alone puts the title on the calendar; it does not create an entry.
	if rec := h.request(http.MethodPut, "/api/v1/catalog/"+strconv.FormatInt(titleID, 10)+"/personal-state", map[string]any{"kind": "favorite", "present": true}, session, member.CSRF, "http://127.0.0.1:8080", testkit.Key(t, h.Fixture)); rec.Code != 204 {
		t.Fatalf("favourite: %d %s", rec.Code, rec.Body.String())
	}
	got := items(apiPath)
	if len(got) != 1 || got[0]["catalog_id"] != strconv.FormatInt(titleID, 10) || got[0]["title_catalog_id"] != strconv.FormatInt(titleID, 10) || got[0]["image_url"] != "https://image.tmdb.org/t/p/w500/synthetic.jpg" {
		t.Fatalf("native favourite missing: %+v", got)
	}
	if _, ok := got[0]["entry_id"]; ok || got[0]["favourite"] != true || got[0]["watchlist"] != false || got[0]["followed"] != false {
		t.Fatalf("favourite flags or fabricated entry: %+v", got[0])
	}
	if _, ok := got[0]["source_event_id"]; ok || got[0]["playable"] != false || got[0]["handoff"] != nil {
		t.Fatalf("Silo source merged into native release: %+v", got[0])
	}
	if n := len(items(apiPath + "&filter=favourites")); n != 1 {
		t.Fatal("favourites filter", n)
	}
	for _, filter := range []string{"following", "watchlist"} {
		if n := len(items(apiPath + "&filter=" + filter)); n != 0 {
			t.Fatal("favourite counted as", filter, n)
		}
	}
	home := request("/api/v1/home", session)
	var homeResponse struct {
		Upcoming []calendarRelease `json:"upcoming"`
	}
	if err = json.Unmarshal(home.Body.Bytes(), &homeResponse); err != nil || home.Code != 200 || len(homeResponse.Upcoming) != 1 || homeResponse.Upcoming[0].CatalogID == nil || !homeResponse.Upcoming[0].Favourite {
		t.Fatalf("favourite not automatically on home calendar: %d %s", home.Code, home.Body.String())
	}
	validateResponse(t, h.server, "/api/v1/home", home)
	for _, path := range []string{"/calendar/" + feed + ".ics", "/calendar/" + feed + ".ics?filter=favourites"} {
		rec := request(path, session)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Verified film") || strings.Contains(rec.Body.String(), "Imported release") || strings.Contains(rec.Body.String(), "Source-only release") || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("feed: %d %s %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
		}
	}
	if rec := request("/calendar/"+feed+".ics?filter=following", session); rec.Code != 200 || strings.Contains(rec.Body.String(), "Verified film") {
		t.Fatal("favourite in the following feed", rec.Body.String())
	}

	// Planning the title links its entry and makes it followed as well.
	if _, err = h.server.library.Track(t.Context(), member, testkit.Key(t, h.Fixture), library.TrackInput{CatalogID: titleID, Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	got = items(apiPath + "&filter=following")
	if len(got) != 1 || got[0]["entry_id"] == nil || got[0]["followed"] != true || got[0]["favourite"] != true || got[0]["watchlist"] != true {
		t.Fatalf("planned favourite: %+v", got)
	}
	if rec := request(apiPath+"&filter=invalid", session); rec.Code != 422 {
		t.Fatal("invalid filter accepted", rec.Code)
	}
	if rec := request(apiPath, h.OwnerRaw); strings.Contains(rec.Body.String(), "Verified film") {
		t.Fatal("membership crossed account boundary")
	}
	feedActor, err := h.server.accounts.FeedActor(t.Context(), feed)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.server.accounts.RevokeFeed(t.Context(), member); err != nil {
		t.Fatal(err)
	}
	if rec := request("/calendar/"+feed+".ics", session); rec.Code != 401 {
		t.Fatal("revoked feed accepted", rec.Code)
	}
	if _, err = store.CalendarEvents(t.Context(), feedActor, day, day, "all"); err == nil {
		t.Fatal("stale feed actor accepted")
	}
}

func TestCalendarHandoffHTTPRequiresAdministrator(t *testing.T) {
	h := application(t)
	store := bloem.Store{Pool: h.Pool}
	instance, _, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "calendar"})
	if err != nil {
		t.Fatal(err)
	}
	h.User(t, "handoff_http_member", "member")
	member, session := h.Login(t, "handoff_http_member")
	listed := h.request(http.MethodGet, "/api/admin/v1/bloem/instances", nil, h.OwnerRaw, "", "", "")
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), instance.ID) {
		t.Fatal("instance selector missing", listed.Code, listed.Body.String())
	}
	settings := bloem.HandoffSettings{Enabled: true, Label: "Watch here", BaseURL: "https://player.example.test"}
	path := "/api/admin/v1/bloem/instances/" + instance.ID + "/handoff"
	rec := h.request(http.MethodPut, path, settings, session, member.CSRF, "http://127.0.0.1:8080", "")
	if rec.Code != 403 {
		t.Fatal("member settings write", rec.Code, rec.Body.String())
	}
	rec = h.request(http.MethodPut, path, settings, h.OwnerRaw, h.Owner.CSRF, "http://127.0.0.1:8080", "")
	if rec.Code != 200 {
		t.Fatal("admin settings write", rec.Code, rec.Body.String())
	}
	rec = h.request(http.MethodGet, path, nil, h.OwnerRaw, "", "", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Watch here") {
		t.Fatal("settings persistence", rec.Code, rec.Body.String())
	}
}
