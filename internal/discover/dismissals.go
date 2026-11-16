package discover

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
)

type Dismissal struct {
	catalog.Result
	CreatedAt time.Time `json:"created_at"`
}
type dismissalKey struct{ Provider, Kind, ID string }

// Dismissals lists only this caller's hidden suggestions, even when they were never
// resolved into the shared catalog. Cursor scope includes user and media type.
func (s Store) Dismissals(ctx context.Context, a identity.Actor, kind, cursor string, limit int) (page.Result[Dismissal], error) {
	out := page.Result[Dismissal]{Items: []Dismissal{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	valid := false
	for _, k := range catalog.TrackableKinds {
		valid = valid || kind == k
	}
	if !valid {
		return out, fault.Invalid("Choose a supported media type.")
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "discover-dismissals:" + strconv.FormatInt(a.TrackingID(), 10) + ":" + kind
	raw, _, has, err := page.DecodeValue(scope, cursor)
	if err != nil {
		return out, err
	}
	key := dismissalKey{}
	if has && (json.Unmarshal(raw, &key) != nil || key.Provider == "" || len(key.Provider) > 32 || key.Kind != kind || key.ID == "" || len(key.ID) > 200) {
		return out, fault.Invalid("The page cursor does not match these filters.")
	}
	rows, err := s.Pool.Query(ctx, `SELECT d.provider,d.kind,d.provider_id,d.created_at,c.id,COALESCE(c.title,d.provider_id),extract(year FROM c.release_date)::int,COALESCE(c.image_url,''),COALESCE(left(c.overview,400),''),COALESCE(c.credits[1:3],'{}'::text[])
 FROM discover_dismissals d LEFT JOIN catalog_items c ON c.provider=d.provider AND c.kind=d.kind AND c.provider_id=d.provider_id
 WHERE d.profile_id=$1 AND d.kind=$2 AND (NOT $3::boolean OR (d.provider,d.kind,d.provider_id)>($4,$5,$6))
 ORDER BY d.provider,d.kind,d.provider_id LIMIT $7`, a.TrackingID(), kind, has, key.Provider, key.Kind, key.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Dismissal
		if err := rows.Scan(&r.Provider, &r.MediaType, &r.ProviderID, &r.CreatedAt, &r.CatalogID, &r.Title, &r.Year, &r.ImageURL, &r.Overview, &r.Credits); err != nil {
			return out, err
		}
		out.Items = append(out.Items, r)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		next := page.EncodeValue(scope, dismissalKey{last.Provider, last.MediaType, last.ProviderID}, 1)
		out.NextCursor = &next
	}
	return out, nil
}
