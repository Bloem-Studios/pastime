package library

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

// CollabList is a list several people curate together.
type CollabList struct {
	ID          int64     `json:"id,string"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Role        string    `json:"role"`
	Owner       Person    `json:"owner"`
	Members     int       `json:"members"`
	Items       int       `json:"items"`
	Covers      []string  `json:"covers"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CollabMember struct {
	Person
	Role    string    `json:"role"`
	AddedAt time.Time `json:"added_at"`
}

type CollabItem struct {
	CatalogID int64     `json:"catalog_id,string"`
	Title     string    `json:"title"`
	MediaType string    `json:"media_type"`
	Year      *int      `json:"year"`
	ImageURL  string    `json:"image_url"`
	Note      string    `json:"note"`
	AddedBy   *Person   `json:"added_by"`
	AddedAt   time.Time `json:"added_at"`
	// EntryID and Status are the viewer's own tracking of the title, when any.
	EntryID *int64  `json:"entry_id,string"`
	Status  *string `json:"status"`
}

type CollabDetail struct {
	CollabList
	MemberList []CollabMember `json:"member_list"`
	ItemList   []CollabItem   `json:"item_list"`
}

type CollabInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     int    `json:"version"`
}

const collabColumns = `l.id,l.name,l.description,m.role,o.username,o.display_name,
	(SELECT count(*) FROM collab_members x WHERE x.list_id=l.id),(SELECT count(*) FROM collab_items x WHERE x.list_id=l.id),
	ARRAY(SELECT c.image_url FROM collab_items i JOIN catalog_items c ON c.id=i.catalog_item_id WHERE i.list_id=l.id AND c.image_url<>'' ORDER BY i.position,i.added_at LIMIT 4),
	l.version,l.updated_at
	FROM collab_lists l JOIN collab_members m ON m.list_id=l.id AND m.user_id=$1 JOIN users o ON o.id=l.owner_id`

func scanCollab(row pgx.Row) (CollabList, error) {
	var l CollabList
	err := row.Scan(&l.ID, &l.Name, &l.Description, &l.Role, &l.Owner.Username, &l.Owner.DisplayName, &l.Members, &l.Items, &l.Covers, &l.Version, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.NotFound
	}
	return l, err
}

func collabInput(in CollabInput) (CollabInput, error) {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	if in.Name == "" || len([]rune(in.Name)) > 100 || len([]rune(in.Description)) > 2000 {
		return in, fault.Invalid("Use a name of 1 to 100 characters and a description up to 2000.")
	}
	return in, nil
}

// collabRole returns the actor's role on a list; lists they are not on do not exist.
func collabRole(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, listID int64, lock bool) (string, error) {
	sql := "SELECT m.role FROM collab_lists l JOIN collab_members m ON m.list_id=l.id WHERE l.id=$1 AND m.user_id=$2"
	if lock {
		sql += " FOR UPDATE OF l"
	}
	var role string
	err := q.QueryRow(ctx, sql, listID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.NotFound
	}
	return role, err
}

