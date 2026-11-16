package catalog

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// seriesRefreshInterval is how often tracked shows are refreshed (EnqueueStale). An
// episode match that misses refetches a cached show or season at most this often, so
// a reference to an episode nobody lists costs no repeated provider calls.
const seriesRefreshInterval = 24 * time.Hour

// seasonWalkBudget bounds how many unloaded seasons one match fetches while looking
// for an episode id outside the season its reference names. Interactive callers must
// answer within the HTTP deadline.
const seasonWalkBudget = 6

// errSeasonsLoading asks the caller to retry a match that used its season budget. The
// seasons it loaded stay cached, so a retry continues where it stopped.
var errSeasonsLoading = &fault.Error{Status: 503, Code: "catalog_loading", Message: "This show's episodes are still loading from the metadata provider. Try again shortly."}

// errNotListed means a show does not list an episode id, as distinct from an id that
// belongs to another show. It never leaves matchEpisode.
var errNotListed = errors.New("episode id not listed for this show")

// errEpisodePending asks the caller to retry a reference to an episode the provider may
// not list yet; see showSeasons.unlisted. A definite no-match stays a 404.
var errEpisodePending = &fault.Error{Status: 503, Code: "catalog_pending", Message: "This episode is not listed by the metadata provider yet. Try again later."}

// NotListedYet reports the retryable answer for an episode the provider may not list
// yet. A caller that cannot retry hours later treats it as a miss.
func NotListedYet(err error) bool {
	return errors.Is(err, errEpisodePending) || errors.Is(err, errDetailsPending)
}

// pendingRefreshInterval is how often a reference to an episode a show does not list yet
// may refetch the show and that season, so each retry can find a newly listed episode
// without refetching on every request.
const pendingRefreshInterval = time.Hour

// pendingEpisodeWindow bounds how far past the last listed episode a reference still
// counts as not listed yet. Numbers further out are another numbering, not a new episode.
const pendingEpisodeWindow = 25

// episodeLookup asks TMDB, at most once per match, where the episode's IMDb and TVDB
// ids place it. Only episode results count.
type episodeLookup struct {
	tmdb  finder
	ref   Reference
	done  bool
	found []provider.FoundEpisode
}

func (l *episodeLookup) episodes(ctx context.Context) ([]provider.FoundEpisode, error) {
	if l.done {
		return l.found, nil
	}
	for _, external := range [][2]string{{"imdb", l.ref.IMDB}, {"tvdb", l.ref.TVDB}} {
		if external[1] == "" {
			continue
		}
		found, err := l.tmdb.Find(ctx, external[0], external[1])
		if err != nil && !errors.Is(err, provider.ErrNotFound) {
			return nil, providerFault(err)
		}
		l.found = append(l.found, found.Episodes...)
	}
	l.done = true
	return l.found, nil
}

// matchEpisode finds the show's episode from the reference's identifiers and numbers,
// checked against its episode title and air date. Other services number episodes in
// other orders (TVDB, DVD, absolute), and an episode TMDB id another service stored
// may have been assigned by such numbers, so no single piece of evidence wins on its
// own. The candidate is:
//  1. the episode with the reference's TMDB id, wherever TMDB places it;
//  2. otherwise, or when the show no longer lists that id, TMDB's placement of the
//     episode's IMDb or TVDB id within this show;
//  3. only when the reference sent no episode identifier that could contradict them,
//     the episode at its own season and episode numbers.
//
// settle then accepts or replaces the candidate. Identifiers naming another show or
// more than one episode refuse the match.
//
// A candidate found by the reference's numbers alone is first checked against the
// episode's air date and title, from TheTVDB when the reference names its TheTVDB
// episode and lacks them: another catalog may number the show differently (TheTVDB's
// anime and long-running reality seasons), and the same numbers then name another
// episode. When nothing settles, the episode that aired on the reference's air date
// is the match (byAirDate).
func (s *Service) matchEpisode(ctx context.Context, show Item, ref Reference, lookup *episodeLookup) (Item, error) {
	h := &showSeasons{s: s, show: show, budget: seasonWalkBudget, loaded: map[int64]bool{}, interval: seriesRefreshInterval}
	ep, byNumbers, err := h.candidate(ctx, ref, lookup)
	if err == nil && byNumbers {
		ref, _ = s.describe(ctx, ref)
	}
	if err == nil {
		ep, err = h.settle(ctx, ep, ref)
	}
	if !isNoMatch(err) {
		return ep, err
	}
	ref, failed := s.describe(ctx, ref)
	dated, ok, dateErr := h.byAirDate(ctx, ref)
	switch {
	case dateErr != nil:
		return Item{}, dateErr
	case ok:
		return dated, nil
	case failed:
		// TheTVDB could not say when the episode aired just now; a refusal would be
		// final, so the sender is asked to try again.
		return Item{}, errDetailsPending
	}
	return Item{}, err
}

