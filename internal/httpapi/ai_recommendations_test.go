package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"strings"
	"testing"
	"time"
)

func TestAIRecommendationsEndpointSharesLinkedProfileArtwork(t *testing.T) {
	h := application(t)
	h.User(t, "ai_viewer", "member")
	member, session := h.Login(t, "ai_viewer")
	const issuer = "https://sso.example.test/bloem"
	const subject = "ai-viewer"
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, member.User.ID); err != nil {
		t.Fatal(err)
	}
	store := bloem.Store{Pool: h.Pool}
	instance, raw, err := store.Register(t.Context(), h.Owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://media.example.test", Scope: "ai", IdentityIssuer: issuer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Provision(t.Context(), raw, bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", DefaultProfileID: "main", AccountName: "Household", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: issuer, IdentitySubject: subject})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Pool.Exec(t.Context(), `INSERT INTO catalog_items(kind,provider,provider_id,title,unit,image_url) VALUES('movie','tmdb','42','Verified film','minutes','https://image.tmdb.org/t/p/w500/synthetic.jpg')`); err != nil {
		t.Fatal(err)
	}
	in := bloem.RecommendationsInput{Version: 1, InstanceID: instance.ID, AccountID: "source", ProfileID: "main", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Items: []bloem.AIRecommendation{{ContentID: "personal", Kind: "movie", Title: "AI result", Reason: "Taste match", ExternalIDs: map[string]string{"tmdb": "42"}}}}
	if err = store.AcceptRecommendations(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	response := h.request("GET", "/api/v1/ai-recommendations", nil, session, "", "", "")
	requireCode(t, response, 200)
	body := response.Body.String()
	if !strings.Contains(body, "Taste match") || !strings.Contains(body, "synthetic.jpg") || !strings.Contains(body, "catalog_id") || strings.Contains(body, `"from"`) {
		t.Fatalf("AI projection missing verified poster or includes social data: %s", body)
	}
	choice := map[string]any{"source_instance_id": instance.ID, "content_id": "personal", "dismissed": true}
	requireCode(t, h.request("PUT", "/api/v1/ai-recommendations/feedback", choice, session, "", "http://127.0.0.1:8080", ""), 403)
	requireCode(t, h.request("PUT", "/api/v1/ai-recommendations/feedback", choice, session, member.CSRF, "http://127.0.0.1:8080", ""), 204)
	response = h.request("GET", "/api/v1/ai-recommendations", nil, session, "", "", "")
	requireCode(t, response, 200)
	if strings.Contains(response.Body.String(), "Taste match") {
		t.Fatal("dismissed card remained visible")
	}
	response = h.request("GET", "/api/v1/ai-recommendations/feedback", nil, session, "", "", "")
	requireCode(t, response, 200)
	validateResponse(t, h.server, "/api/v1/ai-recommendations/feedback", response)
	if !strings.Contains(response.Body.String(), "AI result") {
		t.Fatal("missing durable undo", response.Body.String())
	}
	// Cookie sessions cannot read the machine feedback queue.
	requireCode(t, h.request("POST", "/api/v1/integrations/bloem/recommendations/feedback:pending", nil, session, member.CSRF, "http://127.0.0.1:8080", ""), 401)
	choice["dismissed"] = false
	requireCode(t, h.request("PUT", "/api/v1/ai-recommendations/feedback", choice, session, member.CSRF, "http://127.0.0.1:8080", ""), 204)
	response = h.request("GET", "/api/v1/ai-recommendations", nil, session, "", "", "")
	if !strings.Contains(response.Body.String(), "Taste match") {
		t.Fatal("undo did not restore card")
	}
	response = h.request("GET", "/api/v1/ai-recommendations", nil, h.OwnerRaw, "", "", "")
	requireCode(t, response, 200)
	if strings.Contains(response.Body.String(), "Taste match") {
		t.Fatal("AI crossed profiles")
	}
}
