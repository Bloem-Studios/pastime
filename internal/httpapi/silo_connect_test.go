package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

const siloTestOrigin = "http://127.0.0.1:8080"

type connectView struct {
	Available     bool   `json:"available"`
	ServiceAccess bool   `json:"service_access"`
	SiloURL       string `json:"silo_url"`
	Profiles      []struct {
		ProfileID string     `json:"profile_id"`
		Name      string     `json:"name"`
		Connected bool       `json:"connected"`
		ExpiresAt *time.Time `json:"expires_at"`
	} `json:"profiles"`
}

type siloKey struct {
	Credential  string    `json:"credential"`
	ExpiresAt   time.Time `json:"expires_at"`
	ProfileName string    `json:"profile_name"`
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s: %v", w.Code, w.Body.String(), err)
	}
	return out
}

// The Connect Silo page creates a profile's Silo key for its signed-in owner, binds a
// profile that provisioning never enrolled, and revokes keys on request.
func TestSiloConnectPage(t *testing.T) {
	h := application(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	h.server.secrets = box
	h.server.catalog = &catalog.Service{Pool: h.Pool, Providers: map[string]provider.Provider{"tmdb": syntheticTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	f := &syncFixture{harness: h, store: bloem.Store{Pool: h.Pool, Box: box}}
	owner := h.Owner
	var second int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,'Second') RETURNING id`, owner.User.ID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	connect := "/api/v1/integrations/silo/connect"
	keyPath := func(profile int64) string {
		return fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", profile)
	}

	// No Silo server registered yet.
	w := h.request("GET", connect, nil, h.OwnerRaw, "", "", "")
	requireCode(t, w, 200)
	view := decodeJSON[connectView](t, w)
	if view.Available || view.SiloURL != "" || !view.ServiceAccess || len(view.Profiles) != 2 || view.Profiles[1].Name != "Second" || view.Profiles[1].Connected {
		t.Fatalf("view before registration: %+v", view)
	}
	w = h.request("POST", keyPath(second), map[string]any{}, h.OwnerRaw, owner.CSRF, siloTestOrigin, "")
	requireCode(t, w, 409)
	if problemCode(t, w) != "silo_not_registered" {
		t.Fatal(w.Body.String())
	}

	instance, _, err := f.store.Register(ctx, owner, bloem.InstanceInput{ID: testkit.Key(t, h.Fixture), URL: "https://silo.example", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	f.instance = instance
	view = decodeJSON[connectView](t, h.request("GET", connect, nil, h.OwnerRaw, "", "", ""))
	if !view.Available || view.SiloURL != "https://silo.example" {
		t.Fatalf("view after registration: %+v", view)
	}

	// Session only, with CSRF and origin.
	requireCode(t, h.request("POST", keyPath(second), map[string]any{}, h.OwnerRaw, "", siloTestOrigin, ""), 403)
	_, token, err := h.Accounts.MintToken(ctx, owner, identity.TokenInput{Name: "Script", Scopes: []string{"profile:read", "library:write"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, f.call(t, "POST", keyPath(second), token, map[string]any{}), 403)

	w = h.request("POST", keyPath(second), map[string]any{}, h.OwnerRaw, owner.CSRF, siloTestOrigin, "")
	requireCode(t, w, 200)
	first := decodeJSON[siloKey](t, w)
	if !strings.HasPrefix(first.Credential, "ptbs_") || first.ProfileName != "Second" || time.Until(first.ExpiresAt) < 89*24*time.Hour {
		t.Fatalf("created key: %+v", first)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("a key response must not be cached")
	}
	wantSource := fmt.Sprintf("pastime-account-%d", owner.User.ID)
	wantProfile := fmt.Sprintf("pastime-profile-%d", second)
	binding := strict[struct {
		InstanceID string `json:"instance_id"`
		AccountID  string `json:"account_id"`
		ProfileID  string `json:"profile_id"`
		Name       string `json:"name"`
	}](t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", first.Credential, map[string]any{}))
	if binding.InstanceID != instance.ID || binding.AccountID != wantSource || binding.ProfileID != wantProfile || binding.Name != "Second" {
		t.Fatalf("self-service binding: %+v", binding)
	}
	var origins string
	if err = h.Pool.QueryRow(ctx, `SELECT string_agg(origin,',') FROM bloem_profiles WHERE instance_id=$1`, instance.ID).Scan(&origins); err != nil || origins != "self_service" {
		t.Fatal("binding origin", origins, err)
	}
	// Plays sent with the key land in the chosen profile.
	play := map[string]any{"version": 1, "instance_id": instance.ID, "account_id": wantSource, "profile_id": wantProfile, "entity_id": "media_a", "event_id": "pevt_a",
		"revision": 1, "operation": "stop", "occurred_at": time.Now().UTC().Add(-time.Minute), "completed": true, "consumption_id": "play_a", "position_seconds": 100, "duration_seconds": 100,
		"media": map[string]any{"kind": "movie", "external_ids": map[string]string{"tmdb": "42"}}}
	result := strict[struct {
		Status       string `json:"status"`
		PlayRecorded bool   `json:"play_recorded"`
	}](t, f.call(t, "POST", "/api/v1/integrations/bloem/events:apply", first.Credential, play))
	if result.Status != "applied" || !result.PlayRecorded {
		t.Fatalf("play: %+v", result)
	}
	var plays int
	if err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM plays WHERE profile_id=$1`, second).Scan(&plays); err != nil || plays != 1 {
		t.Fatal("play not in the chosen profile", plays, err)
	}
	view = decodeJSON[connectView](t, h.request("GET", connect, nil, h.OwnerRaw, "", "", ""))
	if view.Profiles[0].Connected || !view.Profiles[1].Connected || view.Profiles[1].ExpiresAt == nil {
		t.Fatalf("connected profiles: %+v", view.Profiles)
	}

	// A new key replaces the old one once Silo uses it.
	next := decodeJSON[siloKey](t, h.request("POST", keyPath(second), map[string]any{"instance_id": instance.ID}, h.OwnerRaw, owner.CSRF, siloTestOrigin, ""))
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", first.Credential, map[string]any{}), 200)
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", next.Credential, map[string]any{}), 200)
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", first.Credential, map[string]any{}), 401)
	var accounts int
	if err = h.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_accounts WHERE instance_id=$1`, instance.ID).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatal("a second key created another source account", accounts, err)
	}

	// Disconnect revokes every key of the profile at once.
	requireCode(t, h.request("DELETE", keyPath(second), nil, h.OwnerRaw, owner.CSRF, siloTestOrigin, ""), 204)
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", next.Credential, map[string]any{}), 401)
	view = decodeJSON[connectView](t, h.request("GET", connect, nil, h.OwnerRaw, "", "", ""))
	if view.Profiles[1].Connected {
		t.Fatal("a revoked profile still shows as connected")
	}

	// Other people's and archived profiles are not found.
	h.User(t, "silo_member", "member")
	member, memberRaw := h.Login(t, "silo_member")
	requireCode(t, h.request("POST", keyPath(second), map[string]any{}, memberRaw, member.CSRF, siloTestOrigin, ""), 404)
	requireCode(t, h.request("DELETE", keyPath(second), nil, memberRaw, member.CSRF, siloTestOrigin, ""), 404)
	var archived int64
	if err = h.Pool.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name,archived_at) VALUES($1,'Old',clock_timestamp()) RETURNING id`, owner.User.ID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	requireCode(t, h.request("POST", keyPath(archived), map[string]any{}, h.OwnerRaw, owner.CSRF, siloTestOrigin, ""), 404)
	requireCode(t, h.request("POST", keyPath(second), map[string]any{"instance_id": "another-silo"}, h.OwnerRaw, owner.CSRF, siloTestOrigin, ""), 409)
}

// A profile that provisioning already bound keeps its source identifiers, so the
// consumption IDs Silo derives for its plays stay the same; an excluded binding is
// refused; tracking access is required.
func TestSiloConnectReusesLegacyBindings(t *testing.T) {
	f := newSyncFixture(t)
	ctx := t.Context()
	f.User(t, "legacy_member", "member")
	member, memberRaw := f.Login(t, "legacy_member")
	var profile, kid int64
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default`, member.User.ID).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,'Kid') RETURNING id`, member.User.ID).Scan(&kid); err != nil {
		t.Fatal(err)
	}
	// The shape live provisioning left for a linked account.
	execSQL(t, f, `INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,owns_account) VALUES($1,'7',$2,'3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71',3,decode(repeat('ab',32),'hex'),true,'linked',false)`, f.instance.ID, member.User.ID)
	execSQL(t, f, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile,active,source_revision) VALUES($1,'7','3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71',$2,$3,false,true,4)`, f.instance.ID, member.User.ID, profile)
	execSQL(t, f, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile,excluded) VALUES($1,'7','b1c2d3e4-0000-4000-8000-000000000002',$2,$3,false,true)`, f.instance.ID, member.User.ID, kid)
	keyPath := func(p int64) string { return fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", p) }

	w := f.request("POST", keyPath(profile), map[string]any{}, memberRaw, member.CSRF, siloTestOrigin, "")
	requireCode(t, w, 200)
	key := decodeJSON[siloKey](t, w)
	binding := strict[struct {
		InstanceID string `json:"instance_id"`
		AccountID  string `json:"account_id"`
		ProfileID  string `json:"profile_id"`
		Name       string `json:"name"`
	}](t, f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", key.Credential, map[string]any{}))
	if binding.AccountID != "7" || binding.ProfileID != "3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71" {
		t.Fatalf("a reconnected legacy profile must keep its source IDs: %+v", binding)
	}
	var origins string
	if err := f.Pool.QueryRow(ctx, `SELECT string_agg(origin,',' ORDER BY profile_id) FROM bloem_profiles WHERE instance_id=$1 AND account_id=$2`, f.instance.ID, member.User.ID).Scan(&origins); err != nil || origins != "provisioned,provisioned" {
		t.Fatal("reuse must not change the binding's origin", origins, err)
	}
	w = f.request("POST", keyPath(kid), map[string]any{}, memberRaw, member.CSRF, siloTestOrigin, "")
	requireCode(t, w, 409)
	if problemCode(t, w) != "profile_excluded" {
		t.Fatal(w.Body.String())
	}

	// Without tracking access the person cannot use Pastime at all, and an existing
	// key answers plugin 0.2.0 with 403 rather than 401, so Silo reports the access
	// problem instead of asking for a new key.
	enableAccessPolicy(t, f)
	requireCode(t, f.request("GET", "/api/v1/integrations/silo/connect", nil, memberRaw, "", "", ""), 401)
	view := decodeJSON[connectView](t, f.request("GET", "/api/v1/integrations/silo/connect", nil, f.OwnerRaw, "", "", ""))
	if !view.ServiceAccess {
		t.Fatal("an owner keeps tracking access")
	}
	w = f.plainCall(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", key.Credential, map[string]any{})
	requireCode(t, w, 403)
	if problemCode(t, w) != "service_access_required" {
		t.Fatal(w.Body.String())
	}
	// Plugin 0.1.14 would spend Silo's export attempts on a 403; it keeps its 401.
	w = f.call(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", key.Credential, map[string]any{})
	requireCode(t, w, 401)
}

// A binding or source account that Silo provisioning turned off (a removed Silo
// profile, a disabled Silo account) stays off: a person's own key does not undo it.
// Bindings and accounts the Connect Silo page created are reactivated as before.
func TestSiloConnectKeepsWhatSiloTurnedOff(t *testing.T) {
	f := newSyncFixture(t)
	ctx := t.Context()
	f.User(t, "turned_off", "member")
	member, memberRaw := f.Login(t, "turned_off")
	var profile int64
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default`, member.User.ID).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", profile)
	execSQL(t, f, `INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,owns_account) VALUES($1,'9',$2,'b1c2d3e4-0000-4000-8000-000000000009',3,decode(repeat('ab',32),'hex'),true,'linked',false)`, f.instance.ID, member.User.ID)
	execSQL(t, f, `INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile,active,source_revision) VALUES($1,'9','b1c2d3e4-0000-4000-8000-000000000009',$2,$3,false,false,4)`, f.instance.ID, member.User.ID, profile)
	refused := func(what string) {
		t.Helper()
		w := f.request("POST", path, map[string]any{}, memberRaw, member.CSRF, siloTestOrigin, "")
		requireCode(t, w, 409)
		if problemCode(t, w) != "silo_account_inactive" {
			t.Fatal(what, w.Body.String())
		}
	}
	refused("a binding Silo turned off")
	execSQL(t, f, `UPDATE bloem_profiles SET active=true WHERE instance_id=$1 AND profile_id=$2`, f.instance.ID, profile)
	execSQL(t, f, `UPDATE bloem_accounts SET active=false WHERE instance_id=$1 AND source_account_id='9'`, f.instance.ID)
	refused("an account Silo turned off")
	var active bool
	if err := f.Pool.QueryRow(ctx, `SELECT active FROM bloem_accounts WHERE instance_id=$1 AND source_account_id='9'`, f.instance.ID).Scan(&active); err != nil || active {
		t.Fatal("the source account was reactivated", active, err)
	}
	// A new profile under that account is refused too, without a new account row.
	var other int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,'Other') RETURNING id`, member.User.ID).Scan(&other); err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", other), map[string]any{}, memberRaw, member.CSRF, siloTestOrigin, "")
	requireCode(t, w, 409)
	if problemCode(t, w) != "silo_account_inactive" {
		t.Fatal(w.Body.String())
	}

	// Rows the Connect Silo page created come back on an explicit connect.
	f.User(t, "self_made", "member")
	owner, ownerRaw := f.Login(t, "self_made")
	var own int64
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default`, owner.User.ID).Scan(&own); err != nil {
		t.Fatal(err)
	}
	ownPath := fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", own)
	requireCode(t, f.request("POST", ownPath, map[string]any{}, ownerRaw, owner.CSRF, siloTestOrigin, ""), 200)
	execSQL(t, f, `UPDATE bloem_accounts SET active=false WHERE instance_id=$1 AND account_id=$2`, f.instance.ID, owner.User.ID)
	execSQL(t, f, `UPDATE bloem_profiles SET active=false WHERE instance_id=$1 AND profile_id=$2`, f.instance.ID, own)
	w = f.request("POST", ownPath, map[string]any{}, ownerRaw, owner.CSRF, siloTestOrigin, "")
	requireCode(t, w, 200)
	key := decodeJSON[siloKey](t, w)
	requireCode(t, f.plainCall(t, "POST", "/api/v1/integrations/bloem/credentials:inspect", key.Credential, map[string]any{}), 200)

	// A Connect Silo key is kept only as its digest: the sealed value is empty (header,
	// nonce and tag only), and machine reissue cannot reveal it.
	var issuance string
	var sealedValue []byte
	if err := f.Pool.QueryRow(ctx, `SELECT issuance_id,credential_ciphertext FROM bloem_sync_credentials WHERE profile_id=$1 AND revoked_at IS NULL`, own).Scan(&issuance, &sealedValue); err != nil {
		t.Fatal(err)
	}
	if len(sealedValue) != 9+12+16 {
		t.Fatalf("a Connect Silo key was stored recoverably (%d bytes)", len(sealedValue))
	}
	if _, err := f.store.IssueCredential(ctx, f.provision, own, issuance); !errors.Is(err, fault.Conflict) {
		t.Fatal("machine reissue of a Connect Silo key", err)
	}
}

func enableAccessPolicy(t *testing.T, f *syncFixture) {
	t.Helper()
	now := time.Now()
	execSQL(t, f, `SELECT pastime_apply_service_access($1,$2,$3,'[]'::jsonb)`, "https://access.example", now, now.Add(time.Minute))
}

// Key creation and revocation are limited per account.
func TestSiloConnectIsRateLimited(t *testing.T) {
	f := newSyncFixture(t)
	path := fmt.Sprintf("/api/v1/integrations/silo/profiles/%d/credential", f.Owner.TrackingID())
	for range 10 {
		requireCode(t, f.request("POST", path, map[string]any{}, f.OwnerRaw, f.Owner.CSRF, siloTestOrigin, ""), 200)
	}
	w := f.request("POST", path, map[string]any{}, f.OwnerRaw, f.Owner.CSRF, siloTestOrigin, "")
	requireCode(t, w, 429)
	if w.Header().Get("Retry-After") != "60" {
		t.Fatal("missing Retry-After")
	}
}