var errDetailsPending = &fault.Error{Status: 503, Code: "catalog_pending", Message: "This episode's details could not be fetched just now. Try again later."}

func isNoMatch(err error) bool {
	var fe *fault.Error
	return errors.As(err, &fe) && fe.Code == ErrNoMatch.Code
}

// candidate is the episode the reference's identifiers, or else its numbers, name; see
// matchEpisode. byNumbers reports that the numbers alone chose it.
func (h *showSeasons) candidate(ctx context.Context, ref Reference, lookup *episodeLookup) (Item, bool, error) {
	if ref.TMDB != "" {
		if ep, err := h.byID(ctx, ref.TMDB, ref.Season); !errors.Is(err, errNotListed) {
			return ep, false, err
		}
	}
	found, err := lookup.episodes(ctx)
	if err != nil {
		return Item{}, false, err
	}
	var placed *provider.FoundEpisode
	for i := range found {
		if found[i].ShowID != h.show.ProviderID || placed != nil && *placed != found[i] {
			return Item{}, false, ErrNoMatch
		}
		placed = &found[i]
	}
	switch {
	case placed != nil && placed.ID != "":
		ep, err := h.byID(ctx, placed.ID, &placed.Season)
		if errors.Is(err, errNotListed) {
			ep, err = h.unlisted(ctx, placed.Season, placed.Episode, func(ctx context.Context) (Item, bool, error) { return h.cached(ctx, placed.ID) }, ErrNoMatch)
		}
		return ep, false, err
	case placed != nil:
		ep, err := h.at(ctx, placed.Season, placed.Episode)
		return ep, false, err
	case ref.TMDB == "" && ref.Season != nil && ref.Episode != nil:
		ep, err := h.at(ctx, *ref.Season, *ref.Episode)
		return ep, true, err
	case ref.Season != nil && ref.Episode != nil:
		// The show does not list the episode's TMDB id and nothing places it. The source's
		// numbers never match it, but they tell a new episode from a wrong identifier.
		ep, err := h.unlisted(ctx, *ref.Season, *ref.Episode, func(ctx context.Context) (Item, bool, error) { return h.cached(ctx, ref.TMDB) }, ErrNoMatch)
		return ep, false, err
	}
	return Item{}, false, ErrNoMatch
}

// settle decides on a candidate episode. A candidate at the reference's numbers, or
// any candidate when the reference has none, is accepted unless its title and air
// date both disagree with the reference. A candidate an identifier placed at other
// numbers competes with the episode at the reference's numbers: the reference's title
// or air date must agree with exactly one of the two, which is accepted. Without them
// the identifier and the numbers contradict each other and the match is refused.
func (h *showSeasons) settle(ctx context.Context, ep Item, ref Reference) (Item, error) {
	if ref.Season == nil || ref.Episode == nil || numberedAt(ep, *ref.Season, *ref.Episode) {
		if disagrees(ref, ep) {
			return Item{}, ErrNoMatch
		}
		return ep, nil
	}
	if normalizeTitle(ref.Title) == "" && airDate(ref.AirDate) == nil {
		return Item{}, ErrNoMatch
	}
	numbered, err := h.at(ctx, *ref.Season, *ref.Episode)
	if NotListedYet(err) && agrees(ref, ep) {
		return ep, nil
	}
	var fe *fault.Error
	if err != nil && !(errors.As(err, &fe) && fe.Code == ErrNoMatch.Code) {
		return Item{}, err
	}
	byID, byNumbers := agrees(ref, ep), err == nil && agrees(ref, numbered)
	switch {
	case byID && !byNumbers:
		return ep, nil
	case byNumbers && !byID:
		return numbered, nil
	}
	return Item{}, ErrNoMatch
}

