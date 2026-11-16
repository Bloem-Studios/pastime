package catalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
)

// Reference is what an external system says was watched: identifiers when it has them,
// titles otherwise. Show fields describe the series of an episode. For an episode,
// Title is the episode's own and AirDate (YYYY-MM-DD) its first air date; both only
// corroborate its identifiers and numbers (see matchEpisode).
type Reference struct {
	Kind                         string // movie, episode or series
	TMDB, IMDB, TVDB             string
	ShowTMDB, ShowIMDB, ShowTVDB string
	Season, Episode              *int
	Title, ShowTitle             string
	Year                         *int
	AirDate                      string
}

// Match is the catalog record a reference resolved to. Episode is set for episodes.
type Match struct {
	Title   Item
	Episode *Item
}

// ErrNoMatch means the reference could not be tied to a catalog record with confidence.
var ErrNoMatch = &fault.Error{Status: 404, Code: "no_match", Message: "No matching title was found."}

// finder resolves IMDb/TVDB identifiers; the TMDB adapter implements it.
type finder interface {
	Find(ctx context.Context, source, id string) (provider.Found, error)
}

func (s *Service) tmdb() (finder, bool) {
	p, ok := s.Providers["tmdb"].(finder)
	return p, ok
}

// MatchVideo resolves a movie or episode reference, preferring stable identifiers over
// titles. Title matching requires an exact normalized title and, when known, the year.
// Episodes are matched by their own identifiers before season and episode numbers;
// see matchEpisode.
func (s *Service) MatchVideo(ctx context.Context, ref Reference) (Match, error) {
	tmdb, ok := s.tmdb()
	if !ok {
		return Match{}, providerFault(provider.ErrNotConfigured)
	}
	if ref.Kind == "movie" {
		id, err := s.movieID(ctx, tmdb, ref)
		if err != nil {
			return Match{}, err
		}
		it, err := s.Resolve(ctx, "tmdb", "movie", id)
		return Match{Title: it}, err
	}
	lookup := &episodeLookup{tmdb: tmdb, ref: ref}
	showID, err := s.showID(ctx, tmdb, ref, lookup)
	if err != nil {
		return Match{}, err
	}
	show, err := s.Resolve(ctx, "tmdb", "series", showID)
	if err != nil {
		return Match{}, err
	}
	if ref.Kind == "series" {
		return Match{Title: show}, nil
	}
	ep, err := s.matchEpisode(ctx, show, ref, lookup)
	if isNoMatch(err) {
		moved, movedEp, ok, moveErr := s.matchInPlacedShow(ctx, show, ref, lookup)
		switch {
		case moveErr != nil:
			return Match{}, moveErr
		case ok:
			return Match{Title: moved, Episode: &movedEp}, nil
		}
	}
	if err != nil {
		return Match{}, err
	}
	return Match{Title: show, Episode: &ep}, nil
}

// matchInPlacedShow handles a show TheTVDB keeps as one series where TMDB has several
// (The Great British Bake Off before and after its move, every Super Sentai): the
// series identifiers name one TMDB show while TMDB places the episode's own TheTVDB or
// IMDb id in exactly one other. The episode is then matched in that show, by its own
// identifiers and checked by its numbers, title and air date like any other match;
// moving to another show also needs the title or air date to agree, so identifiers
// alone never carry a play away from the show it was sent for.
func (s *Service) matchInPlacedShow(ctx context.Context, show Item, ref Reference, lookup *episodeLookup) (Item, Item, bool, error) {
	found, err := lookup.episodes(ctx)
	if err != nil {
		return Item{}, Item{}, false, err
	}
	other := ""
	for _, f := range found {
		if f.ShowID == "" || f.ShowID == show.ProviderID || other != "" && f.ShowID != other {
			return Item{}, Item{}, false, nil
		}
		other = f.ShowID
	}
	if other == "" {
		return Item{}, Item{}, false, nil
	}
	moved, err := s.Resolve(ctx, "tmdb", "series", other)
	if err != nil {
		return Item{}, Item{}, false, nil
	}
	// The series identifiers belong to the series TheTVDB merged; only the placed show
	// stands for it now.
	placed := ref
	placed.ShowTMDB, placed.ShowTVDB, placed.ShowIMDB = other, "", ""
	ep, err := s.matchEpisode(ctx, moved, placed, lookup)
	if isNoMatch(err) {
		return Item{}, Item{}, false, nil
	}
	if err != nil {
		return Item{}, Item{}, false, err
	}
	if described, _ := s.describe(ctx, placed); !agrees(described, ep) {
		return Item{}, Item{}, false, nil
	}
	return moved, ep, true, nil
}

