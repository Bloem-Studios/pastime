package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// Query returns a provider page. An empty query requests browsing. Unsupported
// controls are rejected; a provider failure never silently repeats page one.
func (s *Service) Query(ctx context.Context, kind, chosen string, o provider.QueryOptions) (SearchResult, error) {
	out := SearchResult{Items: []Result{}, Source: "local", Availability: "not_configured"}
	if !trackable(kind) {
		return out, fault.Invalid("Choose a supported media type.")
	}
	o.Query = strings.Join(strings.Fields(o.Query), " ")
	if n := utf8.RuneCountInString(o.Query); n > 100 && !(kind == "podcast" && provider.FeedURL(o.Query)) && !(kind == "video" && n <= 200 && provider.YouTubeVideoID(o.Query) != "") {
		return out, fault.Invalid("Search for 1 to 100 characters.")
	}

	if o.Page < 0 || o.Page > 500 || o.Year < 0 || o.Year > 9999 || utf8.RuneCountInString(o.Genre) > 100 {
		return out, fault.Invalid("Choose valid catalog filters and a page from 1 to 500.")
	}
	name := s.Defaults[kind]
	if chosen != "" {
		name = chosen
	}
	out.Provider = name
	var choice *ProviderChoice
	for _, c := range s.Choices() {
		if c.Name == name {
			for _, k := range c.Kinds {
				if k == kind {
					copy := c
					choice = &copy
					break
				}
			}
		}
	}
	if chosen != "" && choice == nil {
		return out, fault.Invalid("Choose a metadata provider for this media type.")
	}
	caps := provider.QueryCapabilities{}
	if choice != nil {
		caps = choice.Search
		if o.Query == "" {
			caps = choice.Browse
		}
	}
	if (o.Page > 1 && !caps.Pagination) || (o.Year > 0 && !caps.Year) || (o.Genre != "" && !caps.Genre) {
		return out, fault.Invalid("This provider does not support those catalog filters.")
	}
	if o.Sort != "" {
		valid := false
		for _, sort := range caps.Sorts {
			valid = valid || sort == o.Sort
		}
		if !valid {
			return out, fault.Invalid("This provider does not support that sort order.")
		}
	}
	if name == "tmdb" && o.Genre != "" {
		n, err := strconv.Atoi(o.Genre)
		if err != nil || n < 1 {
			return out, fault.Invalid("Choose a numeric TMDB genre id.")
		}
	}
	if choice == nil || !choice.Configured {
		out.Degraded = true
		if o.Page > 1 || o.Year > 0 || o.Genre != "" || o.Sort != "" {
			return out, nil
		}
		if o.Query != "" {
			out, err := s.local(ctx, kind, o.Query)
			out.Provider = name
			out.Degraded = true
			out.Availability = "not_configured"
			return out, err
		}
		return out, nil
	}
	if o.Query == "" && !caps.Supported {
		out.Availability = "search_only"
		return out, nil
	}
	// Old single-batch adapters retain their URL/privacy validation and cache behavior.
	q, ok := s.Providers[name].(provider.Querier)
	if !ok {
		if o.Query != "" {
			return s.SearchProvider(ctx, kind, o.Query, name)
		}
		t, ok := s.Providers[name].(provider.Trender)
		if !ok {
			out.Availability = "search_only"
			return out, nil
		}
		records, err := cached(s, ctx, "catalog:browse:v1:"+name+":"+kind, 6*time.Hour, func(ctx context.Context) ([]provider.Record, error) { return t.Trending(ctx, kind) })
		if err != nil {
			out.Degraded = true
			out.Availability = "temporarily_unavailable"
			return out, nil
		}
		out.Source, out.Availability = "provider", "available"
		for _, r := range records {
			out.Items = append(out.Items, resultOf(r))
		}
		return out, s.annotateResults(ctx, out.Items)
	}
	if utf8.RuneCountInString(o.Query) > 100 {
		return out, fault.Invalid("Search for 1 to 100 characters.")
	}
	o.Page = max(o.Page, 1)
	raw, _ := json.Marshal(o)
	hash := sha256.Sum256(raw)
	key := "catalog:query:v1:" + name + ":" + kind + ":" + hex.EncodeToString(hash[:])
	page, err := cached(s, ctx, key, time.Hour, func(ctx context.Context) (provider.RecordPage, error) {
		found, err := q.Query(ctx, kind, o)
		if err != nil {
			return found, err
		}
		normalized := make([]provider.Record, 0, min(20, len(found.Items)))
		for _, r := range found.Items {
			if len(normalized) == 20 {
				break
			}
			if r, err := provider.Normalize(r); err == nil {
				r.Children = nil
				normalized = append(normalized, r)
			}
		}
		found.Items = normalized
		return found, nil
	})
	if err != nil {
		failure := err
		// Unfiltered first-page search can use local search. Filtered/paged requests return
		// an explicit outage and no mismatching/unfiltered fallback results.
		if o.Query != "" && o.Page == 1 && o.Year == 0 && o.Genre == "" && o.Sort == "" {
			out, err = s.local(ctx, kind, o.Query)
			if err != nil {
				return out, err
			}
		}
		out.Provider = name
		out.Degraded = true
		out.Availability = "temporarily_unavailable"
		if fault.Public(failure).Code == "provider_not_configured" || errors.Is(failure, provider.ErrNotConfigured) {
			out.Availability = "not_configured"
		}
		return out, nil
	}
	out.Source, out.Availability = "provider", "available"
	for _, r := range page.Items {
		out.Items = append(out.Items, resultOf(r))
	}
	if page.NextPage > o.Page && page.NextPage <= 500 {
		next := strconv.Itoa(page.NextPage)
		out.NextPage = &next
	}
	return out, s.annotateResults(ctx, out.Items)
}

