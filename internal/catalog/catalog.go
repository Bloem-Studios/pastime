// Package catalog owns shared provider metadata. Private membership annotations are
// explicitly attached for one actor after public metadata caching; shared records and
// cache entries never contain private tracking state.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

// Item is a catalog record as exposed by the native API.
type Item struct {
	ID             int64          `json:"id,string"`
	Kind           string         `json:"kind"`
	Provider       string         `json:"provider"`
	ProviderID     string         `json:"provider_id"`
	ParentID       *int64         `json:"parent_id,string"`
	SeasonNumber   *int           `json:"season_number"`
	EpisodeNumber  *int           `json:"episode_number"`
	Title          string         `json:"title"`
	OriginalTitle  string         `json:"original_title"`
	Overview       string         `json:"overview"`
	ImageURL       string         `json:"image_url"`
	BackdropURL    string         `json:"backdrop_url"`
	ReleaseDate    *string        `json:"release_date"`
	Year           *int           `json:"year"`
	RuntimeMinutes *int           `json:"runtime_minutes"`
	Unit           string         `json:"unit"`
	Total          *int           `json:"total"`
	ReleaseStatus  string         `json:"release_status"`
	Genres         []string       `json:"genres"`
	Credits        []string       `json:"credits"`
	ExternalIDs    map[string]any `json:"external_ids"`
	Details        map[string]any `json:"details"`
	ProviderRating *float64       `json:"provider_rating"`
	ChildrenLoaded bool           `json:"children_loaded"`
	RefreshedAt    time.Time      `json:"refreshed_at"`
}

// Result is a search hit. CatalogID is set when the record is already in the catalog.
type Result struct {
	Provider       string   `json:"provider"`
	ProviderID     string   `json:"provider_id"`
	MediaType      string   `json:"media_type"`
	Title          string   `json:"title"`
	Year           *int     `json:"year"`
	ImageURL       string   `json:"image_url"`
	Overview       string   `json:"overview"`
	Credits        []string `json:"credits"`
	CatalogID      *int64   `json:"catalog_id,string"`
	LibraryEntryID *int64   `json:"library_entry_id,string"`
	LibraryStatus  *string  `json:"library_status"`
}

type SearchResult struct {
	Items []Result `json:"items"`
	// Source is "provider" for live provider results and "local" for the catalog fallback.
	Source       string  `json:"source"`
	Degraded     bool    `json:"degraded"`
	Provider     string  `json:"provider"`
	Availability string  `json:"availability"`
	NextPage     *string `json:"next_page"`
}

// Cache is disposable shared state. A miss or failure only costs a provider call.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration)
}

// TrackableKinds are top-level kinds a person can add to their library.
var TrackableKinds = []string{"movie", "series", "anime", "manga", "book", "audiobook", "comic", "game", "boardgame", "music", "podcast", "video"}

func supports(p provider.Provider, kind string) bool {
	for _, k := range p.Kinds() {
		if k == kind {
			return true
		}
	}
	return false
}

func trackable(kind string) bool {
	for _, k := range TrackableKinds {
		if k == kind {
			return true
		}
	}
	return false
}

type Service struct {
	OpenCriticKey string
	CriticHTTP    *http.Client
	Pool          *pgxpool.Pool
	Providers     map[string]provider.Provider
	// Client is the shared provider HTTP client, when the providers came from Providers.
	Client *provider.Client
	// Defaults maps each trackable kind to the provider searched for it.
	Defaults map[string]string
	Cache    Cache
	// MaxAge is how long fetched metadata is served before a refresh is attempted.
	MaxAge time.Duration
	// FetchTimeout bounds one provider operation. Interactive requests must finish
	// inside the HTTP deadline; background workers can allow longer.
	FetchTimeout time.Duration
	group        singleflight.Group
}

