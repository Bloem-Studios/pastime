package provider

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TVDB covers series (and anime as series) through TheTVDB v4 API. It needs a licensed
// API key and, for subscriber keys, the subscriber PIN. Seasons and their episodes come
// with the series in the default (aired) order.
type TVDB struct {
	Client *Client
	Key    string
	PIN    string
	mu     *sync.Mutex
	token  *tvdbToken
}

type tvdbToken struct {
	value   string
	expires time.Time
}

func NewTVDB(c *Client, key, pin string) TVDB {
	return TVDB{Client: c, Key: key, PIN: pin, mu: &sync.Mutex{}, token: &tvdbToken{}}
}

func (TVDB) Name() string    { return "tvdb" }
func (TVDB) Kinds() []string { return []string{"series", "anime"} }

const tvdbAPI = "https://api4.thetvdb.com/v4"

// accessToken logs in once and reuses the token; TVDB tokens last a month.
func (t TVDB) accessToken(ctx context.Context) (string, error) {
	if t.Key == "" || t.mu == nil {
		return "", ErrNotConfigured
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token.value != "" && time.Now().Before(t.token.expires) {
		return t.token.value, nil
	}
	body := map[string]string{"apikey": t.Key}
	if t.PIN != "" {
		body["pin"] = t.PIN
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := t.Client.JSON(ctx, Request{Method: "POST", URL: tvdbAPI + "/login", Body: body}, &out); err != nil {
		return "", err
	}
	if out.Data.Token == "" {
		return "", ErrNotConfigured
	}
	t.token.value, t.token.expires = out.Data.Token, time.Now().Add(25*24*time.Hour)
	return t.token.value, nil
}

func (t TVDB) get(ctx context.Context, path string, out any) error {
	token, err := t.accessToken(ctx)
	if err != nil {
		return err
	}
	err = t.Client.JSON(ctx, Request{URL: tvdbAPI + path, Header: http.Header{"Authorization": {"Bearer " + token}}, Retries: 1}, out)
	if err != nil && strings.Contains(err.Error(), "status 401") {
		// A revoked or expired token: log in again next time.
		t.mu.Lock()
		t.token.value = ""
		t.mu.Unlock()
	}
	return err
}

func (t TVDB) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "series" && kind != "anime" {
		return nil, ErrUnsupported
	}
	var resp struct {
		Data []struct {
			TVDBID    string            `json:"tvdb_id"`
			Name      string            `json:"name"`
			Overview  string            `json:"overview"`
			ImageURL  string            `json:"image_url"`
			FirstAir  string            `json:"first_air_time"`
			Year      string            `json:"year"`
			Status    string            `json:"status"`
			Translate map[string]string `json:"translations"`
		} `json:"data"`
	}
	if err := t.get(ctx, "/search?"+url.Values{"query": {query}, "type": {"series"}, "limit": {"20"}}.Encode(), &resp); err != nil {
		return nil, err
	}
	out := []Record{}
	for _, v := range resp.Data {
		if _, err := strconv.ParseInt(v.TVDBID, 10, 64); err != nil || v.Name == "" {
			continue
		}
		r := Record{Provider: "tvdb", Kind: kind, ProviderID: v.TVDBID, Title: v.Name, Overview: clean(v.Overview, 4000), ImageURL: safeURL(v.ImageURL),
			ReleaseStatus: clean(v.Status, 60), Unit: "episodes", ExternalIDs: map[string]string{"tvdb": v.TVDBID}, Details: map[string]any{}}
		if english := v.Translate["eng"]; english != "" {
			r.Title = english
			if english != v.Name {
				r.OriginalTitle = v.Name
			}
		}
		r.ReleaseDate = date(v.FirstAir)
		if r.ReleaseDate == nil {
			r.ReleaseDate = date(v.Year)
		}
		out = append(out, r)
	}
	return out, nil
}

type tvdbEpisode struct {
	ID           int64   `json:"id"`
	SeasonNumber int     `json:"seasonNumber"`
	Number       int     `json:"number"`
	Name         *string `json:"name"`
	Overview     *string `json:"overview"`
	Aired        *string `json:"aired"`
	Runtime      *int    `json:"runtime"`
	Image        *string `json:"image"`
}

// EpisodeFacts is what TheTVDB says about one of its episodes: where it places it
// and when it aired. Other catalogs number episodes differently; the air date and
// title find the same episode there.
type EpisodeFacts struct {
	SeriesID       string
	Season, Number int
	Absolute       int
	Aired, Name    string
}

// Episode looks up one TheTVDB episode by id.
func (t TVDB) Episode(ctx context.Context, id string) (EpisodeFacts, error) {
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return EpisodeFacts{}, ErrNotFound
	}
	var resp struct {
		Data struct {
			SeriesID       int64   `json:"seriesId"`
			SeasonNumber   int     `json:"seasonNumber"`
			Number         int     `json:"number"`
			AbsoluteNumber *int    `json:"absoluteNumber"`
			Name           *string `json:"name"`
			Aired          *string `json:"aired"`
		} `json:"data"`
	}
	if err := t.get(ctx, "/episodes/"+id, &resp); err != nil {
		return EpisodeFacts{}, err
	}
	d := resp.Data
	if d.SeriesID < 1 {
		return EpisodeFacts{}, ErrNotFound
	}
	out := EpisodeFacts{SeriesID: strconv.FormatInt(d.SeriesID, 10), Season: d.SeasonNumber, Number: d.Number}
	if d.AbsoluteNumber != nil {
		out.Absolute = *d.AbsoluteNumber
	}
	if d.Name != nil {
		out.Name = strings.TrimSpace(*d.Name)
	}
	if d.Aired != nil {
		if _, err := time.Parse(time.DateOnly, *d.Aired); err == nil {
			out.Aired = *d.Aired
		}
	}
	return out, nil
}

