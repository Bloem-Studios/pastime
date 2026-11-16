package catalog_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

// factsTVDB is a synthetic TheTVDB that only describes episodes.
type factsTVDB struct {
	facts map[string]provider.EpisodeFacts
	calls atomic.Int32
	down  atomic.Bool
}

func (*factsTVDB) Name() string    { return "tvdb" }
func (*factsTVDB) Kinds() []string { return []string{"series", "anime"} }
func (*factsTVDB) Search(context.Context, string, string) ([]provider.Record, error) {
	return nil, provider.ErrUnavailable
}
func (*factsTVDB) Fetch(context.Context, string, string) (provider.Record, error) {
	return provider.Record{}, provider.ErrNotFound
}
func (f *factsTVDB) Episode(_ context.Context, id string) (provider.EpisodeFacts, error) {
	f.calls.Add(1)
	if f.down.Load() {
		return provider.EpisodeFacts{}, provider.ErrRateLimited
	}
	if x, ok := f.facts[id]; ok {
		return x, nil
	}
	return provider.EpisodeFacts{}, provider.ErrNotFound
}

type memoryCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemoryCache() *memoryCache { return &memoryCache{m: map[string][]byte{}} }
func (c *memoryCache) Get(_ context.Context, key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[key]
	return v, ok
}
func (c *memoryCache) Set(_ context.Context, key string, value []byte, _ time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = value
}

// animeShow is a TMDB show numbered in two seasons of three and two episodes, one a week
// from 5 January 2000, with titled episodes, and two episodes on 1 March 2000.
func animeShow() *matchTMDB {
	tv := newMatchTMDB()
	tv.numbered("800", 1, 3)
	tv.numbered("800", 2, 2)
	tv.season("800", 3, "800-3-1", "800-3-2")
	for id, facts := range map[string][2]string{
		"800-1-1": {"Romance Dawn", "2000-01-05"}, "800-1-2": {"The Great Swordsman", "2000-01-12"}, "800-1-3": {"Captain Usopp", "2000-01-19"},
		"800-2-1": {"The Black Cat", "2000-01-26"}, "800-2-2": {"The Crescent Moon", "2000-02-02"},
		"800-3-1": {"First Of Two", "2000-03-01"}, "800-3-2": {"Second Of Two", "2000-03-01"},
	} {
		tv.named(id, facts[0], facts[1])
	}
	tv.tvdb["800"] = "81797"
	tv.finds["tvdb:81797"] = provider.Found{Shows: []string{"800"}}
	return tv
}

func TestEpisodeMatchFindsAnEpisodeNumberedDifferentlyByItsAirDate(t *testing.T) {
	_, s := matchService(t, animeShow())
	ctx := context.Background()
	// TheTVDB's S2E1 is TMDB's S1E3: the numbers name another episode, whose title and
	// air date both disagree, so the episode that aired that day is the match.
	ref := catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "1001", Season: n(2), Episode: n(1), Title: "Captain Usopp!", AirDate: "2000-01-19"}
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-1-3", 1, 3)
	// A TheTVDB season TMDB does not have, a day off the TMDB date.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "1002", Season: n(12), Episode: n(5), AirDate: "2000-02-03"}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-2-2", 2, 2)
	// Two episodes aired that day: the title chooses, and without one there is no match.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "1003", Season: n(9), Episode: n(2), Title: "Second of Two", AirDate: "2000-03-01"}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-3-2", 3, 2)
	ref.Title = ""
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	// Nothing aired near the date.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "1004", Season: n(9), Episode: n(9), AirDate: "2010-06-01"}
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
}

func TestEpisodeMatchAsksTheTVDBForTheAirDateOfAnEpisodeItNames(t *testing.T) {
	_, s := matchService(t, animeShow())
	facts := &factsTVDB{facts: map[string]provider.EpisodeFacts{
		"2001": {SeriesID: "81797", Season: 2, Number: 2, Aired: "2000-01-12", Name: "The Greatest Swordsman"},
		"2002": {SeriesID: "99999", Season: 2, Number: 2, Aired: "2000-01-12"},
	}}
	s.Providers["tvdb"] = facts
	s.Cache = newMemoryCache()
	ctx := context.Background()
	ref := catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "2001", Season: n(7), Episode: n(2)}
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-1-2", 1, 2)
	// The answer is kept: a second match asks TheTVDB nothing.
	calls := facts.calls.Load()
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-1-2", 1, 2)
	if facts.calls.Load() != calls {
		t.Fatalf("TheTVDB was asked again: %d calls", facts.calls.Load())
	}
	// Numbers that exist in TMDB but name another episode: TheTVDB's air date refuses
	// them and finds the right one.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "2001", Season: n(2), Episode: n(2)}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-1-2", 1, 2)
	// TheTVDB places the id under another show: its facts are ignored.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "2002", Season: n(7), Episode: n(3)}
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
	// Shows numbered alike keep matching by their numbers.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "2003", Season: n(1), Episode: n(1)}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-1-1", 1, 1)
}

