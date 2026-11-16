package bloem

import (
	"strconv"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Plays and resume points Pastime got from Bloem must never be offered back to
// Bloem; everything else a profile watched is.
func TestPersonalStateReadsWatchedAndProgressFromOtherSources(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://example.invalid", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Provision(ctx, raw, ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Account", ProfileID: "child", DefaultProfileID: "child", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.IssueCredential(ctx, raw, child.ProfileID, "watched-test")
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
	film := item(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,external_ids) VALUES(0,'movie','tmdb','603','Film','minutes',136,'{"imdb":"tt0133093"}') RETURNING id`)
	other := item(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,runtime_minutes) VALUES(0,'movie','tmdb','604','Sequel','minutes',138,100) RETURNING id`)
	onlyBloem := item(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','605','Third','minutes',129) RETURNING id`)
	show := item(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,external_ids) VALUES(0,'series','tmdb','1399','Show','episodes','{"tvdb":"121361","imdb":"tt0944947"}') RETURNING id`)
	season := item(`INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number) VALUES('tmdb','3624','season','Season 1','episodes',$1,1) RETURNING id`, show)
	episode := item(`INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number,runtime_minutes) VALUES('tmdb','63057','episode','Episode 2','episodes',$1,1,2,50) RETURNING id`, season)
	entry := func(title int64, kind string) int64 {
		return item(`INSERT INTO library_entries(profile_id,title,media_type,unit,status,catalog_item_id) VALUES($1,'Entry',$2,'minutes','completed',$3) RETURNING id`, child.ProfileID, kind, title)
	}
	filmEntry, otherEntry, bloemEntry, showEntry := entry(film, "movie"), entry(other, "movie"), entry(onlyBloem, "movie"), entry(show, "series")
	traktAt := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	manualAt := time.Date(2026, 4, 2, 21, 30, 0, 0, time.UTC)
	for _, p := range []struct {
		entry, title int64
		cycle        int
		at           time.Time
		source       string
	}{
		{filmEntry, film, 1, traktAt.Add(-24 * time.Hour), "trakt"},
		{filmEntry, film, 2, traktAt, "trakt"},
		{filmEntry, film, 3, traktAt.Add(240 * time.Hour), "bloem"}, // newer, but Bloem's own
		{bloemEntry, onlyBloem, 1, traktAt, "bloem"},
		{showEntry, episode, 1, manualAt, "manual"},
	} {
		if _, err = f.Pool.Exec(ctx, `INSERT INTO plays(profile_id,entry_id,catalog_item_id,cycle,played_at,source) VALUES($1,$2,$3,$4,$5,$6)`, child.ProfileID, p.entry, p.title, p.cycle, p.at, p.source); err != nil {
			t.Fatal(err)
		}
	}
	_ = otherEntry
	if _, err = f.Pool.Exec(ctx, `INSERT INTO playback_progress(profile_id,catalog_item_id,position_seconds,duration_seconds,source) VALUES($1,$2,600,6000,'plex'),($1,$3,1500,NULL,'jellyfin'),($1,$4,100,1000,'bloem')`, child.ProfileID, film, other, episode); err != nil {
		t.Fatal(err)
	}

	watched, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"watched"}, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(watched.Items) != 2 || watched.NextCursor != "full-v1" {
		t.Fatalf("watched items %+v", watched)
	}
	byKind := map[string]PersonalRemoteItem{}
	for _, it := range watched.Items {
		if it.Kind != "watched" {
			t.Fatalf("unexpected kind %+v", it)
		}
		byKind[it.MediaKind] = it
	}
	movie := byKind["movie"]
	if movie.PlayCount == nil || *movie.PlayCount != 2 || !movie.ChangedAt.Equal(traktAt) || movie.ExternalIDs["tmdb"] != "603" || movie.ExternalIDs["imdb"] != "tt0133093" {
		t.Fatalf("movie watched %+v", movie)
	}
	ep := byKind["episode"]
	if ep.PlayCount == nil || *ep.PlayCount != 1 || !ep.ChangedAt.Equal(manualAt) || ep.ExternalIDs["tmdb"] != "63057" ||
		ep.SeriesExternalIDs["tmdb"] != "1399" || ep.SeriesExternalIDs["tvdb"] != "121361" || ep.SeasonNumber == nil || *ep.SeasonNumber != 1 || ep.EpisodeNumber == nil || *ep.EpisodeNumber != 2 {
		t.Fatalf("episode watched %+v", ep)
	}

	progress, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"progress"}, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(progress.Items) != 2 {
		t.Fatalf("progress items %+v", progress.Items)
	}
	want := map[string]float64{"catalog:" + strconv.FormatInt(film, 10): 10, "catalog:" + strconv.FormatInt(other, 10): 25}
	for _, it := range progress.Items {
		if it.Kind != "progress" || it.ProgressPercent == nil || *it.ProgressPercent != want[it.ProviderItemKey] || it.ChangedAt.IsZero() {
			t.Fatalf("progress item %+v", it)
		}
	}

	if _, err = s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"watched", "progress", "favorite", "watchlist", "rating"}, PageSize: 100}); err != nil {
		t.Fatal("all five families", err)
	}
	if _, err = s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"history"}, PageSize: 100}); err == nil {
		t.Fatal("unknown family accepted")
	}
}