// airDateSeasonBudget bounds how many seasons byAirDate inspects to find the one that
// holds a date, so a reference whose date the show never aired costs a few season
// loads at most: halving finds a season among 2^9 in nine.
const airDateSeasonBudget = 9

// byAirDate finds the show's episode that aired on the reference's air date: the one
// that aired that day, or, when none did, the one a day either side (broadcast dates
// move across time zones). The title chooses between several; without a title that
// tells them apart there is no match. Stored episodes answer first; otherwise the
// season that must hold the date is loaded (seasonFor) and they are asked again.
func (h *showSeasons) byAirDate(ctx context.Context, ref Reference) (Item, bool, error) {
	aired := airDate(ref.AirDate)
	if aired == nil {
		return Item{}, false, nil
	}
	exact, near, err := h.airedAround(ctx, *aired)
	if err != nil {
		return Item{}, false, err
	}
	if len(exact)+len(near) == 0 {
		if err = h.seasonFor(ctx, *aired, ref.Season); err != nil {
			return Item{}, false, err
		}
		if exact, near, err = h.airedAround(ctx, *aired); err != nil {
			return Item{}, false, err
		}
	}
	if len(exact) > 0 {
		ep, ok := oneByTitle(exact, ref.Title)
		return ep, ok, nil
	}
	ep, ok := oneByTitle(near, ref.Title)
	return ep, ok, nil
}

// airedAround lists the show's stored episodes that aired on the day (exact) and a
// day either side (near).
func (h *showSeasons) airedAround(ctx context.Context, aired time.Time) ([]Item, []Item, error) {
	rows, err := h.s.Pool.Query(ctx, `SELECT id FROM catalog_items WHERE root_id=$1 AND kind='episode'
		AND release_date BETWEEN $2::date-1 AND $2::date+1 ORDER BY release_date, season_number, episode_number, id LIMIT 50`, h.show.ID, aired)
	if err != nil {
		return nil, nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, err
	}
	var exact, near []Item
	for _, id := range ids {
		ep, err := h.s.Get(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if listed := episodeAirDate(ep); listed != nil && listed.Equal(aired) {
			exact = append(exact, ep)
		} else {
			near = append(near, ep)
		}
	}
	return exact, near, nil
}

// seasonFor loads the season that must hold the date: the specials for a special;
// otherwise the regular seasons are halved by number, as they air in order, each step
// loading at most the one season it looks at. A season without dated episodes ends
// the search.
func (h *showSeasons) seasonFor(ctx context.Context, aired time.Time, hint *int) error {
	seasons, err := h.seasons(ctx)
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		if refreshed, err := h.refresh(ctx); err != nil || !refreshed {
			return err
		}
		if seasons, err = h.seasons(ctx); err != nil {
			return err
		}
	}
	var regular []Item
	for _, season := range seasons {
		if *season.SeasonNumber == 0 {
			if hint != nil && *hint == 0 {
				_, _, err := h.span(ctx, season)
				return err
			}
			continue
		}
		regular = append(regular, season)
	}
	sort.Slice(regular, func(i, j int) bool { return *regular[i].SeasonNumber < *regular[j].SeasonNumber })
	day := 24 * time.Hour
	for lo, hi, steps := 0, len(regular)-1, 0; lo <= hi && steps < airDateSeasonBudget; steps++ {
		mid := (lo + hi) / 2
		first, last, err := h.span(ctx, regular[mid])
		switch {
		case err != nil:
			return err
		case first == nil:
			return nil
		case aired.Before(first.Add(-day)):
			hi = mid - 1
		case aired.After(last.Add(day)):
			lo = mid + 1
		default:
			return nil
		}
	}
	return nil
}

// span loads a season's episodes when needed and reports its first and last air dates.
func (h *showSeasons) span(ctx context.Context, season Item) (*time.Time, *time.Time, error) {
	if !season.ChildrenLoaded {
		if _, err := h.fill(ctx, season); err != nil {
			return nil, nil, err
		}
	}
	var first, last *time.Time
	err := h.s.Pool.QueryRow(ctx, `SELECT min(release_date)::timestamptz, max(release_date)::timestamptz FROM catalog_items WHERE parent_id=$1 AND kind='episode'`, season.ID).Scan(&first, &last)
	return first, last, err
}

