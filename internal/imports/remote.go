package imports

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

// Credentials are instance-wide API keys for services that require an application
// registration. Services without one are unavailable to link.
type Credentials struct {
	TraktClientID, TraktClientSecret string
	SimklClientID                    string
	MALClientID                      string
	LastfmKey                        string
	SteamKey                         string
	BGGToken                         string
}

// LinkConfig is the decrypted configuration of a linked service. Which fields apply
// depends on the kind.
type LinkConfig struct {
	Generation   int64  `json:"-"`
	URL          string `json:"url,omitempty"`
	Token        string `json:"token,omitempty"`
	Username     string `json:"username,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	SteamID      string `json:"steam_id,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// Push marks plays recorded here as played on a Jellyfin or Emby server.
	Push    bool      `json:"push,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
}

// ErrCredentials reports that a service rejected the stored credentials; the person must
// reconnect. It is not retried.
var ErrRateLimited = fmt.Errorf("%w: rate limited", provider.ErrUnavailable)

var ErrCredentials = fmt.Errorf("%w: the service rejected the saved credentials; reconnect it", ErrFormat)

// remoteLimit bounds one response body; large libraries are paged.
const remoteLimit = 32 << 20

func (s Store) httpClient(allowPrivate bool) *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return guarded(allowPrivate)
}

// getJSON fetches and decodes one response. 401 and 403 become ErrCredentials; other
// failures are retryable outages.
func getJSON(ctx context.Context, c *http.Client, method, target string, header http.Header, body io.Reader, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("%w: the service address is not valid", ErrFormat)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", "Pastime/0.3")
	resp, err := c.Do(req)
	if err != nil {
		if errors.Is(err, errPrivateNet) || strings.Contains(err.Error(), errPrivateNet.Error()) {
			return nil, fmt.Errorf("%w: this server does not allow connections to private network addresses", ErrFormat)
		}
		return nil, provider.ErrUnavailable
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, remoteLimit+1))
	if err != nil {
		return nil, provider.ErrUnavailable
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, ErrCredentials
	case resp.StatusCode == 404:
		return nil, provider.ErrNotFound
	case resp.StatusCode == 429:
		return resp.Header, ErrRateLimited
	case resp.StatusCode >= 500:
		return nil, provider.ErrUnavailable
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("%w: the service answered %d", ErrFormat, resp.StatusCode)
	case len(data) > remoteLimit:
		return nil, fmt.Errorf("%w: the service returned too much at once", ErrFormat)
	}
	if out != nil {
		if err = json.Unmarshal(data, out); err != nil {
			return nil, fmt.Errorf("%w: the service returned something unexpected", ErrFormat)
		}
	}
	return resp.Header, nil
}

func baseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("%w: enter the server's full address, such as https://media.example", ErrFormat)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// fetchRemote reads a linked service. state carries the sync position between runs and
// is returned updated.
func (s Store) fetchRemote(ctx context.Context, kind string, cfg *LinkConfig, state map[string]any, allowPrivate bool) ([]Record, error) {
	c := s.httpClient(allowPrivate)
	switch kind {
	case "trakt":
		return s.fetchTrakt(ctx, c, cfg, state)
	case "simkl":
		return s.fetchSimkl(ctx, c, cfg)
	case "mal":
		return s.fetchMAL(ctx, c, cfg)
	case "kitsu":
		return fetchKitsu(ctx, c, cfg)
	case "anilist":
		a, ok := s.Catalog.Providers["anilist"].(provider.AniList)
		if !ok {
			return nil, provider.ErrNotConfigured
		}
		entries, err := a.UserLists(ctx, cfg.Username)
		if err != nil {
			return nil, err
		}
		return fromAniList(entries), nil
	case "hardcover":
		return fetchHardcover(ctx, c, cfg)
	case "steam":
		return s.fetchSteam(ctx, c, cfg)
	case "bgg":
		return s.fetchBGG(ctx, c, cfg)
	case "jellyfin", "emby":
		return fetchJellyfin(ctx, c, kind, cfg)
	case "plex":
		return fetchPlex(ctx, c, cfg, state)
	case "audiobookshelf":
		return fetchAudiobookshelf(ctx, c, cfg)
	case "lastfm":
		return s.fetchLastfm(ctx, c, cfg, state)
	case "listenbrainz":
		return fetchListenBrainz(ctx, c, "https://api.listenbrainz.org/1", "listenbrainz", cfg, state)
	case "koito":
		// Koito serves the ListenBrainz API from its own address.
		base, err := baseURL(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("%w: enter the Koito server address", ErrFormat)
		}
		return fetchListenBrainz(ctx, c, base+"/apis/listenbrainz/1", "koito", cfg, state)
	case "mylar3":
		return fetchMylar(ctx, c, cfg)
	case "komga":
		return fetchKomga(ctx, c, cfg)
	case "kavita":
		return fetchKavita(ctx, c, cfg)
	case "kapowarr":
		return fetchKapowarr(ctx, c, cfg)
	case "radarr", "sonarr":
		return fetchArr(ctx, c, kind, cfg)
	case "gpodder":
		return fetchGPodder(ctx, c, cfg, state)
	case "pocketcasts":
		return fetchPocketCasts(ctx, c, cfg)
	case "stremio":
		return fetchStremio(ctx, c, cfg)
	case "psn":
		return fetchPSN(ctx, c, cfg)
	case "xbox":
		return fetchXbox(ctx, c, cfg)
	case "storyteller":
		return fetchStoryteller(ctx, c, cfg)
	}
	return nil, ErrFormat
}

// ---------------------------------------------------------------- Trakt

const traktAPI = "https://api.trakt.tv"

func (s Store) traktHeader(cfg *LinkConfig) http.Header {
	return http.Header{"Trakt-Api-Version": {"2"}, "Trakt-Api-Key": {s.Creds.TraktClientID}, "Authorization": {"Bearer " + cfg.Token}, "Content-Type": {"application/json"}}
}

// refreshTrakt renews an expired access token; the caller saves the new tokens.
func (s Store) refreshTrakt(ctx context.Context, c *http.Client, cfg *LinkConfig) error {
	if cfg.Expires.IsZero() || time.Until(cfg.Expires) > 24*time.Hour {
		return nil
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": cfg.RefreshToken, "client_id": s.Creds.TraktClientID, "client_secret": s.Creds.TraktClientSecret,
		"redirect_uri": "urn:ietf:wg:oauth:2.0:oob", "grant_type": "refresh_token"})
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		CreatedAt    int64  `json:"created_at"`
	}
	if _, err := getJSON(ctx, c, "POST", traktAPI+"/oauth/token", http.Header{"Content-Type": {"application/json"}}, strings.NewReader(string(body)), &tok); err != nil {
		return err
	}
	cfg.Token, cfg.RefreshToken, cfg.Expires = tok.AccessToken, tok.RefreshToken, time.Unix(tok.CreatedAt+tok.ExpiresIn, 0)
	return nil
}

func (s Store) fetchTrakt(ctx context.Context, c *http.Client, cfg *LinkConfig, state map[string]any) ([]Record, error) {
	if s.Creds.TraktClientID == "" {
		return nil, provider.ErrNotConfigured
	}
	if err := s.refreshTrakt(ctx, c, cfg); err != nil {
		return nil, err
	}
	var sections []traktSection
	// History is read from the last position onward; ratings and the watchlist are small.
	since, _ := state["history_after"].(string)
	newest := since
	for page := 1; page <= 100; page++ {
		target := fmt.Sprintf("%s/sync/history?limit=1000&page=%d", traktAPI, page)
		if since != "" {
			target += "&start_at=" + url.QueryEscape(since)
		}
		var entries []traktEntry
		header, err := getJSON(ctx, c, "GET", target, s.traktHeader(cfg), nil, &entries)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.WatchedAt > newest {
				newest = e.WatchedAt
			}
		}
		sections = append(sections, traktSection{"history", entries})
		if pages, _ := strconv.Atoi(header.Get("X-Pagination-Page-Count")); page >= pages {
			break
		}
	}
	for _, part := range []struct{ kind, path string }{{"ratings", "/sync/ratings/movies"}, {"ratings", "/sync/ratings/shows"}, {"watchlist", "/sync/watchlist/movies"}, {"watchlist", "/sync/watchlist/shows"}} {
		var entries []traktEntry
		if _, err := getJSON(ctx, c, "GET", traktAPI+part.path, s.traktHeader(cfg), nil, &entries); err != nil {
			return nil, err
		}
		sections = append(sections, traktSection{part.kind, entries})
	}
	if newest != "" {
		state["history_after"] = newest
	}
	return fromTrakt(sections), nil
}

// ---------------------------------------------------------------- Simkl

func (s Store) fetchSimkl(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if s.Creds.SimklClientID == "" {
		return nil, provider.ErrNotConfigured
	}
	type ids struct {
		IMDB string `json:"imdb"`
		TMDB any    `json:"tmdb"`
		TVDB any    `json:"tvdb"`
		MAL  any    `json:"mal"`
	}
	type work struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   ids    `json:"ids"`
	}
	type episode struct {
		Number    int    `json:"number"`
		WatchedAt string `json:"watched_at"`
	}
	type item struct {
		Status        string `json:"status"`
		LastWatchedAt string `json:"last_watched_at"`
		UserRating    *int   `json:"user_rating"`
		Movie         *work  `json:"movie"`
		Show          *work  `json:"show"`
		Seasons       []struct {
			Number   int       `json:"number"`
			Episodes []episode `json:"episodes"`
		} `json:"seasons"`
		Episodes []episode `json:"episodes"` // anime without seasons
	}
	var all struct {
		Movies []item `json:"movies"`
		Shows  []item `json:"shows"`
		Anime  []item `json:"anime"`
	}
	header := http.Header{"Simkl-Api-Key": {s.Creds.SimklClientID}, "Authorization": {"Bearer " + cfg.Token}}
	if _, err := getJSON(ctx, c, "GET", "https://api.simkl.com/sync/all-items/?extended=full&episode_watched_at=yes", header, nil, &all); err != nil {
		return nil, err
	}
	status := map[string]string{"watching": "in_progress", "plantowatch": "planned", "completed": "completed", "hold": "paused", "dropped": "dropped", "notinteresting": "dropped"}
	anyID := func(v any) string {
		switch t := v.(type) {
		case string:
			return t
		case float64:
			return strconv.FormatInt(int64(t), 10)
		}
		return ""
	}
	var out []Record
	for _, m := range all.Movies {
		if m.Movie == nil {
			continue
		}
		r := Record{Label: fmt.Sprintf("%s (%d)", m.Movie.Title, m.Movie.Year), Kind: "movie", Status: status[m.Status], Rating: m.UserRating,
			Video: &catalog.Reference{Kind: "movie", TMDB: anyID(m.Movie.IDs.TMDB), IMDB: m.Movie.IDs.IMDB, Title: m.Movie.Title, Year: year(strconv.Itoa(m.Movie.Year))}}
		if at, ok := parseDate(m.LastWatchedAt); ok && m.Status == "completed" {
			r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "simkl:movie:" + anyID(m.Movie.IDs.TMDB) + m.Movie.IDs.IMDB})
		}
		out = append(out, r)
	}
	for _, list := range [][]item{all.Shows, all.Anime} {
		for _, sh := range list {
			if sh.Show == nil {
				continue
			}
			r := Record{Label: sh.Show.Title, Kind: "series", Status: status[sh.Status], Rating: sh.UserRating}
			tmdb := anyID(sh.Show.IDs.TMDB)
			if mal := anyID(sh.Show.IDs.MAL); tmdb == "" && mal != "" {
				// Anime known only to MyAnimeList is tracked through AniList.
				r.Kind = "anime"
				r.External = &ExternalRef{Provider: "anilist", Kind: "anime", Scheme: "mal", Value: mal}
				for _, e := range sh.Episodes {
					if at, ok := parseDate(e.WatchedAt); ok {
						r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "simkl:mal:" + mal + ":" + strconv.Itoa(e.Number)})
					}
				}
				r.Progress = intp(len(sh.Episodes))
				out = append(out, r)
				continue
			}
			r.Video = &catalog.Reference{Kind: "series", ShowTMDB: tmdb, ShowIMDB: sh.Show.IDs.IMDB, ShowTVDB: anyID(sh.Show.IDs.TVDB), ShowTitle: sh.Show.Title}
			for _, season := range sh.Seasons {
				for _, e := range season.Episodes {
					if at, ok := parseDate(e.WatchedAt); ok {
						r.Plays = append(r.Plays, PlayRef{Season: intp(season.Number), Episode: intp(e.Number), At: at,
							ExternalID: fmt.Sprintf("simkl:%s:%d:%d", tmdb+sh.Show.IDs.IMDB, season.Number, e.Number)})
					}
				}
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- MyAnimeList

func (s Store) fetchMAL(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if s.Creds.MALClientID == "" {
		return nil, provider.ErrNotConfigured
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("%w: enter a MyAnimeList username", ErrFormat)
	}
	header := http.Header{"X-MAL-CLIENT-ID": {s.Creds.MALClientID}}
	status := map[string]string{"watching": "in_progress", "reading": "in_progress", "completed": "completed", "on_hold": "paused", "dropped": "dropped", "plan_to_watch": "planned", "plan_to_read": "planned"}
	var out []Record
	for _, kind := range []string{"anime", "manga"} {
		next := fmt.Sprintf("https://api.myanimelist.net/v2/users/%s/%slist?fields=list_status&limit=1000&nsfw=true", url.PathEscape(cfg.Username), kind)
		for pages := 0; next != "" && pages < 50; pages++ {
			var page struct {
				Data []struct {
					Node struct {
						ID    int64  `json:"id"`
						Title string `json:"title"`
					} `json:"node"`
					ListStatus struct {
						Status          string `json:"status"`
						Score           int    `json:"score"`
						EpisodesWatched int    `json:"num_episodes_watched"`
						ChaptersRead    int    `json:"num_chapters_read"`
						FinishDate      string `json:"finish_date"`
						IsRewatching    bool   `json:"is_rewatching"`
					} `json:"list_status"`
				} `json:"data"`
				Paging struct {
					Next string `json:"next"`
				} `json:"paging"`
			}
			if _, err := getJSON(ctx, c, "GET", next, header, nil, &page); err != nil {
				return nil, err
			}
			for _, d := range page.Data {
				id := strconv.FormatInt(d.Node.ID, 10)
				r := Record{Label: d.Node.Title, Kind: kind, Status: status[d.ListStatus.Status], External: &ExternalRef{Provider: "anilist", Kind: kind, Scheme: "mal", Value: id}}
				if d.ListStatus.IsRewatching {
					r.Status = "in_progress"
				}
				if d.ListStatus.Score > 0 {
					r.Rating = intp(d.ListStatus.Score)
				}
				progress := d.ListStatus.EpisodesWatched
				if kind == "manga" {
					progress = d.ListStatus.ChaptersRead
				}
				if progress > 0 {
					r.Progress = intp(progress)
				}
				if at, ok := parseDate(d.ListStatus.FinishDate); ok && r.Status == "completed" {
					r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "mal:" + kind + ":" + id})
				}
				out = append(out, r)
			}
			next = page.Paging.Next
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- Kitsu

func fetchKitsu(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("%w: enter a Kitsu username", ErrFormat)
	}
	header := http.Header{"Accept": {"application/vnd.api+json"}}
	var users struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if _, err := getJSON(ctx, c, "GET", "https://kitsu.io/api/edge/users?filter[slug]="+url.QueryEscape(strings.ToLower(cfg.Username)), header, nil, &users); err != nil {
		return nil, err
	}
	if len(users.Data) == 0 {
		if _, err := getJSON(ctx, c, "GET", "https://kitsu.io/api/edge/users?filter[name]="+url.QueryEscape(cfg.Username), header, nil, &users); err != nil {
			return nil, err
		}
	}
	if len(users.Data) == 0 {
		return nil, fmt.Errorf("%w: no Kitsu user with that name", ErrFormat)
	}
	status := map[string]string{"current": "in_progress", "planned": "planned", "completed": "completed", "on_hold": "paused", "dropped": "dropped"}
	type ref struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	var out []Record
	for _, kind := range []string{"anime", "manga"} {
		next := fmt.Sprintf("https://kitsu.io/api/edge/library-entries?filter[userId]=%s&filter[kind]=%s&include=%s,%s.mappings&page[limit]=500", users.Data[0].ID, kind, kind, kind)
		for pages := 0; next != "" && pages < 40; pages++ {
			var page struct {
				Data []struct {
					ID         string `json:"id"`
					Attributes struct {
						Status       string `json:"status"`
						Progress     int    `json:"progress"`
						RatingTwenty *int   `json:"ratingTwenty"`
						FinishedAt   string `json:"finishedAt"`
					} `json:"attributes"`
					Relationships struct {
						Anime struct{ Data *ref } `json:"anime"`
						Manga struct{ Data *ref } `json:"manga"`
					} `json:"relationships"`
				} `json:"data"`
				Included []struct {
					Type       string `json:"type"`
					ID         string `json:"id"`
					Attributes struct {
						CanonicalTitle string `json:"canonicalTitle"`
						ExternalSite   string `json:"externalSite"`
						ExternalID     string `json:"externalId"`
					} `json:"attributes"`
					Relationships struct {
						Item struct{ Data *ref } `json:"item"`
					} `json:"relationships"`
				} `json:"included"`
				Links struct {
					Next string `json:"next"`
				} `json:"links"`
			}
			if _, err := getJSON(ctx, c, "GET", next, header, nil, &page); err != nil {
				return nil, err
			}
			titles := map[string]string{}
			mal := map[string]string{}
			anilist := map[string]string{}
			for _, inc := range page.Included {
				switch inc.Type {
				case "anime", "manga":
					titles[inc.ID] = inc.Attributes.CanonicalTitle
				case "mappings":
					if inc.Relationships.Item.Data == nil {
						continue
					}
					switch inc.Attributes.ExternalSite {
					case "myanimelist/" + kind:
						mal[inc.Relationships.Item.Data.ID] = inc.Attributes.ExternalID
					case "anilist/" + kind:
						anilist[inc.Relationships.Item.Data.ID] = inc.Attributes.ExternalID
					}
				}
			}
			for _, d := range page.Data {
				media := d.Relationships.Anime.Data
				if kind == "manga" {
					media = d.Relationships.Manga.Data
				}
				if media == nil {
					continue
				}
				r := Record{Label: titles[media.ID], Kind: kind, Status: status[d.Attributes.Status]}
				switch {
				case anilist[media.ID] != "":
					r.Direct = &DirectRef{Provider: "anilist", Kind: kind, ID: anilist[media.ID]}
				case mal[media.ID] != "":
					r.External = &ExternalRef{Provider: "anilist", Kind: kind, Scheme: "mal", Value: mal[media.ID]}
				default:
					r.Title = &catalog.TitleReference{Kind: kind, Title: titles[media.ID]}
				}
				if d.Attributes.RatingTwenty != nil && *d.Attributes.RatingTwenty > 0 {
					r.Rating = intp((*d.Attributes.RatingTwenty + 1) / 2)
				}
				if d.Attributes.Progress > 0 {
					r.Progress = intp(d.Attributes.Progress)
				}
				if at, ok := parseDate(d.Attributes.FinishedAt); ok && r.Status == "completed" {
					r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "kitsu:" + d.ID})
				}
				out = append(out, r)
			}
			next = page.Links.Next
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- Hardcover

func fetchHardcover(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(cfg.Token), "Bearer "))
	if token == "" {
		return nil, fmt.Errorf("%w: paste your Hardcover API token", ErrFormat)
	}
	status := map[int]string{1: "planned", 2: "in_progress", 3: "completed", 4: "paused", 5: "dropped"}
	var out []Record
	for offset := 0; offset < 20000; offset += 500 {
		query := `query($offset:Int!){me{user_books(limit:500,offset:$offset,order_by:{id:asc}){id status_id rating last_read_date book{title release_year contributions{author{name}}} edition{isbn_13 isbn_10}}}}`
		body, _ := json.Marshal(map[string]any{"query": query, "variables": map[string]any{"offset": offset}})
		var resp struct {
			Data struct {
				Me []struct {
					UserBooks []struct {
						ID           int64    `json:"id"`
						StatusID     int      `json:"status_id"`
						Rating       *float64 `json:"rating"`
						LastReadDate string   `json:"last_read_date"`
						Book         struct {
							Title         string `json:"title"`
							ReleaseYear   *int   `json:"release_year"`
							Contributions []struct {
								Author struct {
									Name string `json:"name"`
								} `json:"author"`
							} `json:"contributions"`
						} `json:"book"`
						Edition *struct {
							ISBN13 string `json:"isbn_13"`
							ISBN10 string `json:"isbn_10"`
						} `json:"edition"`
					} `json:"user_books"`
				} `json:"me"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		header := http.Header{"Authorization": {"Bearer " + token}, "Content-Type": {"application/json"}}
		if _, err := getJSON(ctx, c, "POST", "https://api.hardcover.app/v1/graphql", header, strings.NewReader(string(body)), &resp); err != nil {
			return nil, err
		}
		if len(resp.Errors) > 0 {
			return nil, fmt.Errorf("%w: Hardcover said: %s", ErrFormat, resp.Errors[0].Message)
		}
		if len(resp.Data.Me) == 0 {
			return nil, ErrCredentials
		}
		books := resp.Data.Me[0].UserBooks
		for _, b := range books {
			author := ""
			if len(b.Book.Contributions) > 0 {
				author = b.Book.Contributions[0].Author.Name
			}
			ref := &catalog.BookReference{Title: b.Book.Title, Author: author, Kind: "book"}
			if b.Edition != nil {
				ref.ISBN = firstNonEmpty(b.Edition.ISBN13, b.Edition.ISBN10)
			}
			r := Record{Label: b.Book.Title, Kind: "book", Book: ref, Status: status[b.StatusID]}
			if b.Rating != nil && *b.Rating > 0 {
				r.Rating = intp(int(*b.Rating*2 + 0.5))
			}
			if at, ok := parseDate(b.LastReadDate); ok && r.Status == "completed" {
				r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "hardcover:" + strconv.FormatInt(b.ID, 10)})
			}
			out = append(out, r)
		}
		if len(books) < 500 {
			break
		}
	}
	return out, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ---------------------------------------------------------------- Steam

