package connections

import (
	"errors"
	"testing"
	"time"
)

var now = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

func TestParsePlex(t *testing.T) {
	ev, err := ParsePlex([]byte(`{"event":"media.scrobble","Account":{"title":"Synthetic Viewer"},"Metadata":{"type":"episode","grandparentTitle":"Synthetic Harbour","parentIndex":2,"index":5,"title":"Tide","Guid":[{"id":"imdb://tt0000099"},{"id":"tmdb://555"},{"id":"tvdb://777"}]}}`), now)
	if err != nil || ev.Account != "Synthetic Viewer" || ev.Reference.Kind != "episode" || ev.Reference.TVDB != "777" || ev.Reference.IMDB != "tt0000099" || *ev.Reference.Season != 2 || *ev.Reference.Episode != 5 || ev.Reference.ShowTitle != "Synthetic Harbour" {
		t.Fatalf("episode: %+v %v", ev, err)
	}
	movie, err := ParsePlex([]byte(`{"event":"media.scrobble","Metadata":{"type":"movie","title":"Synthetic Film","year":2001,"Guid":[{"id":"tmdb://42"}]}}`), now)
	if err != nil || movie.Reference.TMDB != "42" || *movie.Reference.Year != 2001 {
		t.Fatalf("movie: %+v %v", movie, err)
	}
	var ignored Ignored
	for _, body := range []string{`{"event":"media.play","Metadata":{"type":"movie"}}`, `{"event":"media.scrobble","Metadata":{"type":"track"}}`, `not json`} {
		if _, err := ParsePlex([]byte(body), now); !errors.As(err, &ignored) {
			t.Fatalf("%s: expected ignored, got %v", body, err)
		}
	}
}

func TestParseEmbyAndJellyfin(t *testing.T) {
	emby, err := ParseEmbyStyle([]byte(`{"Event":"playback.stop","User":{"Name":"viewer"},"PlaybackInfo":{"PlayedToCompletion":true},"Item":{"Type":"Movie","Name":"Synthetic Film","ProductionYear":1999,"ProviderIds":{"Tmdb":"42","Imdb":"tt0000042"}}}`), now)
	if err != nil || emby.Reference.Kind != "movie" || emby.Reference.TMDB != "42" || emby.Account != "viewer" {
		t.Fatalf("emby: %+v %v", emby, err)
	}
	if _, err = ParseEmbyStyle([]byte(`{"Event":"playback.stop","PlaybackInfo":{"PlayedToCompletion":false},"Item":{"Type":"Movie"}}`), now); err == nil {
		t.Fatal("partial playback accepted")
	}
	marked, err := ParseEmbyStyle([]byte(`{"Event":"item.markplayed","Item":{"Type":"Episode","SeriesName":"Synthetic Harbour","ParentIndexNumber":1,"IndexNumber":3,"ProviderIds":{"Tvdb":"888"}}}`), now)
	if err != nil || marked.Reference.Kind != "episode" || marked.Reference.TVDB != "888" || *marked.Reference.Episode != 3 {
		t.Fatalf("mark played: %+v %v", marked, err)
	}
	flat, err := ParseEmbyStyle([]byte(`{"NotificationType":"PlaybackStop","PlayedToCompletion":"True","ItemType":"Episode","SeriesName":"Synthetic Harbour","SeasonNumber":"1","EpisodeNumber":"2","Provider_tvdb":"999","NotificationUsername":"viewer","UtcTimestamp":"2026-09-21T11:30:00Z"}`), now)
	if err != nil || flat.Reference.TVDB != "999" || *flat.Reference.Season != 1 || !flat.PlayedAt.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("jellyfin flat: %+v %v", flat, err)
	}
	future, _ := ParseEmbyStyle([]byte(`{"NotificationType":"PlaybackStop","PlayedToCompletion":true,"ItemType":"Movie","Provider_tmdb":"1","UtcTimestamp":"2030-01-01T00:00:00Z"}`), now)
	if !future.PlayedAt.Equal(now) {
		t.Fatal("future timestamps must not be trusted")
	}
}
