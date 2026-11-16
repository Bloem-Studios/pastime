package library

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// ListExportEntry contains only public identity metadata. Tracking fields and
// list annotations are deliberately absent even for an owner reading privately.
type ListExportEntry struct {
	ID, MediaType, Title string
	Year                 int
	Provider, ProviderID string
	EditionISBN          string
	ExternalIDs          map[string]string
}

// ExportListTx shares native list filtering/order within the caller's consistent
// read. Authorization selects the source actor before calling this method.
func (s Store) ExportListTx(ctx context.Context, tx pgx.Tx, a identity.Actor, listID int64, collaborative bool) ([]ListExportEntry, error) {
	if !a.Allows("lists:read") || !a.Allows("library:read") {
		return nil, fault.Forbidden
	}
	out := []ListExportEntry{}
	if collaborative {
		if err := identity.ForHousehold(ctx, tx, a); err != nil {
			return nil, err
		}
		if _, err := collabRole(ctx, tx, a.User.ID, listID, false); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, `SELECT c.id,c.kind,c.title,COALESCE(extract(year FROM c.release_date)::int,0),c.provider,c.provider_id,c.external_ids
		FROM collab_items i JOIN catalog_items c ON c.id=i.catalog_item_id WHERE i.list_id=$1
		ORDER BY i.position,i.added_at,i.catalog_item_id LIMIT 10001`, listID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var e ListExportEntry
			var id int64
			var raw []byte
			if err := rows.Scan(&id, &e.MediaType, &e.Title, &e.Year, &e.Provider, &e.ProviderID, &raw); err != nil {
				return nil, err
			}
			e.ID = "catalog:" + strconv.FormatInt(id, 10)
			if err := json.Unmarshal(raw, &e.ExternalIDs); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		cursor := ""
		for {
			p, err := s.entries(ctx, a, Filter{ListID: listID, Limit: 100, Cursor: cursor}, tx)
			if err != nil {
				return nil, err
			}
			ids := make([]int64, len(p.Items))
			for i, e := range p.Items {
				ids[i] = e.ID
			}
			metadata := map[int64]ListExportEntry{}
			rows, err := tx.Query(ctx, `SELECT e.id,COALESCE(c.provider,''),COALESCE(c.provider_id,''),COALESCE(c.external_ids,'{}'::jsonb),e.edition
			FROM library_entries e LEFT JOIN catalog_items c ON c.id=e.catalog_item_id WHERE e.profile_id=$1 AND e.id=ANY($2)`, a.TrackingID(), ids)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id int64
				var e ListExportEntry
				var raw []byte
				var editionRaw []byte
				if err := rows.Scan(&id, &e.Provider, &e.ProviderID, &raw, &editionRaw); err != nil {
					rows.Close()
					return nil, err
				}
				if err := json.Unmarshal(raw, &e.ExternalIDs); err != nil {
					rows.Close()
					return nil, err
				}
				var edition Edition
				if len(editionRaw) > 0 && json.Unmarshal(editionRaw, &edition) != nil {
					rows.Close()
					return nil, fault.Invalid("Invalid chosen book edition.")
				}
				e.EditionISBN = edition.ISBN
				metadata[id] = e
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
			for _, item := range p.Items {
				e := metadata[item.ID]
				e.ID = "entry:" + strconv.FormatInt(item.ID, 10)
				e.Title = item.Title
				e.MediaType = item.MediaType
				if item.Year != nil {
					e.Year = *item.Year
				}
				out = append(out, e)
			}
			if len(out) > 10000 {
				return nil, fault.Invalid("This list exceeds the 10,000-title sync limit.")
			}
			if p.NextCursor == nil {
				break
			}
			cursor = *p.NextCursor
		}
	}
	if len(out) > 10000 {
		return nil, fault.Invalid("This list exceeds the 10,000-title sync limit.")
	}
	return out, nil
}
