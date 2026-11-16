package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
)

// machineContract014 pins every machine route plugin 0.1.14 can call, as published
// at 95f503e (the Pastime it ran against): a digest of each operation with its schemas
// resolved. 0.1.14 decodes every answer with unknown fields refused, so a rollback to
// it needs its response contracts unchanged. Deliberate request additions are
// media.catalog_item_id on state/events:apply (only its responses are pinned),
// and optional episode title/air_date and series_title on events:apply (its full
// digest includes those additions). state:read gained the watched and progress families (its full
// digest includes them): 0.1.14 asks only for favourite, watchlist and rating, and
// items of those families carry none of the new optional fields, so its answers are
// byte-for-byte what it decoded before. The strict conversation below still sends
// 0.1.14 payloads and decodes state:read with unknown fields refused.
// Regenerate only for a deliberate, reviewed contract change:
// PASTIME_PRINT_MACHINE_CONTRACT=<openapi.yaml> go test -run TestPlugin014MachineContractIsUnchanged
var machineContract014 = map[string]string{
	"POST /api/v1/integrations/bloem/availability:apply":                     "28d12386cbd83bfe868b73df50367b6d214509f0652ccf34ac72e5391a1bbc9a",
	"POST /api/v1/integrations/bloem/availability:pending":                   "3a3a87266ddb03685c893abca1434f71490b141d73cfa8db690f176cff25eb71",
	"POST /api/v1/integrations/bloem/calendar/invalidate":                    "d0bf90a68415885198f1b85a7c0dd5652bb036ca856c63d15a69a61e6f528c0e",
	"POST /api/v1/integrations/bloem/calendar/leases":                        "89e679a9c21207b959f7be9b41f364ecd35eddb9ee09be9bd4cabf0fd7594e49",
	"POST /api/v1/integrations/bloem/calendar/pages":                         "df6d6eeb85fdc6473da902df1dab31f6000c2ecd46f4e961cee16a4758af07d9",
	"POST /api/v1/integrations/bloem/calendar/{snapshotID}/publish":          "2475e383a0f2ac956738d19c10a7fff0f3fdd8f0be80c6c70cecbf587af0cc48",
	"POST /api/v1/integrations/bloem/credentials:inspect":                    "1ef8c5bc3a049a89bb9e3d20eb40ac87af6363d83834cc9c0e4be9684ddb466d",
	"POST /api/v1/integrations/bloem/events:apply":                           "5e6019ce99fea15ad991e2fd09781fe14cc8d997e9ab97f26d6f6146f0a6747e",
	"POST /api/v1/integrations/bloem/library/mutations:apply":                "02290ba8243c031300b13fec753416e5dd1e0872f772533abe0b65542ef0294b",
	"POST /api/v1/integrations/bloem/library/mutations:pending":              "076abdc58f1025fe99b63d81f2b1d2e0e1f0b023cc386fc34473791ef06d6ef0",
	"POST /api/v1/integrations/bloem/library:apply":                          "5c4ddfa216a3dcfbb3c7e60040ab26cf91e6298c368c326cb40358377eadd285",
	"POST /api/v1/integrations/bloem/library:pending":                        "6bbfddb8f5c22bc6f0fbea2e1965d9868a161a934b72f2e523108b6446411cd0",
	"POST /api/v1/integrations/bloem/library:reject":                         "c1ea8e81422610d91b5fe3ef752a14050032e4160d67d135f2a276dbd00410e1",
	"POST /api/v1/integrations/bloem/lists:attest":                           "af797b8b42ff517d891ec0647929b875738600882a2f487f1098b0c9df6e47a8",
	"POST /api/v1/integrations/bloem/lists:page":                             "a0c36991dce8fb8b20a2991bc4d973f2e069255ea0ab65b04ce2f37239679e54",
	"POST /api/v1/integrations/bloem/lists:resolve":                          "667d288be389a2df1561b2c5527a14b60a698c0a0a6e63afb0cbc7d0d84211cf",
	"POST /api/v1/integrations/bloem/profiles/{profileID}/credentials:issue": "63575c3ee2381e67ba61b5e4102b0801e4118dc884285f76b8eb71d3a74eb050",
	"POST /api/v1/integrations/bloem/profiles:provision":                     "629272cccab14d00f161e9f6942a5997a8b1ed635b7a12086a7ed15fc0d917b4",
	"POST /api/v1/integrations/bloem/recommendations":                        "b29cd6f86b9d9ed9cbb8ca163f7dfec07acac005bc941ba73691514bcc990b69",
	"POST /api/v1/integrations/bloem/recommendations/feedback:ack":           "a3fcd8b4ad72d77d00b0d25445b3fd8d1befa9132003ba29c1f9fc3686cd7252",
	"POST /api/v1/integrations/bloem/recommendations/feedback:pending":       "749cc8d34e705b2eb04061f0fc3aee418495bbf3b93bd594f8c92a7c9ba83434",
	"POST /api/v1/integrations/bloem/snapshots:complete":                     "587e2e7f1efb2b2babb8d556e3c087ff71079b1d9884d44d4ce9087ed86c5e0a",
	"POST /api/v1/integrations/bloem/snapshots:page":                         "f9b23b0dd8ea5a94f3c6676b7d38e0faeee3ebde30e4c980ecc51c0d19edae8a",
	"POST /api/v1/integrations/bloem/state/events:apply responses":           "f232cc4fbcfbc918628ac6ed49cb2ff35d4a2b99294dba36ebd8c694b43bd489",
	"POST /api/v1/integrations/bloem/state:read":                             "3160a66c04338646b2f5407ea36588c3eac4bbb1ad251a050b825c845669b72c",
}