const itemColumns = `id,kind,provider,provider_id,parent_id,season_number,episode_number,title,original_title,overview,image_url,backdrop_url,
release_date,runtime_minutes,unit,total,release_status,genres,credits,external_ids,details,provider_rating::float8,children_loaded,refreshed_at`

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	var released *time.Time
	err := row.Scan(&it.ID, &it.Kind, &it.Provider, &it.ProviderID, &it.ParentID, &it.SeasonNumber, &it.EpisodeNumber, &it.Title, &it.OriginalTitle,
		&it.Overview, &it.ImageURL, &it.BackdropURL, &released, &it.RuntimeMinutes, &it.Unit, &it.Total, &it.ReleaseStatus, &it.Genres, &it.Credits,
		&it.ExternalIDs, &it.Details, &it.ProviderRating, &it.ChildrenLoaded, &it.RefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return it, fault.NotFound
	}
	if released != nil {
		s, y := released.Format("2006-01-02"), released.Year()
		it.ReleaseDate, it.Year = &s, &y
	}
	return it, err
}

func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	return scanItem(s.Pool.QueryRow(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE id=$1", id))
}

// Children lists the next hierarchy level in natural order, loading it from the
// provider on first use (for example a season's episodes).
func (s *Service) Children(ctx context.Context, id int64) ([]Item, error) {
	parent, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !parent.ChildrenLoaded && parent.Kind == "season" {
		if err = s.loadSeason(ctx, parent); err != nil && !errors.Is(err, provider.ErrUnsupported) {
			return nil, err
		}
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE parent_id=$1 ORDER BY season_number NULLS LAST, episode_number NULLS LAST, release_date DESC NULLS LAST, id LIMIT 2000", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Service) loadSeason(ctx context.Context, season Item) error {
	p, ok := s.Providers[season.Provider].(provider.ChildFetcher)
	if !ok || season.ParentID == nil {
		return provider.ErrUnsupported
	}
	show, err := s.Get(ctx, *season.ParentID)
	if err != nil {
		return err
	}
	_, err, _ = s.group.Do("season:"+strconv.FormatInt(season.ID, 10), func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
		defer cancel()
		rec, err := p.FetchChild(fetchCtx, provider.Record{Provider: show.Provider, Kind: show.Kind, ProviderID: show.ProviderID},
			provider.Record{Provider: season.Provider, Kind: "season", ProviderID: season.ProviderID, Title: season.Title, SeasonNumber: season.SeasonNumber})
		if err != nil {
			return nil, providerFault(err)
		}
		if rec, err = provider.Normalize(rec); err != nil {
			return nil, providerFault(err)
		}
		return nil, s.save(ctx, rec, show.ID)
	})
	return err
}

func providerFault(err error) error {
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return fault.NotFound
	case errors.Is(err, provider.ErrNotConfigured):
		return &fault.Error{Status: 503, Code: "provider_not_configured", Message: "This metadata provider is not configured on this server."}
	case errors.Is(err, provider.ErrUnsupported):
		return fault.Invalid("This provider does not support that media type.")
	case errors.Is(err, provider.ErrRateLimited):
		return &fault.Error{Status: 503, Code: "provider_rate_limited", Message: "The metadata provider is rate limiting requests. Try again shortly."}
	}
	return &fault.Error{Status: 503, Code: "provider_unavailable", Message: "The metadata provider is unavailable. Try again shortly."}
}

