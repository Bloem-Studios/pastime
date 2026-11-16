package catalog_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// fakeTV is a synthetic provider: show 100 has specials plus two regular seasons.
type fakeTV struct {
	fetches, seasons atomic.Int32
	down             bool
}

func (*fakeTV) Name() string    { return "faketv" }
func (*fakeTV) Kinds() []string { return []string{"series", "movie"} }
func n(v int) *int              { return &v }
func (f *fakeTV) Search(ctx context.Context, kind, q string) ([]provider.Record, error) {
	if f.down {
		return nil, provider.ErrUnavailable
	}
	return []provider.Record{{Provider: "faketv", Kind: kind, ProviderID: "100", Title: "Synthetic Harbour"}}, nil
}
func (f *fakeTV) Fetch(ctx context.Context, kind, id string) (provider.Record, error) {
	f.fetches.Add(1)
	if f.down {
		return provider.Record{}, provider.ErrUnavailable
	}
	if kind == "movie" {
		return provider.Record{Provider: "faketv", Kind: "movie", ProviderID: id, Title: "Synthetic Film " + id, RuntimeMinutes: n(95), Total: n(95)}, nil
	}
	r := provider.Record{Provider: "faketv", Kind: "series", ProviderID: id, Title: "Synthetic Harbour", Total: n(5), ChildrenLoaded: true}
	for _, s := range []struct{ num, count int }{{0, 1}, {1, 3}, {2, 2}} {
		r.Children = append(r.Children, provider.Record{Provider: "faketv", Kind: "season", ProviderID: id + ":" + strconv.Itoa(s.num), Title: "Season " + strconv.Itoa(s.num), SeasonNumber: n(s.num), Total: n(s.count)})
	}
	return r, nil
}
func (f *fakeTV) FetchChild(ctx context.Context, parent, child provider.Record) (provider.Record, error) {
	f.seasons.Add(1)
	count := map[int]int{0: 1, 1: 3, 2: 2}[*child.SeasonNumber]
	out := child
	out.ChildrenLoaded = true
	for e := 1; e <= count; e++ {
		// Season 2's last episode airs in ten days; everything else aired last year.
		aired := time.Now().UTC().AddDate(-1, 0, e)
		if *child.SeasonNumber == 2 && e == count {
			aired = time.Now().UTC().AddDate(0, 0, 10)
		}
		out.Children = append(out.Children, provider.Record{Provider: "faketv", Kind: "episode", ProviderID: parent.ProviderID + "-" + strconv.Itoa(*child.SeasonNumber) + "-" + strconv.Itoa(e),
			Title: "Episode " + strconv.Itoa(e), SeasonNumber: child.SeasonNumber, EpisodeNumber: n(e), ReleaseDate: &aired, RuntimeMinutes: n(30)})
	}
	return out, nil
}

func setup(t *testing.T) (*testkit.Fixture, *catalog.Service, *fakeTV) {
	f := testkit.New(t)
	tv := &fakeTV{}
	return f, &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"faketv": tv}, Defaults: map[string]string{"series": "faketv", "movie": "faketv"}}, tv
}

