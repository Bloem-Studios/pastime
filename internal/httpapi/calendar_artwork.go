package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

type calendarTitleIdentity struct {
	Kind   string `json:"kind"`
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}
type calendarTitle struct {
	Backdrop                       string
	ID                             int64
	Kind, Image                    string
	IDs                            map[string]string
	Entry                          *int64
	Followed, Favourite, Watchlist bool
	LocalFavourite, LocalWatchlist *bool
}

func calendarRootIdentity(event bloem.CalendarEvent) (string, map[string]string) {
	if event.Kind == "episode" {
		return "series", event.SeriesExternalIDs
	}
	if event.Kind == "ebook" {
		return "book", bookArtworkIDs(event.ExternalIDs)
	}
	if event.Kind == "audiobook" {
		return event.Kind, bookArtworkIDs(event.ExternalIDs)
	}
	if event.Kind == "movie" || event.Kind == "series" {
		return event.Kind, event.ExternalIDs
	}
	return "", nil
}

// Only shared provider metadata is enriched. Viewing a source never enrolls a
// title into a person's library, and source release IDs remain source IDs.
func (s *Server) enrichCalendarTitles(ctx context.Context, a identity.Actor, out []calendarRelease, source []bloem.CalendarEvent) error {
	identities := make([]calendarTitleIdentity, 0)
	seen := map[calendarTitleIdentity]bool{}
	events := map[string]bloem.CalendarEvent{}
	for _, event := range source {
		events[event.InstanceID+"\x00"+event.EventID] = event
		kind, ids := calendarRootIdentity(event)
		if kind == "" {
			continue
		}
		for _, scheme := range artworkIdentitySchemes(kind) {
			if ids[scheme] == "" {
				continue
			}
			key := calendarTitleIdentity{kind, scheme, ids[scheme]}
			if !seen[key] {
				identities = append(identities, key)
				seen[key] = true
			}
		}
	}
	if len(identities) == 0 {
		return nil
	}
	raw, err := json.Marshal(identities)
	if err != nil {
		return err
	}
	rows, err := s.library.Pool.Query(ctx, `SELECT DISTINCT c.id,c.kind,c.image_url,c.backdrop_url,c.external_ids,c.provider,c.provider_id,e.id,COALESCE(e.status IN ('planned','in_progress','paused'),false),
 EXISTS(SELECT 1 FROM profile_catalog_memberships m WHERE m.profile_id=$2 AND m.catalog_item_id=c.id AND m.kind='favorite' AND m.present),
 EXISTS(SELECT 1 FROM profile_catalog_memberships m WHERE m.profile_id=$2 AND m.catalog_item_id=c.id AND m.kind='watchlist' AND m.present),
 (SELECT m.present FROM profile_catalog_memberships m WHERE m.profile_id=$2 AND m.catalog_item_id=c.id AND m.kind='favorite' AND m.source<>'bloem'),
 (SELECT m.present FROM profile_catalog_memberships m WHERE m.profile_id=$2 AND m.catalog_item_id=c.id AND m.kind='watchlist' AND m.source<>'bloem')
 FROM jsonb_to_recordset($1::jsonb) AS ref(kind text,scheme text,value text)
 JOIN catalog_items c ON c.parent_id IS NULL AND c.kind=ref.kind AND
 ((c.provider=ref.scheme AND c.provider_id=ref.value) OR c.external_ids @> jsonb_build_object(ref.scheme,ref.value) OR c.external_ids @> jsonb_build_object(ref.scheme,CASE WHEN ref.value ~ '^[0-9]{1,18}$' THEN to_jsonb(ref.value::bigint) ELSE to_jsonb(ref.value) END))
 LEFT JOIN library_entries e ON e.catalog_item_id=c.id AND e.profile_id=$2`, raw, a.TrackingID())
	if err != nil {
		return err
	}
	index := map[calendarTitleIdentity][]calendarTitle{}
	for rows.Next() {
		var title calendarTitle
		var rawIDs json.RawMessage
		var provider, providerID string
		if err = rows.Scan(&title.ID, &title.Kind, &title.Image, &title.Backdrop, &rawIDs, &provider, &providerID, &title.Entry, &title.Followed, &title.Favourite, &title.Watchlist, &title.LocalFavourite, &title.LocalWatchlist); err != nil {
			rows.Close()
			return err
		}
		title.IDs = externalCalendarIDs(rawIDs)
		if title.IDs[provider] != "" && title.IDs[provider] != providerID {
			continue
		}
		if title.IDs[provider] == "" {
			title.IDs[provider] = providerID
		}
		for _, scheme := range artworkIdentitySchemes(title.Kind) {
			if title.IDs[scheme] != "" {
				key := calendarTitleIdentity{title.Kind, scheme, title.IDs[scheme]}
				index[key] = append(index[key], title)
			}
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	requests := []catalog.CalendarTitleRequest{}
	for i := range out {
		r := &out[i]
		if r.SourceEventID == "" {
			continue
		}
		event, ok := events[r.SourceInstanceID+"\x00"+r.SourceEventID]
		if !ok {
			continue
		}
		kind, ids := calendarRootIdentity(event)
		if kind == "" {
			continue
		}
		candidates := map[int64]calendarTitle{}
		for _, scheme := range artworkIdentitySchemes(kind) {
			for _, title := range index[calendarTitleIdentity{kind, scheme, ids[scheme]}] {
				if artworkIDsMatch(kind, title.IDs, ids) {
					candidates[title.ID] = title
				}
			}
		}
		if len(candidates) == 1 {
			for _, title := range candidates {
				r.ParentCatalog = stringID(title.ID)
				r.ImageURL = title.Image
				r.BackdropURL = title.Backdrop
				r.Followed = r.Followed || title.Followed
				r.Favourite = r.Favourite || title.Favourite
				r.Watchlist = r.Watchlist || title.Watchlist
				// A local tombstone is a choice, not an absent imported flag.
				// Normal personal synchronization arbitrates later source edits.
				if title.LocalFavourite != nil {
					r.Favourite = *title.LocalFavourite
					r.selectionOverride = true
				}
				if title.LocalWatchlist != nil {
					r.Watchlist = *title.LocalWatchlist
					r.selectionOverride = true
				}
				if title.Entry != nil {
					r.EntryID = stringID(*title.Entry)
				}
			}
			continue
		}
		if len(candidates) > 1 || s.catalog == nil {
			continue
		}
		requests = append(requests, catalog.CalendarTitleRequest{Kind: kind, IDs: ids})
	}
	if s.catalog != nil {
		_, err = s.catalog.EnqueueCalendarTitles(ctx, requests, time.Now())
		return err
	}
	return nil
}

// Book identifiers have their own media namespaces. Never borrow a movie/series
// identity or choose a poster by a fuzzy title match.
func artworkIdentitySchemes(kind string) []string {
	if kind == "book" || kind == "audiobook" {
		return []string{"isbn", "asin"}
	}
	return []string{"tmdb", "tvdb", "imdb"}
}
func artworkIDsMatch(kind string, a, b map[string]string) bool {
	shared := false
	for _, key := range artworkIdentitySchemes(kind) {
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

func bookArtworkIDs(ids map[string]string) map[string]string {
	out := make(map[string]string, len(ids))
	for key, value := range ids {
		out[key] = value
	}
	out["isbn"] = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(ids["isbn"]), "-", ""), " ", ""))
	out["asin"] = strings.ToUpper(strings.TrimSpace(ids["asin"]))
	return out
}