// machineDigests digests each machine-credential operation of a contract with every
// schema reference resolved, keyed "METHOD path" (state/events:apply: responses only).
func machineDigests(t *testing.T, spec *openapi3.T) map[string]string {
	t.Helper()
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var resolve func(v any, depth int) any
	resolve = func(v any, depth int) any {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok && depth < 32 {
				return resolve(schemas[strings.TrimPrefix(ref, "#/components/schemas/")], depth+1)
			}
			out := map[string]any{}
			for k, item := range x {
				out[k] = resolve(item, depth)
			}
			return out
		case []any:
			out := make([]any, len(x))
			for i, item := range x {
				out[i] = resolve(item, depth)
			}
			return out
		}
		return v
	}
	digests := map[string]string{}
	for path, item := range doc["paths"].(map[string]any) {
		for method, raw := range item.(map[string]any) {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			security, _ := json.Marshal(op["security"])
			if !strings.Contains(string(security), "BloemProvisionCredential") && !strings.Contains(string(security), "BloemSyncCredential") {
				continue
			}
			key := strings.ToUpper(method) + " " + path
			var pinned any = op
			if path == "/api/v1/integrations/bloem/state/events:apply" {
				key, pinned = key+" responses", op["responses"]
			}
			b, err := json.Marshal(resolve(pinned, 0))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(b)
			digests[key] = hex.EncodeToString(sum[:])
		}
	}
	return digests
}