// A long show whose seasons carry no dates (One Piece in TMDB): the season that holds
// the date is found by halving the seasons by number, loading a few of them.
func TestEpisodeMatchByAirDateHalvesALongShowsSeasons(t *testing.T) {
	tv := newMatchTMDB()
	start := time.Date(2005, 1, 3, 0, 0, 0, 0, time.UTC)
	for season := 1; season <= 20; season++ {
		tv.numbered("700", season, 4)
		for e := 1; e <= 4; e++ {
			id := "700-" + strconv.Itoa(season) + "-" + strconv.Itoa(e)
			tv.named(id, "Episode "+id, start.AddDate(0, 0, 7*((season-1)*4+e-1)).Format(time.DateOnly))
		}
	}
	_, s := matchService(t, tv)
	ctx := context.Background()
	aired := start.AddDate(0, 0, 7*(16*4+2)).Format(time.DateOnly) // season 17, episode 3
	before := tv.seasonLoads.Load()
	m, err := s.MatchVideo(ctx, catalog.Reference{Kind: "episode", ShowTMDB: "700", Season: n(99), Episode: n(1), AirDate: aired})
	wantEpisode(t, m, err, "700-17-3", 17, 3)
	if loads := tv.seasonLoads.Load() - before; loads > 6 {
		t.Fatalf("loaded %d seasons to find one of twenty", loads)
	}
	// Now stored, the next episode of that season is found without loading anything.
	before = tv.seasonLoads.Load()
	aired = start.AddDate(0, 0, 7*(16*4+3)).Format(time.DateOnly)
	m, err = s.MatchVideo(ctx, catalog.Reference{Kind: "episode", ShowTMDB: "700", Season: n(98), Episode: n(4), AirDate: aired})
	wantEpisode(t, m, err, "700-17-4", 17, 4)
	if loads := tv.seasonLoads.Load() - before; loads != 0 {
		t.Fatalf("loaded %d seasons for a stored episode", loads)
	}
}

// TheTVDB rate limited or down: a reference only its air date could match is asked
// to retry, never refused, and matches once TheTVDB answers.
func TestEpisodeMatchRetriesWhileTheTVDBIsUnavailable(t *testing.T) {
	_, s := matchService(t, animeShow())
	facts := &factsTVDB{facts: map[string]provider.EpisodeFacts{"3001": {SeriesID: "81797", Aired: "2000-02-02", Name: "The Crescent Moon"}}}
	facts.down.Store(true)
	s.Providers["tvdb"] = facts
	s.Cache = newMemoryCache()
	ctx := context.Background()
	ref := catalog.Reference{Kind: "episode", ShowTVDB: "81797", TVDB: "3001", Season: n(30), Episode: n(4)}
	m, err := s.MatchVideo(ctx, ref)
	wantPending(t, m, err)
	if !catalog.NotListedYet(err) {
		t.Fatal("a retry for older plugins must look like a not-yet-listed episode")
	}
	facts.down.Store(false)
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "800-2-2", 2, 2)
}

// TheTVDB keeps The Great British Bake Off as one series; TMDB has the BBC years and
// the Channel 4 years as two shows. The series identifiers name the later show, the
// episode's own TheTVDB id places it in the earlier one, and the play goes there.
func TestEpisodeMatchFollowsTheShowTheEpisodesOwnIDsName(t *testing.T) {
	tv := newMatchTMDB()
	tv.numbered("401", 1, 6) // BBC
	tv.numbered("402", 1, 10)
	tv.tvdb["402"] = "184871"
	tv.finds["tvdb:184871"] = provider.Found{Shows: []string{"402"}}
	tv.finds["tvdb:5550001"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "401-1-2", ShowID: "401", Season: 1, Episode: 2}}}
	tv.named("401-1-2", "Biscuits", "2010-08-24")
	tv.named("402-1-2", "Cake Week", "2017-09-05")
	_, s := matchService(t, tv)
	ctx := context.Background()
	ref := catalog.Reference{Kind: "episode", ShowTVDB: "184871", TVDB: "5550001", Season: n(1), Episode: n(2), Title: "Biscuits", AirDate: "2010-08-24"}
	m, err := s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "401-1-2", 1, 2)
	if m.Title.ProviderID != "401" {
		t.Fatalf("matched under show %s", m.Title.ProviderID)
	}
	// An episode the series show itself lists stays there.
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "184871", Season: n(1), Episode: n(2), Title: "Cake Week", AirDate: "2017-09-05"}
	m, err = s.MatchVideo(ctx, ref)
	wantEpisode(t, m, err, "402-1-2", 1, 2)
	// Ids placed in two other shows say nothing reliable: no match.
	tv.finds["tvdb:5550002"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "401-1-3", ShowID: "401", Season: 1, Episode: 3}}}
	tv.finds["imdb:tt9990002"] = provider.Found{Episodes: []provider.FoundEpisode{{ID: "403-1-3", ShowID: "403", Season: 1, Episode: 3}}}
	ref = catalog.Reference{Kind: "episode", ShowTVDB: "184871", TVDB: "5550002", IMDB: "tt9990002", Season: n(1), Episode: n(3)}
	m, err = s.MatchVideo(ctx, ref)
	wantRefused(t, m, err)
}
