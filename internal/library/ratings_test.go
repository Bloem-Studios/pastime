package library_test

import (
	"context"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestRatingTimestampAndStatusHistory(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	in := library.EntryInput{Title: "A book", MediaType: "book", Unit: "pages", Status: "planned"}
	out, err := s.Create(ctx, f.Owner, testkit.Key(t, f), in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, f.Owner, out.ID)
	if err != nil || e.ScoredAt != nil {
		t.Fatal(e, err)
	}
	n := 8
	in.Rating = &n
	in.Version = e.Version
	in.Status = "paused"
	if _, err = s.Replace(ctx, f.Owner, out.ID, testkit.Key(t, f), in); err != nil {
		t.Fatal(err)
	}
	e, err = s.Get(ctx, f.Owner, out.ID)
	if err != nil || e.ScoredAt == nil {
		t.Fatal(e, err)
	}
	at := *e.ScoredAt
	in.Version = e.Version
	in.Notes = "Unrelated edit"
	if _, err = s.Replace(ctx, f.Owner, out.ID, testkit.Key(t, f), in); err != nil {
		t.Fatal(err)
	}
	e, _ = s.Get(ctx, f.Owner, out.ID)
	if !e.ScoredAt.Equal(at) {
		t.Fatal("unchanged rating restamped")
	}
	history, err := s.StatusHistory(ctx, f.Owner, out.ID, "", 30)
	if err != nil || len(history.Items) < 2 || history.Items[0].Status != "paused" {
		t.Fatal(history, err)
	}
	other := f.User(t, "rating_other", "member")
	if _, err = s.StatusHistory(ctx, other, out.ID, "", 30); err == nil {
		t.Fatal("private history disclosed")
	}
}

func TestDistinctEpisodeRatingsAndAutomaticIntent(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var root, one, two int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total,release_status) VALUES('manual','rating-show','series','Synthetic Show','episodes',2,'Returning Series') RETURNING id").Scan(&root); err != nil {
		t.Fatal(err)
	}
	for n, dest := range []*int64{&one, &two} {
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number) VALUES('manual',$1,'episode','Synthetic Episode','episodes',$2,1,$3) RETURNING id", fmt.Sprint(n), root, n+1).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.ApplyImport(ctx, f.Owner.User.ID, "test", library.ImportItem{TitleID: root, Status: "paused", Plays: []library.ImportPlay{{TargetID: one, At: time.Now(), ExternalID: "one"}, {TargetID: two, At: time.Now(), ExternalID: "two"}}})
	if err != nil || !out.Created {
		t.Fatal(out, err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, root)
	if err != nil || entry.Status != "paused" {
		t.Fatal(entry, err)
	}
	eight, twoScore := 8, 2
	first, err := s.RateEpisode(ctx, f.Owner, entry.ID, one, library.EpisodeRatingInput{Rating: &eight})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RateEpisode(ctx, f.Owner, entry.ID, two, library.EpisodeRatingInput{Rating: &twoScore}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at,media_type,source) VALUES($1,$2,$3,2,clock_timestamp(),'series','test')", f.Owner.User.ID, entry.ID, one); err != nil {
		t.Fatal(err)
	}
	summary, err := s.DerivedRating(ctx, f.Owner, entry.ID, nil)
	if err != nil || summary.Rating == nil || *summary.Rating != 5 || summary.WatchedEpisodes != 2 {
		t.Fatal(summary, err)
	}
	// An episode of the show can be rated without being watched, and counts.
	var unwatched int64
	if err = f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,parent_id,season_number,episode_number) VALUES('manual','unwatched','episode','Synthetic Episode','episodes',$1,1,3) RETURNING id", root).Scan(&unwatched); err != nil {
		t.Fatal(err)
	}
	five := 5
	if _, err = s.RateEpisode(ctx, f.Owner, entry.ID, unwatched, library.EpisodeRatingInput{Rating: &five}); err != nil {
		t.Fatal("unwatched episode rating refused", err)
	}
	summary, err = s.DerivedRating(ctx, f.Owner, entry.ID, nil)
	if err != nil || summary.Rating == nil || *summary.Rating != 5 || summary.RatedEpisodes != 3 || summary.WatchedEpisodes != 2 {
		t.Fatal(summary, err)
	}
	listed, err := s.EpisodeRatings(ctx, f.Owner, entry.ID, "", 30)
	if err != nil || len(listed.Items) != 3 {
		t.Fatal("rated unwatched episode not listed", listed, err)
	}
	cleared, err := s.RateEpisode(ctx, f.Owner, entry.ID, one, library.EpisodeRatingInput{Version: first.Version})
	if err != nil || cleared.ScoredAt == nil || !cleared.ScoredAt.After(*first.ScoredAt) {
		t.Fatal(cleared, err)
	}
	other := f.User(t, "episode_other", "member")
	if _, err = s.RateEpisode(ctx, other, entry.ID, two, library.EpisodeRatingInput{Rating: &eight}); err == nil {
		t.Fatal("cross-account rating accepted")
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, root)
	if _, err = s.Delete(ctx, f.Owner, entry.ID, testkit.Key(t, f), entry.Version); err != nil {
		t.Fatal(err)
	}
	out, err = s.ApplyImport(ctx, f.Owner.User.ID, "test", library.ImportItem{TitleID: root, Status: "completed"})
	if err != nil || out.Created {
		t.Fatal("deleted title recreated", out, err)
	}
	if _, err = s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: root}); err != nil {
		t.Fatal("explicit re-add failed", err)
	}
}

func TestImportedRatingDateClearReplayAndCompletionEvidence(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var root int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES('manual','completion-evidence','series','Synthetic Series','episodes',2) RETURNING id").Scan(&root); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2005, 2, 3, 4, 5, 6, 0, time.UTC)
	rating := 8
	imported := library.ImportItem{TitleID: root, Status: "completed", Rating: &rating, ScoredAt: &stamp, Lists: []string{"Imported list"}}
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "wetrakr", imported); err != nil {
		t.Fatal(err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, root)
	if err != nil || entry.Status != "in_progress" || entry.ScoredAt == nil || !entry.ScoredAt.Equal(stamp) {
		t.Fatal(entry, err)
	}
	if _, err = s.ApplyImport(ctx, f.Owner.User.ID, "wetrakr", imported); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, root)
	in := library.EntryInput{Title: entry.Title, MediaType: entry.MediaType, Unit: entry.Unit, Total: entry.Total, Status: entry.Status, Version: entry.Version}
	if _, err = s.Replace(ctx, f.Owner, entry.ID, testkit.Key(t, f), in); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, root)
	if entry.ScoredAt == nil || !entry.ScoredAt.After(stamp) || entry.Rating != nil {
		t.Fatal("clear missing evidence", entry)
	}
	cleared := *entry.ScoredAt
	if _, err = s.ApplyImport(ctx, f.Owner.User.ID, "wetrakr", imported); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, root)
	if entry.Rating != nil || !entry.ScoredAt.Equal(cleared) {
		t.Fatal("import replay overwrote clear", entry)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE catalog_items SET release_status='Ended' WHERE id=$1", root); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyImport(ctx, f.Owner.User.ID, "wetrakr", imported); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.EntryForCatalog(ctx, f.Owner, root)
	if entry.Status != "completed" {
		t.Fatal("terminal evidence ignored", entry)
	}
}
