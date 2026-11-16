package library

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
)

// ChangeRetention is how long the change feed is kept.
const ChangeRetention = 90 * 24 * time.Hour

// Change is one entry or play that changed. Upserts carry the current record; a record
// deleted since shows as a delete.
type Change struct {
	Entity   string      `json:"entity"`
	EntityID int64       `json:"entity_id,string"`
	Op       string      `json:"op"`
	At       time.Time   `json:"at"`
	Entry    *Summary    `json:"entry,omitempty"`
	Play     *ChangePlay `json:"play,omitempty"`
}

type ChangePlay struct {
	Unmapped  bool      `json:"unmapped"`
	ID        int64     `json:"id,string"`
	EntryID   int64     `json:"entry_id,string"`
	CatalogID int64     `json:"catalog_id,string"`
	PlayedAt  time.Time `json:"played_at"`
	Cycle     int       `json:"cycle"`
	Source    string    `json:"source"`
	Season    *int      `json:"season"`
	Episode   *int      `json:"episode"`
}

type ChangePage struct {
	Items      []Change `json:"items"`
	NextCursor string   `json:"next_cursor"`
	HasMore    bool     `json:"has_more"`
}

func changeCursor(id int64, at time.Time) string {
	return strconv.FormatInt(id, 36) + "." + strconv.FormatInt(at.Unix(), 36)
}

var errCursorExpired = &fault.Error{Status: 410, Code: "cursor_expired", Message: "This cursor is older than the change feed keeps. Capture a fresh cursor, download the library again, then replay changes from that cursor."}

// Changes returns what changed after the cursor, oldest first. Without a cursor it
// returns no changes and the cursor for now, to capture BEFORE a full download. Replay from that cursor after the download.
func (s Store) Changes(ctx context.Context, a identity.Actor, cursor string, limit int) (ChangePage, error) {
	out := ChangePage{Items: []Change{}}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	if limit < 1 || limit > 500 {
		limit = 200
	}
	if cursor == "" {
		var head int64
		if err := s.Pool.QueryRow(ctx, "SELECT COALESCE(max(id),0) FROM change_log WHERE profile_id=$1", a.TrackingID()).Scan(&head); err != nil {
			return out, err
		}
		out.NextCursor = changeCursor(head, time.Now())
		return out, nil
	}
	rawID, rawAt, ok := strings.Cut(cursor, ".")
	since, err1 := strconv.ParseInt(rawID, 36, 64)
	at, err2 := strconv.ParseInt(rawAt, 36, 64)
	if !ok || err1 != nil || err2 != nil || since < 0 {
		return out, fault.Invalid("Use a cursor returned by the change feed.")
	}
	if time.Since(time.Unix(at, 0)) > ChangeRetention-24*time.Hour {
		return out, errCursorExpired
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.id,l.entity,l.entity_id,l.op,l.created_at FROM change_log l WHERE l.profile_id=$1 AND l.id>$2 ORDER BY l.id LIMIT $3`, a.TrackingID(), since, limit+1)
	if err != nil {
		return out, err
	}
	var last int64
	var lastAt time.Time
	entries, plays := map[int64]bool{}, map[int64]bool{}
	for rows.Next() {
		var c Change
		var id int64
		if err := rows.Scan(&id, &c.Entity, &c.EntityID, &c.Op, &c.At); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		last, lastAt = id, c.At
		out.Items = append(out.Items, c)
		if c.Op == "upsert" {
			if c.Entity == "entry" {
				entries[c.EntityID] = true
			} else {
				plays[c.EntityID] = true
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ChangePage{}, err
	}
	if last == 0 {
		// Nothing newer exists, so the cursor is current as of now.
		out.NextCursor = changeCursor(since, time.Now())
		return out, nil
	}
	out.NextCursor = changeCursor(last, lastAt)
	current := map[int64]*Summary{}
	if len(entries) > 0 {
		ids := keys(entries)
		rows, err := s.Pool.Query(ctx, "SELECT "+columns+from+" WHERE e.profile_id=$1 AND e.id=ANY($2)", a.TrackingID(), ids)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var e Summary
			if err := rows.Scan(fields(&e)...); err != nil {
				rows.Close()
				return out, err
			}
			current[e.ID] = &e
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return ChangePage{}, err
		}
	}
	currentPlays := map[int64]*ChangePlay{}
	if len(plays) > 0 {
		rows, err := s.Pool.Query(ctx, `SELECT p.id,p.entry_id,p.catalog_item_id,p.played_at,p.cycle,p.source,p.unmapped,c.season_number,c.episode_number
			FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id WHERE p.profile_id=$1 AND p.id=ANY($2)`, a.TrackingID(), keys(plays))
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var p ChangePlay
			if err := rows.Scan(&p.ID, &p.EntryID, &p.CatalogID, &p.PlayedAt, &p.Cycle, &p.Source, &p.Unmapped, &p.Season, &p.Episode); err != nil {
				rows.Close()
				return out, err
			}
			currentPlays[p.ID] = &p
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return ChangePage{}, err
		}
	}
	for i := range out.Items {
		c := &out.Items[i]
		if c.Op != "upsert" {
			continue
		}
		if c.Entity == "entry" {
			c.Entry = current[c.EntityID]
			if c.Entry == nil {
				c.Op = "delete"
			}
		} else {
			c.Play = currentPlays[c.EntityID]
			if c.Play == nil {
				c.Op = "delete"
			}
		}
	}
	return out, nil
}

// PruneChanges drops feed rows older than the retention window.
func (s Store) PruneChanges(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM change_log WHERE created_at < clock_timestamp()-make_interval(secs=>$1)", ChangeRetention.Seconds())
	return err
}

func keys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