// Resolve returns the catalog record for a provider identity, fetching and storing it
// when absent or stale. Concurrent resolutions of the same record share one fetch.
// ResolveExternal finds a title by another service's identifier: a stored record whose
// external ids match, else the provider's own lookup. scheme names the identifier
// (mal, steam, release, recording).
func (s *Service) ResolveExternal(ctx context.Context, providerName, kind, scheme, value string) (Item, error) {
	if scheme == "" || value == "" || len(value) > 200 {
		return Item{}, ErrNoMatch
	}
	existing, err := scanItem(s.Pool.QueryRow(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE provider=$1 AND kind=$2 AND parent_id IS NULL AND external_ids @> jsonb_build_object($3::text,$4::text) LIMIT 1", providerName, kind, scheme, value))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, fault.NotFound) {
		return Item{}, err
	}
	p, ok := s.Providers[providerName]
	if !ok {
		return Item{}, provider.ErrNotConfigured
	}
	finder, ok := p.(provider.ExternalFinder)
	if !ok {
		return Item{}, ErrNoMatch
	}
	ctx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	defer cancel()
	id, err := finder.FindExternal(ctx, kind, scheme, value)
	if errors.Is(err, provider.ErrNotFound) {
		return Item{}, ErrNoMatch
	}
	if err != nil {
		return Item{}, fault.Unavailable
	}
	return s.Resolve(ctx, providerName, kind, id)
}

func (s *Service) Resolve(ctx context.Context, providerName, kind, providerID string) (Item, error) {
	if providerName == "rss" {
		if err := s.publicFeed(ctx, providerID); err != nil {
			return Item{}, err
		}
	}
	if !trackable(kind) {
		return Item{}, fault.Invalid("Choose a supported media type.")
	}
	p, ok := s.Providers[providerName]
	if !ok || !supports(p, kind) {
		return Item{}, fault.Invalid("Choose a supported metadata provider for this media type.")
	}
	if providerID == "" || len(providerID) > 200 || !utf8.ValidString(providerID) {
		return Item{}, fault.Invalid("Provide a valid provider identifier.")
	}
	existing, err := scanItem(s.Pool.QueryRow(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE provider=$1 AND kind=$2 AND provider_id=$3", providerName, kind, providerID))
	if err == nil && time.Since(existing.RefreshedAt) < s.maxAge() {
		return existing, nil
	}
	if err != nil && !errors.Is(err, fault.NotFound) {
		return Item{}, err
	}
	v, err, _ := s.group.Do("resolve:"+providerName+":"+kind+":"+providerID, func() (any, error) {
		return s.fetchAndSave(ctx, p, kind, providerID)
	})
	if err != nil {
		// Stale metadata is better than none while a provider is down.
		if existing.ID != 0 {
			return existing, nil
		}
		return Item{}, err
	}
	return s.Get(ctx, v.(int64))
}

// Refresh refetches a top-level record regardless of age. Used by background jobs.
func (s *Service) Refresh(ctx context.Context, id int64) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if it.ParentID != nil {
		return nil
	}
	p, ok := s.Providers[it.Provider]
	if !ok {
		return nil
	}
	_, err = s.fetchAndSave(ctx, p, it.Kind, it.ProviderID)
	return err
}

func (s *Service) fetchTimeout() time.Duration {
	if s.FetchTimeout <= 0 {
		return 4 * time.Second
	}
	return s.FetchTimeout
}

func (s *Service) maxAge() time.Duration {
	if s.MaxAge <= 0 {
		return 7 * 24 * time.Hour
	}
	return s.MaxAge
}

func (s *Service) fetchAndSave(ctx context.Context, p provider.Provider, kind, id string) (int64, error) {
	if p.Name() == "rss" {
		if err := s.publicFeed(ctx, id); err != nil {
			return 0, err
		}
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
	defer cancel()
	rec, err := p.Fetch(fetchCtx, kind, id)
	if err != nil {
		return 0, providerFault(err)
	}
	if rec, err = provider.Normalize(rec); err != nil {
		return 0, providerFault(err)
	}
	if err = s.save(ctx, rec, 0); err != nil {
		return 0, err
	}
	var out int64
	err = s.Pool.QueryRow(ctx, "SELECT id FROM catalog_items WHERE provider=$1 AND kind=$2 AND provider_id=$3", rec.Provider, rec.Kind, rec.ProviderID).Scan(&out)
	return out, err
}

const upsertSQL = `INSERT INTO catalog_items(kind,provider,provider_id,parent_id,season_number,episode_number,title,original_title,overview,image_url,backdrop_url,
release_date,runtime_minutes,unit,total,release_status,genres,credits,external_ids,details,provider_rating,children_loaded)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
ON CONFLICT(provider,kind,provider_id) DO UPDATE SET parent_id=EXCLUDED.parent_id,season_number=EXCLUDED.season_number,episode_number=EXCLUDED.episode_number,
title=EXCLUDED.title,original_title=EXCLUDED.original_title,overview=EXCLUDED.overview,image_url=EXCLUDED.image_url,backdrop_url=EXCLUDED.backdrop_url,
release_date=EXCLUDED.release_date,runtime_minutes=EXCLUDED.runtime_minutes,unit=EXCLUDED.unit,total=EXCLUDED.total,release_status=EXCLUDED.release_status,
genres=EXCLUDED.genres,credits=EXCLUDED.credits,external_ids=EXCLUDED.external_ids,details=EXCLUDED.details,provider_rating=EXCLUDED.provider_rating,
children_loaded=catalog_items.children_loaded OR EXCLUDED.children_loaded,refreshed_at=clock_timestamp()
RETURNING id`

// save writes a record and its children in one transaction. Children are upserted,
// never deleted: plays reference episodes, and a provider dropping an episode must not
// erase someone's history.
func (s *Service) save(ctx context.Context, rec provider.Record, parentID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = upsert(ctx, tx, rec, parentID); err != nil {
		return err
	}
	// A newly loaded season can change the next episode of everyone following the show.
	if rec.Kind == "season" && parentID > 0 {
		if _, err = tx.Exec(ctx, library.NextEpisodeSQL+"e.catalog_item_id=$1", parentID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func upsert(ctx context.Context, tx pgx.Tx, rec provider.Record, parentID int64) error {
	var parent *int64
	if parentID > 0 {
		parent = &parentID
	}
	external := map[string]string{}
	for k, v := range rec.ExternalIDs {
		external[k] = v
	}
	details := rec.Details
	if details == nil {
		details = map[string]any{}
	}
	genres, credits := rec.Genres, rec.Credits
	if genres == nil {
		genres = []string{}
	}
	if credits == nil {
		credits = []string{}
	}
	var id int64
	err := tx.QueryRow(ctx, upsertSQL, rec.Kind, rec.Provider, rec.ProviderID, parent, rec.SeasonNumber, rec.EpisodeNumber, rec.Title, rec.OriginalTitle,
		rec.Overview, rec.ImageURL, rec.BackdropURL, rec.ReleaseDate, rec.RuntimeMinutes, rec.Unit, rec.Total, rec.ReleaseStatus, genres, credits,
		external, details, rec.Rating, rec.ChildrenLoaded).Scan(&id)
	if err != nil {
		return err
	}
	if len(rec.Children) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	grandchildren := map[int]provider.Record{}
	for i, child := range rec.Children {
		if child.Details == nil {
			child.Details = map[string]any{}
		}
		if child.ExternalIDs == nil {
			child.ExternalIDs = map[string]string{}
		}
		if child.Genres == nil {
			child.Genres = []string{}
		}
		if child.Credits == nil {
			child.Credits = []string{}
		}
		if len(child.Children) > 0 {
			grandchildren[i] = child
		}
		batch.Queue(upsertSQL, child.Kind, child.Provider, child.ProviderID, id, child.SeasonNumber, child.EpisodeNumber, child.Title, child.OriginalTitle,
			child.Overview, child.ImageURL, child.BackdropURL, child.ReleaseDate, child.RuntimeMinutes, child.Unit, child.Total, child.ReleaseStatus,
			child.Genres, child.Credits, child.ExternalIDs, child.Details, child.Rating, child.ChildrenLoaded)
	}
	results := tx.SendBatch(ctx, batch)
	childIDs := make([]int64, len(rec.Children))
	for i := range rec.Children {
		if err = results.QueryRow().Scan(&childIDs[i]); err != nil {
			results.Close()
			return err
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	for i, child := range grandchildren {
		for _, g := range child.Children {
			if err = upsert(ctx, tx, g, childIDs[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Search queries the kind's provider, falling back to the local catalog when the
// provider is unavailable or unconfigured. Results are cached briefly in Valkey.
func (s *Service) Search(ctx context.Context, kind, query string) (SearchResult, error) {
	return s.SearchProvider(ctx, kind, query, "")
}

// SearchProvider searches one named provider of the kind, or the kind's default when
// the name is empty.
func (s *Service) SearchProvider(ctx context.Context, kind, query, chosen string) (SearchResult, error) {
	query = strings.Join(strings.Fields(query), " ")
	if !trackable(kind) {
		return SearchResult{}, fault.Invalid("Choose a supported media type.")
	}
	if n := utf8.RuneCountInString(query); n < 1 || (n > 100 && !(kind == "podcast" && provider.FeedURL(query)) && !(kind == "video" && n <= 200 && provider.YouTubeVideoID(query) != "")) {
		return SearchResult{}, fault.Invalid("Search for 1 to 100 characters.")
	}
	name := s.Defaults[kind]
	if chosen != "" {
		p, ok := s.Providers[chosen]
		if !ok || !supports(p, kind) {
			return SearchResult{}, fault.Invalid("Choose a metadata provider for this media type.")
		}
		name = chosen
	}
	// A pasted feed address finds the podcast in its own feed.
	if kind == "podcast" && provider.FeedURL(query) && s.Providers["rss"] != nil {
		if err := s.publicFeed(ctx, query); err != nil {
			return SearchResult{}, err
		}
		name = "rss"
	}
	p, ok := s.Providers[name]
	if !ok {
		out, err := s.local(ctx, kind, query)
		out.Degraded = true
		out.Provider, out.Availability = name, "not_configured"
		return out, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(query)))
	key := "catalog:search:v1:" + name + ":" + kind + ":" + hex.EncodeToString(sum[:12])
	var records []provider.Record
	if s.Cache != nil {
		if data, hit := s.Cache.Get(ctx, key); hit {
			if json.Unmarshal(data, &records) != nil {
				records = nil
			}
		}
	}
	if records == nil {
		v, err, _ := s.group.Do(key, func() (any, error) {
			searchCtx, cancel := context.WithTimeout(ctx, s.fetchTimeout())
			defer cancel()
			found, err := p.Search(searchCtx, kind, query)
			if err != nil {
				return nil, err
			}
			normalized := make([]provider.Record, 0, len(found))
			for _, r := range found {
				if r, err := provider.Normalize(r); err == nil {
					r.Children = nil
					normalized = append(normalized, r)
				}
			}
			if s.Cache != nil {
				if data, err := json.Marshal(normalized); err == nil {
					s.Cache.Set(ctx, key, data, time.Hour)
				}
			}
			return normalized, nil
		})
		if err != nil {
			out, localErr := s.local(ctx, kind, query)
			out.Degraded = true
			out.Provider, out.Availability = name, "temporarily_unavailable"
			if errors.Is(err, provider.ErrNotConfigured) {
				out.Availability = "not_configured"
			}
			return out, localErr
		}
		records = v.([]provider.Record)
	}
	out := SearchResult{Items: make([]Result, 0, len(records)), Source: "provider", Provider: name, Availability: "available"}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ProviderID)
		out.Items = append(out.Items, Result{Provider: r.Provider, ProviderID: r.ProviderID, MediaType: r.Kind, Title: r.Title, Year: r.Year(), ImageURL: r.ImageURL, Overview: truncate(r.Overview, 400), Credits: first(r.Credits, 3)})
	}
	if len(ids) > 0 {
		rows, err := s.Pool.Query(ctx, "SELECT provider_id,id FROM catalog_items WHERE provider=$1 AND kind=$2 AND provider_id=ANY($3)", name, kind, ids)
		if err != nil {
			return out, err
		}
		known := map[string]int64{}
		for rows.Next() {
			var pid string
			var id int64
			if err = rows.Scan(&pid, &id); err != nil {
				rows.Close()
				return out, err
			}
			known[pid] = id
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return out, err
		}
		for i := range out.Items {
			if id, ok := known[out.Items[i].ProviderID]; ok {
				out.Items[i].CatalogID = &id
			}
		}
	}
	return out, nil
}

// local is the bounded PostgreSQL fallback over already-catalogued shared metadata.
// Word matches come first from the full-text index; fuzzy trigram matching only runs
// when they leave the page short, because common words match most of the catalog.
func (s *Service) local(ctx context.Context, kind, query string) (SearchResult, error) {
	out := SearchResult{Items: []Result{}, Source: "local", Availability: "local_only"}
	const cols = `SELECT id,provider,provider_id,kind,title,release_date,image_url,overview,credits FROM catalog_items `
	if err := s.collect(ctx, &out, cols+`WHERE parent_id IS NULL AND kind=$1 AND search_document @@ plainto_tsquery('simple',public.search_fold($2))
	ORDER BY ts_rank(search_document, plainto_tsquery('simple',public.search_fold($2))) DESC, id DESC LIMIT 20`, kind, query); err != nil {
		return out, err
	}
	if len(out.Items) >= 20 {
		return out, nil
	}
	seen := make([]int64, 0, len(out.Items))
	for _, r := range out.Items {
		seen = append(seen, *r.CatalogID)
	}
	err := s.collect(ctx, &out, cols+`WHERE parent_id IS NULL AND kind=$1 AND public.search_fold($2) OPERATOR(public.<%) public.search_fold(title) AND id<>ALL($3)
	ORDER BY public.search_fold(title) OPERATOR(public.<->>) public.search_fold($2), id DESC LIMIT $4`, kind, query, seen, 20-len(out.Items))
	return out, err
}

func (s *Service) collect(ctx context.Context, out *SearchResult, sql string, args ...any) error {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		var id int64
		var released *time.Time
		if err = rows.Scan(&id, &r.Provider, &r.ProviderID, &r.MediaType, &r.Title, &released, &r.ImageURL, &r.Overview, &r.Credits); err != nil {
			return err
		}
		if released != nil {
			y := released.Year()
			r.Year = &y
		}
		r.Overview, r.Credits, r.CatalogID = truncate(r.Overview, 400), first(r.Credits, 3), &id
		out.Items = append(out.Items, r)
	}
	return rows.Err()
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func first(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	if in == nil {
		return []string{}
	}
	return in
}

// Shared RSS records may only use feed addresses already published by the public
// directory provider. A URL's spelling cannot reliably identify private credentials.
func (s *Service) publicFeed(ctx context.Context, address string) error {
	var published bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_items WHERE provider='itunes' AND kind='podcast' AND external_ids->>'feed'=$1)`, address).Scan(&published); err != nil {
		return err
	}
	if !published {
		return fault.Invalid("This feed is not verified in the public podcast directory. Find the podcast by name first; private feed addresses are not supported.")
	}
	return nil
}

// LoadSeasons makes sure the named seasons of a show have their episodes stored, so
// episode-level history can be matched against them. Unknown seasons are ignored.
func (s *Service) LoadSeasons(ctx context.Context, showID int64, seasons []int) error {
	if len(seasons) == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+itemColumns+" FROM catalog_items WHERE parent_id=$1 AND kind='season' AND season_number=ANY($2) AND NOT children_loaded LIMIT 100", showID, seasons)
	if err != nil {
		return err
	}
	pending := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, it)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, season := range pending {
		if err = s.loadSeason(ctx, season); err != nil && !errors.Is(err, provider.ErrUnsupported) {
			return err
		}
	}
	return nil
}
