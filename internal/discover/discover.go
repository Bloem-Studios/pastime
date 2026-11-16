// Package discover assembles suggestions: what is popular now, titles related to what a
// person rated or finished, and well-rated titles from their favourite genres. Titles
// already in the library and dismissed suggestions are left out.
package discover

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool    *pgxpool.Pool
	Catalog *catalog.Service
}

// Reason carries localization inputs, not server prose. Local genre/catalog rows
// describe already catalogued public metadata, not a broad provider browse feed.
type Reason struct {
	Code   string  `json:"code"`
	Title  *string `json:"title"`
	Genre  *string `json:"genre"`
	Source string  `json:"source"`
}

// Row is one shelf of suggestions.
type Row struct {
	Key    string           `json:"key"`
	Reason Reason           `json:"reason"`
	Title  string           `json:"title"`
	Items  []catalog.Result `json:"items"`
}

type Feed struct {
	MediaType string `json:"media_type"`
	Rows      []Row  `json:"rows"`
	// Degraded is set when the popular row could not be fetched.
	Degraded     bool   `json:"degraded"`
	Provider     string `json:"provider"`
	Availability string `json:"availability"`
}

const rowSize = 20

// Feed builds Discover for one media type.
func (s Store) Feed(ctx context.Context, a identity.Actor, kind string) (Feed, error) {
	out := Feed{MediaType: kind, Rows: []Row{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	valid := false
	for _, k := range catalog.TrackableKinds {
		valid = valid || k == kind
	}
	if !valid {
		return out, fault.Invalid("Choose a supported media type.")
	}
	out.Provider, out.Availability = s.Catalog.Availability(kind)
	var rows []Row
	trending, err := s.Catalog.Trending(ctx, kind)
	if err != nil {
		out.Degraded = true
		out.Availability = "temporarily_unavailable"
	} else if len(trending) > 0 {
		out.Availability = "available"
		rows = append(rows, Row{Key: "trending", Title: "Popular now", Reason: Reason{Code: "popular", Source: "provider"}, Items: trending})
	}
	related, err := s.related(ctx, a.TrackingID(), kind)
	if err != nil {
		return out, err
	}
	rows = append(rows, related...)
	genres, err := s.genres(ctx, a.TrackingID(), kind)
	if err != nil {
		return out, err
	}
	rows = append(rows, genres...)
	if err := s.filter(ctx, a.TrackingID(), rows); err != nil {
		return out, err
	}
	for _, r := range rows {
		if len(r.Items) > 0 {
			out.Rows = append(out.Rows, r)
		}
	}
	if len(out.Rows) == 0 {
		items, err := s.catalogFallback(ctx, kind)
		if err != nil {
			return out, err
		}
		fallback := []Row{{Key: "catalog", Title: "From the existing catalog", Reason: Reason{Code: "catalog", Source: "local"}, Items: items}}
		if err := s.filter(ctx, a.TrackingID(), fallback); err != nil {
			return out, err
		}
		if len(fallback[0].Items) > 0 {
			out.Rows = append(out.Rows, fallback[0])
		}
	}
	return out, nil
}

// related uses the recommendations stored with up to three titles the person rated
// highly or finished recently.
func (s Store) related(ctx context.Context, userID int64, kind string) ([]Row, error) {
	rows, err := s.Pool.Query(ctx, `SELECT e.title,c.details->'related' FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id
		WHERE e.profile_id=$1 AND e.media_type=$2 AND e.status IN ('completed','in_progress','rated') AND e.catalog_item_id IS NOT NULL
		AND (e.rating IS NULL OR e.rating>=7) AND jsonb_typeof(c.details->'related')='array' AND jsonb_array_length(c.details->'related')>0
		ORDER BY e.rating DESC NULLS LAST, e.last_activity_at DESC NULLS LAST LIMIT 3`, userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var title string
		var raw []byte
		if err := rows.Scan(&title, &raw); err != nil {
			return nil, err
		}
		var related []struct {
			Provider string `json:"provider"`
			Kind     string `json:"kind"`
			ID       string `json:"id"`
			Title    string `json:"title"`
			ImageURL string `json:"image_url"`
			Year     *int   `json:"year"`
		}
		if json.Unmarshal(raw, &related) != nil {
			continue
		}
		row := Row{Key: "related:" + title, Title: "Because you liked " + title, Reason: Reason{Code: "related", Title: &title, Source: "provider"}}
		for _, r := range related {
			if r.Provider != "" && r.ID != "" && r.Title != "" && r.Kind == kind {
				row.Items = append(row.Items, catalog.Result{Provider: r.Provider, ProviderID: r.ID, MediaType: r.Kind, Title: r.Title, Year: r.Year, ImageURL: r.ImageURL, Credits: []string{}})
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// genres suggests well-rated catalogued titles from the person's two favourite genres.
func (s Store) genres(ctx context.Context, userID int64, kind string) ([]Row, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g FROM library_entries e JOIN catalog_items c ON c.id=e.catalog_item_id, unnest(c.genres) g
		WHERE e.profile_id=$1 AND e.media_type=$2 AND e.status IN ('completed','in_progress','rated') AND (e.rating IS NULL OR e.rating>=6)
		GROUP BY g ORDER BY sum(COALESCE(e.rating,6)) DESC, g LIMIT 2`, userID, kind)
	if err != nil {
		return nil, err
	}
	var genres []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			rows.Close()
			return nil, err
		}
		genres = append(genres, g)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out []Row
	for _, g := range genres {
		items, err := s.Pool.Query(ctx, `SELECT c.id,c.provider,c.provider_id,c.kind,c.title,extract(year FROM c.release_date)::int,c.image_url,left(c.overview,400),c.credits[1:3]
			FROM catalog_items c WHERE c.parent_id IS NULL AND c.kind=$2 AND c.genres @> ARRAY[$3]
			AND NOT EXISTS(SELECT 1 FROM library_entries e WHERE e.profile_id=$1 AND e.catalog_item_id=c.id)
			ORDER BY c.provider_rating DESC NULLS LAST, c.id DESC LIMIT 40`, userID, kind, g)
		if err != nil {
			return nil, err
		}
		row := Row{Key: "genre:" + g, Title: "More " + g, Reason: Reason{Code: "genre", Genre: &g, Source: "local"}}
		for items.Next() {
			var r catalog.Result
			var id int64
			if err := items.Scan(&id, &r.Provider, &r.ProviderID, &r.MediaType, &r.Title, &r.Year, &r.ImageURL, &r.Overview, &r.Credits); err != nil {
				items.Close()
				return nil, err
			}
			r.CatalogID = &id
			row.Items = append(row.Items, r)
		}
		items.Close()
		if err = items.Err(); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// filter drops titles already in the library, dismissed suggestions and repeats across
// rows, then fills catalog ids where known.
func (s Store) filter(ctx context.Context, userID int64, rows []Row) error {
	var providers, kinds, ids []string
	for _, r := range rows {
		for _, it := range r.Items {
			providers, kinds, ids = append(providers, it.Provider), append(kinds, it.MediaType), append(ids, it.ProviderID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	res, err := s.Pool.Query(ctx, `SELECT k.provider,k.kind,k.provider_id,c.id,
		(EXISTS(SELECT 1 FROM library_entries e WHERE e.profile_id=$4 AND e.catalog_item_id=c.id)
		 OR EXISTS(SELECT 1 FROM discover_dismissals d WHERE d.profile_id=$4 AND d.provider=k.provider AND d.kind=k.kind AND d.provider_id=k.provider_id))
		FROM unnest($1::text[],$2::text[],$3::text[]) AS k(provider,kind,provider_id)
		LEFT JOIN catalog_items c ON c.provider=k.provider AND c.kind=k.kind AND c.provider_id=k.provider_id`, providers, kinds, ids, userID)
	if err != nil {
		return err
	}
	defer res.Close()
	type key [3]string
	hidden, known := map[key]bool{}, map[key]int64{}
	for res.Next() {
		var p, k, pid string
		var id *int64
		var hide bool
		if err := res.Scan(&p, &k, &pid, &id, &hide); err != nil {
			return err
		}
		if hide {
			hidden[key{p, k, pid}] = true
		}
		if id != nil {
			known[key{p, k, pid}] = *id
		}
	}
	if err := res.Err(); err != nil {
		return err
	}
	seen := map[key]bool{}
	for i := range rows {
		kept := rows[i].Items[:0]
		for _, it := range rows[i].Items {
			if len(kept) == rowSize {
				break
			}
			k := key{it.Provider, it.MediaType, it.ProviderID}
			if hidden[k] || seen[k] {
				continue
			}
			seen[k] = true
			if id, ok := known[k]; ok {
				it.CatalogID = &id
			}
			kept = append(kept, it)
		}
		rows[i].Items = kept
	}
	return nil
}

// catalogFallback is a bounded cold-start fallback over shared metadata. It never
// reads another person's taste, library, ratings or private tracking information.
func (s Store) catalogFallback(ctx context.Context, kind string) ([]catalog.Result, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,provider,provider_id,kind,title,extract(year FROM release_date)::int,image_url,left(overview,400),credits[1:3]
 FROM catalog_items WHERE parent_id IS NULL AND kind=$1 ORDER BY provider_rating DESC NULLS LAST,id DESC LIMIT 40`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []catalog.Result{}
	for rows.Next() {
		var r catalog.Result
		var id int64
		if err := rows.Scan(&id, &r.Provider, &r.ProviderID, &r.MediaType, &r.Title, &r.Year, &r.ImageURL, &r.Overview, &r.Credits); err != nil {
			return nil, err
		}
		r.CatalogID = &id
		out = append(out, r)
	}
	return out, rows.Err()
}

// Dismiss hides a suggestion, or shows it again when undo is set.
func (s Store) Dismiss(ctx context.Context, a identity.Actor, providerName, kind, providerID string, undo bool) error {
	return identity.WriteOnly(ctx, s.Pool, a, "library:write", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Allows("library:write") {
			return fault.Forbidden
		}
		if providerName == "" || kind == "" || providerID == "" || len(providerID) > 200 || len(providerName) > 32 || len(kind) > 32 {
			return fault.Invalid("Name the suggestion's provider, media type and id.")
		}
		if undo {
			_, err := tx.Exec(ctx, "DELETE FROM discover_dismissals WHERE profile_id=$1 AND provider=$2 AND kind=$3 AND provider_id=$4", a.TrackingID(), providerName, kind, providerID)
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO discover_dismissals(profile_id,provider,kind,provider_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", a.TrackingID(), providerName, kind, providerID)
		if err != nil {
			var pg interface{ SQLState() string }
			if errors.As(err, &pg) && pg.SQLState() == "23514" {
				return fault.Invalid("Name the suggestion's provider, media type and id.")
			}
		}
		return err
	})
}
