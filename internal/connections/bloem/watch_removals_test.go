package bloem

import (
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Removing a show from one connected profile's library notes a watched removal
// that only that profile's connection reads; another profile on the same Bloem
// account keeps its watches, a newer play retires the note, and a profile with
// no Bloem connection notes nothing.
func TestRemovingATitleNotesAWatchedRemovalForThatProfileOnly(t *testing.T) {
	f, s, raw, instance, robin, _ := calendarFixture(t)
	ctx := t.Context()
	lib := library.Store{Pool: f.Pool}
	robinCred, err := s.IssueCredential(ctx, raw, robin.TrackingID(), "robin")
	if err != nil {
		t.Fatal(err)
	}
	sam := withIdentity(managed(instance.ID, "source-account"), bloemIssuer, subjectA)
	sam.ProfileID, sam.Name = "sam", "Sam"
	samMapping, err := s.Provision(ctx, raw, sam)
	if err != nil {
		t.Fatal(err)
	}
	if samMapping.ProfileID == robin.TrackingID() {
		t.Fatalf("sam shares robin's profile: %+v", samMapping)
	}
	samCred, err := s.IssueCredential(ctx, raw, samMapping.ProfileID, "sam")
	if err != nil {
		t.Fatal(err)
	}

	item := func(sql string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := f.Pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	show := item(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,external_ids) VALUES(0,'series','tmdb','1399','Show','episodes','{"tvdb":"121361"}') RETURNING id`)
	season := item(`INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb','3624','season','Season 1','episodes',$1,1) RETURNING id`, show)
	episode := item(`INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number) VALUES('tmdb','63057','episode','Episode 1','episodes',$1,1,1) RETURNING id`, season)
	watchedAt := time.Date(2026, 4, 8, 19, 34, 0, 0, time.UTC)
	track := func(profile int64) int64 {
		t.Helper()
		entry := item(`INSERT INTO library_entries(profile_id,title,media_type,unit,status,catalog_item_id) VALUES($1,'Show','series','episodes','in_progress',$2) RETURNING id`, profile, show)
		if _, err := f.Pool.Exec(ctx, `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source) VALUES($1,$2,$3,1,$4,'bloem')`, profile, entry, episode, watchedAt); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	remove := func(entry int64) {
		t.Helper()
		var version int
		if err := f.Pool.QueryRow(ctx, `SELECT version FROM library_entries WHERE id=$1`, entry).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.Delete(ctx, robin, entry, testkit.Key(t, f), version); err != nil {
			t.Fatal(err)
		}
	}
	robinEntry := track(robin.TrackingID())
	track(samMapping.ProfileID)
	before := time.Now().Add(-time.Second)
	remove(robinEntry)

	removals := func(cred string) []PersonalRemoteItem {
		t.Helper()
		page, err := s.ReadPersonalState(ctx, cred, PersonalRead{Kinds: []string{"watched"}, PageSize: 100})
		if err != nil {
			t.Fatal(err)
		}
		var out []PersonalRemoteItem
		for _, it := range page.Items {
			if it.Removed {
				out = append(out, it)
			}
		}
		return out
	}
	got := removals(robinCred.Credential)
	if len(got) != 1 {
		t.Fatalf("robin's removals %+v", got)
	}
	r := got[0]
	if r.Kind != "watched" || r.MediaKind != "series" || r.ProviderItemKey != "removed:catalog:"+strconv.FormatInt(show, 10) ||
		r.ExternalIDs["tmdb"] != "1399" || r.ExternalIDs["tvdb"] != "121361" || r.ChangedAt.Before(before) || r.PlayCount != nil {
		t.Fatalf("robin's removal %+v", r)
	}
	if got := removals(samCred.Credential); len(got) != 0 {
		t.Fatalf("sam received robin's removal: %+v", got)
	}

	// Robin watches the show again after removing it: the note retires.
	again := item(`INSERT INTO library_entries(profile_id,title,media_type,unit,status,catalog_item_id) VALUES($1,'Show','series','episodes','in_progress',$2) RETURNING id`, robin.TrackingID(), show)
	if _, err = f.Pool.Exec(ctx, `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source) VALUES($1,$2,$3,1,clock_timestamp()+interval '1 minute','bloem')`, robin.TrackingID(), again, episode); err != nil {
		t.Fatal(err)
	}
	if got := removals(robinCred.Credential); len(got) != 0 {
		t.Fatalf("a newer play did not retire the note: %+v", got)
	}

	// The owner's profile has no Bloem connection: removing notes nothing.
	ownerEntry := item(`INSERT INTO library_entries(profile_id,title,media_type,unit,status,catalog_item_id) VALUES($1,'Show','series','episodes','in_progress',$2) RETURNING id`, f.Owner.TrackingID(), show)
	if _, err = lib.Delete(ctx, f.Owner, ownerEntry, testkit.Key(t, f), 1); err != nil {
		t.Fatal(err)
	}
	var notes int
	if err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM watch_removals WHERE profile_id=$1`, f.Owner.TrackingID()).Scan(&notes); err != nil || notes != 0 {
		t.Fatalf("unconnected profile noted %d removals (%v)", notes, err)
	}
}
