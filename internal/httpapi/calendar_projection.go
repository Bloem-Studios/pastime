package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// Release identifiers are absent for source-only events. A title identifier may
// refer to independently verified shared metadata; identifiers are never guessed.
type calendarRelease struct {
	selectionOverride bool
	library.Release
	CatalogID          *string              `json:"catalog_id,omitempty"`
	EntryID            *string              `json:"entry_id,omitempty"`
	ParentCatalog      *string              `json:"title_catalog_id,omitempty"`
	SourceInstanceID   string               `json:"source_instance_id,omitempty"`
	SourceEventID      string               `json:"source_event_id,omitempty"`
	SourceContentID    string               `json:"source_content_id,omitempty"`
	AuthorityExpiresAt *time.Time           `json:"source_authority_expires_at,omitempty"`
	AirAt              *time.Time           `json:"air_at,omitempty"`
	AirTimezone        string               `json:"air_timezone,omitempty"`
	Watched            bool                 `json:"watched"`
	Followed           bool                 `json:"followed"`
	Favourite          bool                 `json:"favourite"`
	Watchlist          bool                 `json:"watchlist"`
	Playable           bool                 `json:"playable"`
	Handoff            *bloem.HandoffTarget `json:"handoff"`
}
type calendarIdentity struct {
	IDs, SeriesIDs       map[string]string
	Favourite, Watchlist bool
}

func stringID(v int64) *string {
	if v <= 0 {
		return nil
	}
	s := strconv.FormatInt(v, 10)
	return &s
}
func matchIDs(a, b map[string]string) bool {
	shared := false
	for _, key := range []string{"tmdb", "tvdb", "imdb"} {
		if a[key] == "" || b[key] == "" {
			continue
		}
		if a[key] != b[key] {
			return false
		}
		shared = true
	}
	return shared
}
func conflictingIDs(a, b map[string]string) bool {
	for _, key := range []string{"tmdb", "tvdb", "imdb"} {
		if a[key] != "" && b[key] != "" && a[key] != b[key] {
			return true
		}
	}
	return false
}
func sameRelease(n calendarRelease, s bloem.CalendarEvent, id calendarIdentity) bool {
	if n.Kind != s.Kind || n.Date != s.Date || n.ReleaseType != s.ReleaseType {
		return false
	}
	if n.Kind == "episode" {
		if n.SeasonNumber == nil || n.EpisodeNumber == nil || s.SeasonNumber == nil || s.EpisodeNumber == nil || *n.SeasonNumber != int(*s.SeasonNumber) || *n.EpisodeNumber != int(*s.EpisodeNumber) {
			return false
		}
		return !conflictingIDs(id.IDs, s.ExternalIDs) && !conflictingIDs(id.SeriesIDs, s.SeriesExternalIDs) && (matchIDs(id.SeriesIDs, s.SeriesExternalIDs) || matchIDs(id.IDs, s.ExternalIDs))
	}
	return matchIDs(id.IDs, s.ExternalIDs)
}
func sourceRelease(s bloem.CalendarEvent) calendarRelease {
	kind := s.Kind
	media := kind
	if media == "episode" {
		media = "series"
	}
	if media == "ebook" {
		media = "book"
	}
	title := s.Title
	if s.EpisodeTitle != "" {
		title = s.EpisodeTitle
	}
	r := calendarRelease{Release: library.Release{Date: s.Date, Kind: kind, Title: title, EntryTitle: s.Title, MediaType: media, ReleaseType: s.ReleaseType}, SourceInstanceID: s.InstanceID, SourceEventID: s.EventID, SourceContentID: s.ContentID, AuthorityExpiresAt: s.AuthorityExpiresAt, AirAt: s.AirAt, AirTimezone: s.AirTimezone, Watched: s.Watched, Followed: s.Followed, Favourite: s.Favourite, Watchlist: s.Watchlist, Playable: s.Playable}
	if s.SeasonNumber != nil {
		v := int(*s.SeasonNumber)
		r.SeasonNumber = &v
	}
	if s.EpisodeNumber != nil {
		v := int(*s.EpisodeNumber)
		r.EpisodeNumber = &v
	}
	return r
}

