package catalog_test

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// matchTMDB is a synthetic TMDB for episode matching. Each show lists its seasons as
// episode TMDB ids in TMDB's own order (episode number = position + 1), and finds
// answers /find lookups by "source:id", typed the way TMDB answers them. An episode is
// titled "Episode <id>" with no air date unless named says otherwise.
type matchTMDB struct {
	mu                              sync.Mutex
	shows                           map[string]map[int][]string
	tvdb                            map[string]string
	finds                           map[string]provider.Found
	titles, airDates                map[string]string
	fetches, seasonLoads, findCalls atomic.Int32
}

func newMatchTMDB() *matchTMDB {
	return &matchTMDB{shows: map[string]map[int][]string{}, tvdb: map[string]string{}, finds: map[string]provider.Found{},
		titles: map[string]string{}, airDates: map[string]string{}}
}

// named gives an episode TMDB's title and air date (YYYY-MM-DD).
func (m *matchTMDB) named(id, title, airDate string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.titles[id], m.airDates[id] = title, airDate
}

// numbered fills a season with ids "<show>-<season>-<episode>".
func (m *matchTMDB) numbered(show string, season, count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shows[show] == nil {
		m.shows[show] = map[int][]string{}
	}
	ids := make([]string, count)
	for i := range ids {
		ids[i] = show + "-" + strconv.Itoa(season) + "-" + strconv.Itoa(i+1)
	}
	m.shows[show][season] = ids
}
func (m *matchTMDB) season(show string, season int, ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shows[show] == nil {
		m.shows[show] = map[int][]string{}
	}
	m.shows[show][season] = ids
}
func (m *matchTMDB) appendEpisode(show string, season int, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shows[show][season] = append(m.shows[show][season], id)
}

func (*matchTMDB) Name() string    { return "tmdb" }
func (*matchTMDB) Kinds() []string { return []string{"movie", "series"} }
func (*matchTMDB) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, provider.ErrUnavailable
}
func (m *matchTMDB) Fetch(_ context.Context, kind, id string) (provider.Record, error) {
	m.fetches.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	seasons, ok := m.shows[id]
	if kind != "series" || !ok {
		return provider.Record{}, provider.ErrNotFound
	}
	r := provider.Record{Provider: "tmdb", Kind: "series", ProviderID: id, Title: "Synthetic show " + id, ChildrenLoaded: true, ExternalIDs: map[string]string{}}
	if v := m.tvdb[id]; v != "" {
		r.ExternalIDs["tvdb"] = v
	}
	numbers := make([]int, 0, len(seasons))
	for number := range seasons {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		r.Children = append(r.Children, provider.Record{Provider: "tmdb", Kind: "season", ProviderID: id + ":" + strconv.Itoa(number),
			Title: "Season " + strconv.Itoa(number), SeasonNumber: n(number), Total: n(len(seasons[number]))})
	}
	return r, nil
}
func (m *matchTMDB) FetchChild(_ context.Context, parent, child provider.Record) (provider.Record, error) {
	m.seasonLoads.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	number := *child.SeasonNumber
	ids, ok := m.shows[parent.ProviderID][number]
	if !ok {
		return provider.Record{}, provider.ErrNotFound
	}
	out := provider.Record{Provider: "tmdb", Kind: "season", ProviderID: child.ProviderID, Title: child.Title, SeasonNumber: n(number), ChildrenLoaded: true, Total: n(len(ids))}
	for i, id := range ids {
		ep := provider.Record{Provider: "tmdb", Kind: "episode", ProviderID: id, Title: "Episode " + id, SeasonNumber: n(number), EpisodeNumber: n(i + 1)}
		if title := m.titles[id]; title != "" {
			ep.Title = title
		}
		if aired, err := time.Parse(time.DateOnly, m.airDates[id]); err == nil {
			ep.ReleaseDate = &aired
		}
		out.Children = append(out.Children, ep)
	}
	return out, nil
}
func (m *matchTMDB) Find(_ context.Context, source, id string) (provider.Found, error) {
	m.findCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if found, ok := m.finds[source+":"+id]; ok {
		return found, nil
	}
	return provider.Found{}, provider.ErrNotFound
}

func matchService(t *testing.T, tv *matchTMDB) (*testkit.Fixture, *catalog.Service) {
	t.Helper()
	f := testkit.New(t)
	return f, &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": tv}, Defaults: map[string]string{"series": "tmdb", "movie": "tmdb"}}
}

