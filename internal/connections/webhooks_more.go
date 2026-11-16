package connections

import (
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
)

// ParseGeneric reads scrobbles from Kodi's webhook add-on and from any client that
// posts a small normalized document. Kodi reports start, stop and end; an end, or a stop
// past 80%, is a viewing. The normalized form says completed, or gives a position and
// duration.
//
//	{"media_type":"episode","show_tmdb_id":100,"season":1,"episode":2,"completed":true}
func ParseGeneric(body []byte, now time.Time) (Event, error) {
	m, err := decode(body)
	if err != nil {
		return Event{}, err
	}
	if event := strings.ToLower(str(field(m, "event"))); event != "" {
		return parseKodi(m, event, now)
	}
	if !genericPlayed(m) {
		return Event{}, Ignored("playback is not complete")
	}
	ref := catalog.Reference{Title: str(field(m, "title")), Year: num(field(m, "year"))}
	switch strings.ToLower(str(field(m, "media_type"))) {
	case "movie":
		ref.Kind, ref.TMDB, ref.IMDB, ref.TVDB = "movie", str(field(m, "tmdb_id")), str(field(m, "imdb_id")), str(field(m, "tvdb_id"))
	case "episode":
		ref.Kind, ref.IMDB, ref.TVDB = "episode", str(field(m, "imdb_id")), str(field(m, "tvdb_id"))
		ref.ShowTMDB, ref.ShowIMDB, ref.ShowTVDB = str(field(m, "show_tmdb_id")), str(field(m, "show_imdb_id")), str(field(m, "show_tvdb_id"))
		ref.ShowTitle, ref.Season, ref.Episode = str(field(m, "show_title")), num(field(m, "season")), num(field(m, "episode"))
	default:
		return Event{}, Ignored("media_type must be movie or episode")
	}
	ev := Event{Reference: ref, PlayedAt: now, Account: str(field(m, "username"))}
	if at, err := time.Parse(time.RFC3339, str(field(m, "watched_at"))); err == nil && !at.After(now) {
		ev.PlayedAt = at.UTC()
	}
	return ev, nil
}

func genericPlayed(m map[string]any) bool {
	if v := field(m, "completed"); v != nil {
		return truthy(v)
	}
	position := num(field(m, "position_seconds"))
	if position == nil {
		return false
	}
	if duration := num(field(m, "duration_seconds")); duration != nil && *duration > 0 {
		return *position >= *duration-30
	}
	return *position >= 15*60
}

func parseKodi(m map[string]any, event string, now time.Time) (Event, error) {
	switch event {
	case "end":
	case "stop":
		percent, _ := strconv.ParseFloat(str(field(object(field(m, "progress")), "percent")), 64)
		if percent < 80 {
			return Event{}, Ignored("stopped before 80%")
		}
	default:
		return Event{}, Ignored("event " + event + " is not a completed viewing")
	}
	ids, show := object(field(m, "uniqueIds")), object(field(m, "tvShowUniqueIds"))
	ref := catalog.Reference{Title: str(field(m, "title")), Year: num(field(m, "year"))}
	switch strings.ToLower(str(field(m, "mediaType"))) {
	case "movie":
		ref.Kind, ref.TMDB, ref.IMDB, ref.TVDB = "movie", str(ids["tmdb"]), str(ids["imdb"]), str(ids["tvdb"])
	case "episode":
		ref.Kind, ref.IMDB, ref.TVDB = "episode", str(ids["imdb"]), str(ids["tvdb"])
		ref.ShowTMDB, ref.ShowIMDB, ref.ShowTVDB = str(show["tmdb"]), str(show["imdb"]), str(show["tvdb"])
		ref.ShowTitle, ref.Season, ref.Episode = str(field(m, "tvShowTitle")), num(field(m, "season")), num(field(m, "episode"))
	default:
		return Event{}, Ignored("only movies and episodes are tracked from Kodi")
	}
	return Event{Reference: ref, PlayedAt: now}, nil
}

// ParseSeerr reads Overseerr and Jellyseerr notifications. A request adds the movie or
// show as planned. The flat keys of a custom template ({"media_tmdbid": ...}) and the
// default nested template are both accepted. Account is the requester, so the
// connection's username filter limits it to one person's requests.
func ParseSeerr(body []byte, now time.Time) (Event, error) {
	m, err := decode(body)
	if err != nil {
		return Event{}, err
	}
	if strings.Contains(strings.ToUpper(str(field(m, "notification_type"))), "TEST") {
		return Event{}, Ignored("test notification")
	}
	media, request := object(field(m, "media")), object(field(m, "request"))
	pick := func(flat string, nested map[string]any, key string) string {
		if v := str(field(m, flat)); v != "" {
			return v
		}
		return str(field(nested, key))
	}
	kind := strings.ToLower(pick("media_type", media, "media_type"))
	tmdb := pick("media_tmdbid", media, "tmdbId")
	status := strings.ToUpper(pick("media_status", media, "status"))
	if n, err := strconv.Atoi(tmdb); err != nil || n <= 0 {
		return Event{}, Ignored("no TMDB identifier")
	}
	switch status {
	case "", "UNKNOWN", "DELETED", "BLACKLISTED":
		return Event{}, Ignored("media status " + status + " is not a request")
	}
	ref := catalog.Reference{Title: str(field(m, "subject"))}
	switch kind {
	case "movie":
		ref.Kind, ref.TMDB = "movie", tmdb
	case "tv":
		ref.Kind, ref.ShowTMDB = "series", tmdb
	default:
		return Event{}, Ignored("only movies and shows are tracked from requests")
	}
	account := pick("requestedBy_username", request, "requestedBy_username")
	if account == "" {
		account = str(field(m, "notifyuser_username"))
	}
	return Event{Reference: ref, PlayedAt: now, Account: account, Plan: true}, nil
}