func agreeID(current *string, candidate string) error {
	if candidate == "" {
		return nil
	}
	if *current != "" && *current != candidate {
		return ErrNoMatch
	}
	*current = candidate
	return nil
}

// oneID is the single identifier a lookup answered. Several different answers are
// ambiguous unless one of them is the identifier already agreed on.
func oneID(agreed string, ids []string) (string, error) {
	distinct := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !slices.Contains(distinct, id) {
			distinct = append(distinct, id)
		}
	}
	switch {
	case len(distinct) == 0:
		return "", nil
	case len(distinct) == 1:
		return distinct[0], nil
	case agreed != "" && slices.Contains(distinct, agreed):
		return agreed, nil
	}
	return "", ErrNoMatch
}

func (s *Service) movieID(ctx context.Context, tmdb finder, ref Reference) (string, error) {
	matched := ref.TMDB
	for _, external := range [][2]string{{"imdb", ref.IMDB}, {"tvdb", ref.TVDB}} {
		source, id := external[0], external[1]
		if id == "" {
			continue
		}
		candidate, err := s.byExternal(ctx, "movie", source, id)
		if err != nil {
			return "", err
		}
		if candidate == "" {
			found, err := tmdb.Find(ctx, source, id)
			if err != nil && !errors.Is(err, provider.ErrNotFound) {
				return "", providerFault(err)
			}
			if candidate, err = oneID(matched, found.Movies); err != nil {
				return "", err
			}
		}
		if err := agreeID(&matched, candidate); err != nil {
			return "", err
		}
	}
	if matched != "" {
		return matched, nil
	}
	return s.byTitle(ctx, "movie", ref.Title, ref.Year)
}

// showID agrees the series identifiers on one TMDB show. Only TV results of a lookup
// count: TVDB numbers series and episodes independently, so a series id can also be
// the TVDB id of some other show's episode. Without series identifiers, an episode's
// own identifiers name its show.
func (s *Service) showID(ctx context.Context, tmdb finder, ref Reference, lookup *episodeLookup) (string, error) {
	matched := ref.ShowTMDB
	for _, external := range [][2]string{{"imdb", ref.ShowIMDB}, {"tvdb", ref.ShowTVDB}} {
		source, id := external[0], external[1]
		if id == "" {
			continue
		}
		candidate, err := s.byExternal(ctx, "series", source, id)
		if err != nil {
			return "", err
		}
		if candidate == "" {
			found, err := tmdb.Find(ctx, source, id)
			if err != nil && !errors.Is(err, provider.ErrNotFound) {
				return "", providerFault(err)
			}
			if candidate, err = oneID(matched, found.Shows); err != nil {
				return "", err
			}
		}
		if err := agreeID(&matched, candidate); err != nil {
			return "", err
		}
	}
	if matched == "" && ref.Kind == "episode" {
		found, err := lookup.episodes(ctx)
		if err != nil {
			return "", err
		}
		shows := make([]string, 0, len(found))
		for _, e := range found {
			shows = append(shows, e.ShowID)
		}
		if matched, err = oneID("", shows); err != nil {
			return "", err
		}
	}
	if matched != "" {
		return matched, nil
	}
	return s.byTitle(ctx, "series", ref.ShowTitle, nil)
}