func episodeRef(show string, season, number int) catalog.Reference {
	return catalog.Reference{Kind: "episode", ShowTMDB: show, Season: n(season), Episode: n(number)}
}

func wantEpisode(t *testing.T, m catalog.Match, err error, id string, season, number int) {
	t.Helper()
	if err != nil {
		t.Fatalf("want %s at S%dE%d: %v", id, season, number, err)
	}
	if m.Episode == nil || m.Episode.ProviderID != id || m.Episode.SeasonNumber == nil || *m.Episode.SeasonNumber != season ||
		m.Episode.EpisodeNumber == nil || *m.Episode.EpisodeNumber != number {
		t.Fatalf("want %s at S%dE%d, got %+v", id, season, number, m.Episode)
	}
}

// wantRefused accepts only a matching refusal, never a provider outage or a match.
func wantRefused(t *testing.T, m catalog.Match, err error) {
	t.Helper()
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Code != "no_match" {
		episode := ""
		if m.Episode != nil {
			episode = m.Episode.ProviderID
		}
		t.Fatalf("want a no_match refusal, got episode %q and %v", episode, err)
	}
}

// wantPending accepts only a retryable "not listed yet" answer.
func wantPending(t *testing.T, m catalog.Match, err error) {
	t.Helper()
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 503 || fe.Code != "catalog_pending" || m.Episode != nil {
		episode := ""
		if m.Episode != nil {
			episode = m.Episode.ProviderID
		}
		t.Fatalf("want a catalog_pending retry, got episode %q and %v", episode, err)
	}
}

func age(t *testing.T, f *testkit.Fixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// A1: a series TVDB id that TVDB also uses for another show's episode must not veto the
// show; TMDB's /find answers both, and only the TV result names a series.
func TestEpisodeMatchIgnoresAnotherShowsEpisodeSharingTheSeriesTVDBID(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("125988", 1, 10)
	tv.numbered("125988", 2, 10)
	tv.tvdb["125988"] = "403245"
	tv.finds["tvdb:403245"] = provider.Found{Shows: []string{"125988"}, Episodes: []provider.FoundEpisode{{ID: "266535", ShowID: "3797", Season: 2, Episode: 8}}}
	_, s := matchService(t, tv)
	ref := episodeRef("125988", 2, 4)
	ref.ShowTVDB, ref.TMDB = "403245", "125988-2-4"
	m, err := s.MatchVideo(context.Background(), ref)
	wantEpisode(t, m, err, "125988-2-4", 2, 4)
	if m.Title.ProviderID != "125988" {
		t.Fatal("matched the wrong show", m.Title.ProviderID)
	}
}

// Adults S2E2 "Fake ID": Bloem recorded TMDB id 7371062, which TMDB lists as S2E3
// "Talia", and TMDB's own mapping of the episode's TVDB id agrees with that id. An id
// found at other numbers than the source's is not trusted on its own: the title or air
// date must agree with exactly one of it and the episode at the source's numbers.
func TestEpisodeMatchSettlesAnIDAtOtherNumbersByTitleOrAirDate(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("260458", 1, 3)
	tv.season("260458", 2, "7371061", "7371063", "7371062", "7371064")
	tv.named("7371061", "The Scrub", "2026-08-27")
	tv.named("7371063", "Fake ID", "2026-08-27")
	tv.named("7371062", "Talia", "2026-09-03")
	tv.named("7371064", "Raccoon", "2026-09-03")
	tv.finds["tvdb:11826831"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "7371062", ShowID: "260458", Season: 2, Episode: 3}}}
	_, s := matchService(t, tv)
	ctx := context.Background()
	fakeID := episodeRef("260458", 2, 2)
	fakeID.TMDB, fakeID.TVDB = "7371062", "11826831"
	m, err := s.MatchVideo(ctx, fakeID)
	wantRefused(t, m, err)
	byTitle := fakeID
	byTitle.Title = "Fake ID"
	m, err = s.MatchVideo(ctx, byTitle)
	wantEpisode(t, m, err, "7371063", 2, 2)
	byAirDate := fakeID
	byAirDate.AirDate = "2026-08-27"
	m, err = s.MatchVideo(ctx, byAirDate)
	wantEpisode(t, m, err, "7371063", 2, 2)
	// Bloem's "Talia" carries the TMDB id of "Fake ID".
	talia := episodeRef("260458", 2, 3)
	talia.TMDB = "7371063"
	m, err = s.MatchVideo(ctx, talia)
	wantRefused(t, m, err)
	talia.Title = "Talia"
	m, err = s.MatchVideo(ctx, talia)
	wantEpisode(t, m, err, "7371062", 2, 3)
	// An air date both episodes share settles nothing: Talia and Raccoon aired together.
	raccoon := episodeRef("260458", 2, 4)
	raccoon.TMDB, raccoon.AirDate = "7371062", "2026-09-03"
	m, err = s.MatchVideo(ctx, raccoon)
	wantRefused(t, m, err)
	// An id at the source's numbers needs no other lookup and no corroboration.
	finds := tv.findCalls.Load()
	scrub := episodeRef("260458", 2, 1)
	scrub.TMDB, scrub.TVDB = "7371061", "11429840"
	m, err = s.MatchVideo(ctx, scrub)
	wantEpisode(t, m, err, "7371061", 2, 1)
	if tv.findCalls.Load() != finds {
		t.Fatal("an episode TMDB id at the source's numbers needed no /find call")
	}
}

