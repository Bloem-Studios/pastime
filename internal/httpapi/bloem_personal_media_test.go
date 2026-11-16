package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// Personal-state titles resolve series by their show identifiers, and a title that
// Silo knows only by the catalog key Pastime reported resolves by that catalog item.
func TestBloemPersonalMediaResolution(t *testing.T) {
	f := newSyncFixture(t)
	ctx := t.Context()
	profile, key := f.profile(t, "main", "Main")
	path := "/api/v1/integrations/bloem/state/events:apply"
	revision := 0
	event := func(operation string, media map[string]any) map[string]any {
		revision++
		return map[string]any{"version": 1, "instance_id": f.instance.ID, "account_id": f.account, "profile_id": "main",
			"entity_id": "state-" + fmt.Sprint(media["kind"]), "event_id": fmt.Sprintf("pevt_%d", revision), "revision": revision,
			"operation": operation, "occurred_at": time.Now().UTC().Add(-time.Hour).Add(time.Duration(revision) * time.Second), "media": media}
	}
	// A series not yet in the catalog: matching used to read only episode fields and
	// answered no_match for every series favourite.
	w := f.call(t, "POST", path, key, event("favorite_add", map[string]any{"kind": "series", "external_ids": map[string]string{"tmdb": "100"}}))
	if out := strict[struct {
		Status       string `json:"status"`
		PlayRecorded bool   `json:"play_recorded"`
	}](t, w); out.Status != "applied" {
		t.Fatal("series favourite", out)
	}
	var series int64
	if err := f.Pool.QueryRow(ctx, `SELECT catalog_item_id FROM profile_catalog_memberships WHERE profile_id=$1 AND kind='favorite' AND present`, profile).Scan(&series); err != nil {
		t.Fatal("series favourite not recorded", err)
	}

	// Removal by catalog key, as state:read reported it (catalog:<id>), and its repeat.
	byKey := map[string]any{"kind": "series", "external_ids": map[string]string{}, "catalog_item_id": fmt.Sprint(series)}
	w = f.call(t, "POST", path, key, event("favorite_remove", byKey))
	requireCode(t, w, 200)
	if out := strict[struct {
		Status       string `json:"status"`
		PlayRecorded bool   `json:"play_recorded"`
	}](t, w); out.Status != "applied" {
		t.Fatal("removal by catalog key", out)
	}
	var present bool
	if err := f.Pool.QueryRow(ctx, `SELECT present FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2 AND kind='favorite'`, profile, series).Scan(&present); err != nil || present {
		t.Fatal("favourite still present", present, err)
	}
	w = f.call(t, "POST", path, key, event("favorite_remove", map[string]any{"kind": "series", "catalog_item_id": series}))
	if out := strict[struct {
		Status       string `json:"status"`
		PlayRecorded bool   `json:"play_recorded"`
	}](t, w); out.Status != "no_change" {
		t.Fatal("a repeated removal must answer no_change", out)
	}

	var season int64
	if err := f.Pool.QueryRow(ctx, `SELECT id FROM catalog_items WHERE parent_id=$1 ORDER BY id LIMIT 1`, series).Scan(&season); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		media map[string]any
		code  int
	}{
		"wrong kind":            {map[string]any{"kind": "movie", "catalog_item_id": fmt.Sprint(series)}, 404},
		"not a top-level title": {map[string]any{"kind": "series", "catalog_item_id": fmt.Sprint(season)}, 404},
		"unknown item":          {map[string]any{"kind": "series", "catalog_item_id": "999999999"}, 404},
		"no identifier":         {map[string]any{"kind": "series", "external_ids": map[string]string{}}, 422},
		"malformed identifier":  {map[string]any{"kind": "series", "catalog_item_id": "catalog:12"}, 422},
		"zero":                  {map[string]any{"kind": "series", "catalog_item_id": "0"}, 422},
	} {
		t.Run(name, func(t *testing.T) {
			requireCode(t, f.call(t, "POST", path, key, event("favorite_add", c.media)), c.code)
		})
	}
	// Playback media is unchanged: it does not accept a catalog item.
	play := map[string]any{"version": 1, "instance_id": f.instance.ID, "account_id": f.account, "profile_id": "main", "entity_id": "media_x", "event_id": "pevt_play",
		"revision": 1, "operation": "start", "occurred_at": time.Now().UTC().Add(-time.Minute), "completed": false, "position_seconds": 1, "duration_seconds": 100,
		"media": map[string]any{"kind": "movie", "external_ids": map[string]string{"tmdb": "42"}, "catalog_item_id": "1"}}
	requireCode(t, f.call(t, "POST", "/api/v1/integrations/bloem/events:apply", key, play), 422)
}