func (s Store) fetchSteam(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	key := firstNonEmpty(cfg.Token, s.Creds.SteamKey)
	if key == "" {
		return nil, provider.ErrNotConfigured
	}
	id := strings.TrimSpace(cfg.SteamID)
	if id == "" {
		return nil, fmt.Errorf("%w: enter your Steam ID or profile name", ErrFormat)
	}
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		var vanity struct {
			Response struct {
				SteamID string `json:"steamid"`
				Success int    `json:"success"`
			} `json:"response"`
		}
		if _, err = getJSON(ctx, c, "GET", "https://api.steampowered.com/ISteamUser/ResolveVanityURL/v1/?key="+url.QueryEscape(key)+"&vanityurl="+url.QueryEscape(id), nil, nil, &vanity); err != nil {
			return nil, err
		}
		if vanity.Response.Success != 1 {
			return nil, fmt.Errorf("%w: no Steam profile with that name", ErrFormat)
		}
		id = vanity.Response.SteamID
	}
	var owned struct {
		Response struct {
			Games []struct {
				AppID           int64  `json:"appid"`
				Name            string `json:"name"`
				PlaytimeForever int    `json:"playtime_forever"`
				LastPlayed      int64  `json:"rtime_last_played"`
			} `json:"games"`
		} `json:"response"`
	}
	if _, err := getJSON(ctx, c, "GET", "https://api.steampowered.com/IPlayerService/GetOwnedGames/v1/?include_appinfo=1&format=json&key="+url.QueryEscape(key)+"&steamid="+url.QueryEscape(id), nil, nil, &owned); err != nil {
		return nil, err
	}
	var out []Record
	for _, g := range owned.Response.Games {
		// Unplayed games in a Steam library are not something the person tracks.
		if g.PlaytimeForever <= 0 {
			continue
		}
		app := strconv.FormatInt(g.AppID, 10)
		out = append(out, Record{Label: g.Name, Kind: "game", Status: "in_progress", Progress: intp(g.PlaytimeForever),
			External: &ExternalRef{Provider: "igdb", Kind: "game", Scheme: "steam", Value: app},
			Title:    &catalog.TitleReference{Kind: "game", Title: g.Name}})
	}
	return out, nil
}

