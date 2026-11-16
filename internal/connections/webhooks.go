package connections

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
)

// Event is a completed viewing reported by a media server.
type Event struct {
	EventID   string // Stable delivery identity, when supplied by the sender.
	Reference catalog.Reference
	PlayedAt  time.Time
	// Account is the media server's user name, used for the optional username filter.
	Account string
	// Plan adds the title as planned instead of recording a viewing (media requests).
	Plan bool
}

// Parsed payloads distinguish "not a completed viewing" (ignored, not an error) from
// malformed input.
type Ignored string

func (i Ignored) Error() string { return string(i) }

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return ""
}

func num(v any) *int {
	s := str(v)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 1000000 {
		return nil
	}
	return &n
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

func object(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// field reads a key case-insensitively, since server templates vary in casing.
func field(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return nil
}

func decode(body []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return nil, Ignored("payload is not a JSON object")
	}
	return m, nil
}

// ParsePlex reads the JSON "payload" part of a Plex webhook. Only media.scrobble, which
// Plex sends once playback passes 90%, counts as a viewing.
func ParsePlex(body []byte, now time.Time) (Event, error) {
	m, err := decode(body)
	if err != nil {
		return Event{}, err
	}
	if str(m["event"]) != "media.scrobble" {
		return Event{}, Ignored("event " + str(m["event"]) + " is not a completed viewing")
	}
	meta := object(m["Metadata"])
	ev := Event{Account: str(object(m["Account"])["title"]), PlayedAt: now}
	ids := map[string]string{}
	if guids, ok := meta["Guid"].([]any); ok {
		for _, g := range guids {
			id := str(object(g)["id"])
			if scheme, value, ok := strings.Cut(id, "://"); ok {
				ids[scheme] = value
			}
		}
	}
	ref := catalog.Reference{Title: str(meta["title"]), Year: num(meta["year"])}
	switch str(meta["type"]) {
	case "movie":
		ref.Kind, ref.TMDB, ref.IMDB, ref.TVDB = "movie", ids["tmdb"], ids["imdb"], ids["tvdb"]
	case "episode":
		// Plex episode GUIDs identify the episode; TMDB episode ids cannot be looked up
		// alone, so IMDb/TVDB and then show title with season/episode numbers are used.
		ref.Kind, ref.IMDB, ref.TVDB = "episode", ids["imdb"], ids["tvdb"]
		ref.ShowTitle, ref.Season, ref.Episode = str(meta["grandparentTitle"]), num(meta["parentIndex"]), num(meta["index"])
	default:
		return Event{}, Ignored("only movies and episodes are tracked from Plex")
	}
	ev.Reference = ref
	return ev, nil
}

// ParseEmbyStyle reads Emby webhooks and Jellyfin webhook-plugin payloads. Jellyfin's
// plugin sends a flat template (NotificationType, Provider_tmdb, ...); Emby and
// Emby-compatible templates nest the item. Both are accepted.
func ParseEmbyStyle(body []byte, now time.Time) (Event, error) {
	m, err := decode(body)
	if err != nil {
		return Event{}, err
	}
	if field(m, "NotificationType") != nil {
		return parseJellyfinFlat(m, now)
	}
	event := strings.ToLower(str(field(m, "Event")))
	item := object(field(m, "Item"))
	complete := false
	switch event {
	case "playback.stop":
		complete = truthy(field(object(field(m, "PlaybackInfo")), "PlayedToCompletion"))
	case "item.markplayed", "markplayed":
		complete = true
	case "stop":
		complete = truthy(field(object(field(item, "UserData")), "Played"))
	}
	if !complete {
		return Event{}, Ignored("event " + event + " is not a completed viewing")
	}
	ev := Event{Account: str(field(object(field(m, "User")), "Name")), PlayedAt: now}
	providers := object(field(item, "ProviderIds"))
	ref := catalog.Reference{Title: str(field(item, "Name")), Year: num(field(item, "ProductionYear"))}
	switch strings.ToLower(str(field(item, "Type"))) {
	case "movie":
		ref.Kind, ref.TMDB, ref.IMDB, ref.TVDB = "movie", str(field(providers, "Tmdb")), str(field(providers, "Imdb")), str(field(providers, "Tvdb"))
	case "episode":
		ref.Kind, ref.IMDB, ref.TVDB = "episode", str(field(providers, "Imdb")), str(field(providers, "Tvdb"))
		ref.ShowTitle, ref.Season, ref.Episode = str(field(item, "SeriesName")), num(field(item, "ParentIndexNumber")), num(field(item, "IndexNumber"))
	default:
		return Event{}, Ignored("only movies and episodes are tracked")
	}
	ev.Reference = ref
	return ev, nil
}

func parseJellyfinFlat(m map[string]any, now time.Time) (Event, error) {
	kind := str(field(m, "NotificationType"))
	complete := (kind == "PlaybackStop" && truthy(field(m, "PlayedToCompletion"))) || (kind == "UserDataSaved" && truthy(field(m, "Played")))
	if !complete {
		return Event{}, Ignored("notification " + kind + " is not a completed viewing")
	}
	ev := Event{Account: str(field(m, "NotificationUsername")), PlayedAt: now}
	ref := catalog.Reference{Title: str(field(m, "Name")), Year: num(field(m, "Year"))}
	switch strings.ToLower(str(field(m, "ItemType"))) {
	case "movie":
		ref.Kind, ref.TMDB, ref.IMDB, ref.TVDB = "movie", str(field(m, "Provider_tmdb")), str(field(m, "Provider_imdb")), str(field(m, "Provider_tvdb"))
	case "episode":
		ref.Kind, ref.IMDB, ref.TVDB = "episode", str(field(m, "Provider_imdb")), str(field(m, "Provider_tvdb"))
		ref.ShowTitle, ref.Season, ref.Episode = str(field(m, "SeriesName")), num(field(m, "SeasonNumber")), num(field(m, "EpisodeNumber"))
	default:
		return Event{}, Ignored("only movies and episodes are tracked")
	}
	if at, err := time.Parse(time.RFC3339, str(field(m, "UtcTimestamp"))); err == nil && at.Before(now.Add(time.Minute)) && now.Sub(at) < 7*24*time.Hour {
		ev.PlayedAt = at
	}
	ev.Reference = ref
	return ev, nil
}

// DeliveryID accepts explicit event identifiers only. A media item ID is not a
// viewing identity; senders without an event ID keep the documented time heuristic.
func DeliveryID(body []byte) string {
	m, err := decode(body)
	if err != nil {
		return ""
	}
	for _, key := range []string{"event_id", "eventId", "notification_id", "notificationId"} {
		if id := str(field(m, key)); id != "" && len(id) <= 200 {
			return id
		}
	}
	return ""
}