// A renumbered episode is followed to wherever TMDB places its id, across seasons,
// when its title agrees there and not at the source's numbers.
func TestEpisodeMatchFollowsARenumberedIDThatTheTitleConfirms(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("8592", 5, 22)
	tv.numbered("8592", 6, 20)
	tv.numbered("8592", 7, 13)
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("8592", 6, 21)
	ref.TMDB = "8592-7-1"
	m, err := s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	ref.Title = "Episode 8592-7-1"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "8592-7-1", 7, 1)
	// Stargate SG-1: a renumbered pilot, then a title naming the episode at the
	// source's numbers instead of the id's.
	tv.numbered("4629", 1, 21)
	ref = episodeRef("4629", 1, 2)
	ref.TMDB, ref.Title = "4629-1-1", "Episode 4629-1-1"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "4629-1-1", 1, 1)
	ref.Title = "Episode 4629-1-2"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "4629-1-2", 1, 2)
	// A TMDB id the show no longer lists gives way to the episode's TVDB id, as TMDB
	// places it in this show and settled the same way, but never to the source's
	// numbers alone.
	tv.finds["tvdb:72449-1-3"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "4629-1-3", ShowID: "4629", Season: 1, Episode: 3}}}
	ref = episodeRef("4629", 1, 4)
	ref.TMDB, ref.TVDB = "336099", "72449-1-3"
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	ref.Title = "Episode 4629-1-3"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "4629-1-3", 1, 3)
	ref.TVDB = "unmapped"
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
}

// One Piece specials: Bloem's season 0 TMDB ids were assigned by TVDB's order, so the
// id and the numbers agree and still name another special. Its title and air date
// both disagreeing refuse it. Either agreeing keeps it: titles are translated and
// reworded, and an air date moves a day across time zones.
func TestEpisodeMatchRefusesAnEpisodeWhoseTitleAndAirDateBothDisagree(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("37854", 1, 3)
	tv.season("37854", 0, "852733", "852736", "852742", "852743", "852747")
	tv.named("852743", "Protect! The Last Great Performance", "2003-12-14")
	tv.named("852747", "The Detective Memoirs of Chief Straw Hat Luffy", "2005-12-18")
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("37854", 0, 5)
	ref.TMDB, ref.Title, ref.AirDate = "852747", "Jango's Dance Carnival", "2001-03-03"
	m, err := s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	for _, agreeing := range [][2]string{
		{"The Detective Memoirs of Chief Straw Hat Luffy", "2001-03-03"},
		{"Straw Hat Luffy's Detective Memoirs", "2005-12-19"},
		{"Jango's Dance Carnival", ""},
	} {
		ref.Title, ref.AirDate = agreeing[0], agreeing[1]
		m, err = s.MatchVideo(ctx, ref)
		wantEpisode(t, m, err, "852747", 0, 5)
	}
	// Numbers alone are checked the same way.
	byNumber := episodeRef("37854", 0, 4)
	byNumber.Title, byNumber.AirDate = "Clockwork Island Adventure", "2001-03-03"
	m, err = s.MatchVideo(ctx, byNumber)
	wantRefused(t, m, err)
}