func (s Store) CollabLists(ctx context.Context, a identity.Actor) ([]CollabList, error) {
	out := []CollabList{}
	if !a.Allows("lists:read") {
		return out, fault.Forbidden
	}
	if err := identity.ForHousehold(ctx, s.Pool, a); err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+collabColumns+" ORDER BY l.updated_at DESC LIMIT 200", a.User.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanCollab(rows)
		if err != nil {
			return out, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s Store) CreateCollab(ctx context.Context, a identity.Actor, in CollabInput) (CollabList, error) {
	if !a.Allows("lists:write") {
		return CollabList{}, fault.Forbidden
	}
	in, err := collabInput(in)
	if err != nil {
		return CollabList{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return CollabList{}, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "lists:write"); err != nil {
		return CollabList{}, err
	}
	if err = identity.ForHousehold(ctx, tx, a); err != nil {
		return CollabList{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM collab_lists WHERE owner_id=$1", a.User.ID).Scan(&count); err != nil {
		return CollabList{}, err
	}
	if count >= 100 {
		return CollabList{}, fault.Invalid("You can own up to 100 collaborative lists.")
	}
	var id int64
	if err = tx.QueryRow(ctx, "INSERT INTO collab_lists(owner_id,name,description) VALUES($1,$2,$3) RETURNING id", a.User.ID, in.Name, in.Description).Scan(&id); err != nil {
		return CollabList{}, err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO collab_members(list_id,user_id,role,added_by) VALUES($1,$2,'owner',$2)", id, a.User.ID); err != nil {
		return CollabList{}, err
	}
	l, err := scanCollab(tx.QueryRow(ctx, "SELECT "+collabColumns+" WHERE l.id=$2", a.User.ID, id))
	if err != nil {
		return l, err
	}
	return l, tx.Commit(ctx)
}

func (s Store) Collab(ctx context.Context, a identity.Actor, id int64) (CollabDetail, error) {
	var out CollabDetail
	if !a.Allows("lists:read") {
		return out, fault.Forbidden
	}
	if err := identity.ForHousehold(ctx, s.Pool, a); err != nil {
		return out, err
	}
	l, err := scanCollab(s.Pool.QueryRow(ctx, "SELECT "+collabColumns+" WHERE l.id=$2", a.User.ID, id))
	if err != nil {
		return out, err
	}
	out.CollabList, out.MemberList, out.ItemList = l, []CollabMember{}, []CollabItem{}
	rows, err := s.Pool.Query(ctx, `SELECT u.username,u.display_name,m.role,m.added_at FROM collab_members m JOIN users u ON u.id=m.user_id
		WHERE m.list_id=$1 ORDER BY m.role='owner' DESC, m.added_at`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m CollabMember
		if err := rows.Scan(&m.Username, &m.DisplayName, &m.Role, &m.AddedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.MemberList = append(out.MemberList, m)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT c.id,c.title,c.kind,extract(year FROM c.release_date)::int,c.image_url,i.note,u.username,u.display_name,i.added_at,e.id,e.status
		FROM collab_items i JOIN catalog_items c ON c.id=i.catalog_item_id LEFT JOIN users u ON u.id=i.added_by
		LEFT JOIN library_entries e ON e.profile_id=$2 AND e.catalog_item_id=c.id
		WHERE i.list_id=$1 ORDER BY i.position,i.added_at LIMIT 1000`, id, a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var it CollabItem
		var username, display *string
		if err := rows.Scan(&it.CatalogID, &it.Title, &it.MediaType, &it.Year, &it.ImageURL, &it.Note, &username, &display, &it.AddedAt, &it.EntryID, &it.Status); err != nil {
			return out, err
		}
		if username != nil {
			it.AddedBy = &Person{Username: *username, DisplayName: *display}
		}
		out.ItemList = append(out.ItemList, it)
	}
	return out, rows.Err()
}

// collabWrite runs fn for a member whose role allows it, then bumps the list.
func (s Store) collabWrite(ctx context.Context, a identity.Actor, id int64, owner bool, fn func(pgx.Tx, string) error) error {
	if !a.Allows("lists:write") {
		return fault.Forbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "lists:write"); err != nil {
		return err
	}
	if err = identity.ForHousehold(ctx, tx, a); err != nil {
		return err
	}
	role, err := collabRole(ctx, tx, a.User.ID, id, true)
	if err != nil {
		return err
	}
	if role == "viewer" || (owner && role != "owner") {
		return fault.Forbidden
	}
	if err = fn(tx, role); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE collab_lists SET updated_at=clock_timestamp(),version=version+1 WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) UpdateCollab(ctx context.Context, a identity.Actor, id int64, in CollabInput) error {
	in, err := collabInput(in)
	if err != nil {
		return err
	}
	return s.collabWrite(ctx, a, id, true, func(tx pgx.Tx, _ string) error {
		tag, err := tx.Exec(ctx, "UPDATE collab_lists SET name=$2,description=$3 WHERE id=$1 AND version=$4", id, in.Name, in.Description, in.Version)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.Conflict
		}
		return err
	})
}

func (s Store) DeleteCollab(ctx context.Context, a identity.Actor, id int64) error {
	return s.collabWrite(ctx, a, id, true, func(tx pgx.Tx, _ string) error {
		_, err := tx.Exec(ctx, "DELETE FROM collab_lists WHERE id=$1 AND owner_id=$2", id, a.User.ID)
		return err
	})
}

// SetCollabMember adds someone by username or changes their role. Only the owner can.
func (s Store) SetCollabMember(ctx context.Context, a identity.Actor, id int64, username, role string) error {
	if role != "editor" && role != "viewer" {
		return fault.Invalid("Role must be editor or viewer.")
	}
	return s.collabWrite(ctx, a, id, true, func(tx pgx.Tx, _ string) error {
		var members int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM collab_members WHERE list_id=$1", id).Scan(&members); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO collab_members(list_id,user_id,role,added_by) SELECT $1,u.id,$3,$4 FROM users u
			WHERE u.username=lower($2) AND u.active AND u.id<>$4 AND ($5<50 OR EXISTS(SELECT 1 FROM collab_members x WHERE x.list_id=$1 AND x.user_id=u.id))
			ON CONFLICT (list_id,user_id) DO UPDATE SET role=EXCLUDED.role WHERE collab_members.role<>'owner'`, id, strings.TrimSpace(username), role, a.User.ID, members)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.Invalid("Choose another active person on this server. A list holds up to 50 people.")
		}
		return err
	})
}

// RemoveCollabMember removes someone; the owner removes others, anyone may leave.
func (s Store) RemoveCollabMember(ctx context.Context, a identity.Actor, id int64, username string) error {
	if !a.Allows("lists:write") {
		return fault.Forbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "lists:write"); err != nil {
		return err
	}
	if err = identity.ForHousehold(ctx, tx, a); err != nil {
		return err
	}
	role, err := collabRole(ctx, tx, a.User.ID, id, true)
	if err != nil {
		return err
	}
	self := strings.EqualFold(strings.TrimSpace(username), a.User.Username)
	switch {
	case self && role == "owner":
		return fault.Invalid("The owner cannot leave; delete the list instead.")
	case !self && role != "owner":
		return fault.Forbidden
	}
	tag, err := tx.Exec(ctx, "DELETE FROM collab_members m USING users u WHERE m.list_id=$1 AND m.user_id=u.id AND u.username=lower($2) AND m.role<>'owner'", id, strings.TrimSpace(username))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fault.NotFound
	}
	return tx.Commit(ctx)
}