func (t TVDB) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "series" && kind != "anime" {
		return Record{}, ErrUnsupported
	}
	if n, err := strconv.ParseInt(id, 10, 64); err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	var resp struct {
		Data struct {
			ID             int64   `json:"id"`
			Name           string  `json:"name"`
			Overview       *string `json:"overview"`
			Image          *string `json:"image"`
			FirstAired     *string `json:"firstAired"`
			AverageRuntime *int    `json:"averageRuntime"`
			OriginalLang   string  `json:"originalLanguage"`
			Status         struct {
				Name string `json:"name"`
			} `json:"status"`
			Genres []struct {
				Name string `json:"name"`
			} `json:"genres"`
			RemoteIDs []struct {
				ID         string `json:"id"`
				SourceName string `json:"sourceName"`
			} `json:"remoteIds"`
			Seasons []struct {
				ID     int64   `json:"id"`
				Number int     `json:"number"`
				Name   *string `json:"name"`
				Image  *string `json:"image"`
				Type   struct {
					Type string `json:"type"`
				} `json:"type"`
			} `json:"seasons"`
			Episodes []tvdbEpisode `json:"episodes"`
		} `json:"data"`
	}
	if err := t.get(ctx, "/series/"+id+"/extended?meta=episodes&short=true", &resp); err != nil {
		return Record{}, err
	}
	v := resp.Data
	if v.ID < 1 || v.Name == "" {
		return Record{}, ErrInvalidPayload
	}
	r := Record{Provider: "tvdb", Kind: kind, ProviderID: strconv.FormatInt(v.ID, 10), Title: v.Name, Unit: "episodes", ReleaseStatus: clean(v.Status.Name, 60),
		RuntimeMinutes: v.AverageRuntime, ExternalIDs: map[string]string{"tvdb": strconv.FormatInt(v.ID, 10)}, Details: map[string]any{}, Genres: []string{}}
	if v.Overview != nil {
		r.Overview = clean(*v.Overview, 4000)
	}
	if v.Image != nil {
		r.ImageURL = safeURL(*v.Image)
	}
	if v.FirstAired != nil {
		r.ReleaseDate = date(*v.FirstAired)
	}
	for _, g := range v.Genres {
		r.Genres = append(r.Genres, g.Name)
	}
	for _, remote := range v.RemoteIDs {
		switch strings.ToLower(remote.SourceName) {
		case "imdb":
			r.ExternalIDs["imdb"] = clean(remote.ID, 40)
		case "themoviedb.com", "tmdb":
			r.ExternalIDs["tmdb"] = clean(remote.ID, 40)
		}
	}
	// Episodes in the default order, grouped under their official seasons.
	bySeason := map[int][]tvdbEpisode{}
	for _, e := range v.Episodes {
		bySeason[e.SeasonNumber] = append(bySeason[e.SeasonNumber], e)
	}
	seasons := map[int]bool{}
	numbers := []int{}
	for _, s := range v.Seasons {
		if s.Type.Type != "official" || seasons[s.Number] {
			continue
		}
		seasons[s.Number] = true
		numbers = append(numbers, s.Number)
	}
	for n := range bySeason {
		if !seasons[n] {
			seasons[n] = true
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	total := 0
	for _, n := range numbers {
		episodes := bySeason[n]
		sort.Slice(episodes, func(i, j int) bool { return episodes[i].Number < episodes[j].Number })
		num := n
		season := Record{Provider: "tvdb", Kind: "season", ProviderID: r.ProviderID + ":" + strconv.Itoa(n), Title: "Season " + strconv.Itoa(n), SeasonNumber: &num,
			Total: intPtr(len(episodes)), ChildrenLoaded: true}
		if n == 0 {
			season.Title = "Specials"
		} else {
			total += len(episodes)
		}
		for _, e := range episodes {
			sn, en := e.SeasonNumber, e.Number
			ep := Record{Provider: "tvdb", Kind: "episode", ProviderID: strconv.FormatInt(e.ID, 10), Title: "Episode " + strconv.Itoa(en),
				SeasonNumber: &sn, EpisodeNumber: &en, RuntimeMinutes: e.Runtime}
			if e.Name != nil && *e.Name != "" {
				ep.Title = clean(*e.Name, 500)
			}
			if e.Overview != nil {
				ep.Overview = clean(*e.Overview, 4000)
			}
			if e.Aired != nil {
				ep.ReleaseDate = date(*e.Aired)
			}
			if e.Image != nil {
				ep.ImageURL = safeURL(*e.Image)
			}
			season.Children = append(season.Children, ep)
		}
		r.Children = append(r.Children, season)
	}
	r.ChildrenLoaded = true
	r.Total = intPtr(total)
	return r, nil
}