// One Piece: without an episode TMDB id, TMDB's position for the episode's TVDB id
// replaces the source's TVDB-ordered season and episode numbers when the title or air
// date confirms it there.
func TestEpisodeMatchUsesTMDBPositionForAnEpisodeTVDBID(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("37854", 1, 61)
	tv.numbered("37854", 2, 16)
	tv.named("37854-1-45", "Episode 37854-1-45", "2000-11-08")
	tv.named("37854-2-14", "Episode 37854-2-14", "2001-08-29")
	tv.tvdb["37854"] = "81797"
	tv.finds["tvdb:81797"] = provider.Found{Shows: []string{"37854"}}
	tv.finds["tvdb:361901"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "37854-1-45", ShowID: "37854", Season: 1, Episode: 45}}}
	tv.finds["tvdb:361902"] = provider.Found{Episodes: []provider.FoundEpisode{{ShowID: "37854", Season: 1, Episode: 46}}}
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("37854", 2, 14)
	ref.ShowTVDB, ref.TVDB = "81797", "361901"
	m, err := s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	ref.AirDate = "2000-11-09"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "37854-1-45", 1, 45)
	// A /find answer without the episode's id still places it by TMDB's numbers.
	ref.TVDB, ref.Episode, ref.AirDate, ref.Title = "361902", n(15), "", "Episode 37854-1-46"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "37854-1-46", 1, 46)
	// Plex and Jellyfin send no series ids: the episode id names the show and its place,
	// and they send the episode's title.
	tv.finds["tvdb:361903"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "37854-2-3", ShowID: "37854", Season: 2, Episode: 3}}}
	m, err = s.MatchVideo(ctx, catalog.Reference{Kind: "episode", TVDB: "361903", Title: "Episode 37854-2-3", ShowTitle: "One Piece", Season: n(3), Episode: n(1)})
	wantEpisode(t, m, err, "37854-2-3", 2, 3)
}

// Season 0 specials match through their ids, confirmed by title, even where TVDB numbers
// them differently.
func TestEpisodeMatchSpecialsByIdentifier(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("500", 0, 3)
	tv.numbered("500", 1, 5)
	tv.finds["tvdb:sp3"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "500-0-3", ShowID: "500", Season: 0, Episode: 3}}}
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("500", 0, 5)
	ref.TMDB, ref.Title = "500-0-2", "Episode 500-0-2"
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "500-0-2", 0, 2)
	ref = episodeRef("500", 0, 7)
	ref.TVDB, ref.Title = "sp3", "Episode 500-0-3"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "500-0-3", 0, 3)
	// A special the provider files as a regular episode is still found by its id.
	ref = episodeRef("500", 0, 9)
	ref.TMDB, ref.Title = "500-1-5", "Episode 500-1-5"
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "500-1-5", 1, 5)
}

// Silo: a show cached before its newest season was listed is refreshed once its daily
// refresh is due, never on every miss.
func TestEpisodeMatchRefreshesAStaleShowForANewerSeason(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("125988", 1, 10)
	tv.numbered("125988", 2, 10)
	f, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("125988", 2, 1)
	ref.TMDB = "125988-2-1"
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "125988-2-1", 2, 1)
	tv.numbered("125988", 3, 10)
	// The next season of a running show may simply not be listed yet: retry, but do
	// not refetch a show fetched within the hour.
	newer := episodeRef("125988", 3, 4)
	newer.TMDB = "125988-3-4"
	m, err = s.MatchVideo(ctx, newer)
	wantPending(t, m, err)
	if tv.fetches.Load() != 1 {
		t.Fatal("a freshly fetched show was refetched on a miss", tv.fetches.Load())
	}
	byNumber := episodeRef("125988", 3, 5)
	m, err = s.MatchVideo(ctx, byNumber)
	wantPending(t, m, err)
	age(t, f, "UPDATE catalog_items SET refreshed_at=clock_timestamp()-interval '2 days' WHERE provider='tmdb' AND kind='series' AND provider_id='125988'")
	m, err = s.MatchVideo(ctx, newer)
	wantEpisode(t, m, err, "125988-3-4", 3, 4)
	if tv.fetches.Load() != 2 {
		t.Fatal("stale show was not refreshed exactly once", tv.fetches.Load())
	}
	m, err = s.MatchVideo(ctx, byNumber)
	wantEpisode(t, m, err, "125988-3-5", 3, 5)
}