// Index reliable provider identities so merging stays bounded for large calendars.
func calendarMatchKeys(r calendarRelease, id calendarIdentity) []string {
	keys := []string{}
	coordinate := ""
	if r.Kind == "episode" && r.SeasonNumber != nil && r.EpisodeNumber != nil {
		coordinate = strconv.Itoa(*r.SeasonNumber) + ":" + strconv.Itoa(*r.EpisodeNumber)
	}
	for namespace, values := range map[string]map[string]string{"own": id.IDs, "series": id.SeriesIDs} {
		if namespace == "series" && (r.Kind != "episode" || coordinate == "") {
			continue
		}
		for _, provider := range []string{"tmdb", "tvdb", "imdb"} {
			if values[provider] == "" {
				continue
			}
			raw, _ := json.Marshal([]string{r.Kind, r.Date, r.ReleaseType, coordinate, namespace, provider, values[provider]})
			keys = append(keys, string(raw))
		}
	}
	return keys
}
func mergeCalendar(native []library.Release, source []bloem.CalendarEvent, ids map[int64]calendarIdentity, filter string) []calendarRelease {
	out := make([]calendarRelease, 0, len(native)+len(source))
	identities := []calendarIdentity{}
	index := map[string][]int{}
	addIndex := func(i int) {
		for _, key := range calendarMatchKeys(out[i], identities[i]) {
			index[key] = append(index[key], i)
		}
	}
	for _, n := range native {
		id := ids[n.CatalogID]
		// The native calendar says why each title is listed: a following library entry,
		// a favourite or a watchlist membership.
		out = append(out, calendarRelease{Release: n, CatalogID: stringID(n.CatalogID), EntryID: stringID(n.EntryID), ParentCatalog: stringID(n.ParentCatalog), Followed: n.Following, Favourite: n.Favourite || id.Favourite, Watchlist: n.Watchlist || id.Watchlist})
		identities = append(identities, id)
		addIndex(len(out) - 1)
	}
	for _, event := range source {
		projection := sourceRelease(event)
		id := calendarIdentity{IDs: event.ExternalIDs, SeriesIDs: event.SeriesExternalIDs}
		candidates := map[int]bool{}
		for _, key := range calendarMatchKeys(projection, id) {
			for _, i := range index[key] {
				candidates[i] = true
			}
		}
		selected := -1
		for i := range candidates {
			if (selected < 0 || i < selected) && sameRelease(out[i], event, identities[i]) {
				selected = i
			}
		}
		if selected < 0 {
			out = append(out, projection)
			identities = append(identities, id)
			addIndex(len(out) - 1)
			continue
		}
		r := &out[selected]
		if r.SourceEventID == "" {
			r.SourceInstanceID = event.InstanceID
			r.SourceEventID = event.EventID
			r.SourceContentID = event.ContentID
			r.AuthorityExpiresAt = event.AuthorityExpiresAt
			r.AirAt = event.AirAt
			r.AirTimezone = event.AirTimezone
			r.Playable = event.Playable
		} else if event.AuthorityExpiresAt != nil && (r.AuthorityExpiresAt == nil || event.AuthorityExpiresAt.Before(*r.AuthorityExpiresAt)) {
			r.AuthorityExpiresAt = event.AuthorityExpiresAt
		}
		r.Watched = r.Watched || event.Watched
		r.Followed = r.Followed || event.Followed
		r.Favourite = r.Favourite || event.Favourite
		r.Watchlist = r.Watchlist || event.Watchlist
	}
	visible := out[:0]
	for _, r := range out {
		if filter == "following" && !r.Followed || filter == "favourites" && !r.Favourite || filter == "watchlist" && !r.Watchlist {
			continue
		}
		visible = append(visible, r)
	}
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].Date != visible[j].Date {
			return visible[i].Date < visible[j].Date
		}
		return visible[i].EntryTitle < visible[j].EntryTitle
	})
	return visible
}
func (s *Server) mergedCalendar(ctx context.Context, a identity.Actor, from, to time.Time, filter string) ([]calendarRelease, error) {
	native, err := s.library.Calendar(ctx, a, from, to)
	if err != nil {
		return nil, err
	}
	return s.mergeCalendarSources(ctx, a, from, to, filter, native)
}
func (s *Server) mergeCalendarSources(ctx context.Context, a identity.Actor, from, to time.Time, filter string, native []library.Release) ([]calendarRelease, error) {
	var err error
	if filter == "" {
		filter = "all"
	}
	switch filter {
	case "all", "following", "favourites", "watchlist":
	default:
		return nil, fault.Invalid("Invalid calendar filter.")
	}
	// Silo calendar rows are no longer shown: the plain Silo plugin sends no calendar,
	// and the native calendar covers favourites and watchlist itself. Stored rows from
	// the earlier plugin stay untouched (the earlier plugin still writes them if it is
	// ever re-enabled); the merge below is kept until those routes retire.
	source := []bloem.CalendarEvent{}
	ids := map[int64]calendarIdentity{}
	selected := make([]int64, 0, len(native))
	for _, r := range native {
		selected = append(selected, r.CatalogID)
	}
	// Provider identities only serve the source merge.
	if len(selected) > 0 && len(source) > 0 {
		rows, err := s.library.Pool.Query(ctx, `SELECT x.id,x.external_ids,top.external_ids,x.provider,x.provider_id,top.provider,top.provider_id FROM catalog_items x JOIN catalog_items top ON top.id=x.root_id WHERE x.id=ANY($1::bigint[])`, selected)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var raw, topRaw json.RawMessage
			var provider, providerID, topProvider, topProviderID string
			if err = rows.Scan(&id, &raw, &topRaw, &provider, &providerID, &topProvider, &topProviderID); err != nil {
				return nil, err
			}
			own, series := externalCalendarIDs(raw), externalCalendarIDs(topRaw)
			if own[provider] == "" {
				own[provider] = providerID
			}
			if series[topProvider] == "" {
				series[topProvider] = topProviderID
			}
			ids[id] = calendarIdentity{IDs: own, SeriesIDs: series}
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	settings, err := s.bloemStore().CalendarHandoffs(ctx, source)
	if err != nil {
		return nil, err
	}
	out := mergeCalendar(native, source, ids, "all")
	if err = s.enrichCalendarTitles(ctx, a, out, source); err != nil {
		return nil, err
	}
	visible := out[:0]
	for _, r := range out {
		if r.selectionOverride && !r.Followed && !r.Favourite && !r.Watchlist {
			continue
		}
		if filter == "following" && !r.Followed || filter == "favourites" && !r.Favourite || filter == "watchlist" && !r.Watchlist {
			continue
		}
		visible = append(visible, r)
	}
	out = visible
	for i := range out {
		if out[i].Playable {
			out[i].Handoff = bloem.CalendarHandoff(settings[out[i].SourceInstanceID], out[i].SourceContentID)
		}
	}
	return out, nil
}
func externalCalendarIDs(raw []byte) map[string]string {
	var values map[string]json.RawMessage
	_ = json.Unmarshal(raw, &values)
	out := map[string]string{}
	for key, v := range values {
		var text string
		if json.Unmarshal(v, &text) == nil {
			out[key] = text
		} else {
			var n json.Number
			if json.Unmarshal(v, &n) == nil {
				out[key] = n.String()
			}
		}
	}
	return out
}
func calendarICS(releases []calendarRelease, now time.Time) string {
	var b strings.Builder
	line := func(s string) {
		for len(s) > 75 {
			cut := 75
			for cut > 0 && s[cut]&0xc0 == 0x80 {
				cut--
			}
			b.WriteString(s[:cut] + "\r\n")
			s = " " + s[cut:]
		}
		b.WriteString(s + "\r\n")
	}
	esc := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`, "\r", "")
	for _, v := range []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//Bloem Studios//Pastime//EN", "CALSCALE:GREGORIAN", "X-WR-CALNAME:Releases"} {
		line(v)
	}
	for _, r := range releases {
		day, err := time.Parse("2006-01-02", r.Date)
		if err != nil {
			continue
		}
		uid := ""
		if r.CatalogID != nil {
			uid = "release-" + *r.CatalogID
			if r.ReleaseType != "" {
				uid += "-" + r.ReleaseType
			}
		} else {
			sum := sha256.Sum256([]byte(r.SourceInstanceID + "\x00" + r.SourceEventID))
			uid = "source-" + hex.EncodeToString(sum[:])
		}
		summary := r.EntryTitle
		child := r.CatalogID == nil || r.ParentCatalog == nil || *r.CatalogID != *r.ParentCatalog
		if r.Kind != "movie" && child {
			if r.SeasonNumber != nil && r.EpisodeNumber != nil {
				summary += " S" + strconv.Itoa(*r.SeasonNumber) + "E" + strconv.Itoa(*r.EpisodeNumber)
			}
			if r.Title != r.EntryTitle || r.CatalogID != nil {
				summary += " · " + r.Title
			}
		}
		if r.ReleaseType != "" {
			label := map[string]string{"theatrical": "in cinemas", "digital": "digital", "physical": "disc"}[r.ReleaseType]
			if label == "" {
				label = r.ReleaseType
			}
			summary += " (" + label + ")"
		}

		line("BEGIN:VEVENT")
		line("UID:" + uid + "@pastime")
		line("DTSTAMP:" + now.UTC().Format("20060102T150405Z"))
		if r.AirAt != nil {
			line("DTSTART:" + r.AirAt.UTC().Format("20060102T150405Z"))
		} else {
			line("DTSTART;VALUE=DATE:" + day.Format("20060102"))
			line("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102"))
		}
		line("SUMMARY:" + esc.Replace(summary))
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}