// oneByTitle picks the only episode, or the only one with the reference's title.
func oneByTitle(episodes []Item, title string) (Item, bool) {
	if len(episodes) == 1 {
		return episodes[0], true
	}
	var titled []Item
	if t := normalizeTitle(title); t != "" {
		for _, e := range episodes {
			if normalizeTitle(e.Title) == t {
				titled = append(titled, e)
			}
		}
	}
	if len(titled) == 1 {
		return titled[0], true
	}
	return Item{}, false
}

func numberedAt(ep Item, season, episode int) bool {
	return ep.SeasonNumber != nil && *ep.SeasonNumber == season && ep.EpisodeNumber != nil && *ep.EpisodeNumber == episode
}

// agrees reports whether the reference's title or air date matches the episode. Air
// dates a day apart agree: a broadcast date moves across time zones.
func agrees(ref Reference, ep Item) bool {
	if title := normalizeTitle(ref.Title); title != "" && title == normalizeTitle(ep.Title) {
		return true
	}
	return sameDay(airDate(ref.AirDate), episodeAirDate(ep))
}

// disagrees reports whether the reference's title and air date are both known and
// neither matches the episode. A title alone never disagrees: services translate and
// reword episode titles.
func disagrees(ref Reference, ep Item) bool {
	aired, listed := airDate(ref.AirDate), episodeAirDate(ep)
	return normalizeTitle(ref.Title) != "" && normalizeTitle(ep.Title) != "" && aired != nil && listed != nil && !agrees(ref, ep)
}

func sameDay(a, b *time.Time) bool {
	if a == nil || b == nil {
		return false
	}
	d := a.Sub(*b)
	return d >= -24*time.Hour && d <= 24*time.Hour
}

func airDate(s string) *time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil
	}
	return &t
}

func episodeAirDate(ep Item) *time.Time {
	if ep.ReleaseDate == nil {
		return nil
	}
	return airDate(*ep.ReleaseDate)
}

// showSeasons loads one show's seasons for a single match: each at most once, stale
// caches refetched no more often than seriesRefreshInterval, and unhinted seasons
// within the match's budget.
type showSeasons struct {
	s         *Service
	show      Item
	budget    int
	refreshed bool
	loaded    map[int64]bool
	// interval is the age at which cached show and season data is refetched on a miss:
	// seriesRefreshInterval, or pendingRefreshInterval while checking a pending episode.
	interval time.Duration
}

// byID finds the episode with a provider id under the show: in the cache, then in the
// hinted season, then in the nearest seasons not loaded yet, then after refreshing a
// stale show. An id stored under another show refuses the match; an id the show does
// not list returns errNotListed.
func (h *showSeasons) byID(ctx context.Context, id string, hint *int) (Item, error) {
	if ep, ok, err := h.cached(ctx, id); err != nil || ok {
		return ep, err
	}
	if hint != nil {
		season, ok, err := h.season(ctx, *hint)
		if err != nil {
			return Item{}, err
		}
		if ok {
			fetched, err := h.fill(ctx, season)
			if err != nil {
				return Item{}, err
			}
			if fetched {
				if ep, ok, err := h.cached(ctx, id); err != nil || ok {
					return ep, err
				}
			}
		}
	}
	for {
		seasons, err := h.seasons(ctx)
		if err != nil {
			return Item{}, err
		}
		for _, season := range nearest(seasons, hint) {
			if season.ChildrenLoaded || h.loaded[season.ID] {
				continue
			}
			if h.budget == 0 {
				return Item{}, errSeasonsLoading
			}
			h.budget--
			if _, err = h.fill(ctx, season); err != nil {
				return Item{}, err
			}
			if ep, ok, err := h.cached(ctx, id); err != nil || ok {
				return ep, err
			}
		}
		refreshed, err := h.refresh(ctx)
		if err != nil {
			return Item{}, err
		}
		if !refreshed {
			return Item{}, errNotListed
		}
	}
}