func (s Store) AddCollabItem(ctx context.Context, a identity.Actor, id, catalogID int64, note string) error {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return fault.Invalid("Keep the note under 500 characters.")
	}
	return s.collabWrite(ctx, a, id, false, func(tx pgx.Tx, _ string) error {
		tag, err := tx.Exec(ctx, `INSERT INTO collab_items(list_id,catalog_item_id,added_by,note,position)
			SELECT $1,c.id,$3,$4,COALESCE((SELECT max(position)+1 FROM collab_items WHERE list_id=$1),0) FROM catalog_items c
			WHERE c.id=$2 AND c.parent_id IS NULL AND (SELECT count(*) FROM collab_items WHERE list_id=$1)<1000
			ON CONFLICT (list_id,catalog_item_id) DO UPDATE SET note=CASE WHEN EXCLUDED.note<>'' THEN EXCLUDED.note ELSE collab_items.note END`, id, catalogID, a.User.ID, note)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.Invalid("Choose a title from the catalog. A list holds up to 1000 titles.")
		}
		return err
	})
}

func (s Store) RemoveCollabItem(ctx context.Context, a identity.Actor, id, catalogID int64) error {
	return s.collabWrite(ctx, a, id, false, func(tx pgx.Tx, _ string) error {
		tag, err := tx.Exec(ctx, "DELETE FROM collab_items WHERE list_id=$1 AND catalog_item_id=$2", id, catalogID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return err
	})
}

// ReorderCollab sets the order of the listed titles; titles not named keep their
// relative order after them.
func (s Store) ReorderCollab(ctx context.Context, a identity.Actor, id int64, catalogIDs []int64) error {
	if len(catalogIDs) > 1000 {
		return fault.Invalid("Too many titles.")
	}
	return s.collabWrite(ctx, a, id, false, func(tx pgx.Tx, _ string) error {
		_, err := tx.Exec(ctx, `WITH wanted AS (SELECT v.id, v.ord FROM unnest($2::bigint[]) WITH ORDINALITY AS v(id, ord)),
			ranked AS (SELECT i.catalog_item_id, row_number() OVER (ORDER BY w.ord NULLS LAST, i.position, i.added_at) - 1 AS pos
				FROM collab_items i LEFT JOIN wanted w ON w.id=i.catalog_item_id WHERE i.list_id=$1)
			UPDATE collab_items i SET position=r.pos FROM ranked r WHERE i.list_id=$1 AND i.catalog_item_id=r.catalog_item_id`, id, catalogIDs)
		return err
	})
}