func episodes(t *testing.T, s *catalog.Service, show catalog.Item) map[int][]catalog.Item {
	t.Helper()
	seasons, err := s.Children(context.Background(), show.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int][]catalog.Item{}
	for _, season := range seasons {
		eps, err := s.Children(context.Background(), season.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[*season.SeasonNumber] = eps
	}
	return out
}

func TestResolveCachesAndLoadsSeasonsLazily(t *testing.T) {
	_, s, tv := setup(t)
	ctx := context.Background()
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Resolve(ctx, "faketv", "series", "100"); err != nil || tv.fetches.Load() != 1 {
		t.Fatalf("fresh record refetched: %d %v", tv.fetches.Load(), err)
	}
	eps := episodes(t, s, show)
	if len(eps[0]) != 1 || len(eps[1]) != 3 || len(eps[2]) != 2 || tv.seasons.Load() != 3 {
		t.Fatalf("unexpected episodes %v after %d season loads", len(eps), tv.seasons.Load())
	}
	episodes(t, s, show)
	if tv.seasons.Load() != 3 {
		t.Fatal("loaded seasons refetched")
	}
	if _, err = s.Resolve(ctx, "faketv", "book", "1"); err == nil {
		t.Fatal("unsupported kind accepted")
	}
	if _, err = s.Resolve(ctx, "unknown", "series", "1"); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestSearchFallsBackToLocalCatalogWhenProviderIsDown(t *testing.T) {
	_, s, tv := setup(t)
	ctx := context.Background()
	if _, err := s.Resolve(ctx, "faketv", "series", "100"); err != nil {
		t.Fatal(err)
	}
	live, err := s.Search(ctx, "series", "harbour")
	if err != nil || live.Source != "provider" || len(live.Items) != 1 || live.Items[0].CatalogID == nil {
		t.Fatalf("live search: %+v %v", live, err)
	}
	tv.down = true
	local, err := s.Search(ctx, "series", "harbor")
	if err != nil || local.Source != "local" || !local.Degraded || len(local.Items) != 1 {
		t.Fatalf("fuzzy local fallback: %+v %v", local, err)
	}
	// A stale record is still served when the provider cannot refresh it.
	s.MaxAge = time.Nanosecond
	if _, err = s.Resolve(ctx, "faketv", "series", "100"); err != nil {
		t.Fatal("stale metadata withheld during outage", err)
	}
}

func TestEpisodePlaysDriveProgressAndCompletion(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	eps := episodes(t, s, show)
	m, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: show.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: show.ID}); fault.Public(err).Code != "already_tracked" {
		t.Fatal("duplicate tracking accepted", err)
	}
	play := func(version int, items ...catalog.Item) library.Mutation {
		t.Helper()
		in := library.PlayInput{Version: version}
		for _, it := range items {
			in.Items = append(in.Items, library.PlayItem{CatalogID: it.ID})
		}
		out, err := lib.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The special counts as a play but not toward the regular-season total.
	v := play(m.Version, eps[0][0], eps[1][0])
	e, _ := lib.Get(ctx, f.Owner, m.ID)
	if e.Progress != 1 || e.Status != "in_progress" || *e.Total != 5 {
		t.Fatalf("after first episode: %d %s", e.Progress, e.Status)
	}
	v = play(v.Version, eps[1][1], eps[1][2], eps[2][0], eps[2][1])
	e, _ = lib.Get(ctx, f.Owner, m.ID)
	if e.Progress != 5 || e.Status != "completed" || e.CompletedCycles != 1 || e.FinishedAt == nil {
		t.Fatalf("after all episodes: %d %s %d", e.Progress, e.Status, e.CompletedCycles)
	}
	plays, err := lib.Plays(ctx, f.Owner, m.ID, 0)
	if err != nil || len(plays) != 6 {
		t.Fatalf("plays %d %v", len(plays), err)
	}
	// Removing a mistaken mark reopens the show and reverses the completion count.
	if v, err = lib.RemovePlay(ctx, f.Owner, m.ID, plays[5].ID, testkit.Key(t, f), v.Version); err != nil {
		t.Fatal(err)
	}
	e, _ = lib.Get(ctx, f.Owner, m.ID)
	if e.Progress != 4 || e.Status != "in_progress" || e.CompletedCycles != 0 {
		t.Fatalf("after unplay: %d %s %d", e.Progress, e.Status, e.CompletedCycles)
	}
	// Plays must target this show's own tree.
	movie, err := s.Resolve(ctx, "faketv", "movie", "7")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lib.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: v.Version, Items: []library.PlayItem{{CatalogID: movie.ID}}}); err == nil {
		t.Fatal("play outside the show accepted")
	}
	member := f.User(t, "catalog_member", "member")
	if _, err = lib.Plays(ctx, member, m.ID, 0); !errors.Is(err, fault.NotFound) {
		t.Fatal("another account read plays", err)
	}
	// A movie play completes it with runtime progress; a rewatch is a new cycle.
	mm, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: movie.ID})
	if err != nil {
		t.Fatal(err)
	}
	mv, err := lib.AddPlays(ctx, f.Owner, mm.ID, testkit.Key(t, f), library.PlayInput{Version: mm.Version, Items: []library.PlayItem{{CatalogID: movie.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	e, _ = lib.Get(ctx, f.Owner, mm.ID)
	if e.Status != "completed" || e.Progress != 95 || e.ImageURL != "" || e.CatalogID == nil || *e.CatalogID != movie.ID {
		t.Fatalf("movie after play: %+v", e.Summary)
	}
	if _, err = lib.Restart(ctx, f.Owner, mm.ID, testkit.Key(t, f), mv.Version); err != nil {
		t.Fatal(err)
	}
	if plays, _ = lib.Plays(ctx, f.Owner, mm.ID, 0); len(plays) != 0 {
		t.Fatal("new cycle inherited plays")
	}
	if plays, _ = lib.Plays(ctx, f.Owner, mm.ID, 1); len(plays) != 1 {
		t.Fatal("prior cycle history lost")
	}
}

func TestStaleTrackedRecordsAreScheduledOncePerDay(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (library.Store{Pool: f.Pool}).Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: show.ID}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(48 * time.Hour)
	for _, want := range []int{1, 0} {
		if n, err := s.EnqueueStale(ctx, later); err != nil || n != want {
			t.Fatalf("scheduled %d %v", n, err)
		}
	}
	var jobs int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE kind=$1 AND dedupe_key LIKE $2", catalog.RefreshJob, "%:"+later.UTC().Format("2006-01-02")).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("duplicate refresh jobs: %d %v", jobs, err)
	}
}

func TestHistoryStatsCalendarAndHome(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	eps := episodes(t, s, show)
	m, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: show.ID})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	v, err := lib.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: m.Version, Items: []library.PlayItem{{CatalogID: eps[1][0].ID, PlayedAt: &past}, {CatalogID: eps[1][1].ID}}})
	if err != nil {
		t.Fatal(err)
	}
	// Up next is the first unwatched aired regular episode; the calendar has the future one.
	home, err := lib.Home(ctx, f.Owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(home.UpNext) != 1 || home.UpNext[0].Episode.CatalogID != eps[1][2].ID || len(home.Continue) != 1 || len(home.Upcoming) != 1 || home.Upcoming[0].CatalogID != eps[2][1].ID {
		t.Fatalf("home: up next %+v upcoming %+v", home.UpNext, home.Upcoming)
	}
	// History pages newest first with a keyset cursor.
	first, err := lib.History(ctx, f.Owner, library.HistoryFilter{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == nil || first.Items[0].CatalogID != eps[1][1].ID || *first.Items[0].Minutes != 30 {
		t.Fatalf("history page 1: %+v %v", first, err)
	}
	second, err := lib.History(ctx, f.Owner, library.HistoryFilter{Limit: 1, Cursor: *first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].CatalogID != eps[1][0].ID || second.NextCursor != nil {
		t.Fatalf("history page 2: %+v %v", second, err)
	}
	if _, err = lib.History(ctx, f.Owner, library.HistoryFilter{Limit: 1, Cursor: *first.NextCursor, MediaType: "movie"}); err == nil {
		t.Fatal("cursor reused across filters")
	}
	stats, err := lib.Insights(ctx, f.Owner, time.Now().AddDate(0, 0, -7), time.Now().Add(time.Hour))
	if err != nil || stats.Plays != 2 || stats.Minutes != 60 || len(stats.Days) != 2 || len(stats.ByType) != 1 || stats.ByType[0].Plays != 2 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	// Deleting from history reprojects the entry.
	if _, err = lib.DeletePlay(ctx, f.Owner, second.Items[0].PlayID, testkit.Key(t, f)); err != nil {
		t.Fatal(err)
	}
	if e, _ := lib.Get(ctx, f.Owner, m.ID); e.Progress != 1 || e.Version <= v.Version {
		t.Fatalf("after history delete: progress %d version %d", e.Progress, e.Version)
	}
	// The daily rollup and the up-next pointer follow the deletion.
	if stats, err = lib.Insights(ctx, f.Owner, time.Now().AddDate(0, 0, -7), time.Now().Add(time.Hour)); err != nil || stats.Plays != 1 || stats.Minutes != 30 || len(stats.Days) != 1 {
		t.Fatalf("stats after delete: %+v %v", stats, err)
	}
	if home, err = lib.Home(ctx, f.Owner, time.Now()); err != nil || len(home.UpNext) != 1 || home.UpNext[0].Episode.CatalogID != eps[1][0].ID {
		t.Fatalf("up next after delete: %+v %v", home.UpNext, err)
	}
	// Plays are counted under the catalog's media type, so it cannot be edited away.
	e, _ := lib.Get(ctx, f.Owner, m.ID)
	if _, err = lib.Replace(ctx, f.Owner, m.ID, testkit.Key(t, f), library.EntryInput{Version: e.Version, Title: e.Title, MediaType: "book", Unit: "pages", Status: e.Status}); !strings.Contains(fault.Public(err).Message, "media type") {
		t.Fatal("catalog entry changed media type", fault.Public(err).Message)
	}
	member := f.User(t, "insight_member", "member")
	if other, _ := lib.History(ctx, member, library.HistoryFilter{}); len(other.Items) != 0 {
		t.Fatal("history leaked across accounts")
	}
	if _, err = lib.DeletePlay(ctx, member, first.Items[0].PlayID, testkit.Key(t, f)); !errors.Is(err, fault.NotFound) {
		t.Fatal("another account deleted a play", err)
	}
	if cal, _ := lib.Calendar(ctx, member, time.Now(), time.Now().AddDate(0, 1, 0)); len(cal) != 0 {
		t.Fatal("calendar leaked across accounts")
	}
}

func TestCalendarFeedFollowsAccountState(t *testing.T) {
	f, _, _ := setup(t)
	ctx := context.Background()
	member := f.User(t, "feed_member", "member")
	raw, err := f.Accounts.RotateFeed(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if a, err := f.Accounts.FeedActor(ctx, raw); err != nil || a.User.ID != member.User.ID || a.Allows("library:write") || a.IsAdmin() {
		t.Fatalf("feed actor: %+v %v", a, err)
	}
	again, err := f.Accounts.RotateFeed(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.FeedActor(ctx, raw); err == nil {
		t.Fatal("rotated feed secret still works")
	}
	if _, err = f.Accounts.UpdateUser(ctx, f.Owner, member.User.ID, identity.UserPatch{Version: member.User.Version, Active: ptr(false)}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.FeedActor(ctx, again); err == nil {
		t.Fatal("disabled account feed still works")
	}
}

func ptr[T any](v T) *T { return &v }

func TestSkippedEpisodesCompleteWithoutPlays(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	show, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	eps := episodes(t, s, show)
	m, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: show.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Skipping the first episode moves up next past it without recording a play.
	v, err := lib.Skip(ctx, f.Owner, m.ID, testkit.Key(t, f), m.Version, []string{strconv.FormatInt(eps[1][0].ID, 10)}, true)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := lib.Home(ctx, f.Owner, time.Now())
	if len(home.UpNext) != 1 || home.UpNext[0].Episode.CatalogID != eps[1][1].ID {
		t.Fatalf("up next after skip: %+v", home.UpNext)
	}
	var items []library.PlayItem
	for _, e := range []catalog.Item{eps[1][1], eps[1][2], eps[2][0], eps[2][1]} {
		items = append(items, library.PlayItem{CatalogID: e.ID})
	}
	if _, err = lib.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: v.Version, Items: items}); err != nil {
		t.Fatal(err)
	}
	e, _ := lib.Get(ctx, f.Owner, m.ID)
	if e.Status != "completed" || e.Progress != 5 {
		t.Fatalf("skip counts toward completion: %d %s", e.Progress, e.Status)
	}
	if plays, _ := lib.Plays(ctx, f.Owner, m.ID, 0); len(plays) != 4 {
		t.Fatalf("skip recorded as a play: %d", len(plays))
	}
	if skipped, _ := lib.Skipped(ctx, f.Owner, m.ID, 0); len(skipped) != 1 {
		t.Fatalf("skipped list %v", skipped)
	}
	// Unskipping reopens the show; skipping a foreign record is rejected.
	if _, err = lib.Skip(ctx, f.Owner, m.ID, testkit.Key(t, f), e.Version, []string{strconv.FormatInt(eps[1][0].ID, 10)}, false); err != nil {
		t.Fatal(err)
	}
	if e, _ = lib.Get(ctx, f.Owner, m.ID); e.Status != "in_progress" || e.Progress != 4 {
		t.Fatalf("after unskip: %d %s", e.Progress, e.Status)
	}
	movie, _ := s.Resolve(ctx, "faketv", "movie", "7")
	if _, err = lib.Skip(ctx, f.Owner, m.ID, testkit.Key(t, f), e.Version, []string{strconv.FormatInt(movie.ID, 10)}, true); err == nil {
		t.Fatal("skipped a record outside the show")
	}
}

func TestRemapMovesPlaysByEpisodeAndArtworkOverride(t *testing.T) {
	f, s, _ := setup(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	wrong, err := s.Resolve(ctx, "faketv", "series", "100")
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.Resolve(ctx, "faketv", "series", "200")
	if err != nil {
		t.Fatal(err)
	}
	wrongEps, rightEps := episodes(t, s, wrong), episodes(t, s, right)
	m, err := lib.Track(ctx, f.Owner, testkit.Key(t, f), library.TrackInput{CatalogID: wrong.ID})
	if err != nil {
		t.Fatal(err)
	}
	v, err := lib.AddPlays(ctx, f.Owner, m.ID, testkit.Key(t, f), library.PlayInput{Version: m.Version, Items: []library.PlayItem{{CatalogID: wrongEps[1][0].ID}, {CatalogID: wrongEps[1][1].ID}}})
	if err != nil {
		t.Fatal(err)
	}
	movie, _ := s.Resolve(ctx, "faketv", "movie", "7")
	if _, err = lib.Remap(ctx, f.Owner, m.ID, testkit.Key(t, f), v.Version, movie.ID); err == nil {
		t.Fatal("remapped a show to a film")
	}
	res, err := lib.Remap(ctx, f.Owner, m.ID, testkit.Key(t, f), v.Version, right.ID)
	if err != nil || res.PlaysMoved != 2 || res.PlaysDropped != 0 {
		t.Fatalf("remap %+v %v", res, err)
	}
	plays, _ := lib.Plays(ctx, f.Owner, m.ID, 0)
	if len(plays) != 2 || plays[0].CatalogID != rightEps[1][0].ID && plays[1].CatalogID != rightEps[1][0].ID {
		t.Fatalf("plays after remap: %+v", plays)
	}
	e, _ := lib.Get(ctx, f.Owner, m.ID)
	if *e.CatalogID != right.ID || e.Progress != 2 {
		t.Fatalf("entry after remap: %+v", e.Summary)
	}
	if _, err = lib.SetArtwork(ctx, f.Owner, m.ID, testkit.Key(t, f), e.Version, "http://insecure.example/a.jpg"); err == nil {
		t.Fatal("insecure artwork accepted")
	}
	if _, err = lib.SetArtwork(ctx, f.Owner, m.ID, testkit.Key(t, f), e.Version, "https://images.example/cover.jpg"); err != nil {
		t.Fatal(err)
	}
	if e, _ = lib.Get(ctx, f.Owner, m.ID); e.ImageURL != "https://images.example/cover.jpg" {
		t.Fatalf("override not applied: %q", e.ImageURL)
	}
}
