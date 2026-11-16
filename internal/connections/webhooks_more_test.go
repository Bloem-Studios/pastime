package connections

import (
	"errors"
	"testing"
	"time"
)

func TestParseGenericAndKodi(t *testing.T) {
	now := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	ev, err := ParseGeneric([]byte(`{"event":"stop","mediaType":"episode","tvShowTitle":"Synthetic Harbour","season":1,"episode":2,"uniqueIds":{"tvdb":"9001"},"tvShowUniqueIds":{"tmdb":"100"},"progress":{"percent":92.5}}`), now)
	if err != nil || ev.Reference.Kind != "episode" || ev.Reference.ShowTMDB != "100" || ev.Reference.TVDB != "9001" || *ev.Reference.Episode != 2 {
		t.Fatal(ev, err)
	}
	var ignored Ignored
	if _, err = ParseGeneric([]byte(`{"event":"stop","mediaType":"movie","progress":{"percent":40}}`), now); !errors.As(err, &ignored) {
		t.Fatal("early stop counted", err)
	}
	if _, err = ParseGeneric([]byte(`{"event":"start","mediaType":"movie"}`), now); !errors.As(err, &ignored) {
		t.Fatal("start counted", err)
	}
	ev, err = ParseGeneric([]byte(`{"media_type":"movie","tmdb_id":42,"position_seconds":5990,"duration_seconds":6000,"watched_at":"2026-02-01T20:00:00Z"}`), now)
	if err != nil || ev.Reference.TMDB != "42" || !ev.PlayedAt.Equal(time.Date(2026, 2, 1, 20, 0, 0, 0, time.UTC)) {
		t.Fatal(ev, err)
	}
	if _, err = ParseGeneric([]byte(`{"media_type":"movie","tmdb_id":42,"position_seconds":60,"duration_seconds":6000}`), now); !errors.As(err, &ignored) {
		t.Fatal("partial playback counted", err)
	}
}

func TestParseSeerr(t *testing.T) {
	now := time.Now()
	ev, err := ParseSeerr([]byte(`{"notification_type":"MEDIA_PENDING","subject":"Wanted Show","media":{"media_type":"tv","tmdbId":"6","status":"PENDING"},"request":{"requestedBy_username":"sam"}}`), now)
	if err != nil || !ev.Plan || ev.Reference.Kind != "series" || ev.Reference.ShowTMDB != "6" || ev.Account != "sam" {
		t.Fatal(ev, err)
	}
	ev, err = ParseSeerr([]byte(`{"media_type":"movie","media_tmdbid":77,"media_status":"AVAILABLE"}`), now)
	if err != nil || ev.Reference.TMDB != "77" {
		t.Fatal(ev, err)
	}
	var ignored Ignored
	for _, body := range []string{`{"notification_type":"TEST_NOTIFICATION"}`, `{"media_type":"movie","media_tmdbid":77,"media_status":"UNKNOWN"}`, `{"media_type":"music","media_tmdbid":77,"media_status":"PENDING"}`} {
		if _, err = ParseSeerr([]byte(body), now); !errors.As(err, &ignored) {
			t.Fatal(body, err)
		}
	}
}