// at finds the episode with a number in a season, reloading a stale season once.
func (h *showSeasons) at(ctx context.Context, number, episode int) (Item, error) {
	season, ok, err := h.season(ctx, number)
	if err != nil {
		return Item{}, err
	}
	if ok && !season.ChildrenLoaded {
		if _, err = h.fill(ctx, season); err != nil {
			return Item{}, err
		}
	}
	for ok {
		ep, found, err := h.numbered(ctx, season.ID, episode)
		if err != nil || found {
			return ep, err
		}
		if ok, err = h.fill(ctx, season); err != nil {
			return Item{}, err
		}
	}
	missing := &fault.Error{Status: 404, Code: "no_match", Message: "Episode S" + strconv.Itoa(number) + "E" + strconv.Itoa(episode) + " was not found for this show."}
	return h.unlisted(ctx, number, episode, func(ctx context.Context) (Item, bool, error) {
		season, ok, err := h.season(ctx, number)
		if err != nil || !ok {
			return Item{}, false, err
		}
		return h.numbered(ctx, season.ID, episode)
	}, missing)
}

// numbered finds a season's episode by its number among the cached episodes.
func (h *showSeasons) numbered(ctx context.Context, seasonID int64, episode int) (Item, bool, error) {
	episodes, err := h.s.Children(ctx, seasonID)
	if err != nil {
		return Item{}, false, err
	}
	for _, e := range episodes {
		if e.EpisodeNumber != nil && *e.EpisodeNumber == episode {
			return e, true, nil
		}
	}
	return Item{}, false, nil
}

// unlisted answers a reference whose episode the show does not list. When the episode
// lies just past the show's latest listed episode, or in the season after its latest,
// and the show has not ended, the provider may simply not list it yet: the show and
// that season are refetched at most once per pendingRefreshInterval, find looks again,
// and a miss asks for a retry. Anything else (a gap inside the listed range, an ended
// show, numbers far outside it) is the definite refusal missing.
func (h *showSeasons) unlisted(ctx context.Context, number, episode int, find func(context.Context) (Item, bool, error), missing error) (Item, error) {
	if showEnded(h.show.ReleaseStatus) {
		return Item{}, missing
	}
	pending, err := h.beyondListed(ctx, number, episode)
	if err != nil || !pending {
		return Item{}, firstError(err, missing)
	}
	h.interval = pendingRefreshInterval
	defer func() { h.interval = seriesRefreshInterval }()
	refreshed, err := h.refresh(ctx)
	if err != nil {
		return Item{}, err
	}
	season, ok, err := h.season(ctx, number)
	if err != nil {
		return Item{}, err
	}
	fetched := false
	if ok {
		if fetched, err = h.fill(ctx, season); err != nil {
			return Item{}, err
		}
	}
	if refreshed || fetched {
		if ep, found, err := find(ctx); err != nil || found {
			return ep, err
		}
	}
	return Item{}, errEpisodePending
}

// beyondListed reports whether season and episode lie just past what the show lists: in
// its latest season after the last listed episode, or early in the season after it.
func (h *showSeasons) beyondListed(ctx context.Context, number, episode int) (bool, error) {
	seasons, err := h.seasons(ctx)
	if err != nil {
		return false, err
	}
	var latest *Item
	for i := range seasons {
		if latest == nil || *seasons[i].SeasonNumber > *latest.SeasonNumber {
			latest = &seasons[i]
		}
	}
	switch {
	case latest == nil:
		return number <= 1 && episode <= pendingEpisodeWindow, nil
	case number == *latest.SeasonNumber+1:
		return episode <= pendingEpisodeWindow, nil
	case number != *latest.SeasonNumber:
		return false, nil
	}
	if !latest.ChildrenLoaded {
		if _, err = h.fill(ctx, *latest); err != nil {
			return false, err
		}
	}
	episodes, err := h.s.Children(ctx, latest.ID)
	if err != nil {
		return false, err
	}
	last := 0
	for _, e := range episodes {
		if e.EpisodeNumber != nil && *e.EpisodeNumber > last {
			last = *e.EpisodeNumber
		}
	}
	return episode > last && episode <= last+pendingEpisodeWindow, nil
}

// showEnded reports a provider status that rules out new episodes: TMDB's Ended and
// Canceled, TVDB's Ended.
func showEnded(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ended", "canceled", "cancelled":
		return true
	}
	return false
}