// D: a season cached while it was airing is reloaded for a missing episode when its
// episodes are a day old, or when a later show refresh counts more episodes.
func TestEpisodeMatchReloadsAStaleSeasonForANewEpisode(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("777", 1, 3)
	f, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("777", 1, 1)
	ref.TMDB = "777-1-1"
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "777-1-1", 1, 1)
	loads := tv.seasonLoads.Load()
	tv.appendEpisode("777", 1, "777-1-4")
	byID := episodeRef("777", 1, 4)
	byID.TMDB = "777-1-4"
	m, err = s.MatchVideo(ctx, byID)
	wantPending(t, m, err)
	if tv.seasonLoads.Load() != loads {
		t.Fatal("a freshly loaded season was refetched on a miss")
	}
	agedEpisodes := "UPDATE catalog_items SET refreshed_at=clock_timestamp()-interval '2 days' WHERE kind='episode' AND provider_id LIKE '777-1-%'"
	age(t, f, agedEpisodes)
	m, err = s.MatchVideo(ctx, byID)
	wantEpisode(t, m, err, "777-1-4", 1, 4)
	// The number-only path reloads a stale season the same way.
	tv.appendEpisode("777", 1, "777-1-5")
	age(t, f, agedEpisodes)
	m, err = s.MatchVideo(ctx, episodeRef("777", 1, 5))
	wantEpisode(t, m, err, "777-1-5", 1, 5)
	// A show refresh that counts more episodes than are cached makes the season stale
	// even within the day.
	tv.appendEpisode("777", 1, "777-1-6")
	if err = s.Refresh(ctx, m.Title.ID); err != nil {
		t.Fatal(err)
	}
	byID = episodeRef("777", 1, 6)
	byID.TMDB = "777-1-6"
	m, err = s.MatchVideo(ctx, byID)
	wantEpisode(t, m, err, "777-1-6", 1, 6)
}

// Numbers are used only when no stable episode identifier is available or resolves.
func TestEpisodeMatchFallsBackToNumbersWithoutAStableEpisodeID(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("900", 1, 3)
	tv.tvdb["900"] = "9900"
	tv.finds["tvdb:9900"] = provider.Found{Shows: []string{"900"}}
	_, s := matchService(t, tv)
	ctx := context.Background()
	m, err := s.MatchVideo(ctx, episodeRef("900", 1, 2))
	wantEpisode(t, m, err, "900-1-2", 1, 2)
	ref := catalog.Reference{Kind: "episode", ShowTVDB: "9900", TVDB: "unmapped", Season: n(1), Episode: n(3)}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "900-1-3", 1, 3)
	// Jeopardy!: a year-numbered TVDB season that TMDB does not have stays unmatched.
	m, err = s.MatchVideo(ctx, episodeRef("900", 2025, 104))
	wantRefused(t, m, err)
}

// Regression: number-only matching never overrides an identifier that names a different
// episode, show, or more than one episode.
func TestEpisodeMatchNumbersNeverOverrideAConflictingIdentifier(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("100", 1, 3)
	tv.numbered("200", 1, 3)
	tv.finds["tvdb:other-show"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "200-1-2", ShowID: "200", Season: 1, Episode: 2}}}
	tv.finds["tvdb:first"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "100-1-2", ShowID: "100", Season: 1, Episode: 2}}}
	tv.finds["imdb:tt0000003"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "100-1-3", ShowID: "100", Season: 1, Episode: 3}}}
	tv.finds["tvdb:show-200"] = provider.Found{Shows: []string{"200"}}
	tv.finds["tvdb:two-shows"] = provider.Found{Shows: []string{"100", "200"}}
	_, s := matchService(t, tv)
	ctx := context.Background()
	other := episodeRef("200", 1, 1)
	other.TMDB = "200-1-1"
	m, err := s.MatchVideo(ctx, other)
	wantEpisode(t, m, err, "200-1-1", 1, 1)
	cases := map[string]catalog.Reference{
		"episode TMDB id of another show":      {Kind: "episode", ShowTMDB: "100", TMDB: "200-1-1", Season: n(1), Episode: n(1)},
		"episode TMDB id absent from the show": {Kind: "episode", ShowTMDB: "100", TMDB: "404404", Season: n(1), Episode: n(2)},
		"episode TVDB id of another show":      {Kind: "episode", ShowTMDB: "100", TVDB: "other-show", Season: n(1), Episode: n(2)},
		"episode ids naming two episodes":      {Kind: "episode", ShowTMDB: "100", TVDB: "first", IMDB: "tt0000003", Season: n(1), Episode: n(2)},
		"series TVDB id of another show":       {Kind: "episode", ShowTMDB: "100", ShowTVDB: "show-200", Season: n(1), Episode: n(1)},
		"series TVDB id naming two shows":      {Kind: "episode", ShowTVDB: "two-shows", Season: n(1), Episode: n(1)},
	}
	for name, ref := range cases {
		t.Run(name, func(t *testing.T) {
			m, err := s.MatchVideo(ctx, ref)
			wantRefused(t, m, err)
		})
	}
}