func (s *Service) annotateResults(ctx context.Context, items []Result) error {
	refs := make([]*Result, len(items))
	for i := range items {
		refs[i] = &items[i]
	}
	return s.annotate(ctx, refs)
}

// Membership attaches only the caller's tracking identity/status. Call after public
// metadata caching; neither this response nor its private fields enter that cache.
func (s *Service) Membership(ctx context.Context, a identity.Actor, items []Result) error {
	if !a.Allows("library:read") {
		return fault.Forbidden
	}
	for i := range items {
		items[i].LibraryEntryID = nil
		items[i].LibraryStatus = nil
	}
	if len(items) == 0 {
		return nil
	}
	// The selected or bound profile's own entries only, never the household's.
	profile := a.TrackingID()
	if profile == 0 {
		return fault.Forbidden
	}
	providers, kinds, ids := []string{}, []string{}, []string{}
	for _, r := range items {
		providers = append(providers, r.Provider)
		kinds = append(kinds, r.MediaType)
		ids = append(ids, r.ProviderID)
	}
	rows, err := s.Pool.Query(ctx, `SELECT k.provider,k.kind,k.provider_id,e.id,e.status FROM unnest($1::text[],$2::text[],$3::text[]) AS k(provider,kind,provider_id)
 JOIN catalog_items c ON c.provider=k.provider AND c.kind=k.kind AND c.provider_id=k.provider_id
 JOIN library_entries e ON e.catalog_item_id=c.id AND e.profile_id=$4`, providers, kinds, ids, profile)
	if err != nil {
		return err
	}
	defer rows.Close()
	type membership struct {
		id     int64
		status string
	}
	known := map[[3]string]membership{}
	for rows.Next() {
		var p, k, pid, status string
		var id int64
		if err := rows.Scan(&p, &k, &pid, &id, &status); err != nil {
			return err
		}
		known[[3]string{p, k, pid}] = membership{id, status}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range items {
		if m, ok := known[[3]string{items[i].Provider, items[i].MediaType, items[i].ProviderID}]; ok {
			items[i].LibraryEntryID = &m.id
			items[i].LibraryStatus = &m.status
		}
	}
	return nil
}
