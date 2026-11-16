package library_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"strings"
	"testing"
	"time"
)

func TestTrackUnicodeTitle(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	for i, title := range []string{strings.Repeat("界", 100), strings.Repeat("😀", 250)} {
		var id int64
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual',$1,'book',$2,'pages') RETURNING id", string(rune('a'+i)), title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		out, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id})
		if err != nil {
			t.Fatal(err)
		}
		entry, err := s.Get(ctx, f.Owner, out.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := []rune(title)
		if len(want) > 200 {
			want = want[:200]
		}
		if entry.Title != string(want) {
			t.Fatal("title not clipped by characters")
		}
	}
}

func TestCompletionPathsAndCycleDates(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	for _, path := range []string{"replace", "bulk"} {
		var id int64
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES('manual',$1,'book','Synthetic book','pages',240) RETURNING id", path).Scan(&id); err != nil {
			t.Fatal(err)
		}
		m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id})
		if err != nil {
			t.Fatal(err)
		}
		if path == "replace" {
			in := input("Synthetic book")
			in.Version = m.Version
			in.Status = "completed"
			in.Progress = 240
			_, err = s.Replace(ctx, f.Owner, m.ID, testkit.Key(t, f), in)
		} else {
			_, err = s.Bulk(ctx, f.Owner, testkit.Key(t, f), library.BulkInput{Action: "set_status", Status: "completed", EntryIDs: []string{fmt.Sprint(m.ID)}})
		}
		if err != nil {
			t.Fatal(err)
		}
		plays, err := s.Plays(ctx, f.Owner, m.ID, 0)
		if err != nil || len(plays) != 1 {
			t.Errorf("%s completion plays=%d error=%v", path, len(plays), err)
		}
		_, err = f.Pool.Exec(ctx, "UPDATE library_entries SET started_at='2001-01-01' WHERE id=$1", m.ID)
		if err != nil {
			t.Fatal(err)
		}
		e, err := s.Get(ctx, f.Owner, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Restart(ctx, f.Owner, m.ID, testkit.Key(t, f), e.Version)
		if err != nil {
			t.Fatal(err)
		}
		var recent bool
		if err = f.Pool.QueryRow(ctx, "SELECT started_at>'2020-01-01' FROM library_entries WHERE id=$1", m.ID).Scan(&recent); err != nil || !recent {
			t.Errorf("restart retained previous cycle date: %v", err)
		}
	}
}
func TestEditionTotalSurvivesReprojection(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES('manual','edition-review','book','Synthetic book','pages',240) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.SetEdition(ctx, f.Owner, m.ID, testkit.Key(t, f), m.Version, &library.Edition{ID: "synthetic", Pages: ptr(320)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: m.Version, Items: []library.PlayItem{{CatalogID: id}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, f.Owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Total == nil || *e.Total != 320 || e.Progress != 320 {
		t.Fatal("edition page count lost", e.Total, e.Progress)
	}
}

func TestCollaborationRejectsRevokedSession(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	l, err := s.CreateCollab(ctx, f.Owner, library.CollabInput{Name: "Synthetic list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateCollab(ctx, f.Owner, l.ID, library.CollabInput{Name: "Changed", Version: l.Version}); err == nil {
		t.Error("revoked actor edited list")
	}
	if _, err = s.CreateCollab(ctx, f.Owner, library.CollabInput{Name: "Changed"}); err == nil {
		t.Error("revoked actor created list")
	}
	if err = s.DeleteCollab(ctx, f.Owner, l.ID); err == nil {
		t.Error("revoked actor deleted list")
	}
}

func TestChangeFeedOrdersConcurrentCommits(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	first := create(t, f, s, f.Owner, "First")
	second := create(t, f, s, f.Owner, "Second")
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "UPDATE library_entries SET notes='first' WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.Pool.Exec(ctx, "UPDATE library_entries SET notes='second' WHERE id=$1", second.ID)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("later change committed ahead of pending cursor: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCopyRejectsForeignCustomField(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	m := create(t, f, s, f.Owner, "Book")
	if _, err := s.SaveCopy(ctx, f.Owner, m.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "ebook", Custom: map[string]any{"999999": "private"}}); err == nil {
		t.Fatal("unknown custom field accepted")
	}
	field, err := s.SaveField(ctx, f.Owner, 0, testkit.Key(t, f), library.FieldInput{Name: "Condition", Kind: "choice", Choices: []string{"new", "used"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveCopy(ctx, f.Owner, m.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "ebook", Custom: map[string]any{fmt.Sprint(field.ID): "invalid"}}); err == nil {
		t.Fatal("unlisted choice accepted")
	}
}

func TestInsightsUseExactWindow(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,runtime_minutes) VALUES('manual','window','movie','Window','minutes',90) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "INSERT INTO plays(user_id,entry_id,catalog_item_id,cycle,played_at) VALUES($1,$2,$3,1,'2025-01-01 09:00Z'),($1,$2,$3,2,'2025-01-01 15:00Z')", f.Owner.User.ID, m.ID, id); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	out, err := s.Insights(ctx, f.Owner, from, from.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if out.Plays != 1 || out.Minutes != 90 {
		t.Fatal("partial-day window included other plays", out.Plays, out.Minutes)
	}
}

func TestRollbackKeepsEditsMadeDuringImport(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var id int64
	var run string
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES('manual','rollback-review','book','Synthetic','pages',240) RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(ctx, "INSERT INTO import_runs(user_id,source) VALUES($1,'native') RETURNING id", f.Owner.User.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "native", library.ImportItem{RunID: run, TitleID: id}); err != nil {
		t.Fatal(err)
	}
	e, err := s.EntryForCatalog(ctx, f.Owner, id)
	if err != nil {
		t.Fatal(err)
	}
	in := input("Edited during import")
	in.Version = e.Version
	if _, err = s.Replace(ctx, f.Owner, e.ID, testkit.Key(t, f), in); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.RollbackImport(ctx, f.Owner.User.ID, run, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, f.Owner, e.ID); err != nil {
		t.Fatal("user's edits were deleted", err)
	}
}
func TestRemapReplayKeepsCounts(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var old, newID int64
	for i, dst := range []*int64{&old, &newID} {
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit,total) VALUES('manual',$1,'movie','Synthetic','minutes',90) RETURNING id", fmt.Sprint(i)).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	m, err := s.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: old, Status: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	key := testkit.Key(t, f)
	first, err := s.Remap(ctx, f.Owner, m.ID, key, m.Version, newID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Remap(ctx, f.Owner, m.ID, key, m.Version, newID)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlaysMoved != 1 || again.PlaysMoved != first.PlaysMoved || !again.Replayed {
		t.Fatal("remap receipt lost counts", first, again)
	}
}

func TestAcceptedRecommendationSchedulesMetadata(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	other := f.User(t, "recommender", "member")
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual','recommend-review','series','Synthetic series','episodes') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Recommend(ctx, other, library.RecommendInput{To: f.Owner.User.Username, CatalogID: fmt.Sprint(id)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Respond(ctx, f.Owner, rec.ID, true); err != nil {
		t.Fatal(err)
	}
	var queued bool
	if err = f.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM jobs WHERE kind='catalog_refresh' AND payload->>'catalog_id'=$1)", fmt.Sprint(id)).Scan(&queued); err != nil || !queued {
		t.Fatal("accepted title omitted metadata scheduling", err)
	}
}

func TestRecommendationResendPreservesRecipientDecision(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	sender := f.User(t, "repeat_sender", "member")
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual','repeat','movie','Repeat','minutes') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	in := library.RecommendInput{To: f.Owner.User.Username, CatalogID: fmt.Sprint(id)}
	rec, err := s.Recommend(ctx, sender, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Respond(ctx, f.Owner, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	rec, err = s.Recommend(ctx, sender, in)
	if err != nil || rec.Status != "dismissed" {
		t.Fatal("sender reset recipient decision", rec.Status, err)
	}
}

func TestCollectionPaginationKeepsFullTotals(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	entry := create(t, f, s, f.Owner, "Many copies")
	if _, err := f.Pool.Exec(ctx, `INSERT INTO owned_copies(user_id,entry_id,format,price_cents,currency) SELECT $1,$2,'dvd',100,'EUR' FROM generate_series(1,501)`, f.Owner.User.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	cursor := ""
	for {
		p, err := s.CopyPage(ctx, f.Owner, 0, "", cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 501 || p.Titles != 1 || p.Spent["EUR"] != 50100 || p.Formats["dvd"] != 501 {
			t.Fatal("totals describe only a page", p.Total, p.Spent)
		}
		for _, c := range p.Items {
			if seen[c.ID] {
				t.Fatal("duplicate page member")
			}
			seen[c.ID] = true
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if len(seen) != 501 {
		t.Fatal("collection truncated", len(seen))
	}
	other := f.User(t, "other_collector", "member")
	if _, err := s.CopyPage(ctx, other, 0, "", cursor, 100); err == nil {
		t.Fatal("cross-account cursor accepted")
	}
}

func TestUnlistedShareRequiresRevocableSecret(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	slug := "guessable"
	m, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Secret list", Visibility: "unlisted", Slug: &slug, Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Public(ctx, f.Owner.User.Username, slug); err == nil {
		t.Fatal("guessable slug grants unlisted access")
	}
	l, err := s.List(ctx, f.Owner, m.ID)
	if err != nil || l.ShareToken == nil || l.ShareExpiresAt == nil {
		t.Fatal(l, err)
	}
	old := *l.ShareToken
	if _, err = s.Public(ctx, f.Owner.User.Username, old); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveList(ctx, f.Owner, l.ID, testkit.Key(t, f), library.ListInput{Name: l.Name, Visibility: l.Visibility, Slug: l.Slug, Kind: l.Kind, Version: l.Version, RotateShare: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Public(ctx, f.Owner.User.Username, old); err == nil {
		t.Fatal("rotated link still works")
	}
	l, err = s.List(ctx, f.Owner, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE personal_lists SET share_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Public(ctx, f.Owner.User.Username, *l.ShareToken); err == nil {
		t.Fatal("expired link still works")
	}
}

func TestPublicListContinuesBeyondFiveHundred(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	slug := "long-list"
	m, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Long list", Visibility: "public", Slug: &slug, Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `WITH entries AS (INSERT INTO library_entries(user_id,title,media_type,unit) SELECT $1,'Title '||n,'book','pages' FROM generate_series(1,501) n RETURNING id)
 INSERT INTO list_entries(user_id,list_id,entry_id,position) SELECT $1,$2,id,row_number() OVER(ORDER BY id) FROM entries`, f.Owner.User.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	cursor := ""
	count := 0
	for {
		p, err := s.PublicPage(ctx, f.Owner.User.Username, slug, cursor, 100)
		if err != nil {
			t.Fatal(err)
		}
		count += len(p.Items)
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if count != 501 {
		t.Fatal("public list truncated", count)
	}
}

func TestSmartListSnapshotFreezesMembership(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	create(t, f, s, f.Owner, "First matching film")
	source, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Live", Kind: "smart", Rules: library.Rules{MediaTypes: []string{"book"}}})
	if err != nil {
		t.Fatal(err)
	}
	ref := fmt.Sprint(source.ID)
	frozen, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Frozen", Kind: "manual", SnapshotOf: &ref})
	if err != nil {
		t.Fatal(err)
	}
	create(t, f, s, f.Owner, "Second matching film")
	live, err := s.Entries(ctx, f.Owner, library.Filter{ListID: source.ID})
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := s.Entries(ctx, f.Owner, library.Filter{ListID: frozen.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(live.Items) != 2 || len(fixed.Items) != 1 {
		t.Fatal("snapshot membership changed", len(live.Items), len(fixed.Items))
	}
	other := f.User(t, "snapshot_other", "member")
	if _, err = s.SaveList(ctx, other, 0, testkit.Key(t, f), library.ListInput{Name: "Stolen", Kind: "manual", SnapshotOf: &ref}); err == nil {
		t.Fatal("copied another account list")
	}
}

func TestUnknownRuntimeBoundaryWindow(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual','unknown-runtime','movie','Unknown runtime','minutes') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "native", library.ImportItem{TitleID: id, Plays: []library.ImportPlay{{TargetID: id, At: at, ExternalID: "unknown"}}}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Insights(ctx, f.Owner, at.Add(-time.Minute), at.Add(time.Minute))
	if err != nil || stats.Plays != 1 || stats.UnknownRuntime != 1 {
		t.Fatal("unknown runtime broke partial-day statistics", stats, err)
	}
}

func TestRemapRetainsUnmatchedTrackWithoutGuessing(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var oldID, newID, trackID int64
	for _, v := range []struct {
		id  *int64
		pid string
	}{{&oldID, "old-album"}, {&newID, "new-album"}} {
		if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual',$1,'music',$1,'tracks') RETURNING id", v.pid).Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,parent_id,kind,title,unit) VALUES('manual','old-track',$1,'track','Original track','items') RETURNING id", oldID).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, "INSERT INTO catalog_items(provider,provider_id,parent_id,kind,title,unit) VALUES('manual','new-track-a',$1,'track','Other A','items'),('manual','new-track-b',$1,'track','Other B','items')", newID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyImport(ctx, f.Owner.User.ID, "native", library.ImportItem{TitleID: oldID, Plays: []library.ImportPlay{{TargetID: trackID, At: time.Now(), ExternalID: "track"}}}); err != nil {
		t.Fatal(err)
	}
	entry, err := s.EntryForCatalog(ctx, f.Owner, oldID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewRemap(ctx, f.Owner, entry.ID, entry.Version, newID)
	if err != nil || preview.PlaysMoved != 0 || preview.PlaysRetained != 1 {
		t.Fatal("incorrect remap preview", preview, err)
	}
	key := testkit.Key(t, f)
	if _, err = s.Remap(ctx, f.Owner, entry.ID, key, preview.Version, newID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.Remap(ctx, f.Owner, entry.ID, key, preview.Version, newID)
	if err != nil || !replay.Replayed || replay.PlaysRetained != 1 {
		t.Fatal("retained count missing from receipt", replay, err)
	}
	var actual int64
	if err = f.Pool.QueryRow(ctx, "SELECT catalog_item_id FROM plays WHERE user_id=$1 AND entry_id=$2", f.Owner.User.ID, entry.ID).Scan(&actual); err != nil || actual != trackID {
		t.Fatal("unmatched play discarded or assigned arbitrarily", actual, err)
	}
}

func TestRecommendationOptOut(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	sender := f.User(t, "optout_sender", "member")
	if _, err := f.Pool.Exec(ctx, `INSERT INTO user_preferences(profile_id,prefs) VALUES($1,'{"decline_recommendations":true}') ON CONFLICT(profile_id) DO UPDATE SET prefs=EXCLUDED.prefs`, f.Owner.TrackingID()); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.Pool.QueryRow(ctx, "INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('manual','optout','movie','Opt out','minutes') RETURNING id").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recommend(ctx, sender, library.RecommendInput{To: f.Owner.User.Username, CatalogID: fmt.Sprint(id)}); err == nil {
		t.Fatal("recipient opt-out ignored")
	}
}

func TestCollectionSearchIsPrivateAndCursorBound(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	entry := create(t, f, s, f.Owner, "Synthetic Collector")
	if _, err := f.Pool.Exec(ctx, "INSERT INTO owned_copies(user_id,entry_id,format,edition) SELECT $1,$2,'digital','Annual' FROM generate_series(1,3)", f.Owner.User.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	found, err := s.CopyPage(ctx, f.Owner, 0, "", "", 1, "annual")
	if err != nil || found.Total != 3 || len(found.Items) != 1 || found.NextCursor == nil {
		t.Fatal(found, err)
	}
	if _, err = s.CopyPage(ctx, f.Owner, 0, "", *found.NextCursor, 1, "Collector"); err == nil {
		t.Fatal("cursor accepted for different query")
	}
	missing, err := s.CopyPage(ctx, f.Owner, 0, "", "", 10, "missing")
	if err != nil || missing.Total != 0 {
		t.Fatal(missing, err)
	}
	other := f.User(t, "search_other", "member")
	private, err := s.CopyPage(ctx, other, 0, "", "", 10, "annual")
	if err != nil || private.Total != 0 || len(private.Items) != 0 {
		t.Fatal(private, err)
	}
}

// A shared list's cursor names only the last entry: the owner's rating and activity
// time stay on the server, whatever sort the owner saved. A cursor naming an entry
// outside the list is refused rather than used as an anchor.
func TestPublicListCursorHoldsNoPrivateSortValue(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	s := library.Store{Pool: f.Pool}
	var ids []int64
	rows, err := f.Pool.Query(ctx, `INSERT INTO library_entries(user_id,title,media_type,unit,rating,last_activity_at)
		VALUES($1,'Shared high','book','pages',9,'2026-01-03T04:05:06Z'),($1,'Shared low','book','pages',7,'2026-01-02T04:05:06Z'),($1,'Shared unrated','book','pages',NULL,'2026-01-01T04:05:06Z') RETURNING id`, f.Owner.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	var outside int64
	if err = f.Pool.QueryRow(ctx, `INSERT INTO library_entries(user_id,title,media_type,unit,rating) VALUES($1,'Private film','movie','minutes',8) RETURNING id`, f.Owner.User.ID).Scan(&outside); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ kind, sort string }{{"manual", "rating"}, {"manual", "activity"}, {"smart", "rating"}, {"smart", "activity"}} {
		slug := c.kind + "-" + c.sort
		m, err := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: slug, Visibility: "public", Slug: &slug, Kind: c.kind, Sort: c.sort, Rules: library.Rules{MediaTypes: []string{"book"}}})
		if err != nil {
			t.Fatal(err)
		}
		if c.kind == "manual" {
			if _, err = f.Pool.Exec(ctx, `INSERT INTO list_entries(user_id,list_id,entry_id,position) SELECT $1,$2,id,n FROM unnest($3::bigint[]) WITH ORDINALITY u(id,n)`, f.Owner.User.ID, m.ID, ids); err != nil {
				t.Fatal(err)
			}
		}
		cursor := ""
		var titles []string
		for {
			p, err := s.PublicPage(ctx, f.Owner.User.Username, slug, cursor, 1)
			if err != nil {
				t.Fatal(slug, err)
			}
			for _, item := range p.Items {
				titles = append(titles, item.Title)
			}
			if p.NextCursor == nil {
				break
			}
			cursor = *p.NextCursor
			data, err := base64.RawURLEncoding.DecodeString(cursor)
			if err != nil {
				t.Fatal(err)
			}
			var parts []json.RawMessage
			if err = json.Unmarshal(data, &parts); err != nil || len(parts) != 3 || string(parts[1]) != "null" {
				t.Fatalf("%s cursor carries a sort value: %s", slug, data)
			}
		}
		if strings.Join(titles, "|") != "Shared high|Shared low|Shared unrated" {
			t.Fatalf("%s public order: %v", slug, titles)
		}
		first, err := s.PublicPage(ctx, f.Owner.User.Username, slug, "", 1)
		if err != nil || first.NextCursor == nil {
			t.Fatal(slug, err)
		}
		data, _ := base64.RawURLEncoding.DecodeString(*first.NextCursor)
		var parts []json.RawMessage
		_ = json.Unmarshal(data, &parts)
		forged, _ := json.Marshal([]any{parts[0], nil, outside})
		if _, err = s.PublicPage(ctx, f.Owner.User.Username, slug, base64.RawURLEncoding.EncodeToString(forged), 1); err == nil {
			t.Fatalf("%s accepted a cursor naming an entry outside the list", slug)
		}
	}
}