// byExternal refuses ambiguous catalog identifiers and propagates database errors.
func (s *Service) byExternal(ctx context.Context, kind, source, id string) (string, error) {
	var ids []string
	err := s.Pool.QueryRow(ctx, `SELECT array_agg(provider_id) FROM (SELECT provider_id FROM catalog_items WHERE provider='tmdb' AND kind=$1 AND parent_id IS NULL AND external_ids->>$2=$3 LIMIT 2) x`, kind, source, id).Scan(&ids)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if len(ids) > 1 {
		return "", ErrNoMatch
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", nil
}

func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Service) byTitle(ctx context.Context, kind, title string, year *int) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", ErrNoMatch
	}
	found, err := s.Search(ctx, kind, title)
	if err != nil {
		return "", err
	}
	if found.Degraded {
		return "", providerFault(provider.ErrUnavailable)
	}
	want := normalizeTitle(title)
	matched := ""
	for _, r := range found.Items {
		if r.Provider != "tmdb" || normalizeTitle(r.Title) != want {
			continue
		}
		// Allow a one-year difference: release dates vary by region.
		if year != nil && r.Year != nil && (*r.Year-*year > 1 || *year-*r.Year > 1) {
			continue
		}
		if matched != "" && matched != r.ProviderID {
			return "", ErrNoMatch
		}
		matched = r.ProviderID
	}
	if matched != "" {
		return matched, nil
	}
	return "", ErrNoMatch
}

// EpisodeMap loads every season of a show once and maps (season, episode) to catalog
// ids, so an import with hundreds of episodes costs one provider call per season.
func (s *Service) EpisodeMap(ctx context.Context, showID int64) (map[[2]int]int64, error) {
	seasons, err := s.Children(ctx, showID)
	if err != nil {
		return nil, err
	}
	out := map[[2]int]int64{}
	for _, se := range seasons {
		if se.Kind != "season" || se.SeasonNumber == nil {
			continue
		}
		episodes, err := s.Children(ctx, se.ID)
		if err != nil {
			return nil, err
		}
		for _, e := range episodes {
			if e.EpisodeNumber != nil {
				out[[2]int{*se.SeasonNumber, *e.EpisodeNumber}] = e.ID
			}
		}
	}
	return out, nil
}

// ChildByProviderID finds a direct child (a podcast episode or comic issue) by its
// provider identity.
func (s *Service) ChildByProviderID(ctx context.Context, parentID int64, providerID string) (int64, error) {
	children, err := s.Children(ctx, parentID)
	if err != nil {
		return 0, err
	}
	for _, c := range children {
		if c.ProviderID == providerID {
			return c.ID, nil
		}
	}
	return 0, ErrNoMatch
}

// BookReference identifies a book from a reading service export.
type BookReference struct {
	ISBN, Title, Author string
	Kind                string // book or audiobook
}

