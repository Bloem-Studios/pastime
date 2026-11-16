package admin

import (
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/catalog"
	"testing"
)

func TestUsabilityAdminInventoryIncludesActualAndExpectedProviders(t *testing.T) {
	ps, defaults, _ := catalog.Providers(catalog.Credentials{TVDBKey: "synthetic"})
	s := Store{Catalog: &catalog.Service{Providers: ps, Defaults: defaults}}
	raw, _ := json.Marshal(s.providerStatus(nil))
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	found := map[string]map[string]any{}
	for _, p := range items {
		found[p["name"].(string)] = p
	}
	for _, name := range []string{"tmdb", "tvdb", "mangabaka", "rss", "gcd", "youtube", "igdb"} {
		if found[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
	if p := found["tvdb"]; p != nil {
		if p["health"] != "unknown" || p["setup_hint"] == "" {
			t.Errorf("missing honest health/PIN diagnostic: %v", p)
		}
	}
	if p := found["gcd"]; p != nil {
		if p["configured"] != true || len(p["kinds"].([]any)) != 1 {
			t.Errorf("alternative adapter kinds not reported: %v", p)
		}
	}
}