func firstError(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

// cached finds a stored episode by provider id. One stored under another show means
// the reference's identifiers disagree, which refuses rather than guesses.
func (h *showSeasons) cached(ctx context.Context, id string) (Item, bool, error) {
	var episodeID, rootID int64
	err := h.s.Pool.QueryRow(ctx, "SELECT id, root_id FROM catalog_items WHERE provider=$1 AND kind='episode' AND provider_id=$2", h.show.Provider, id).Scan(&episodeID, &rootID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	if rootID != h.show.ID {
		return Item{}, false, ErrNoMatch
	}
	ep, err := h.s.Get(ctx, episodeID)
	return ep, err == nil, err
}

func (h *showSeasons) seasons(ctx context.Context) ([]Item, error) {
	children, err := h.s.Children(ctx, h.show.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(children))
	for _, c := range children {
		if c.Kind == "season" && c.SeasonNumber != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// season finds a season by number, refreshing a stale show once when it is missing.
func (h *showSeasons) season(ctx context.Context, number int) (Item, bool, error) {
	for {
		seasons, err := h.seasons(ctx)
		if err != nil {
			return Item{}, false, err
		}
		for _, season := range seasons {
			if *season.SeasonNumber == number {
				return season, true, nil
			}
		}
		if refreshed, err := h.refresh(ctx); err != nil || !refreshed {
			return Item{}, false, err
		}
	}
}

// fill fetches a season's episodes at most once per match: when they were never
// loaded, or when the cached ones are stale. It reports whether it fetched.
func (h *showSeasons) fill(ctx context.Context, season Item) (bool, error) {
	if h.loaded[season.ID] {
		return false, nil
	}
	if season.ChildrenLoaded {
		stale, err := h.stale(ctx, season.ID)
		if err != nil || !stale {
			return false, err
		}
	}
	if err := h.s.loadSeason(ctx, season); err != nil && !errors.Is(err, provider.ErrUnsupported) {
		return false, err
	}
	h.loaded[season.ID] = true
	return true, nil
}

// stale reports whether a loaded season's episodes may be outdated: fetched longer
// than the refresh interval ago, or fewer than a later show refresh counted. Loading
// a season stores the season before its episodes, so only a show refresh leaves the
// season newer than its newest episode.
func (h *showSeasons) stale(ctx context.Context, seasonID int64) (bool, error) {
	var stale bool
	err := h.s.Pool.QueryRow(ctx, `SELECT COALESCE(max(e.refreshed_at), s.refreshed_at) < clock_timestamp()-make_interval(secs => $2)
		OR COALESCE(count(e.id) < s.total AND s.refreshed_at > max(e.refreshed_at), false)
		FROM catalog_items s LEFT JOIN catalog_items e ON e.parent_id=s.id WHERE s.id=$1 GROUP BY s.id`, seasonID, h.interval.Seconds()).Scan(&stale)
	return stale, err
}

// refresh refetches the show once per match when its metadata is older than the
// refresh interval, so newly listed seasons and episode counts appear.
func (h *showSeasons) refresh(ctx context.Context) (bool, error) {
	if h.refreshed || time.Since(h.show.RefreshedAt) < h.interval {
		return false, nil
	}
	h.refreshed = true
	p, ok := h.s.Providers[h.show.Provider]
	if !ok {
		return false, nil
	}
	_, err, _ := h.s.group.Do("resolve:"+h.show.Provider+":"+h.show.Kind+":"+h.show.ProviderID, func() (any, error) {
		return h.s.fetchAndSave(ctx, p, h.show.Kind, h.show.ProviderID)
	})
	return err == nil, err
}

// nearest orders seasons by distance from the hinted one, the later season first on a
// tie and specials last unless hinted, so a renumbered episode is usually found early.
func nearest(seasons []Item, hint *int) []Item {
	from := 1
	if hint != nil {
		from = *hint
	}
	rank := func(n int) int {
		switch {
		case n == 0 && from != 0:
			return math.MaxInt
		case n >= from:
			return 2 * (n - from)
		}
		return 2*(from-n) + 1
	}
	out := slices.Clone(seasons)
	sort.SliceStable(out, func(i, j int) bool { return rank(*out[i].SeasonNumber) < rank(*out[j].SeasonNumber) })
	return out
}