// Every machine route plugin 0.1.14 calls keeps its published contract and stays
// routed, so a rollback to 0.1.14 keeps working against this Pastime.
func TestPlugin014MachineContractIsUnchanged(t *testing.T) {
	if base := os.Getenv("PASTIME_PRINT_MACHINE_CONTRACT"); base != "" {
		data, err := os.ReadFile(base)
		if err != nil {
			t.Fatal(err)
		}
		spec, err := openapi3.NewLoader().LoadFromData(data)
		if err != nil {
			t.Fatal(err)
		}
		digests := machineDigests(t, spec)
		keys := make([]string, 0, len(digests))
		for k := range digests {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("\t%q: %q,\n", k, digests[k])
		}
		return
	}
	s, err := New(ready, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	current := machineDigests(t, s.Spec)
	for key, want := range machineContract014 {
		if got, ok := current[key]; !ok {
			t.Errorf("%s is no longer documented", key)
		} else if got != want {
			t.Errorf("%s changed its published contract", key)
		}
		method, path, _ := strings.Cut(strings.TrimSuffix(key, " responses"), " ")
		path = strings.NewReplacer("{snapshotID}", "snapshot", "{profileID}", "1").Replace(path)
		if !s.Router.Match(chi.NewRouteContext(), method, path) {
			t.Errorf("%s is no longer routed", key)
		}
	}
	// New machine routes are allowed; they are listed here so the set stays reviewed.
	for key := range current {
		if _, ok := machineContract014[key]; !ok && key != "POST /api/v1/integrations/bloem/state/events:apply" {
			t.Errorf("unreviewed machine route %s", key)
		}
	}
}

// Plugin 0.1.14's own request and response shapes, copied from its client.
type plugin014Binding struct {
	InstanceID string `json:"instance_id"`
	AccountID  string `json:"account_id"`
	ProfileID  string `json:"profile_id"`
	Name       string `json:"name"`
}
type plugin014Media struct {
	Kind              string            `json:"kind"`
	ExternalIDs       map[string]string `json:"external_ids"`
	SeriesExternalIDs map[string]string `json:"series_external_ids,omitempty"`
	Season            *int32            `json:"season,omitempty"`
	Episode           *int32            `json:"episode,omitempty"`
}
type plugin014Event struct {
	Rating          *int           `json:"rating,omitempty"`
	ListPosition    *int32         `json:"position,omitempty"`
	Version         int            `json:"version"`
	InstanceID      string         `json:"instance_id"`
	AccountID       string         `json:"account_id"`
	ProfileID       string         `json:"profile_id"`
	EntityID        string         `json:"entity_id"`
	EventID         string         `json:"event_id"`
	Revision        uint64         `json:"revision"`
	Operation       string         `json:"operation"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Completed       bool           `json:"completed"`
	ConsumptionID   string         `json:"consumption_id,omitempty"`
	PositionSeconds int            `json:"position_seconds"`
	DurationSeconds int            `json:"duration_seconds"`
	Media           plugin014Media `json:"media"`
}
type plugin014EventResult struct {
	Status       string `json:"status"`
	PlayRecorded bool   `json:"play_recorded"`
}
type plugin014PersonalPage struct {
	Items []struct {
		ProviderItemKey string            `json:"provider_item_key"`
		Kind            string            `json:"kind"`
		MediaKind       string            `json:"media_kind"`
		ExternalIDs     map[string]string `json:"external_ids"`
		Removed         bool              `json:"removed"`
		Rating          *int              `json:"rating,omitempty"`
		Position        *int32            `json:"position,omitempty"`
		ChangedAt       time.Time         `json:"changed_at"`
	} `json:"items"`
	NextPageToken    string   `json:"next_page_token"`
	NextCursor       string   `json:"next_cursor"`
	CompleteSnapshot bool     `json:"complete_snapshot"`
	Warnings         []string `json:"warnings"`
}

// Plugin 0.1.14's whole conversation, with its own payloads and strict decoding:
// provision, issue, inspect, scrobble, personal state, state:read and a snapshot.
func TestPlugin014ConversationStillWorks(t *testing.T) {
	f := newSyncFixture(t)
	ctx := t.Context()
	base := "/api/v1/integrations/bloem"
	profile := map[string]any{"version": 1, "instance_id": f.instance.ID, "account_id": "7", "account_name": "Household", "profile_id": "3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71",
		"default_profile_id": "3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71", "name": "Main", "account_revision": 1, "revision": 1, "account_active": true, "active": true}
	provisioned := strict[struct {
		AccountID       string `json:"account_id"`
		ProfileID       string `json:"profile_id"`
		LinkStatus      string `json:"link_status"`
		ConnectionReady bool   `json:"connection_ready"`
	}](t, f.call(t, http.MethodPost, base+"/profiles:provision", f.provision, profile))
	issued := strict[struct {
		Credential string    `json:"credential"`
		ExpiresAt  time.Time `json:"expires_at"`
	}](t, f.call(t, http.MethodPost, base+"/profiles/"+provisioned.ProfileID+"/credentials:issue", f.provision, map[string]string{"issuance_id": "enroll-1"}))
	key := issued.Credential
	binding := strict[plugin014Binding](t, f.call(t, http.MethodPost, base+"/credentials:inspect", key, struct{}{}))
	if binding.AccountID != "7" || binding.ProfileID != "3f2c9a52-1d4e-4a0b-9a8e-2b1c0d9e8f71" || binding.InstanceID != f.instance.ID {
		t.Fatalf("binding %+v", binding)
	}
	stop := plugin014Event{Version: 1, InstanceID: f.instance.ID, AccountID: "7", ProfileID: binding.ProfileID, EntityID: "media_12b4c378", EventID: "bloem_evt_1",
		Revision: 17000, Operation: "stop", OccurredAt: time.Now().UTC().Add(-time.Minute), Completed: true, ConsumptionID: "play_d5d9186f", PositionSeconds: 100, DurationSeconds: 100,
		Media: plugin014Media{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}}}
	if out := strict[plugin014EventResult](t, f.call(t, http.MethodPost, base+"/events:apply", key, stop)); out.Status != "applied" || !out.PlayRecorded {
		t.Fatalf("scrobble %+v", out)
	}
	// The same viewing exported again later is recorded once.
	export := stop
	export.EventID, export.Revision = "bloem_evt_2", 17001
	if out := strict[plugin014EventResult](t, f.call(t, http.MethodPost, base+"/events:apply", key, export)); out.Status != "applied" || out.PlayRecorded {
		t.Fatalf("export replay %+v", out)
	}
	position := int32(3)
	add := plugin014Event{Version: 1, InstanceID: f.instance.ID, AccountID: "7", ProfileID: binding.ProfileID, EntityID: "state_1464f3d8", EventID: "bloem_evt_3",
		Revision: 1, Operation: "watchlist_add", OccurredAt: time.Now().UTC().Add(-time.Minute), ListPosition: &position, Media: plugin014Media{Kind: "movie", ExternalIDs: map[string]string{"tmdb": "42"}}}
	// 0.1.14 sends personal events in this shape (client.Apply).
	personal := struct {
		Version    int            `json:"version"`
		InstanceID string         `json:"instance_id"`
		AccountID  string         `json:"account_id"`
		ProfileID  string         `json:"profile_id"`
		EntityID   string         `json:"entity_id"`
		EventID    string         `json:"event_id"`
		Revision   uint64         `json:"revision"`
		Operation  string         `json:"operation"`
		OccurredAt time.Time      `json:"occurred_at"`
		Rating     *int           `json:"rating,omitempty"`
		Position   *int32         `json:"position,omitempty"`
		Media      plugin014Media `json:"media"`
	}{add.Version, add.InstanceID, add.AccountID, add.ProfileID, add.EntityID, add.EventID, add.Revision, add.Operation, add.OccurredAt, add.Rating, add.ListPosition, add.Media}
	if out := strict[plugin014EventResult](t, f.call(t, http.MethodPost, base+"/state/events:apply", key, personal)); out.Status != "applied" {
		t.Fatalf("watchlist %+v", out)
	}
	page := strict[plugin014PersonalPage](t, f.call(t, http.MethodPost, base+"/state:read", key, map[string]any{"kinds": []string{"favorite", "watchlist", "rating"}, "page_size": 100}))
	if len(page.Items) != 1 || page.Items[0].Kind != "watchlist" || page.NextCursor != "full-v1" || !page.CompleteSnapshot {
		t.Fatalf("state:read %+v", page)
	}
	receipt := strict[struct {
		Receipt string `json:"receipt"`
	}](t, f.call(t, http.MethodPost, base+"/snapshots:page", f.provision, map[string]any{"snapshot_id": "snapshot-1", "cursor": "", "next_cursor": "", "complete": true, "profiles": []any{profile}}))
	if out := strict[struct {
		Completed bool `json:"completed"`
	}](t, f.call(t, http.MethodPost, base+"/snapshots:complete", f.provision, map[string]string{"snapshot_id": "snapshot-1", "receipt": receipt.Receipt})); !out.Completed {
		t.Fatal("snapshot not completed")
	}
	var plays int
	if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_consumption_receipts WHERE instance_id=$1`, f.instance.ID).Scan(&plays); err != nil || plays != 1 {
		t.Fatal("consumption receipts", plays, err)
	}
	// Every other machine route still answers the provisioning key.
	for key := range machineContract014 {
		method, path, _ := strings.Cut(strings.TrimSuffix(key, " responses"), " ")
		if strings.Contains(path, "credentials:inspect") || strings.Contains(path, "events:apply") || strings.Contains(path, "state:read") {
			continue
		}
		path = strings.NewReplacer("{snapshotID}", "snapshot-x", "{profileID}", provisioned.ProfileID).Replace(path)
		t.Run(path, func(t *testing.T) {
			// The route authenticates the provisioning key and answers past it; f.call
			// also fails on an undocumented status or response shape.
			requireCode(t, f.call(t, method, path, "ptbp_not-a-key", map[string]any{}), http.StatusUnauthorized)
			w := f.call(t, method, path, f.provision, map[string]any{})
			if w.Code == http.StatusUnauthorized || w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
			}
		})
	}
}
