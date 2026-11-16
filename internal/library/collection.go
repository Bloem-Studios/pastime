package library

import (
	"context"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
	"strings"
)

type CopyPage struct {
	page.Result[Copy]
	Total   int              `json:"total"`
	Titles  int              `json:"titles"`
	Spent   map[string]int64 `json:"spent"`
	Formats map[string]int   `json:"formats"`
}

// CopyPage reads a bounded page and full filtered totals from the same snapshot.
func (s Store) CopyPage(ctx context.Context, a identity.Actor, entryID int64, format, cursor string, limit int, search ...string) (CopyPage, error) {
	out := CopyPage{Result: page.Result[Copy]{Items: []Copy{}}, Spent: map[string]int64{}, Formats: map[string]int{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	if format != "" && !oneOf(format, formats...) {
		return out, fault.Invalid("Choose a supported format.")
	}
	q := ""
	if len(search) > 0 {
		q = strings.TrimSpace(search[0])
	}
	if len([]rune(q)) > 100 {
		return out, fault.Invalid("Invalid collection search.")
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := fmt.Sprintf("copies|%d|%d|%s", a.TrackingID(), entryID, format) + "|" + q
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT "+copyColumns+` WHERE o.profile_id=$1 AND ($2::bigint=0 OR o.entry_id=$2) AND ($3='' OR o.format=$3) AND ($6='' OR strpos(lower(e.title||' '||COALESCE(s.title,'')||' '||o.edition),lower($6))>0) AND ($4::bigint=0 OR o.id<$4) ORDER BY o.id DESC LIMIT $5`, a.TrackingID(), entryID, format, last, limit+1, q)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		c, err := scanCopy(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[limit-1].ID)
		out.NextCursor = &next
	}
	err = tx.QueryRow(ctx, `WITH owned AS MATERIALIZED (SELECT entry_id,format,currency,price_cents FROM owned_copies WHERE profile_id=$1 AND ($2::bigint=0 OR entry_id=$2) AND ($4='' OR strpos(lower((SELECT title FROM library_entries WHERE id=owned_copies.entry_id)||' '||COALESCE((SELECT title FROM catalog_items WHERE id=owned_copies.catalog_item_id),'')||' '||edition),lower($4))>0)),
 filtered AS (SELECT * FROM owned WHERE $3='' OR format=$3)
 SELECT (SELECT count(*) FROM filtered),(SELECT count(DISTINCT entry_id) FROM filtered),
 COALESCE((SELECT jsonb_object_agg(currency,cents) FROM (SELECT currency,sum(price_cents) cents FROM filtered WHERE price_cents IS NOT NULL GROUP BY currency) x),'{}'),
 COALESCE((SELECT jsonb_object_agg(format,n) FROM (SELECT format,count(*) n FROM owned GROUP BY format) x),'{}')`, a.TrackingID(), entryID, format, q).Scan(&out.Total, &out.Titles, &out.Spent, &out.Formats)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