// ---------------------------------------------------------------- Jellyfin and Emby

func fetchJellyfin(ctx context.Context, c *http.Client, kind string, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if kind == "emby" {
		base += "/emby"
		if strings.HasSuffix(strings.TrimRight(cfg.URL, "/"), "/emby") {
			base = strings.TrimRight(cfg.URL, "/")
		}
	}
	header := http.Header{"X-Emby-Token": {cfg.Token}}
	userID := cfg.UserID
	if userID == "" {
		var users []struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
		}
		if _, err = getJSON(ctx, c, "GET", base+"/Users", header, nil, &users); err != nil {
			return nil, err
		}
		for _, u := range users {
			if strings.EqualFold(u.Name, cfg.Username) || (cfg.Username == "" && len(users) == 1) {
				userID = u.ID
			}
		}
		if userID == "" {
			return nil, fmt.Errorf("%w: no user named %q on that server", ErrFormat, cfg.Username)
		}
		cfg.UserID = userID
	}
	type item struct {
		ID             string            `json:"Id"`
		Name           string            `json:"Name"`
		Type           string            `json:"Type"`
		ProductionYear int               `json:"ProductionYear"`
		ProviderIDs    map[string]string `json:"ProviderIds"`
		SeriesID       string            `json:"SeriesId"`
		SeriesName     string            `json:"SeriesName"`
		Season         *int              `json:"ParentIndexNumber"`
		Episode        *int              `json:"IndexNumber"`
		UserData       struct {
			LastPlayedDate string `json:"LastPlayedDate"`
			PlayCount      int    `json:"PlayCount"`
		} `json:"UserData"`
	}
	var played []item
	for start := 0; start < 200000; start += 1000 {
		var page struct {
			Items []item `json:"Items"`
			Total int    `json:"TotalRecordCount"`
		}
		q := fmt.Sprintf("%s/Users/%s/Items?IsPlayed=true&Recursive=true&IncludeItemTypes=Movie,Episode&Fields=ProviderIds,UserData&StartIndex=%d&Limit=1000", base, url.PathEscape(userID), start)
		if _, err = getJSON(ctx, c, "GET", q, header, nil, &page); err != nil {
			return nil, err
		}
		played = append(played, page.Items...)
		if start+1000 >= page.Total || len(page.Items) == 0 {
			break
		}
	}
	// Episodes carry their show's identifiers only through the series record.
	series := map[string]map[string]string{}
	var ids []string
	for _, it := range played {
		if it.Type == "Episode" && it.SeriesID != "" && series[it.SeriesID] == nil {
			series[it.SeriesID] = map[string]string{}
			ids = append(ids, it.SeriesID)
		}
	}
	for i := 0; i < len(ids); i += 100 {
		end := min(i+100, len(ids))
		var page struct {
			Items []item `json:"Items"`
		}
		if _, err = getJSON(ctx, c, "GET", base+"/Users/"+url.PathEscape(userID)+"/Items?Fields=ProviderIds&Ids="+strings.Join(ids[i:end], ","), header, nil, &page); err != nil {
			return nil, err
		}
		for _, s := range page.Items {
			series[s.ID] = s.ProviderIDs
		}
	}
	pid := func(m map[string]string, k string) string {
		for key, v := range m {
			if strings.EqualFold(key, k) {
				return v
			}
		}
		return ""
	}
	var m merger
	for _, it := range played {
		at, ok := parseDate(it.UserData.LastPlayedDate)
		if !ok {
			at = time.Now().UTC()
		}
		switch it.Type {
		case "Movie":
			r := m.get("movie:"+it.ID, func() Record {
				return Record{Label: fmt.Sprintf("%s (%d)", it.Name, it.ProductionYear), Kind: "movie", Status: "completed",
					Video: &catalog.Reference{Kind: "movie", TMDB: pid(it.ProviderIDs, "Tmdb"), IMDB: pid(it.ProviderIDs, "Imdb"), Title: it.Name, Year: year(strconv.Itoa(it.ProductionYear))}}
			})
			r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: kind + ":" + it.ID})
		case "Episode":
			if it.Season == nil || it.Episode == nil {
				continue
			}
			ext := series[it.SeriesID]
			r := m.get("series:"+it.SeriesID, func() Record {
				return Record{Label: it.SeriesName, Kind: "series", Video: &catalog.Reference{Kind: "series", ShowTMDB: pid(ext, "Tmdb"), ShowIMDB: pid(ext, "Imdb"), ShowTVDB: pid(ext, "Tvdb"), ShowTitle: it.SeriesName}}
			})
			r.Plays = append(r.Plays, PlayRef{Season: it.Season, Episode: it.Episode, At: at, ExternalID: kind + ":" + it.ID})
		}
	}
	return m.records(), nil
}

