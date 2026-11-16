package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestTrackingProfileRequestBoundary(t *testing.T) {
	h := application(t)
	ctx := context.Background()
	child, err := h.Accounts.CreateProfile(ctx, h.Owner, "Child")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := h.Accounts.SelectProfile(ctx, h.Owner, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.server.library.Create(ctx, selected, testkit.Key(t, h.Fixture), library.EntryInput{Title: "Private child title", MediaType: "book", Unit: "pages", Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	requireCode(t, h.request("GET", "/api/v1/profiles", nil, h.OwnerRaw, "", "", ""), 200)
	req := func(raw string, header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/library", nil)
		if raw == h.OwnerRaw {
			r.AddCookie(&http.Cookie{Name: "pastime_session", Value: raw})
		} else {
			r.Header.Set("Authorization", "Bearer "+raw)
		}
		if header != "" {
			r.Header.Set("X-Pastime-Profile-ID", header)
		}
		out := httptest.NewRecorder()
		h.server.Router.ServeHTTP(out, r)
		return out
	}
	check := func(out *httptest.ResponseRecorder, want int) {
		t.Helper()
		requireCode(t, out, 200)
		var body struct {
			Items []any `json:"items"`
		}
		if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != want {
			t.Fatalf("profile items %d want %d", len(body.Items), want)
		}
	}
	check(req(h.OwnerRaw, ""), 0)
	check(req(h.OwnerRaw, strconv.FormatInt(child.ID, 10)), 1)
	requireCode(t, req(h.OwnerRaw, "not-a-profile"), 404)
	member := h.User(t, "other_profile_owner", "member")
	other, err := h.Accounts.Profiles(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, req(h.OwnerRaw, strconv.FormatInt(other[0].ID, 10)), 404)
	_, raw, err := h.Accounts.MintToken(ctx, selected, identity.TokenInput{Name: "Profile read", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	check(req(raw, ""), 1)
	defaults, err := h.Accounts.Profiles(ctx, h.Owner)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, req(raw, strconv.FormatInt(defaults[0].ID, 10)), 403)
}