// MatchBook resolves a book by ISBN first, then by exact normalized title with an
// author check, so a different book with a similar name is never recorded.
func (s *Service) MatchBook(ctx context.Context, ref BookReference) (Item, error) {
	kind := ref.Kind
	if kind == "" {
		kind = "book"
	}
	isbn := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == 'X' || r == 'x' {
			return r
		}
		return -1
	}, ref.ISBN)
	if len(isbn) == 10 || len(isbn) == 13 {
		if found, err := s.Search(ctx, kind, isbn); err == nil && !found.Degraded && len(found.Items) > 0 {
			return s.Resolve(ctx, found.Items[0].Provider, kind, found.Items[0].ProviderID)
		}
	}
	if strings.TrimSpace(ref.Title) == "" {
		return Item{}, ErrNoMatch
	}
	found, err := s.Search(ctx, kind, strings.TrimSpace(ref.Title+" "+ref.Author))
	if err != nil {
		return Item{}, err
	}
	if found.Degraded {
		return Item{}, providerFault(provider.ErrUnavailable)
	}
	want, author := normalizeTitle(ref.Title), normalizeTitle(ref.Author)
	for _, r := range found.Items {
		if normalizeTitle(r.Title) != want {
			continue
		}
		if author != "" {
			match := false
			for _, c := range r.Credits {
				if n := normalizeTitle(c); n != "" && (strings.Contains(author, n) || strings.Contains(n, author)) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		return s.Resolve(ctx, r.Provider, kind, r.ProviderID)
	}
	return Item{}, ErrNoMatch
}

// TitleReference identifies a work by title alone: a game, an album or a show from a
// service that shares no identifiers.
type TitleReference struct {
	Kind, Title, Creator string
	Year                 *int
}

// MatchTitle resolves a title only on an exact normalized title match, with the year
// within one and a creator check when known, so a similar name is never recorded.
func (s *Service) MatchTitle(ctx context.Context, ref TitleReference) (Item, error) {
	if strings.TrimSpace(ref.Title) == "" || !trackable(ref.Kind) {
		return Item{}, ErrNoMatch
	}
	found, err := s.Search(ctx, ref.Kind, strings.TrimSpace(ref.Title+" "+ref.Creator))
	if err != nil {
		return Item{}, err
	}
	if found.Degraded {
		return Item{}, providerFault(provider.ErrUnavailable)
	}
	var matched *Result
	want, creator := normalizeTitle(ref.Title), normalizeTitle(ref.Creator)
	for _, r := range found.Items {
		if normalizeTitle(r.Title) != want {
			continue
		}
		if ref.Year != nil && r.Year != nil && (*r.Year-*ref.Year > 1 || *ref.Year-*r.Year > 1) {
			continue
		}
		if creator != "" && len(r.Credits) > 0 {
			match := false
			for _, c := range r.Credits {
				if n := normalizeTitle(c); n != "" && (strings.Contains(creator, n) || strings.Contains(n, creator)) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if matched != nil && (matched.Provider != r.Provider || matched.ProviderID != r.ProviderID) {
			return Item{}, ErrNoMatch
		}
		copy := r
		matched = &copy
	}
	if matched != nil {
		if matched.CatalogID != nil {
			return s.Get(ctx, *matched.CatalogID)
		}
		return s.Resolve(ctx, matched.Provider, ref.Kind, matched.ProviderID)
	}
	return Item{}, ErrNoMatch
}

// ChildByTitle finds a direct child by exact normalized title, for sources that name
// episodes but share no identifier with the provider.
func (s *Service) ChildByTitle(ctx context.Context, parentID int64, title string) (int64, error) {
	want := normalizeTitle(title)
	if want == "" {
		return 0, ErrNoMatch
	}
	children, err := s.Children(ctx, parentID)
	if err != nil {
		return 0, err
	}
	var matched int64
	for _, c := range children {
		if normalizeTitle(c.Title) == want {
			if matched != 0 && matched != c.ID {
				return 0, ErrNoMatch
			}
			matched = c.ID
		}
	}
	if matched != 0 {
		return matched, nil
	}
	return 0, ErrNoMatch
}

// ChildTrack only resolves an unambiguous numbered track on this album.
func (s *Service) ChildTrack(ctx context.Context, parentID int64, number int, title string) (int64, error) {
	children, err := s.Children(ctx, parentID)
	if err != nil {
		return 0, err
	}
	var id int64
	for _, c := range children {
		if c.Kind == "track" && c.EpisodeNumber != nil && *c.EpisodeNumber == number && (title == "" || normalizeTitle(c.Title) == normalizeTitle(title)) {
			if id != 0 {
				return 0, ErrNoMatch
			}
			id = c.ID
		}
	}
	if id == 0 {
		return 0, ErrNoMatch
	}
	return id, nil
}
