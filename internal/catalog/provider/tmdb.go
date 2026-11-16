package provider

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TMDB covers movies and television (shows, seasons, episodes).
type TMDB struct {
	Client   *Client
	Key      string
	Language string
	base     string
}

func (TMDB) Name() string    { return "tmdb" }
func (TMDB) Kinds() []string { return []string{"movie", "series"} }

type tmdbGenre struct {
	Name string `json:"name"`
}
type tmdbEpisode struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	EpisodeNumber int     `json:"episode_number"`
	SeasonNumber  int     `json:"season_number"`
	Runtime       int     `json:"runtime"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
}
type tmdbSeason struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	Overview     string        `json:"overview"`
	AirDate      string        `json:"air_date"`
	SeasonNumber int           `json:"season_number"`
	EpisodeCount int           `json:"episode_count"`
	PosterPath   string        `json:"poster_path"`
	Episodes     []tmdbEpisode `json:"episodes"`
}
type tmdbPerson struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Character   string `json:"character"`
	Job         string `json:"job"`
	ProfilePath string `json:"profile_path"`
}
type tmdbStudio struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

// tmdbReleaseDates is TMDB's per-country release list for a movie.
type tmdbReleaseDates struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Dates   []struct {
			Date string `json:"release_date"`
			Type int    `json:"type"`
		} `json:"release_dates"`
	} `json:"results"`
}

// tmdbReleaseTypes names the TMDB release types the calendar can show.
var tmdbReleaseTypes = map[int]string{3: "theatrical", 4: "digital", 5: "physical"}

// releaseDates keeps the earliest date of each calendar type per country:
// {"US": {"theatrical": "2024-03-01", "digital": "2024-04-16"}}.
func releaseDates(v *tmdbReleaseDates) map[string]any {
	out := map[string]any{}
	if v == nil {
		return out
	}
	for _, c := range v.Results {
		if len(c.Country) != 2 {
			continue
		}
		dates := map[string]any{}
		for _, d := range c.Dates {
			kind, ok := tmdbReleaseTypes[d.Type]
			if !ok || len(d.Date) < 10 {
				continue
			}
			day := d.Date[:10]
			if _, err := time.Parse("2006-01-02", day); err != nil {
				continue
			}
			if old, ok := dates[kind].(string); !ok || day < old {
				dates[kind] = day
			}
		}
		if len(dates) > 0 {
			out[c.Country] = dates
		}
	}
	return out
}

type tmdbItem struct {
	ReleaseDates     *tmdbReleaseDates `json:"release_dates"`
	ID               int64             `json:"id"`
	Title            string            `json:"title"`
	Name             string            `json:"name"`
	OriginalTitle    string            `json:"original_title"`
	OriginalName     string            `json:"original_name"`
	Overview         string            `json:"overview"`
	PosterPath       string            `json:"poster_path"`
	BackdropPath     string            `json:"backdrop_path"`
	ReleaseDate      string            `json:"release_date"`
	FirstAirDate     string            `json:"first_air_date"`
	Runtime          int               `json:"runtime"`
	EpisodeRunTime   []int             `json:"episode_run_time"`
	Status           string            `json:"status"`
	VoteAverage      float64           `json:"vote_average"`
	Genres           []tmdbGenre       `json:"genres"`
	NumberOfEpisodes int               `json:"number_of_episodes"`
	Seasons          []tmdbSeason
	Networks         []tmdbStudio `json:"networks"`
	Companies        []tmdbStudio `json:"production_companies"`
	ImdbID           string       `json:"imdb_id"`
	ExternalIDs      struct {
		ImdbID string `json:"imdb_id"`
		TvdbID int64  `json:"tvdb_id"`
	} `json:"external_ids"`
	Credits struct {
		Cast []tmdbPerson `json:"cast"`
		Crew []tmdbPerson `json:"crew"`
	} `json:"credits"`
	CreatedBy       []tmdbPerson `json:"created_by"`
	Recommendations *struct {
		Results []tmdbItem `json:"results"`
	} `json:"recommendations"`
	Translations *struct {
		Translations []struct {
			Language string `json:"iso_639_1"`
			Data     struct {
				Title string `json:"title"`
				Name  string `json:"name"`
			} `json:"data"`
		} `json:"translations"`
	} `json:"translations"`
	MediaType   string  `json:"media_type"`
	GenreIDs    []int   `json:"genre_ids"`
	VoteCount   int     `json:"vote_count"`
	Character   string  `json:"character"`
	Job         string  `json:"job"`
	Popularity  float64 `json:"popularity"`
	NextEpisode *struct {
		AirDate string `json:"air_date"`
	} `json:"next_episode_to_air"`
}

func (t TMDB) image(path, size string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

func (t TMDB) get(ctx context.Context, path string, query url.Values, out any) error {
	if t.Key == "" {
		return ErrNotConfigured
	}
	if query == nil {
		query = url.Values{}
	}
	if t.Language != "" {
		query.Set("language", t.Language)
	}
	header := http.Header{}
	// A v4 read-access token is a JWT; a v3 key is a short hex string.
	if strings.Count(t.Key, ".") == 2 {
		header.Set("Authorization", "Bearer "+t.Key)
	} else {
		query.Set("api_key", t.Key)
	}
	base := t.base
	if base == "" {
		base = "https://api.themoviedb.org/3"
	}
	return t.Client.JSON(ctx, Request{URL: base + path + "?" + query.Encode(), Header: header, Retries: 1}, out)
}

func (t TMDB) Search(ctx context.Context, kind, query string) ([]Record, error) {
	endpoint := map[string]string{"movie": "/search/movie", "series": "/search/tv"}[kind]
	if endpoint == "" {
		return nil, ErrUnsupported
	}
	var out struct {
		Results []tmdbItem `json:"results"`
	}
	if err := t.get(ctx, endpoint, url.Values{"query": {query}, "include_adult": {"false"}}, &out); err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(out.Results))
	for _, item := range out.Results {
		records = append(records, t.record(kind, item))
	}
	return records, nil
}

func (t TMDB) record(kind string, v tmdbItem) Record {
	r := Record{
		Provider: "tmdb", Kind: kind, ProviderID: strconv.FormatInt(v.ID, 10), Overview: v.Overview,
		ImageURL: t.image(v.PosterPath, "w500"), BackdropURL: t.image(v.BackdropPath, "w1280"),
		ReleaseStatus: v.Status, Rating: floatPtr(v.VoteAverage), ExternalIDs: map[string]string{}, Details: map[string]any{},
	}
	for _, g := range v.Genres {
		r.Genres = append(r.Genres, g.Name)
	}
	// Names stay in Credits for search and display; people and studios keep their
	// identifiers in Details so person and studio pages can be opened.
	people := []map[string]any{}
	person := func(p tmdbPerson, role string) {
		r.Credits = append(r.Credits, p.Name)
		if p.ID > 0 && len(people) < 24 {
			people = append(people, map[string]any{"id": strconv.FormatInt(p.ID, 10), "name": clean(p.Name, 200), "role": clean(role, 200), "image_url": t.image(p.ProfilePath, "w185")})
		}
	}
	for _, c := range v.CreatedBy {
		person(c, "Creator")
	}
	for _, c := range v.Credits.Crew {
		if c.Job == "Director" {
			person(c, c.Job)
		}
	}
	for i, c := range v.Credits.Cast {
		if i >= 12 {
			break
		}
		person(c, c.Character)
	}
	if len(people) > 0 {
		r.Details["people"] = people
	}
	studios, refs := []string{}, []map[string]any{}
	for _, group := range []struct {
		kind  string
		items []tmdbStudio
	}{{"network", v.Networks}, {"company", v.Companies}} {
		for _, n := range group.items {
			studios = append(studios, n.Name)
			if n.ID > 0 && len(refs) < 12 {
				refs = append(refs, map[string]any{"kind": group.kind, "id": strconv.FormatInt(n.ID, 10), "name": clean(n.Name, 200)})
			}
		}
	}
	if len(studios) > 0 {
		r.Details["studios"] = bounded(studios)
		r.Details["studio_refs"] = refs
	}
	// Titles in other languages let each person see titles in their own language.
	if v.Translations != nil {
		titles := map[string]string{}
		for _, t := range v.Translations.Translations {
			title := clean(firstNonEmptyString(t.Data.Title, t.Data.Name), 500)
			if len(t.Language) == 2 && title != "" && titles[t.Language] == "" && len(titles) < 80 {
				titles[t.Language] = title
			}
		}
		if len(titles) > 0 {
			r.Details["titles"] = titles
		}
	}
	if v.Recommendations != nil {
		related := []map[string]any{}
		for _, item := range v.Recommendations.Results {
			if len(related) >= 20 {
				break
			}
			rel := t.record(kind, tmdbItem{ID: item.ID, Title: item.Title, Name: item.Name, PosterPath: item.PosterPath, ReleaseDate: item.ReleaseDate, FirstAirDate: item.FirstAirDate})
			related = append(related, map[string]any{"provider": "tmdb", "kind": kind, "id": rel.ProviderID, "title": clean(rel.Title, 500), "image_url": rel.ImageURL, "year": rel.Year()})
		}
		r.Details["related"] = related
	}
	if kind == "movie" {
		r.Title, r.OriginalTitle, r.ReleaseDate, r.RuntimeMinutes = v.Title, v.OriginalTitle, date(v.ReleaseDate), intPtr(v.Runtime)
		if dates := releaseDates(v.ReleaseDates); len(dates) > 0 {
			r.Details["release_dates"] = dates
		}
		if v.ImdbID != "" {
			r.ExternalIDs["imdb"] = v.ImdbID
		}
		return r
	}
	r.Title, r.OriginalTitle, r.ReleaseDate = v.Name, v.OriginalName, date(v.FirstAirDate)
	if len(v.EpisodeRunTime) > 0 {
		r.RuntimeMinutes = intPtr(v.EpisodeRunTime[0])
	}
	if v.ExternalIDs.ImdbID != "" {
		r.ExternalIDs["imdb"] = v.ExternalIDs.ImdbID
	}
	if v.ExternalIDs.TvdbID > 0 {
		r.ExternalIDs["tvdb"] = strconv.FormatInt(v.ExternalIDs.TvdbID, 10)
	}
	if v.NextEpisode != nil && v.NextEpisode.AirDate != "" {
		r.Details["next_air_date"] = v.NextEpisode.AirDate
	}
	// Specials (season 0) are tracked but excluded from the show total.
	total := 0
	for _, s := range v.Seasons {
		n := s.SeasonNumber
		if n > 0 {
			total += s.EpisodeCount
		}
		r.Children = append(r.Children, Record{
			Provider: "tmdb", Kind: "season", ProviderID: r.ProviderID + ":" + strconv.Itoa(n), Title: s.Name,
			Overview: s.Overview, ImageURL: t.image(s.PosterPath, "w500"), ReleaseDate: date(s.AirDate),
			Total: intPtr(s.EpisodeCount), SeasonNumber: &n,
		})
	}
	r.ChildrenLoaded = v.Seasons != nil
	r.Total = intPtr(total)
	if v.NumberOfEpisodes > 0 && total == 0 {
		r.Total = intPtr(v.NumberOfEpisodes)
	}
	return r
}

func (t TMDB) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return Record{}, ErrNotFound
	}
	var v tmdbItem
	switch kind {
	case "movie":
		if err := t.get(ctx, "/movie/"+id, url.Values{"append_to_response": {"credits,recommendations,translations,release_dates"}}, &v); err != nil {
			return Record{}, err
		}
	case "series":
		if err := t.get(ctx, "/tv/"+id, url.Values{"append_to_response": {"credits,external_ids,recommendations,translations"}}, &v); err != nil {
			return Record{}, err
		}
		if v.Seasons == nil {
			v.Seasons = []tmdbSeason{}
		}
	default:
		return Record{}, ErrUnsupported
	}
	return t.record(kind, v), nil
}

// FetchChild loads a season's episodes.
func (t TMDB) FetchChild(ctx context.Context, parent, child Record) (Record, error) {
	if child.Kind != "season" || child.SeasonNumber == nil {
		return Record{}, ErrUnsupported
	}
	var s tmdbSeason
	if err := t.get(ctx, "/tv/"+parent.ProviderID+"/season/"+strconv.Itoa(*child.SeasonNumber), nil, &s); err != nil {
		return Record{}, err
	}
	n := s.SeasonNumber
	out := Record{
		Provider: "tmdb", Kind: "season", ProviderID: parent.ProviderID + ":" + strconv.Itoa(n), Title: s.Name,
		Overview: s.Overview, ImageURL: t.image(s.PosterPath, "w500"), ReleaseDate: date(s.AirDate),
		SeasonNumber: &n, ChildrenLoaded: true, Total: intPtr(len(s.Episodes)),
	}
	for _, e := range s.Episodes {
		season, episode := n, e.EpisodeNumber
		out.Children = append(out.Children, Record{
			Provider: "tmdb", Kind: "episode", ProviderID: strconv.FormatInt(e.ID, 10), Title: episodeTitle(e.Name, episode),
			Overview: e.Overview, ImageURL: t.image(e.StillPath, "w500"), ReleaseDate: date(e.AirDate),
			RuntimeMinutes: intPtr(e.Runtime), Rating: floatPtr(e.VoteAverage), SeasonNumber: &season, EpisodeNumber: &episode,
		})
	}
	return out, nil
}

func episodeTitle(name string, number int) string {
	if strings.TrimSpace(name) == "" {
		return "Episode " + strconv.Itoa(number)
	}
	return name
}

// Found is what an external identifier resolves to at TMDB, kept apart by result type.
// TVDB numbers series and episodes independently, so one TVDB number can name a show
// and some other show's episode; a caller takes only the type it is looking for.
type Found struct {
	Movies, Shows []string
	Episodes      []FoundEpisode
}

// FoundEpisode is an episode with its TMDB id and its place in TMDB's own numbering.
type FoundEpisode struct {
	ID, ShowID      string
	Season, Episode int
}

// Find resolves an IMDb or TVDB identifier to TMDB movies, shows and episodes.
func (t TMDB) Find(ctx context.Context, source, id string) (Found, error) {
	param := map[string]string{"imdb": "imdb_id", "tvdb": "tvdb_id"}[source]
	if param == "" || id == "" || len(id) > 40 || strings.ContainsAny(id, "/?#&") {
		return Found{}, ErrNotFound
	}
	var out struct {
		Movies   []struct{ ID int64 } `json:"movie_results"`
		Shows    []struct{ ID int64 } `json:"tv_results"`
		Episodes []struct {
			ID            int64 `json:"id"`
			ShowID        int64 `json:"show_id"`
			SeasonNumber  int   `json:"season_number"`
			EpisodeNumber int   `json:"episode_number"`
		} `json:"tv_episode_results"`
	}
	if err := t.get(ctx, "/find/"+url.PathEscape(id), url.Values{"external_source": {param}}, &out); err != nil {
		return Found{}, err
	}
	var found Found
	for _, m := range out.Movies {
		if m.ID > 0 {
			found.Movies = append(found.Movies, strconv.FormatInt(m.ID, 10))
		}
	}
	for _, s := range out.Shows {
		if s.ID > 0 {
			found.Shows = append(found.Shows, strconv.FormatInt(s.ID, 10))
		}
	}
	for _, e := range out.Episodes {
		if e.ShowID < 1 || e.SeasonNumber < 0 || e.EpisodeNumber < 1 {
			continue
		}
		episode := FoundEpisode{ShowID: strconv.FormatInt(e.ShowID, 10), Season: e.SeasonNumber, Episode: e.EpisodeNumber}
		if e.ID > 0 {
			episode.ID = strconv.FormatInt(e.ID, 10)
		}
		found.Episodes = append(found.Episodes, episode)
	}
	if len(found.Movies)+len(found.Shows)+len(found.Episodes) == 0 {
		return Found{}, ErrNotFound
	}
	return found, nil
}

// Person is a cast or crew member with their best-known credits.
func (t TMDB) Person(ctx context.Context, id string) (Person, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return Person{}, ErrNotFound
	}
	var v struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Biography   string `json:"biography"`
		Birthday    string `json:"birthday"`
		Deathday    string `json:"deathday"`
		Department  string `json:"known_for_department"`
		ProfilePath string `json:"profile_path"`
		Credits     struct {
			Cast []tmdbItem `json:"cast"`
			Crew []tmdbItem `json:"crew"`
		} `json:"combined_credits"`
	}
	if err := t.get(ctx, "/person/"+id, url.Values{"append_to_response": {"combined_credits"}}, &v); err != nil {
		return Person{}, err
	}
	out := Person{Provider: "tmdb", ID: id, Name: clean(v.Name, 200), Biography: clean(v.Biography, 20000), ImageURL: t.image(v.ProfilePath, "h632"),
		KnownFor: clean(v.Department, 100), Birthday: date(v.Birthday), Deathday: date(v.Deathday)}
	// Best known first: by how many people rated the title, leaving out appearances as
	// themselves on talk, news and reality shows, which would otherwise crowd the top.
	all := []tmdbItem{}
	for _, c := range append(append([]tmdbItem{}, v.Credits.Cast...), v.Credits.Crew...) {
		if !selfAppearance(c) {
			all = append(all, c)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].VoteCount > all[j].VoteCount })
	seen := map[string]int{}
	for _, c := range all {
		kind := map[string]string{"movie": "movie", "tv": "series"}[c.MediaType]
		if kind == "" {
			continue
		}
		role := c.Character
		if role == "" {
			role = c.Job
		}
		key := kind + ":" + strconv.FormatInt(c.ID, 10)
		if i, ok := seen[key]; ok {
			if role != "" && !strings.Contains(out.Credits[i].Role, role) && len(out.Credits[i].Role) < 150 {
				out.Credits[i].Role += ", " + role
			}
			continue
		}
		if len(out.Credits) >= 80 {
			continue
		}
		seen[key] = len(out.Credits)
		out.Credits = append(out.Credits, Credit{Record: t.record(kind, c), Role: clean(role, 200)})
	}
	return out, nil
}

// Studio is a production company or television network with its popular titles.
func (t TMDB) Studio(ctx context.Context, kind, id string) (Studio, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil || (kind != "company" && kind != "network") {
		return Studio{}, ErrNotFound
	}
	var v struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoPath    string `json:"logo_path"`
		Country     string `json:"origin_country"`
	}
	if err := t.get(ctx, "/"+kind+"/"+id, nil, &v); err != nil {
		return Studio{}, err
	}
	out := Studio{Provider: "tmdb", Kind: kind, ID: id, Name: clean(v.Name, 200), Description: clean(v.Description, 5000), LogoURL: t.image(v.LogoPath, "w300"), Country: clean(v.Country, 10)}
	lists := []struct{ kind, path, param string }{{"series", "/discover/tv", "with_networks"}}
	if kind == "company" {
		lists = []struct{ kind, path, param string }{{"movie", "/discover/movie", "with_companies"}, {"series", "/discover/tv", "with_companies"}}
	}
	for _, l := range lists {
		var page struct {
			Results []tmdbItem `json:"results"`
		}
		if err := t.get(ctx, l.path, url.Values{l.param: {id}, "sort_by": {"popularity.desc"}, "include_adult": {"false"}}, &page); err != nil {
			return Studio{}, err
		}
		for _, item := range page.Results {
			out.Titles = append(out.Titles, t.record(l.kind, item))
		}
	}
	return out, nil
}

// Trending lists this week's most popular movies or shows.
func (t TMDB) Trending(ctx context.Context, kind string) ([]Record, error) {
	endpoint := map[string]string{"movie": "/trending/movie/week", "series": "/trending/tv/week"}[kind]
	if endpoint == "" {
		return nil, ErrUnsupported
	}
	var out struct {
		Results []tmdbItem `json:"results"`
	}
	if err := t.get(ctx, endpoint, nil, &out); err != nil {
		return nil, err
	}
	records := make([]Record, 0, len(out.Results))
	for _, item := range out.Results {
		records = append(records, t.record(kind, item))
	}
	return records, nil
}

func selfAppearance(c tmdbItem) bool {
	for _, g := range c.GenreIDs {
		if g == 10763 || g == 10764 || g == 10767 { // news, reality, talk
			return true
		}
	}
	role := strings.ToLower(strings.TrimSpace(c.Character))
	for _, self := range []string{"self", "himself", "herself", "themselves"} {
		if role == self || strings.HasPrefix(role, self+" ") || strings.HasPrefix(role, self+" -") {
			return true
		}
	}
	return false
}

// EpisodeOrder is an alternate grouping of a show's episodes, such as the DVD order.
type EpisodeOrder struct {
	ID, Name, Kind string
	Groups         []EpisodeOrderGroup
}

// EpisodeOrderGroup lists provider episode ids in order.
type EpisodeOrderGroup struct {
	Name     string
	Episodes []string
}

var tmdbOrderKinds = map[int]string{1: "Original air date", 2: "Absolute", 3: "DVD", 4: "Digital", 5: "Story arc", 6: "Production", 7: "TV"}

// EpisodeOrders lists a show's alternate orders without their episodes.
func (t TMDB) EpisodeOrders(ctx context.Context, showID string) ([]EpisodeOrder, error) {
	if _, err := strconv.ParseInt(showID, 10, 64); err != nil {
		return nil, ErrNotFound
	}
	var out struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type int    `json:"type"`
		} `json:"results"`
	}
	if err := t.get(ctx, "/tv/"+showID+"/episode_groups", nil, &out); err != nil {
		return nil, err
	}
	orders := make([]EpisodeOrder, 0, len(out.Results))
	for _, r := range out.Results {
		if r.ID != "" && len(r.ID) <= 60 && !strings.ContainsAny(r.ID, "/?#") {
			orders = append(orders, EpisodeOrder{ID: r.ID, Name: clean(r.Name, 200), Kind: tmdbOrderKinds[r.Type]})
		}
	}
	return orders, nil
}

// EpisodeOrder loads one order with its groups and episodes.
func (t TMDB) EpisodeOrder(ctx context.Context, id string) (EpisodeOrder, error) {
	if id == "" || len(id) > 60 || strings.ContainsAny(id, "/?#") {
		return EpisodeOrder{}, ErrNotFound
	}
	var v struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Type   int    `json:"type"`
		Groups []struct {
			Name     string `json:"name"`
			Order    int    `json:"order"`
			Episodes []struct {
				ID    int64 `json:"id"`
				Order int   `json:"order"`
			} `json:"episodes"`
		} `json:"groups"`
	}
	if err := t.get(ctx, "/tv/episode_group/"+id, nil, &v); err != nil {
		return EpisodeOrder{}, err
	}
	out := EpisodeOrder{ID: v.ID, Name: clean(v.Name, 200), Kind: tmdbOrderKinds[v.Type]}
	sort.SliceStable(v.Groups, func(i, j int) bool { return v.Groups[i].Order < v.Groups[j].Order })
	for _, g := range v.Groups {
		sort.SliceStable(g.Episodes, func(i, j int) bool { return g.Episodes[i].Order < g.Episodes[j].Order })
		group := EpisodeOrderGroup{Name: clean(g.Name, 200)}
		for _, e := range g.Episodes {
			group.Episodes = append(group.Episodes, strconv.FormatInt(e.ID, 10))
		}
		out.Groups = append(out.Groups, group)
	}
	return out, nil
}