// Looking for an episode id across seasons is bounded per match. A match that runs out
// asks to be retried, and the seasons it loaded stay cached so the retry continues.
func TestEpisodeMatchBoundsTheSeasonWalk(t *testing.T) {
	tv := newMatchTMDB()
	for season := 1; season <= 10; season++ {
		tv.numbered("300", season, 2)
	}
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := episodeRef("300", 1, 1)
	ref.TMDB, ref.Title = "300-10-1", "Episode 300-10-1"
	m, err := s.MatchVideo(ctx, ref)
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != 503 || m.Episode != nil {
		t.Fatalf("an unfinished walk must ask for a retry, got %+v %v", m.Episode, err)
	}
	first := tv.seasonLoads.Load()
	if first < 2 || first > 8 {
		t.Fatal("season walk was not bounded", first)
	}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "300-10-1", 10, 1)
	if tv.seasonLoads.Load() != 10 {
		t.Fatal("seasons were reloaded instead of reused", tv.seasonLoads.Load())
	}
}

// An episode just past what a running show lists may not be listed yet: the match asks
// for a retry, and a retry an hour later refetches the show and season once. Gaps inside
// the listed range, ended shows and far-off numbers stay definite refusals.
func TestEpisodeMatchAsksToRetryAnEpisodeNotListedYet(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("610", 1, 3)
	f, s := matchService(t, tv)
	ctx := context.Background()
	first := episodeRef("610", 1, 1)
	first.TMDB = "610-1-1"
	m, err := s.MatchVideo(ctx, first)
	wantEpisode(t, m, err, "610-1-1", 1, 1)
	fetches, loads := tv.fetches.Load(), tv.seasonLoads.Load()

	next := episodeRef("610", 1, 4)
	next.TMDB = "610-1-4"
	m, err = s.MatchVideo(ctx, next)
	wantPending(t, m, err)
	tv.appendEpisode("610", 1, "610-1-4")
	m, err = s.MatchVideo(ctx, next)
	wantPending(t, m, err)
	if tv.fetches.Load() != fetches || tv.seasonLoads.Load() != loads {
		t.Fatal("a pending episode refetched data younger than an hour", tv.fetches.Load(), tv.seasonLoads.Load())
	}
	age(t, f, "UPDATE catalog_items SET refreshed_at=clock_timestamp()-interval '2 hours' WHERE provider='tmdb' AND (provider_id='610' OR provider_id LIKE '610-1-%')")
	m, err = s.MatchVideo(ctx, next)
	wantEpisode(t, m, err, "610-1-4", 1, 4)
	if tv.fetches.Load() != fetches+1 || tv.seasonLoads.Load() != loads+1 {
		t.Fatal("a pending episode must refetch the show and season exactly once", tv.fetches.Load()-fetches, tv.seasonLoads.Load()-loads)
	}

	// By numbers: the season after the latest is pending until the show lists it.
	m, err = s.MatchVideo(ctx, episodeRef("610", 2, 1))
	wantPending(t, m, err)
	tv.numbered("610", 2, 2)
	age(t, f, "UPDATE catalog_items SET refreshed_at=clock_timestamp()-interval '2 hours' WHERE provider='tmdb' AND kind='series' AND provider_id='610'")
	m, err = s.MatchVideo(ctx, episodeRef("610", 2, 1))
	wantEpisode(t, m, err, "610-2-1", 2, 1)

	refused := map[string]catalog.Reference{
		"past an earlier season":           episodeRef("610", 1, 9),
		"far past the last episode":        episodeRef("610", 2, 2+26),
		"two seasons past the latest":      episodeRef("610", 4, 1),
		"unknown id inside the listed run": {Kind: "episode", ShowTMDB: "610", TMDB: "404404", Season: n(2), Episode: n(1)},
	}
	for name, ref := range refused {
		t.Run(name, func(t *testing.T) {
			m, err := s.MatchVideo(ctx, ref)
			wantRefused(t, m, err)
		})
	}
	age(t, f, "UPDATE catalog_items SET release_status='Ended' WHERE provider='tmdb' AND kind='series' AND provider_id='610'")
	m, err = s.MatchVideo(ctx, episodeRef("610", 2, 3))
	wantRefused(t, m, err)
	ended := episodeRef("610", 3, 1)
	ended.TMDB = "610-3-1"
	m, err = s.MatchVideo(ctx, ended)
	wantRefused(t, m, err)
}
