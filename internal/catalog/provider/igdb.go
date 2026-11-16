package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IGDB covers video games. It authenticates through a Twitch application credential.
type IGDB struct {
	Client       *Client
	ClientID     string
	ClientSecret string
	mu           *sync.Mutex
	token        *igdbToken
}

type igdbToken struct {
	value   string
	expires time.Time
}

func NewIGDB(c *Client, id, secret string) IGDB {
	return IGDB{Client: c, ClientID: id, ClientSecret: secret, mu: &sync.Mutex{}, token: &igdbToken{}}
}

func (IGDB) Name() string    { return "igdb" }
func (IGDB) Kinds() []string { return []string{"game"} }

func (g IGDB) accessToken(ctx context.Context) (string, error) {
	if g.ClientID == "" || g.ClientSecret == "" || g.mu == nil {
		return "", ErrNotConfigured
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token.value != "" && time.Now().Before(g.token.expires) {
		return g.token.value, nil
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	q := url.Values{"client_id": {g.ClientID}, "client_secret": {g.ClientSecret}, "grant_type": {"client_credentials"}}
	if err := g.Client.JSON(ctx, Request{Method: "POST", URL: "https://id.twitch.tv/oauth2/token?" + q.Encode()}, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", ErrNotConfigured
	}
	g.token.value = out.AccessToken
	g.token.expires = time.Now().Add(time.Duration(max(out.ExpiresIn-300, 60)) * time.Second)
	return g.token.value, nil
}

type igdbGame struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Summary      string  `json:"summary"`
	FirstRelease int64   `json:"first_release_date"`
	Rating       float64 `json:"total_rating"`
	Cover        struct {
		ImageID string `json:"image_id"`
	} `json:"cover"`
	Artworks []struct {
		ImageID string `json:"image_id"`
	} `json:"artworks"`
	Genres    []struct{ Name string } `json:"genres"`
	Platforms []struct{ Name string } `json:"platforms"`
	Companies []struct {
		Developer bool                  `json:"developer"`
		Company   struct{ Name string } `json:"company"`
	} `json:"involved_companies"`
}

const igdbFields = "fields name,summary,first_release_date,total_rating,cover.image_id,artworks.image_id,genres.name,platforms.name,involved_companies.developer,involved_companies.company.name;"

func (g IGDB) query(ctx context.Context, body string) ([]igdbGame, error) {
	token, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Client-Id": {g.ClientID}, "Authorization": {"Bearer " + token}}
	var out []igdbGame
	err = g.Client.JSON(ctx, Request{Method: "POST", URL: "https://api.igdb.com/v4/games", Header: header, RawBody: []byte(body), ContentType: "text/plain", Retries: 1}, &out)
	return out, err
}

// raw posts an Apicalypse query to any IGDB endpoint and returns the JSON body.
func (g IGDB) raw(ctx context.Context, endpoint, body string) ([]byte, error) {
	token, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	header := http.Header{"Client-Id": {g.ClientID}, "Authorization": {"Bearer " + token}}
	return g.Client.Do(ctx, Request{Method: "POST", URL: "https://api.igdb.com/v4/" + endpoint, Header: header, RawBody: []byte(body), ContentType: "text/plain", Retries: 1})
}

func (g IGDB) record(v igdbGame) Record {
	r := Record{Provider: "igdb", Kind: "game", ProviderID: strconv.FormatInt(v.ID, 10), Title: v.Name, Overview: v.Summary, Details: map[string]any{}, ExternalIDs: map[string]string{}}
	if v.Cover.ImageID != "" {
		r.ImageURL = "https://images.igdb.com/igdb/image/upload/t_cover_big/" + v.Cover.ImageID + ".jpg"
	}
	if len(v.Artworks) > 0 && v.Artworks[0].ImageID != "" {
		r.BackdropURL = "https://images.igdb.com/igdb/image/upload/t_1080p/" + v.Artworks[0].ImageID + ".jpg"
	}
	if v.FirstRelease > 0 {
		t := time.Unix(v.FirstRelease, 0).UTC()
		r.ReleaseDate = date(t.Format("2006-01-02"))
	}
	if v.Rating > 0 {
		r.Rating = floatPtr(v.Rating / 10)
	}
	for _, x := range v.Genres {
		r.Genres = append(r.Genres, x.Name)
	}
	for _, c := range v.Companies {
		if c.Developer {
			r.Credits = append(r.Credits, c.Company.Name)
		}
	}
	platforms := []string{}
	for _, p := range v.Platforms {
		platforms = append(platforms, p.Name)
	}
	if len(platforms) > 0 {
		r.Details["platforms"] = bounded(platforms)
	}
	return r
}

func escapeApicalypse(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ").Replace(s)
}

func (g IGDB) Search(ctx context.Context, kind, query string) ([]Record, error) {
	if kind != "game" {
		return nil, ErrUnsupported
	}
	games, err := g.query(ctx, `search "`+escapeApicalypse(query)+`"; `+igdbFields+` limit 20;`)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(games))
	for _, v := range games {
		out = append(out, g.record(v))
	}
	return out, nil
}

func (g IGDB) Fetch(ctx context.Context, kind, id string) (Record, error) {
	if kind != "game" {
		return Record{}, ErrUnsupported
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		return Record{}, ErrNotFound
	}
	games, err := g.query(ctx, igdbFields+" where id = "+strconv.FormatInt(n, 10)+"; limit 1;")
	if err != nil {
		return Record{}, err
	}
	if len(games) == 0 {
		return Record{}, ErrNotFound
	}
	r := g.record(games[0])
	// Time to beat is optional: a failed lookup leaves the game without estimates.
	if body, err := g.raw(ctx, "game_time_to_beats", "fields hastily,normally,completely,count; where game_id = "+strconv.FormatInt(n, 10)+"; limit 1;"); err == nil {
		var rows []struct {
			Hastily, Normally, Completely, Count int64
		}
		if json.Unmarshal(body, &rows) == nil && len(rows) > 0 {
			beat := map[string]any{}
			for key, seconds := range map[string]int64{"main": rows[0].Hastily, "main_extra": rows[0].Normally, "completionist": rows[0].Completely} {
				if seconds > 0 && seconds < 10000*3600 {
					beat[key] = (seconds + 59) / 60
				}
			}
			if len(beat) > 0 {
				beat["submissions"] = rows[0].Count
				r.Details["time_to_beat"] = beat
			}
		}
	}
	return r, nil
}

// FindExternal maps a Steam app id to the IGDB game id.
func (g IGDB) FindExternal(ctx context.Context, kind, scheme, value string) (string, error) {
	if kind != "game" || scheme != "steam" {
		return "", ErrNotFound
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 1 {
		return "", ErrNotFound
	}
	body, err := g.raw(ctx, "external_games", `fields game; where category = 1 & uid = "`+strconv.FormatInt(n, 10)+`"; limit 1;`)
	if err != nil {
		return "", err
	}
	var rows []struct {
		Game int64 `json:"game"`
	}
	if err = json.Unmarshal(body, &rows); err != nil || len(rows) == 0 || rows[0].Game == 0 {
		return "", ErrNotFound
	}
	return strconv.FormatInt(rows[0].Game, 10), nil
}
