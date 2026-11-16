// Package provider adapts external metadata services to one native record shape.
// Adapters only read public metadata; they never receive user data or write upstream.
package provider

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Record is provider metadata normalized for the shared catalog. Children carry the
// next level of a hierarchy (seasons of a show, episodes of a season) when fetched.
type Record struct {
	Provider       string
	Kind           string
	ProviderID     string
	Title          string
	OriginalTitle  string
	Overview       string
	ImageURL       string
	BackdropURL    string
	ReleaseDate    *time.Time
	RuntimeMinutes *int
	Unit           string
	Total          *int
	ReleaseStatus  string
	Genres         []string
	Credits        []string
	ExternalIDs    map[string]string
	Details        map[string]any
	Rating         *float64
	SeasonNumber   *int
	EpisodeNumber  *int
	Children       []Record
	ChildrenLoaded bool
}

// Year is the release year when known.
func (r Record) Year() *int {
	if r.ReleaseDate == nil {
		return nil
	}
	y := r.ReleaseDate.Year()
	return &y
}

type Provider interface {
	Name() string
	// Kinds lists catalog kinds this adapter can search and fetch.
	Kinds() []string
	Search(ctx context.Context, kind, query string) ([]Record, error)
	Fetch(ctx context.Context, kind, id string) (Record, error)
}

// Person is a cast or crew member with their credits, best known first.
type Person struct {
	Provider, ID, Name, Biography, ImageURL, KnownFor string
	Birthday, Deathday                                *time.Time
	Credits                                           []Credit
}

// Credit is a title a person worked on and their role in it.
type Credit struct {
	Record Record
	Role   string
}

// Studio is a production company, network or publisher with its popular titles.
type Studio struct {
	Provider, Kind, ID, Name, Description, LogoURL, Country string
	Titles                                                  []Record
}

// Directory is a provider with person and studio pages.
type Directory interface {
	Person(ctx context.Context, id string) (Person, error)
	Studio(ctx context.Context, kind, id string) (Studio, error)
}

// Edition is one printing or recording of a work.
type Edition struct {
	ID, Title, Publisher, Year, ISBN, Format, Language, ImageURL string
	Pages                                                        *int
}

// EditionLister lists the editions of a work.
type EditionLister interface {
	Editions(ctx context.Context, kind, id string) ([]Edition, error)
}

// Orderer lists and loads alternate episode orders of a show.
type Orderer interface {
	EpisodeOrders(ctx context.Context, showID string) ([]EpisodeOrder, error)
	EpisodeOrder(ctx context.Context, id string) (EpisodeOrder, error)
}

// Trender lists currently popular titles of a kind, for Discover.
type Trender interface {
	Trending(ctx context.Context, kind string) ([]Record, error)
}

// ChildFetcher loads one child level on demand, such as the episodes of a season.
type ChildFetcher interface {
	FetchChild(ctx context.Context, parent Record, child Record) (Record, error)
}

var (
	ErrNotFound       = errors.New("provider record not found")
	ErrUnavailable    = errors.New("provider unavailable")
	ErrNotConfigured  = errors.New("provider not configured")
	ErrUnsupported    = errors.New("provider does not support this media type")
	ErrRateLimited    = errors.New("provider rate limited")
	ErrInvalidPayload = errors.New("provider returned an unusable response")
)

// UnitFor is the native progress unit of a catalog kind.
func UnitFor(kind string) string {
	switch kind {
	case "series", "anime", "season", "podcast":
		return "episodes"
	case "book":
		return "pages"
	case "manga":
		return "chapters"
	case "music":
		return "tracks"
	case "movie", "episode", "podcast_episode", "audiobook", "game", "track", "video":
		return "minutes"
	default:
		return "items"
	}
}

func clean(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// Normalize bounds every field to the catalog's storage constraints.
func Normalize(r Record) (Record, error) {
	r.Title = clean(strings.ReplaceAll(r.Title, "\n", " "), 500)
	if r.Title == "" || r.ProviderID == "" || len(r.ProviderID) > 200 {
		return r, ErrInvalidPayload
	}
	r.OriginalTitle = clean(strings.ReplaceAll(r.OriginalTitle, "\n", " "), 500)
	if r.OriginalTitle == r.Title {
		r.OriginalTitle = ""
	}
	r.Overview = clean(r.Overview, 20000)
	r.ImageURL = safeURL(r.ImageURL)
	r.BackdropURL = safeURL(r.BackdropURL)
	r.ReleaseStatus = clean(r.ReleaseStatus, 60)
	if r.Unit == "" {
		r.Unit = UnitFor(r.Kind)
	}
	if r.Total != nil && (*r.Total < 1 || *r.Total > 100000000) {
		r.Total = nil
	}
	if r.RuntimeMinutes != nil && (*r.RuntimeMinutes < 0 || *r.RuntimeMinutes > 1000000) {
		r.RuntimeMinutes = nil
	}
	if r.Rating != nil && (*r.Rating < 0 || *r.Rating > 10) {
		r.Rating = nil
	}
	r.Genres = bounded(r.Genres)
	r.Credits = bounded(r.Credits)
	for i := range r.Children {
		child, err := Normalize(r.Children[i])
		if err != nil {
			return r, err
		}
		r.Children[i] = child
	}
	return r, nil
}

func bounded(in []string) []string {
	out := make([]string, 0, min(len(in), 50))
	seen := map[string]bool{}
	for _, v := range in {
		v = clean(v, 200)
		if v != "" && !seen[v] && len(out) < 50 {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func safeURL(u string) string {
	if len(u) > 1000 || !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}

func intPtr(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

func floatPtr(f float64) *float64 {
	if f <= 0 {
		return nil
	}
	return &f
}

// date parses the date layouts providers commonly return.
func date(s string) *time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01", "2006", "January 2, 2006", "Jan 2, 2006", "2 January 2006", "January 2006"} {
		if t, err := time.Parse(layout, s); err == nil && t.Year() > 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			return &t
		}
	}
	return nil
}

// ExternalFinder is implemented by providers that can look a record up by another
// service's identifier (for example a MyAnimeList id on AniList). It returns the
// provider's own id.
type ExternalFinder interface {
	FindExternal(ctx context.Context, kind, scheme, value string) (string, error)
}
