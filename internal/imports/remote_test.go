package imports

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

// redirect sends every request to the fake server, keeping the path and query, so each
// adapter's real API addresses can be answered with synthetic data.
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = r.target.Scheme, r.target.Host
	clone.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func fakeServices(t *testing.T) (*httptest.Server, Store) {
	responses := map[string]string{
		// Jellyfin
		"/Users/u1/Items?IsPlayed": `{"Items":[{"Id":"m1","Name":"Synthetic Film","Type":"Movie","ProductionYear":2001,"ProviderIds":{"Tmdb":"42","Imdb":"tt42"},"UserData":{"LastPlayedDate":"2026-01-02T20:00:00Z"}},
			{"Id":"e1","Name":"Pilot","Type":"Episode","SeriesId":"s1","SeriesName":"Synthetic Harbour","ParentIndexNumber":1,"IndexNumber":1,"UserData":{"LastPlayedDate":"2026-02-01T20:00:00Z"}}],"TotalRecordCount":2}`,
		"/Users/u1/Items?Fields": `{"Items":[{"Id":"s1","ProviderIds":{"Tmdb":"100","Tvdb":"9"}}]}`,
		// Plex
		"/status/sessions/history/all":    `{"MediaContainer":{"Metadata":[{"ratingKey":"7","type":"movie","title":"Synthetic Film","viewedAt":1767384000},{"ratingKey":"8","grandparentRatingKey":"9","type":"episode","grandparentTitle":"Synthetic Harbour","parentIndex":1,"index":2,"viewedAt":1767470400}]}}`,
		"/library/metadata/7":             `{"MediaContainer":{"Metadata":[{"year":2001,"userRating":8.0,"Guid":[{"id":"tmdb://42"},{"id":"imdb://tt42"}]}]}}`,
		"/library/sections/watchlist/all": `{"MediaContainer":{"Metadata":[{"type":"movie","title":"Wanted Film","year":2030,"Guid":[{"id":"tmdb://77"}]}]}}`,
		"/library/metadata/9":             `{"MediaContainer":{"Metadata":[{"year":2020,"Guid":[{"id":"tvdb://9"}]}]}}`,
		// Kitsu
		"/api/edge/users": `{"data":[{"id":"55"}]}`,
		"/api/edge/library-entries": `{"data":[{"id":"le1","attributes":{"status":"completed","progress":12,"ratingTwenty":17,"finishedAt":"2025-05-01T00:00:00Z"},"relationships":{"anime":{"data":{"type":"anime","id":"a1"}},"manga":{}}}],
			"included":[{"type":"anime","id":"a1","attributes":{"canonicalTitle":"Synthetic Anime"}},{"type":"mappings","id":"x","attributes":{"externalSite":"myanimelist/anime","externalId":"321"},"relationships":{"item":{"data":{"type":"anime","id":"a1"}}}}],"links":{}}`,
		// MyAnimeList
		"/v2/users/sam/animelist": `{"data":[{"node":{"id":321,"title":"Synthetic Anime"},"list_status":{"status":"watching","score":8,"num_episodes_watched":5}}],"paging":{}}`,
		"/v2/users/sam/mangalist": `{"data":[],"paging":{}}`,
		// Last.fm
		"/2.0/": `{"recenttracks":{"track":[{"name":"Song A","artist":{"#text":"Band"},"album":{"#text":"First Album","mbid":"11111111-1111-4111-8111-111111111111"},"date":{"uts":"1767384000"}},
			{"name":"Now","artist":{"#text":"Band"},"album":{"#text":"First Album"},"@attr":{"nowplaying":"true"}}],"@attr":{"totalPages":"1"}}}`,
		// Steam
		"/IPlayerService/GetOwnedGames/v1/": `{"response":{"games":[{"appid":620,"name":"Synthetic Portal","playtime_forever":95},{"appid":1,"name":"Never Played","playtime_forever":0}]}}`,
		// Koito (ListenBrainz API on its own server)
		"/apis/listenbrainz/1/user/sam/listens": `{"payload":{"listens":[{"listened_at":1767384000,"track_metadata":{"artist_name":"Band","track_name":"Song A","release_name":"First Album","additional_info":{"recording_mbid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}}]}}`,
		// gPodder
		"/subscriptions/sam.json":  `["https://feeds.example/show.xml","not a feed"]`,
		"/api/2/episodes/sam.json": `{"actions":[{"podcast":"https://feeds.example/show.xml","episode":"https://cdn.example/ep1.mp3","guid":"ep-1","action":"play","timestamp":"2026-02-01T08:00:00","position":1790,"total":1800},{"podcast":"https://feeds.example/show.xml","episode":"https://cdn.example/ep2.mp3","action":"play","position":60,"total":1800}],"timestamp":1767900000}`,
		// Pocket Casts
		"/user/login":        `{"token":"pc-token"}`,
		"/user/podcast/list": `{"podcasts":[{"uuid":"p1","title":"Synthetic Signals","author":"Sam Host"}]}`,
		"/user/history":      `{"episodes":[{"uuid":"e1","title":"Second","podcastUuid":"p1","podcastTitle":"Synthetic Signals","playingStatus":3},{"uuid":"e2","title":"Half","podcastUuid":"p1","podcastTitle":"Synthetic Signals","playingStatus":2}]}`,
		// Stremio
		"/api/login":        `{"result":{"authKey":"ak"}}`,
		"/api/datastoreGet": `{"result":[{"_id":"tt42","name":"Synthetic Film","type":"movie","state":{"lastWatched":"2026-01-02T20:00:00.000Z","timesWatched":1}},{"_id":"tt100","name":"Synthetic Harbour","type":"series","state":{"lastWatched":"2026-02-01T20:00:00.000Z","video_id":"tt100:1:3"}},{"_id":"tt7","name":"Queued","type":"movie","state":{}}]}`,
		// PlayStation
		"/api/authz/v3/oauth/token":        `{"access_token":"psn-token"}`,
		"/api/gamelist/v2/users/me/titles": `{"titles":[{"name":"Synthetic Quest™","playDuration":"PT12H30M5S"}]}`,
		// Xbox
		"/api/v2/account":                `{"content":{"profileUsers":[{"id":"x1"}]}}`,
		"/api/v2/player/titleHistory/x1": `{"titles":[{"name":"Synthetic Racer","type":"Game"},{"name":"Streaming App","type":"App"}]}`,
		// Storyteller
		"/api/v2/books":              `{"books":[{"uuid":"b1","title":"Synthetic Atlas","authors":[{"name":"A. Writer"}]},{"uuid":"b2","title":"Unopened"},{"uuid":"b3","title":"Done Book","status":{"name":"Read"},"position":{}}]}`,
		"/api/v2/books/b1/positions": `{"locator":{"locations":{"totalProgression":0.4}}}`,
		// Radarr and Sonarr
		"/api/v3/movie":  `[{"title":"Wanted Film","year":2030,"tmdbId":77,"monitored":true},{"title":"Ignored","year":2030,"tmdbId":78,"monitored":false}]`,
		"/api/v3/series": `[{"title":"Wanted Show","tvdbId":5,"tmdbId":6,"monitored":true}]`,
		// Audiobookshelf
		"/api/me":        `{"mediaProgress":[{"libraryItemId":"li1","isFinished":true,"progress":1,"currentTime":36000,"finishedAt":1767384000000,"mediaItemType":"book"}]}`,
		"/api/items/li1": `{"media":{"numAudioFiles":3,"metadata":{"title":"Synthetic Audiobook","authors":[{"name":"An Author"}],"isbn":"9780000000002"}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/authz/v3/oauth/authorize" {
			if r.Header.Get("Cookie") != "npsso=good" {
				w.Header().Set("Location", "com.scee.psxandroid.scecompcall://redirect?error=login_required")
			} else {
				w.Header().Set("Location", "com.scee.psxandroid.scecompcall://redirect?code=v3.abc")
			}
			w.WriteHeader(302)
			return
		}
		if r.Header.Get("X-Emby-Token") == "bad" {
			w.WriteHeader(401)
			return
		}
		key := r.URL.Path
		if strings.HasPrefix(key, "/Users/u1/Items") {
			key += "?" + strings.SplitN(r.URL.RawQuery, "=", 2)[0]
		}
		if body, ok := responses[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	target, _ := url.Parse(srv.URL)
	return srv, Store{HTTP: &http.Client{Transport: redirect{target}}, Creds: Credentials{MALClientID: "m", LastfmKey: "l", SteamKey: "s"}}
}

func TestRemoteSourcesProduceMatchableRecords(t *testing.T) {
	srv, s := fakeServices(t)
	defer srv.Close()
	ctx := context.Background()
	fetch := func(kind string, cfg LinkConfig, state map[string]any) []Record {
		t.Helper()
		out, err := s.fetchRemote(ctx, kind, &cfg, state, true)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		sortRecords(out)
		return out
	}
	jf := fetch("jellyfin", LinkConfig{URL: "http://media.local", Token: "t", UserID: "u1"}, map[string]any{})
	if len(jf) != 2 || jf[0].Video.TMDB != "42" || jf[1].Video.ShowTMDB != "100" || *jf[1].Plays[0].Season != 1 || jf[1].Plays[0].ExternalID != "jellyfin:e1" {
		t.Fatalf("jellyfin %+v", jf)
	}
	state := map[string]any{}
	plex := fetch("plex", LinkConfig{URL: "http://media.local", Token: "t"}, state)
	if len(plex) != 3 || plex[0].Video.TMDB != "42" || *plex[0].Rating != 8 || plex[1].Video.ShowTVDB != "9" || plex[2].Status != "planned" || state["viewed_after"].(float64) != 1767470400 {
		t.Fatalf("plex %+v %v", plex, state)
	}
	kitsu := fetch("kitsu", LinkConfig{Username: "sam"}, nil)
	if len(kitsu) != 1 || kitsu[0].External.Value != "321" || *kitsu[0].Rating != 9 || kitsu[0].Status != "completed" || len(kitsu[0].Plays) != 1 {
		t.Fatalf("kitsu %+v", kitsu)
	}
	mal := fetch("mal", LinkConfig{Username: "sam"}, nil)
	if len(mal) != 1 || mal[0].External.Scheme != "mal" || *mal[0].Progress != 5 || mal[0].Status != "in_progress" {
		t.Fatalf("mal %+v", mal)
	}
	lstate := map[string]any{}
	lastfm := fetch("lastfm", LinkConfig{Username: "sam"}, lstate)
	if len(lastfm) != 1 || lastfm[0].External.Scheme != "release" || len(lastfm[0].Plays) != 1 || lstate["after"].(float64) != 1767384000 {
		t.Fatalf("lastfm %+v", lastfm)
	}
	steam := fetch("steam", LinkConfig{SteamID: "76561198000000000"}, nil)
	if len(steam) != 1 || steam[0].External.Value != "620" || *steam[0].Progress != 95 {
		t.Fatalf("steam %+v", steam)
	}
	koito := fetch("koito", LinkConfig{URL: "http://music.local:4110", Username: "sam"}, map[string]any{})
	if len(koito) != 1 || koito[0].Plays[0].ChildID != "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" || !strings.HasPrefix(koito[0].Plays[0].ExternalID, "koito:") {
		t.Fatalf("koito %+v", koito)
	}
	gstate := map[string]any{}
	gp := fetch("gpodder", LinkConfig{Username: "sam", Token: "pw"}, gstate)
	if len(gp) != 1 || gp[0].Direct.ID != "https://feeds.example/show.xml" || len(gp[0].Plays) != 1 || gp[0].Plays[0].ChildID != provider.RSSEpisodeID("https://feeds.example/show.xml", "ep-1") || gstate["since"].(float64) != 1767900000 {
		t.Fatalf("gpodder %+v %v", gp, gstate)
	}
	pc := fetch("pocketcasts", LinkConfig{Username: "sam@example.com", Token: "pw"}, nil)
	if len(pc) != 1 || pc[0].Title.Title != "Synthetic Signals" || len(pc[0].Plays) != 1 || pc[0].Plays[0].ChildTitle != "Second" {
		t.Fatalf("pocketcasts %+v", pc)
	}
	st := fetch("stremio", LinkConfig{Username: "sam@example.com", Token: "pw"}, nil)
	if len(st) != 3 || st[0].Status != "planned" || st[1].Status != "completed" || *st[2].Plays[0].Episode != 3 || st[2].Video.ShowIMDB != "tt100" {
		t.Fatalf("stremio %+v", st)
	}
	psn := fetch("psn", LinkConfig{Token: "good"}, nil)
	if len(psn) != 1 || psn[0].Title.Title != "Synthetic Quest" || *psn[0].Progress != 750 {
		t.Fatalf("psn %+v", psn)
	}
	if _, err := s.fetchRemote(ctx, "psn", &LinkConfig{Token: "expired"}, map[string]any{}, true); !errors.Is(err, ErrCredentials) {
		t.Fatal("expired npsso not reported", err)
	}
	xbox := fetch("xbox", LinkConfig{Token: "key"}, nil)
	if len(xbox) != 1 || xbox[0].Title.Title != "Synthetic Racer" {
		t.Fatalf("xbox %+v", xbox)
	}
	story := fetch("storyteller", LinkConfig{URL: "http://books.local", Token: "tok"}, nil)
	if len(story) != 2 || story[1].Book.Author != "A. Writer" || *story[1].Fraction != 0.4 || story[0].Status != "completed" {
		t.Fatalf("storyteller %+v", story)
	}
	radarr := fetch("radarr", LinkConfig{URL: "http://arr.local", Token: "k"}, nil)
	sonarr := fetch("sonarr", LinkConfig{URL: "http://arr.local", Token: "k"}, nil)
	if len(radarr) != 1 || radarr[0].Status != "planned" || len(sonarr) != 1 || sonarr[0].Video.ShowTVDB != "5" {
		t.Fatalf("arr %+v %+v", radarr, sonarr)
	}
	abs := fetch("audiobookshelf", LinkConfig{URL: "http://abs.local", Token: "k"}, nil)
	if len(abs) != 1 || abs[0].Kind != "audiobook" || abs[0].Status != "completed" || *abs[0].Progress != 600 || abs[0].Book.ISBN != "9780000000002" {
		t.Fatalf("audiobookshelf %+v", abs)
	}
	// Rejected credentials are reported, not retried.
	bad := LinkConfig{URL: "http://media.local", Token: "bad", UserID: "u1"}
	if _, err := s.fetchRemote(ctx, "jellyfin", &bad, map[string]any{}, true); !errors.Is(err, ErrCredentials) {
		t.Fatal("rejected credentials not reported", err)
	}
	// Services that need an application key are unavailable without one.
	if _, err := (Store{HTTP: s.HTTP}).fetchRemote(ctx, "lastfm", &LinkConfig{Username: "sam"}, map[string]any{}, true); !errors.Is(err, provider.ErrNotConfigured) {
		t.Fatal("missing key not reported", err)
	}
	// Without permission, a private address is refused before any request is made.
	guardedStore := Store{Creds: s.Creds}
	if _, err := guardedStore.fetchRemote(ctx, "radarr", &LinkConfig{URL: "http://127.0.0.1:1", Token: "k"}, map[string]any{}, false); err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatal("private address reached", err)
	}
}