// ---------------------------------------------------------------- Plex

func fetchPlex(ctx context.Context, c *http.Client, cfg *LinkConfig, state map[string]any) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"X-Plex-Token": {cfg.Token}}
	since, _ := state["viewed_after"].(float64)
	type entry struct {
		RatingKey            string `json:"ratingKey"`
		GrandparentRatingKey string `json:"grandparentRatingKey"`
		Type                 string `json:"type"`
		Title                string `json:"title"`
		GrandparentTitle     string `json:"grandparentTitle"`
		ParentIndex          *int   `json:"parentIndex"`
		Index                *int   `json:"index"`
		ViewedAt             int64  `json:"viewedAt"`
		AccountID            int64  `json:"accountID"`
	}
	var history []entry
	newest := int64(since)
	for start := 0; start < 100000; start += 500 {
		var page struct {
			MediaContainer struct {
				Metadata []entry `json:"Metadata"`
				Size     int     `json:"size"`
			} `json:"MediaContainer"`
		}
		q := fmt.Sprintf("%s/status/sessions/history/all?sort=viewedAt:desc&X-Plex-Container-Start=%d&X-Plex-Container-Size=500", base, start)
		if cfg.UserID != "" {
			q += "&accountID=" + url.QueryEscape(cfg.UserID)
		}
		if since > 0 {
			q += "&viewedAt>=" + strconv.FormatInt(int64(since), 10)
		}
		if _, err = getJSON(ctx, c, "GET", q, header, nil, &page); err != nil {
			return nil, err
		}
		history = append(history, page.MediaContainer.Metadata...)
		if len(page.MediaContainer.Metadata) < 500 {
			break
		}
	}
	guids := map[string]map[string]string{}
	lookup := func(key string) map[string]string {
		if g, ok := guids[key]; ok || key == "" {
			return g
		}
		var meta struct {
			MediaContainer struct {
				Metadata []struct {
					Guid []struct {
						ID string `json:"id"`
					} `json:"Guid"`
					Year       int     `json:"year"`
					UserRating float64 `json:"userRating"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		out := map[string]string{}
		if _, err := getJSON(ctx, c, "GET", base+"/library/metadata/"+url.PathEscape(key), header, nil, &meta); err == nil && len(meta.MediaContainer.Metadata) > 0 {
			for _, g := range meta.MediaContainer.Metadata[0].Guid {
				if scheme, value, ok := strings.Cut(g.ID, "://"); ok {
					out[scheme] = value
				}
			}
			out["year"] = strconv.Itoa(meta.MediaContainer.Metadata[0].Year)
			if r := meta.MediaContainer.Metadata[0].UserRating; r > 0 && r <= 10 {
				out["rating"] = strconv.Itoa(int(math.Round(r)))
			}
		}
		guids[key] = out
		return out
	}
	var m merger
	for _, h := range history {
		if h.ViewedAt > newest {
			newest = h.ViewedAt
		}
		at := time.Unix(h.ViewedAt, 0).UTC()
		ext := "plex:" + h.RatingKey + ":" + strconv.FormatInt(h.ViewedAt, 10)
		switch h.Type {
		case "movie":
			g := lookup(h.RatingKey)
			r := m.get("movie:"+h.RatingKey, func() Record {
				return Record{Label: h.Title, Kind: "movie", Status: "completed", Rating: plexRating(g), Video: &catalog.Reference{Kind: "movie", TMDB: g["tmdb"], IMDB: g["imdb"], Title: h.Title, Year: year(g["year"])}}
			})
			r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: ext})
		case "episode":
			if h.ParentIndex == nil || h.Index == nil {
				continue
			}
			g := lookup(h.GrandparentRatingKey)
			r := m.get("show:"+h.GrandparentRatingKey, func() Record {
				return Record{Label: h.GrandparentTitle, Kind: "series", Rating: plexRating(g), Video: &catalog.Reference{Kind: "series", ShowTMDB: g["tmdb"], ShowIMDB: g["imdb"], ShowTVDB: g["tvdb"], ShowTitle: h.GrandparentTitle}}
			})
			r.Plays = append(r.Plays, PlayRef{Season: h.ParentIndex, Episode: h.Index, At: at, ExternalID: ext})
		}
	}
	if newest > 0 {
		state["viewed_after"] = float64(newest)
	}
	// The account's watchlist lives on plex.tv. A token that only knows the server has
	// no watchlist, so failures here leave the history as it is.
	for _, w := range plexWatchlist(ctx, c, cfg.Token) {
		m.get(w.key, func() Record { return w.rec })
	}
	return m.records(), nil
}

func plexRating(g map[string]string) *int {
	if n, err := strconv.Atoi(g["rating"]); err == nil && n > 0 {
		return &n
	}
	return nil
}

type plexWanted struct {
	key string
	rec Record
}

const plexDiscover = "https://discover.provider.plex.tv"

func plexWatchlist(ctx context.Context, c *http.Client, token string) []plexWanted {
	var out []plexWanted
	for start := 0; start < 5000; start += 300 {
		var page struct {
			MediaContainer struct {
				Metadata []struct {
					Type  string `json:"type"`
					Title string `json:"title"`
					Year  int    `json:"year"`
					Guid  []struct {
						ID string `json:"id"`
					} `json:"Guid"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := fmt.Sprintf("%s/library/sections/watchlist/all?includeGuids=1&X-Plex-Container-Start=%d&X-Plex-Container-Size=300", plexDiscover, start)
		if _, err := getJSON(ctx, c, "GET", q, http.Header{"X-Plex-Token": {token}}, nil, &page); err != nil {
			return out
		}
		for _, item := range page.MediaContainer.Metadata {
			ids := map[string]string{}
			for _, g := range item.Guid {
				if scheme, value, ok := strings.Cut(g.ID, "://"); ok {
					ids[scheme] = value
				}
			}
			switch item.Type {
			case "movie":
				out = append(out, plexWanted{"movie:w:" + ids["tmdb"] + ids["imdb"] + item.Title, Record{Label: item.Title, Kind: "movie", Status: "planned",
					Video: &catalog.Reference{Kind: "movie", TMDB: ids["tmdb"], IMDB: ids["imdb"], Title: item.Title, Year: year(strconv.Itoa(item.Year))}}})
			case "show":
				out = append(out, plexWanted{"show:w:" + ids["tmdb"] + ids["tvdb"] + item.Title, Record{Label: item.Title, Kind: "series", Status: "planned",
					Video: &catalog.Reference{Kind: "series", ShowTMDB: ids["tmdb"], ShowIMDB: ids["imdb"], ShowTVDB: ids["tvdb"], ShowTitle: item.Title}}})
			}
		}
		if len(page.MediaContainer.Metadata) < 300 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------- Audiobookshelf

func fetchAudiobookshelf(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Authorization": {"Bearer " + cfg.Token}}
	var me struct {
		MediaProgress []struct {
			LibraryItemID string  `json:"libraryItemId"`
			EpisodeID     string  `json:"episodeId"`
			IsFinished    bool    `json:"isFinished"`
			Progress      float64 `json:"progress"`
			CurrentTime   float64 `json:"currentTime"`
			FinishedAt    int64   `json:"finishedAt"`
			MediaItemType string  `json:"mediaItemType"`
		} `json:"mediaProgress"`
	}
	if _, err = getJSON(ctx, c, "GET", base+"/api/me", header, nil, &me); err != nil {
		return nil, err
	}
	var out []Record
	for _, p := range me.MediaProgress {
		if p.MediaItemType != "book" || p.EpisodeID != "" || (p.Progress <= 0 && !p.IsFinished) {
			continue
		}
		var item struct {
			Media struct {
				NumAudioFiles int `json:"numAudioFiles"`
				Metadata      struct {
					Title   string `json:"title"`
					Authors []struct {
						Name string `json:"name"`
					} `json:"authors"`
					ISBN string `json:"isbn"`
				} `json:"metadata"`
			} `json:"media"`
		}
		if _, err = getJSON(ctx, c, "GET", base+"/api/items/"+url.PathEscape(p.LibraryItemID)+"?expanded=1", header, nil, &item); err != nil {
			if errors.Is(err, provider.ErrNotFound) {
				continue
			}
			return nil, err
		}
		kind := "book"
		if item.Media.NumAudioFiles > 0 {
			kind = "audiobook"
		}
		author := ""
		if len(item.Media.Metadata.Authors) > 0 {
			author = item.Media.Metadata.Authors[0].Name
		}
		r := Record{Label: item.Media.Metadata.Title, Kind: kind, Book: &catalog.BookReference{ISBN: item.Media.Metadata.ISBN, Title: item.Media.Metadata.Title, Author: author, Kind: kind}, Status: "in_progress"}
		if kind == "audiobook" && p.CurrentTime > 0 {
			r.Progress = intp(int(p.CurrentTime / 60))
		}
		if p.IsFinished {
			r.Status = "completed"
			at := time.Now().UTC()
			if p.FinishedAt > 0 {
				at = time.UnixMilli(p.FinishedAt).UTC()
			}
			r.Plays = append(r.Plays, PlayRef{At: at, ExternalID: "abs:" + p.LibraryItemID + ":" + strconv.FormatInt(p.FinishedAt, 10)})
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------------------------------------------------------------- Last.fm and ListenBrainz

// albumRecord groups listens by album: the album is the tracked title, each listen a play.
func albumRecord(m *merger, releaseGroup, release, artist, album string) *Record {
	key := "album:" + firstNonEmpty(releaseGroup, release, strings.ToLower(artist+"|"+album))
	return m.get(key, func() Record {
		r := Record{Label: album + " — " + artist, Kind: "music", Status: "in_progress", Title: &catalog.TitleReference{Kind: "music", Title: album, Creator: artist}}
		switch {
		case releaseGroup != "":
			r.Direct = &DirectRef{Provider: "musicbrainz", Kind: "music", ID: releaseGroup}
		case release != "":
			r.External = &ExternalRef{Provider: "musicbrainz", Kind: "music", Scheme: "release", Value: release}
		}
		return r
	})
}

func (s Store) fetchLastfm(ctx context.Context, c *http.Client, cfg *LinkConfig, state map[string]any) ([]Record, error) {
	if s.Creds.LastfmKey == "" {
		return nil, provider.ErrNotConfigured
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("%w: enter a Last.fm username", ErrFormat)
	}
	from, _ := state["after"].(float64)
	newest := int64(from)
	var m merger
	for page := 1; page <= 50; page++ {
		var resp struct {
			RecentTracks struct {
				Track []struct {
					Name   string `json:"name"`
					MBID   string `json:"mbid"` // the recording
					Artist struct {
						Text string `json:"#text"`
					} `json:"artist"`
					Album struct {
						Text string `json:"#text"`
						MBID string `json:"mbid"`
					} `json:"album"`
					Date *struct {
						UTS string `json:"uts"`
					} `json:"date"`
				} `json:"track"`
				Attr struct {
					TotalPages string `json:"totalPages"`
				} `json:"@attr"`
			} `json:"recenttracks"`
		}
		q := fmt.Sprintf("https://ws.audioscrobbler.com/2.0/?method=user.getrecenttracks&format=json&limit=200&page=%d&user=%s&api_key=%s", page, url.QueryEscape(cfg.Username), url.QueryEscape(s.Creds.LastfmKey))
		if from > 0 {
			q += "&from=" + strconv.FormatInt(int64(from)+1, 10)
		}
		if _, err := getJSON(ctx, c, "GET", q, nil, nil, &resp); err != nil {
			return nil, err
		}
		for _, t := range resp.RecentTracks.Track {
			if t.Date == nil || t.Album.Text == "" {
				continue // Now playing, or a single with no album.
			}
			uts, err := strconv.ParseInt(t.Date.UTS, 10, 64)
			if err != nil {
				continue
			}
			if uts > newest {
				newest = uts
			}
			r := albumRecord(&m, "", t.Album.MBID, t.Artist.Text, t.Album.Text)
			r.Plays = append(r.Plays, PlayRef{At: time.Unix(uts, 0).UTC(), ChildID: recordingID(t.MBID), ExternalID: "lastfm:" + t.Date.UTS + ":" + t.Name})
		}
		if total, _ := strconv.Atoi(resp.RecentTracks.Attr.TotalPages); page >= total {
			break
		}
	}
	if newest > 0 {
		state["after"] = float64(newest)
	}
	return m.records(), nil
}

func fetchListenBrainz(ctx context.Context, c *http.Client, api, source string, cfg *LinkConfig, state map[string]any) ([]Record, error) {
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("%w: enter a username", ErrFormat)
	}
	var header http.Header
	if cfg.Token != "" {
		header = http.Header{"Authorization": {"Token " + cfg.Token}}
	}
	after, _ := state["after"].(float64)
	newest := int64(after)
	maxTS := int64(0)
	var m merger
	for page := 0; page < 50; page++ {
		q := api + "/user/" + url.PathEscape(cfg.Username) + "/listens?count=1000"
		if maxTS > 0 {
			q += "&max_ts=" + strconv.FormatInt(maxTS, 10)
		}
		if after > 0 {
			q += "&min_ts=" + strconv.FormatInt(int64(after), 10)
		}
		var resp struct {
			Payload struct {
				Listens []listen `json:"listens"`
			} `json:"payload"`
		}
		if _, err := getJSON(ctx, c, "GET", q, header, nil, &resp); err != nil {
			return nil, err
		}
		if len(resp.Payload.Listens) == 0 {
			break
		}
		for _, l := range resp.Payload.Listens {
			if l.ListenedAt > newest {
				newest = l.ListenedAt
			}
			if maxTS == 0 || l.ListenedAt < maxTS {
				maxTS = l.ListenedAt
			}
			l.addTo(&m, source)
		}
	}
	if newest > 0 {
		state["after"] = float64(newest)
	}
	return m.records(), nil
}

// listen is one ListenBrainz listen, also the shape of submitted listens.
type listen struct {
	ListenedAt    int64 `json:"listened_at"`
	TrackMetadata struct {
		ArtistName     string `json:"artist_name"`
		TrackName      string `json:"track_name"`
		ReleaseName    string `json:"release_name"`
		AdditionalInfo struct {
			ReleaseMBID      string `json:"release_mbid"`
			ReleaseGroupMBID string `json:"release_group_mbid"`
			RecordingMBID    string `json:"recording_mbid"`
		} `json:"additional_info"`
		MBIDMapping *struct {
			ReleaseMBID   string `json:"release_mbid"`
			RecordingMBID string `json:"recording_mbid"`
		} `json:"mbid_mapping"`
	} `json:"track_metadata"`
}

func (l listen) addTo(m *merger, source string) {
	t := l.TrackMetadata
	release := t.AdditionalInfo.ReleaseMBID
	if release == "" && t.MBIDMapping != nil {
		release = t.MBIDMapping.ReleaseMBID
	}
	if t.ReleaseName == "" && release == "" && t.AdditionalInfo.ReleaseGroupMBID == "" {
		return
	}
	r := albumRecord(m, t.AdditionalInfo.ReleaseGroupMBID, release, t.ArtistName, firstNonEmpty(t.ReleaseName, t.TrackName))
	at := time.Unix(l.ListenedAt, 0).UTC()
	if l.ListenedAt == 0 {
		at = time.Now().UTC()
	}
	recording := t.AdditionalInfo.RecordingMBID
	if recording == "" && t.MBIDMapping != nil {
		recording = t.MBIDMapping.RecordingMBID
	}
	r.Plays = append(r.Plays, PlayRef{At: at, ChildID: recordingID(recording), ExternalID: source + ":" + strconv.FormatInt(l.ListenedAt, 10) + ":" + t.TrackName})
}

// recordingID keeps a MusicBrainz recording id, which names an album track, when valid.
func recordingID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 36 || strings.Count(s, "-") != 4 || strings.Trim(s, "0123456789abcdef-") != "" {
		return ""
	}
	return s
}

// ---------------------------------------------------------------- Radarr and Sonarr

func fetchArr(ctx context.Context, c *http.Client, kind string, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"X-Api-Key": {cfg.Token}}
	var out []Record
	if kind == "radarr" {
		var movies []struct {
			Title     string `json:"title"`
			Year      int    `json:"year"`
			TMDBID    int64  `json:"tmdbId"`
			IMDBID    string `json:"imdbId"`
			Monitored bool   `json:"monitored"`
		}
		if _, err = getJSON(ctx, c, "GET", base+"/api/v3/movie", header, nil, &movies); err != nil {
			return nil, err
		}
		for _, mv := range movies {
			if !mv.Monitored {
				continue
			}
			out = append(out, Record{Label: fmt.Sprintf("%s (%d)", mv.Title, mv.Year), Kind: "movie", Status: "planned",
				Video: &catalog.Reference{Kind: "movie", TMDB: idString(mv.TMDBID), IMDB: mv.IMDBID, Title: mv.Title, Year: year(strconv.Itoa(mv.Year))}})
		}
		return out, nil
	}
	var series []struct {
		Title     string `json:"title"`
		TVDBID    int64  `json:"tvdbId"`
		TMDBID    int64  `json:"tmdbId"`
		IMDBID    string `json:"imdbId"`
		Monitored bool   `json:"monitored"`
	}
	if _, err = getJSON(ctx, c, "GET", base+"/api/v3/series", header, nil, &series); err != nil {
		return nil, err
	}
	for _, sh := range series {
		if !sh.Monitored {
			continue
		}
		out = append(out, Record{Label: sh.Title, Kind: "series", Status: "planned",
			Video: &catalog.Reference{Kind: "series", ShowTMDB: idString(sh.TMDBID), ShowTVDB: idString(sh.TVDBID), ShowIMDB: sh.IMDBID, ShowTitle: sh.Title}})
	}
	return out, nil
}

// sortRecords keeps output stable for tests and logs.
func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool { return records[i].Label < records[j].Label })
}

// jellyfinBase is the API root of a Jellyfin server, or of Emby under /emby.
func jellyfinBase(kind, address string) (string, error) {
	base, err := baseURL(address)
	if err != nil {
		return "", err
	}
	if kind == "emby" && !strings.HasSuffix(strings.TrimRight(address, "/"), "/emby") {
		base += "/emby"
	}
	return base, nil
}

// pushJellyfin marks plays recorded here since the last push as played on the linked
// server. Plays that came from that server are skipped, and the first sync only sets
// the starting point so older history is not pushed.
func (s Store) pushJellyfin(ctx context.Context, c *http.Client, kind string, userID, linkID int64, cfg *LinkConfig, state map[string]any) error {
	if !cfg.Push || cfg.UserID == "" {
		return nil
	}
	since, _ := state["push_started"].(string)
	if since == "" {
		state["push_started"] = time.Now().UTC().Format(time.RFC3339Nano)
		return nil
	}
	base, err := jellyfinBase(kind, cfg.URL)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.played_at,c.kind,c.provider_id,c.season_number,c.episode_number,r.provider,r.provider_id
		FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id JOIN catalog_items r ON r.id=c.root_id
		WHERE p.profile_id=$1 AND p.created_at>$2::timestamptz AND p.source NOT IN ('jellyfin','emby') AND c.kind IN ('movie','episode') AND r.provider='tmdb'
		AND NOT EXISTS(SELECT 1 FROM playback_deliveries d WHERE d.link_id=$3 AND d.generation=$4 AND d.play_id=p.id AND (d.delivered_at IS NOT NULL OR d.next_attempt_at>clock_timestamp()))
        ORDER BY p.created_at,p.id LIMIT 500`, userID, since, linkID, cfg.Generation)
	if err != nil {
		return err
	}
	type pending struct {
		playID          int64
		at              time.Time
		kind, id        string
		season, episode *int
		showID          string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		var rootProvider string
		if err := rows.Scan(&p.playID, &p.at, &p.kind, &p.id, &p.season, &p.episode, &rootProvider, &p.showID); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	header := http.Header{"X-Emby-Token": {cfg.Token}}
	find := func(types, tmdb string) (string, error) {
		var out struct {
			Items []struct {
				ID string `json:"Id"`
			} `json:"Items"`
		}
		q := base + "/Users/" + url.PathEscape(cfg.UserID) + "/Items?Recursive=true&Limit=1&IncludeItemTypes=" + types + "&AnyProviderIdEquals=" + url.QueryEscape("Tmdb."+tmdb)
		if _, err := getJSON(ctx, c, "GET", q, header, nil, &out); err != nil {
			return "", err
		}
		if len(out.Items) == 0 {
			return "", nil
		}
		return out.Items[0].ID, nil
	}
	series := map[string]string{}
	episodes := map[string]map[[2]int]string{}
	var failures error
	for _, p := range todo {
		var attempt int
		err = s.Pool.QueryRow(ctx, `INSERT INTO playback_deliveries(link_id,generation,play_id,next_attempt_at)
   SELECT l.id,l.generation,$3,clock_timestamp()+interval '2 minutes' FROM service_links l JOIN tracking_profiles tracking ON tracking.id=l.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
   WHERE l.id=$1 AND l.generation=$2 AND l.profile_id=$4 AND u.active
   ON CONFLICT(link_id,generation,play_id) DO UPDATE SET attempts=playback_deliveries.attempts+1,next_attempt_at=EXCLUDED.next_attempt_at
    WHERE playback_deliveries.delivered_at IS NULL AND playback_deliveries.next_attempt_at<=clock_timestamp()
   RETURNING attempts`, linkID, cfg.Generation, p.playID, userID).Scan(&attempt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}

		target := ""
		switch p.kind {
		case "movie":
			target, err = find("Movie", p.id)
			if err != nil {
				failures = errors.Join(failures, err)
				continue
			}
		case "episode":
			if p.season == nil || p.episode == nil {
				break
			}
			sid, ok := series[p.showID]
			if !ok {
				sid, err = find("Series", p.showID)
				if err != nil {
					failures = errors.Join(failures, err)
					continue
				}
				series[p.showID] = sid
			}
			if sid == "" {
				break
			}
			if episodes[sid] == nil {
				episodes[sid] = map[[2]int]string{}
				var out struct {
					Items []struct {
						ID     string `json:"Id"`
						Season *int   `json:"ParentIndexNumber"`
						Number *int   `json:"IndexNumber"`
					} `json:"Items"`
				}
				if _, err := getJSON(ctx, c, "GET", base+"/Shows/"+url.PathEscape(sid)+"/Episodes?UserId="+url.QueryEscape(cfg.UserID), header, nil, &out); err != nil {
					failures = errors.Join(failures, err)
					delete(episodes, sid)
					continue
				} else {
					for _, e := range out.Items {
						if e.Season != nil && e.Number != nil {
							episodes[sid][[2]int{*e.Season, *e.Number}] = e.ID
						}
					}
				}
			}
			target = episodes[sid][[2]int{*p.season, *p.episode}]
		}
		if target == "" {
			failures = errors.Join(failures, provider.ErrNotFound)
			continue
		}
		q := base + "/Users/" + url.PathEscape(cfg.UserID) + "/PlayedItems/" + url.PathEscape(target) + "?DatePlayed=" + url.QueryEscape(p.at.UTC().Format("20060102150405"))
		if _, err := getJSON(ctx, c, "POST", q, header, nil, nil); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if _, err = s.Pool.Exec(ctx, "UPDATE playback_deliveries SET delivered_at=clock_timestamp() WHERE link_id=$1 AND generation=$2 AND play_id=$3 AND attempts=$4", linkID, cfg.Generation, p.playID, attempt); err != nil {
			return err
		}
	}
	return failures
}

// ---------------------------------------------------------------- gPodder

// fetchGPodder reads subscriptions and finished episodes from gpodder.net or a
// compatible server. Podcasts are read from their own feeds, so an episode action
// matches the feed item with the same GUID or enclosure address.
func fetchGPodder(ctx context.Context, c *http.Client, cfg *LinkConfig, state map[string]any) ([]Record, error) {
	address := strings.TrimSpace(cfg.URL)
	if address == "" {
		address = "https://gpodder.net"
	}
	base, err := baseURL(address)
	if err != nil {
		return nil, err
	}
	user := strings.TrimSpace(cfg.Username)
	if user == "" || cfg.Token == "" {
		return nil, fmt.Errorf("%w: enter your gPodder username and password", ErrFormat)
	}
	header := http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+cfg.Token))}}
	var feeds []string
	if _, err = getJSON(ctx, c, "GET", base+"/subscriptions/"+url.PathEscape(user)+".json", header, nil, &feeds); err != nil {
		return nil, err
	}
	since, _ := state["since"].(float64)
	var actions struct {
		Actions []struct {
			Podcast   string `json:"podcast"`
			Episode   string `json:"episode"`
			GUID      string `json:"guid"`
			Action    string `json:"action"`
			Timestamp string `json:"timestamp"`
			Position  int    `json:"position"`
			Total     int    `json:"total"`
		} `json:"actions"`
		Timestamp int64 `json:"timestamp"`
	}
	q := base + "/api/2/episodes/" + url.PathEscape(user) + ".json"
	if since > 0 {
		q += "?since=" + strconv.FormatInt(int64(since), 10)
	}
	if _, err = getJSON(ctx, c, "GET", q, header, nil, &actions); err != nil {
		return nil, err
	}
	var m merger
	show := func(feed string) *Record {
		return m.get("feed:"+feed, func() Record {
			return Record{Label: feed, Kind: "podcast", Status: "planned", Direct: &DirectRef{Provider: "rss", Kind: "podcast", ID: feed}}
		})
	}
	for _, feed := range feeds {
		if provider.FeedURL(feed) {
			show(feed)
		}
	}
	for _, a := range actions.Actions {
		// A play that reached the last minute of the episode finished it.
		if strings.ToLower(a.Action) != "play" || !provider.FeedURL(a.Podcast) || a.Total <= 0 || a.Position < a.Total-60 {
			continue
		}
		r := show(a.Podcast)
		r.Status = "in_progress"
		at, ok := parseDate(a.Timestamp)
		if !ok {
			at = time.Now().UTC()
		}
		r.Plays = append(r.Plays, PlayRef{At: at, ChildID: provider.RSSEpisodeID(r.Direct.ID, firstNonEmpty(a.GUID, a.Episode)), ExternalID: "gpodder:" + firstNonEmpty(a.GUID, a.Episode)})
	}
	if actions.Timestamp > 0 {
		state["since"] = float64(actions.Timestamp)
	}
	return m.records(), nil
}

// ---------------------------------------------------------------- Pocket Casts

const pocketCastsAPI = "https://api.pocketcasts.com"

// fetchPocketCasts reads subscribed podcasts and finished episodes. Pocket Casts shares
// no feed addresses, so shows match by title and author, and episodes by title.
func fetchPocketCasts(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if strings.TrimSpace(cfg.Username) == "" || cfg.Token == "" {
		return nil, fmt.Errorf("%w: enter your Pocket Casts email and password", ErrFormat)
	}
	body, _ := json.Marshal(map[string]string{"email": strings.TrimSpace(cfg.Username), "password": cfg.Token, "scope": "webplayer"})
	var login struct {
		Token string `json:"token"`
	}
	if _, err := getJSON(ctx, c, "POST", pocketCastsAPI+"/user/login", http.Header{"Content-Type": {"application/json"}}, strings.NewReader(string(body)), &login); err != nil {
		return nil, err
	}
	if login.Token == "" {
		return nil, ErrCredentials
	}
	header := http.Header{"Authorization": {"Bearer " + login.Token}, "Content-Type": {"application/json"}}
	var list struct {
		Podcasts []struct {
			UUID   string `json:"uuid"`
			Title  string `json:"title"`
			Author string `json:"author"`
		} `json:"podcasts"`
	}
	if _, err := getJSON(ctx, c, "POST", pocketCastsAPI+"/user/podcast/list", header, strings.NewReader(`{"v":1}`), &list); err != nil {
		return nil, err
	}
	var history struct {
		Episodes []struct {
			UUID          string `json:"uuid"`
			Title         string `json:"title"`
			PodcastUUID   string `json:"podcastUuid"`
			PodcastTitle  string `json:"podcastTitle"`
			Author        string `json:"author"`
			PlayingStatus int    `json:"playingStatus"`
		} `json:"episodes"`
	}
	if _, err := getJSON(ctx, c, "POST", pocketCastsAPI+"/user/history", header, strings.NewReader(`{}`), &history); err != nil {
		return nil, err
	}
	var m merger
	show := func(uuid, title, author string) *Record {
		return m.get("pc:"+uuid, func() Record {
			return Record{Label: title, Kind: "podcast", Status: "planned", Title: &catalog.TitleReference{Kind: "podcast", Title: title, Creator: author}}
		})
	}
	for _, p := range list.Podcasts {
		if p.UUID != "" && p.Title != "" {
			show(p.UUID, p.Title, p.Author)
		}
	}
	now := time.Now().UTC()
	for _, e := range history.Episodes {
		if e.PlayingStatus != 3 || e.PodcastUUID == "" || e.PodcastTitle == "" || e.Title == "" { // 3 is played
			continue
		}
		r := show(e.PodcastUUID, e.PodcastTitle, e.Author)
		r.Status = "in_progress"
		r.Plays = append(r.Plays, PlayRef{At: now, ChildTitle: e.Title, ExternalID: "pocketcasts:" + e.UUID})
	}
	return m.records(), nil
}

// ---------------------------------------------------------------- Stremio

const stremioAPI = "https://api.strem.io/api"

// fetchStremio reads the Stremio library: watched films, the last watched episode of
// each show, and everything else as planned.
func fetchStremio(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if strings.TrimSpace(cfg.Username) == "" || cfg.Token == "" {
		return nil, fmt.Errorf("%w: enter your Stremio email and password", ErrFormat)
	}
	header := http.Header{"Content-Type": {"application/json"}}
	body, _ := json.Marshal(map[string]any{"type": "Login", "email": strings.TrimSpace(cfg.Username), "password": cfg.Token, "facebook": false})
	var login struct {
		Result struct {
			AuthKey string `json:"authKey"`
		} `json:"result"`
	}
	if _, err := getJSON(ctx, c, "POST", stremioAPI+"/login", header, strings.NewReader(string(body)), &login); err != nil {
		return nil, err
	}
	if login.Result.AuthKey == "" {
		return nil, ErrCredentials
	}
	body, _ = json.Marshal(map[string]any{"authKey": login.Result.AuthKey, "collection": "libraryItem", "ids": []string{}, "all": true})
	var library struct {
		Result []struct {
			ID      string `json:"_id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Removed bool   `json:"removed"`
			Temp    bool   `json:"temp"`
			State   struct {
				LastWatched    string `json:"lastWatched"`
				TimesWatched   int    `json:"timesWatched"`
				FlaggedWatched int    `json:"flaggedWatched"`
				VideoID        string `json:"video_id"`
			} `json:"state"`
		} `json:"result"`
	}
	if _, err := getJSON(ctx, c, "POST", stremioAPI+"/datastoreGet", header, strings.NewReader(string(body)), &library); err != nil {
		return nil, err
	}
	var out []Record
	for _, item := range library.Result {
		imdb := imdbID.FindString(item.ID)
		if imdb == "" || item.Removed && item.State.TimesWatched == 0 {
			continue
		}
		at, ok := parseDate(item.State.LastWatched)
		if !ok {
			at = time.Now().UTC()
		}
		switch item.Type {
		case "movie":
			r := Record{Label: item.Name, Kind: "movie", Status: "planned", Video: &catalog.Reference{Kind: "movie", IMDB: imdb, Title: item.Name}}
			if item.State.TimesWatched > 0 || item.State.FlaggedWatched > 0 {
				r.Status = "completed"
				r.Plays = []PlayRef{{At: at, ExternalID: "stremio:" + imdb + ":" + item.State.LastWatched}}
			}
			out = append(out, r)
		case "series":
			r := Record{Label: item.Name, Kind: "series", Status: "planned", Video: &catalog.Reference{Kind: "series", ShowIMDB: imdb, ShowTitle: item.Name}}
			// video_id is "tt123:season:episode" for the last episode opened.
			if parts := strings.Split(item.State.VideoID, ":"); len(parts) == 3 && item.State.LastWatched != "" {
				season, err1 := strconv.Atoi(parts[1])
				episode, err2 := strconv.Atoi(parts[2])
				if err1 == nil && err2 == nil {
					r.Status = "in_progress"
					r.Plays = []PlayRef{{Season: &season, Episode: &episode, At: at, ExternalID: "stremio:" + item.State.VideoID}}
				}
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- PlayStation

// PlayStation Network sign-in exchanges the account's NPSSO cookie for a token using
// the public PlayStation App client, as community tools do.
const (
	psnAuth     = "https://ca.account.sony.com/api/authz/v3/oauth"
	psnClientID = "09515159-7237-4370-9b40-3806e67c0891"
	psnBasic    = "MDk1MTUxNTktNzIzNy00MzcwLTliNDAtMzgwNmU2N2MwODkxOnVjUGprYTV0bnRCMktxc1A="
	psnRedirect = "com.scee.psxandroid.scecompcall://redirect"
	psnGames    = "https://m.np.playstation.com/api/gamelist/v2/users/me/titles"
)

func psnToken(ctx context.Context, c *http.Client, npsso string) (string, error) {
	q := url.Values{"access_type": {"offline"}, "client_id": {psnClientID}, "redirect_uri": {psnRedirect}, "response_type": {"code"}, "scope": {"psn:mobile.v2.core psn:clientapp"}}
	req, err := http.NewRequestWithContext(ctx, "GET", psnAuth+"/authorize?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", "npsso="+npsso)
	req.Header.Set("User-Agent", "Pastime/0.3")
	noRedirect := *c
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", provider.ErrUnavailable
	}
	resp.Body.Close()
	location, _ := url.Parse(resp.Header.Get("Location"))
	code := ""
	if location != nil {
		code = location.Query().Get("code")
	}
	if code == "" {
		return "", ErrCredentials // An expired NPSSO redirects without a code.
	}
	form := url.Values{"code": {code}, "redirect_uri": {psnRedirect}, "grant_type": {"authorization_code"}, "token_format": {"jwt"}}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	header := http.Header{"Authorization": {"Basic " + psnBasic}, "Content-Type": {"application/x-www-form-urlencoded"}}
	if _, err = getJSON(ctx, c, "POST", psnAuth+"/token", header, strings.NewReader(form.Encode()), &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", ErrCredentials
	}
	return tok.AccessToken, nil
}

// isoMinutes reads an ISO 8601 duration such as PT12H3M4S as whole minutes.
func isoMinutes(s string) int {
	s = strings.TrimPrefix(strings.ToUpper(s), "P")
	days, rest, found := strings.Cut(s, "DT")
	total := 0
	if found {
		if d, err := strconv.Atoi(days); err == nil {
			total += d * 24 * 60
		}
	} else {
		rest = strings.TrimPrefix(s, "T")
	}
	n := 0
	for _, r := range rest {
		switch {
		case r >= '0' && r <= '9':
			n = n*10 + int(r-'0')
		case r == 'H':
			total, n = total+n*60, 0
		case r == 'M':
			total, n = total+n, 0
		case r == 'S', r == '.':
			n = 0
		}
	}
	return total
}

func fetchPSN(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("%w: paste your NPSSO token", ErrFormat)
	}
	token, err := psnToken(ctx, c, strings.TrimSpace(cfg.Token))
	if err != nil {
		return nil, err
	}
	var out []Record
	for offset := 0; offset < 5000; offset += 200 {
		var page struct {
			Titles []struct {
				Name         string `json:"name"`
				PlayDuration string `json:"playDuration"`
			} `json:"titles"`
			NextOffset *int `json:"nextOffset"`
		}
		q := fmt.Sprintf("%s?categories=ps4_game,ps5_native_game&limit=200&offset=%d", psnGames, offset)
		if _, err := getJSON(ctx, c, "GET", q, http.Header{"Authorization": {"Bearer " + token}}, nil, &page); err != nil {
			return nil, err
		}
		for _, t := range page.Titles {
			name := strings.TrimSpace(strings.NewReplacer("™", "", "®", "").Replace(t.Name))
			if name == "" {
				continue
			}
			r := Record{Label: name, Kind: "game", Status: "in_progress", Title: &catalog.TitleReference{Kind: "game", Title: name}}
			if m := isoMinutes(t.PlayDuration); m > 0 {
				r.Progress = &m
			}
			out = append(out, r)
		}
		if page.NextOffset == nil || len(page.Titles) == 0 {
			break
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- Xbox (OpenXBL)

const openXBL = "https://xbl.io/api/v2"

// xblContent unwraps OpenXBL responses, which sometimes nest the payload in content.
func xblContent(raw json.RawMessage) json.RawMessage {
	var wrapped struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Content) > 0 {
		return wrapped.Content
	}
	return raw
}

func fetchXbox(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("%w: paste your OpenXBL API key", ErrFormat)
	}
	header := http.Header{"X-Authorization": {strings.TrimSpace(cfg.Token)}, "X-Contract": {"100"}}
	var raw json.RawMessage
	if _, err := getJSON(ctx, c, "GET", openXBL+"/account", header, nil, &raw); err != nil {
		return nil, err
	}
	var account struct {
		ProfileUsers []struct {
			ID string `json:"id"`
		} `json:"profileUsers"`
	}
	if json.Unmarshal(xblContent(raw), &account) != nil || len(account.ProfileUsers) == 0 || account.ProfileUsers[0].ID == "" {
		return nil, ErrCredentials
	}
	xuid := account.ProfileUsers[0].ID
	if _, err := getJSON(ctx, c, "GET", openXBL+"/player/titleHistory/"+url.PathEscape(xuid), header, nil, &raw); err != nil {
		return nil, err
	}
	var history struct {
		Titles []struct {
			Name         string `json:"name"`
			Type         string `json:"type"`
			TitleHistory struct {
				LastTimePlayed string `json:"lastTimePlayed"`
			} `json:"titleHistory"`
		} `json:"titles"`
	}
	if json.Unmarshal(xblContent(raw), &history) != nil {
		return nil, fmt.Errorf("%w: OpenXBL returned something unexpected", ErrFormat)
	}
	var out []Record
	for _, t := range history.Titles {
		name := strings.TrimSpace(strings.NewReplacer("™", "", "®", "").Replace(t.Name))
		if name == "" || !strings.EqualFold(t.Type, "game") {
			continue // Apps such as streaming services are not games.
		}
		out = append(out, Record{Label: name, Kind: "game", Status: "in_progress", Title: &catalog.TitleReference{Kind: "game", Title: name}})
	}
	return out, nil
}

// ---------------------------------------------------------------- Storyteller

// fetchStoryteller reads reading positions from a Storyteller server. Books match by
// title and author; the position becomes progress through the book.
func fetchStoryteller(ctx context.Context, c *http.Client, cfg *LinkConfig) ([]Record, error) {
	base, err := baseURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Authorization": {"Bearer " + cfg.Token}}
	var raw json.RawMessage
	if _, err = getJSON(ctx, c, "GET", base+"/api/v2/books", header, nil, &raw); err != nil {
		return nil, err
	}
	var books []map[string]any
	if json.Unmarshal(raw, &books) != nil {
		var wrapped struct {
			Books   []map[string]any `json:"books"`
			Results []map[string]any `json:"results"`
		}
		_ = json.Unmarshal(raw, &wrapped)
		books = append(wrapped.Books, wrapped.Results...)
	}
	progression := func(position any) (float64, bool) {
		loc, _ := position.(map[string]any)
		loc, _ = loc["locator"].(map[string]any)
		loc, _ = loc["locations"].(map[string]any)
		f, ok := loc["totalProgression"].(float64)
		return math.Max(0, math.Min(1, f)), ok
	}
	var out []Record
	for i, b := range books {
		if i >= 2000 {
			break
		}
		id := firstNonEmpty(fmt.Sprint(b["uuid"]), fmt.Sprint(b["id"]))
		title := strings.TrimSpace(fmt.Sprint(b["title"]))
		if id == "" || id == "<nil>" || title == "" || title == "<nil>" {
			continue
		}
		position, has := b["position"]
		if !has {
			var p json.RawMessage
			if _, err := getJSON(ctx, c, "GET", base+"/api/v2/books/"+url.PathEscape(id)+"/positions", header, nil, &p); err != nil {
				if errors.Is(err, provider.ErrNotFound) {
					continue // Never opened.
				}
				return nil, err
			}
			_ = json.Unmarshal(p, &position)
		}
		f, ok := progression(position)
		status := ""
		if s, _ := b["status"].(map[string]any); s != nil {
			status = strings.ToLower(fmt.Sprint(s["name"]))
		}
		finished := status == "read" || status == "finished" || status == "completed"
		if (!ok || f <= 0) && !finished {
			continue
		}
		author := ""
		switch a := b["authors"].(type) {
		case []any:
			for _, v := range a {
				if m, ok := v.(map[string]any); ok {
					author = firstNonEmpty(author, strings.TrimSpace(fmt.Sprint(m["name"])))
				} else {
					author = firstNonEmpty(author, strings.TrimSpace(fmt.Sprint(v)))
				}
			}
		}
		r := Record{Label: title, Kind: "book", Status: "in_progress", Book: &catalog.BookReference{Title: title, Author: author, Kind: "book"}}
		if finished {
			r.Status = "completed"
		} else {
			r.Fraction = &f
		}
		out = append(out, r)
	}
	return out, nil
}
