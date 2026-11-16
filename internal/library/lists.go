package library

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
)

func (s Store) Lists(ctx context.Context, a identity.Actor, cursor string, limit int) (page.Result[List], error) {
	out := page.Result[List]{Items: []List{}}
	if !a.Allows("lists:read") {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(limit)
	if err != nil {
		return out, err
	}
	scope := "lists|" + strconv.FormatInt(a.TrackingID(), 10)
	last, err := page.Decode(scope, cursor)
	if err != nil {
		return out, err
	}
	query := `SELECT ` + listColumns + ` FROM personal_lists l WHERE l.profile_id=$1`
	args := []any{a.TrackingID()}
	if last > 0 {
		args = append(args, last)
		query += " AND l.id<$2"
	}
	args = append(args, limit+1)
	query += " ORDER BY l.id DESC LIMIT $" + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, l)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		next := page.Encode(scope, out.Items[len(out.Items)-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}

// EntryLists returns the caller's lists that hold one library entry, by name: the manual
// lists it is a member of and the smart lists whose rules select it. A profile has at most
// 1,000 lists, so the smart lists are checked in one batch rather than paged.
func (s Store) EntryLists(ctx context.Context, a identity.Actor, entryID int64) ([]List, error) {
	out := []List{}
	if !a.Allows("library:read") || !a.Allows("lists:read") {
		return out, fault.Forbidden
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, "SELECT true FROM library_entries WHERE profile_id=$1 AND id=$2", a.TrackingID(), entryID).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	} else if err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+listColumns+` FROM personal_lists l WHERE l.profile_id=$1
		AND (l.kind='smart' OR EXISTS(SELECT 1 FROM list_entries m WHERE m.list_id=l.id AND m.entry_id=$2))
		ORDER BY lower(l.name),l.id`, a.TrackingID(), entryID)
	if err != nil {
		return out, err
	}
	candidates := []List{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		candidates = append(candidates, l)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	batch := &pgx.Batch{}
	smart := []int{}
	for i, l := range candidates {
		if l.Kind != "smart" {
			continue
		}
		var f Filter
		if err = l.Rules.apply(&f); err != nil {
			return out, err
		}
		if err = f.normalize(); err != nil {
			return out, err
		}
		where, args := entryWhere(f, a.TrackingID(), false)
		args = append(args, entryID)
		batch.Queue("SELECT EXISTS(SELECT 1"+from+where+" AND e.id=$"+strconv.Itoa(len(args))+")", args...)
		smart = append(smart, i)
	}
	selected := make([]bool, len(candidates))
	for i, l := range candidates {
		selected[i] = l.Kind != "smart"
	}
	if len(smart) > 0 {
		results := s.Pool.SendBatch(ctx, batch)
		for _, i := range smart {
			if err = results.QueryRow().Scan(&selected[i]); err != nil {
				results.Close()
				return out, err
			}
		}
		if err = results.Close(); err != nil {
			return out, err
		}
	}
	for i, l := range candidates {
		if selected[i] {
			out = append(out, l)
		}
	}
	return out, nil
}

func (s Store) List(ctx context.Context, a identity.Actor, id int64) (List, error) {
	if !a.Allows("lists:read") {
		return List{}, fault.Forbidden
	}
	return scanList(s.Pool.QueryRow(ctx, `SELECT `+listColumns+` FROM personal_lists l WHERE l.profile_id=$1 AND l.id=$2`, a.TrackingID(), id))
}
func checkList(ctx context.Context, tx pgx.Tx, a identity.Actor, id int64, version int) error {
	if version < 1 {
		return fault.Invalid("Include the current list version.")
	}
	var current int
	err := tx.QueryRow(ctx, "SELECT version FROM personal_lists WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	if current != version {
		return fault.Conflict
	}
	return nil
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,58}[a-z0-9])?$`)

func (in *ListInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if len([]rune(in.Name)) < 1 || len([]rune(in.Name)) > 100 || len([]rune(in.Description)) > 2000 {
		return fault.Invalid("Use a list name with 1 to 100 characters and a description up to 2,000 characters.")
	}
	if in.Visibility == "" {
		in.Visibility = "private"
	}
	if in.Kind == "" {
		in.Kind = "manual"
	}
	if in.Sort == "" {
		in.Sort = "position"
		if in.Kind == "smart" {
			in.Sort = "added"
		}
	}
	if !oneOf(in.Visibility, "private", "unlisted", "public") || !oneOf(in.Kind, "manual", "smart") {
		return fault.Invalid("Choose a supported visibility and list kind.")
	}
	if _, ok := sorts[in.Sort]; !ok || (in.Kind == "smart" && in.Sort == "position") {
		return fault.Invalid("Choose a supported sort order.")
	}
	if in.Slug != nil {
		v := strings.ToLower(strings.TrimSpace(*in.Slug))
		if v == "" {
			in.Slug = nil
		} else if !slugPattern.MatchString(v) {
			return fault.Invalid("A link name uses 1 to 60 lowercase letters, digits and hyphens.")
		} else {
			in.Slug = &v
		}
	}
	if in.Visibility != "private" && in.Slug == nil {
		return fault.Invalid("Shared lists need a link name.")
	}
	if in.Kind == "smart" {
		if err := in.Rules.Validate(); err != nil {
			return err
		}
	} else {
		in.Rules = Rules{}
	}
	return nil
}

var duplicateSlug = &fault.Error{Status: 409, Code: "duplicate_slug", Message: "Another of your lists already uses that link name."}

func (s Store) SaveList(ctx context.Context, a identity.Actor, id int64, key string, in ListInput) (Mutation, error) {
	if err := in.validate(); err != nil {
		return Mutation{}, err
	}
	rules, err := json.Marshal(in.Rules)
	if err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "lists:write", key, "list.save/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		out := Mutation{ID: id}
		var err error
		if id == 0 {
			err = tx.QueryRow(ctx, "INSERT INTO personal_lists(profile_id,name,description,visibility,slug,kind,rules,sort) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,version",
				a.TrackingID(), in.Name, in.Description, in.Visibility, in.Slug, in.Kind, rules, in.Sort).Scan(&out.ID, &out.Version)
		} else {
			if err = checkList(ctx, tx, a, id, in.Version); err != nil {
				return out, err
			}
			var kind string
			if err = tx.QueryRow(ctx, "SELECT kind FROM personal_lists WHERE profile_id=$1 AND id=$2", a.TrackingID(), id).Scan(&kind); err != nil {
				return out, err
			}
			if kind != in.Kind {
				return out, fault.Invalid("A list cannot switch between manual and smart. Create a new list instead.")
			}
			err = tx.QueryRow(ctx, "UPDATE personal_lists SET name=$3,description=$4,visibility=$5,slug=$6,rules=$7,sort=$8,share_token=CASE WHEN $9 THEN NULL ELSE share_token END,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version",
				a.TrackingID(), id, in.Name, in.Description, in.Visibility, in.Slug, rules, in.Sort, in.RotateShare).Scan(&out.Version)
		}
		if err == nil && in.SnapshotOf != nil {
			if id != 0 || in.Kind != "manual" || in.Visibility != "private" || !a.Allows("library:read") || !a.Allows("lists:read") {
				return out, fault.Invalid("Create a private manual list for a frozen copy.")
			}
			sourceID, parseErr := identity.ParseID(*in.SnapshotOf)
			if parseErr != nil {
				return out, fault.NotFound
			}
			if err = s.snapshotList(ctx, tx, a.TrackingID(), sourceID, out.ID); err != nil {
				return out, err
			}
		}
		if uniqueViolation(err) {
			err = duplicateSlug
		}
		return out, err
	})
}
func (s Store) DeleteList(ctx context.Context, a identity.Actor, id int64, key string, version int) (Mutation, error) {
	return s.mutate(ctx, a, "lists:write", key, "list.delete/"+strconv.FormatInt(id, 10), version, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if err := checkList(ctx, tx, a, id, version); err != nil {
			return Mutation{}, err
		}
		_, err := tx.Exec(ctx, "DELETE FROM personal_lists WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		return Mutation{ID: id, Version: version, Deleted: true}, err
	})
}
func (s Store) Membership(ctx context.Context, a identity.Actor, listID, entryID int64, key string, version int, present bool) (Mutation, error) {
	input := struct {
		Version int
		Present bool
	}{version, present}
	return s.mutate(ctx, a, "lists:write", key, "list.membership/"+strconv.FormatInt(listID, 10)+"/"+strconv.FormatInt(entryID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if !a.Allows("library:read") {
			return Mutation{}, fault.Forbidden
		}
		var exists int
		err := tx.QueryRow(ctx, "SELECT 1 FROM library_entries WHERE profile_id=$1 AND id=$2 FOR KEY SHARE", a.TrackingID(), entryID).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			return Mutation{}, fault.NotFound
		}
		if err != nil {
			return Mutation{}, err
		}
		if err = checkList(ctx, tx, a, listID, version); err != nil {
			return Mutation{}, err
		}
		var kind string
		if err = tx.QueryRow(ctx, "SELECT kind FROM personal_lists WHERE id=$1", listID).Scan(&kind); err != nil {
			return Mutation{}, err
		}
		if kind != "manual" {
			return Mutation{}, fault.Invalid("Smart lists choose their own titles.")
		}
		var changed int64
		if present {
			r, e := tx.Exec(ctx, `INSERT INTO list_entries(profile_id,list_id,entry_id,position)
				VALUES($1,$2,$3,COALESCE((SELECT max(position) FROM list_entries WHERE list_id=$2),0)+1) ON CONFLICT DO NOTHING`, a.TrackingID(), listID, entryID)
			err = e
			changed = r.RowsAffected()
		} else {
			r, e := tx.Exec(ctx, "DELETE FROM list_entries WHERE profile_id=$1 AND list_id=$2 AND entry_id=$3", a.TrackingID(), listID, entryID)
			err = e
			changed = r.RowsAffected()
		}
		if err != nil {
			return Mutation{}, err
		}
		out := Mutation{ID: listID, Version: version}
		if changed > 0 {
			err = tx.QueryRow(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), listID).Scan(&out.Version)
		}
		return out, err
	})
}

const listColumns = `l.id,l.name,l.description,l.visibility,l.slug,l.kind,l.rules,l.sort,l.version,l.created_at,l.updated_at,
CASE WHEN l.kind='manual' THEN (SELECT count(*) FROM list_entries e WHERE e.list_id=l.id) ELSE -1 END,l.source_kind,l.source_ref,l.synced_at,l.share_token,l.share_expires_at`

func scanList(row pgx.Row) (List, error) {
	var l List
	var rules []byte
	var sourceKind, sourceRef *string
	var synced *time.Time
	err := row.Scan(&l.ID, &l.Name, &l.Description, &l.Visibility, &l.Slug, &l.Kind, &rules, &l.Sort, &l.Version, &l.CreatedAt, &l.UpdatedAt, &l.ItemCount, &sourceKind, &sourceRef, &synced, &l.ShareToken, &l.ShareExpiresAt)
	if err == nil && sourceKind != nil && sourceRef != nil {
		l.Source = &ListSource{Kind: *sourceKind, URL: ListSourceURL(*sourceKind, *sourceRef), SyncedAt: synced}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return l, fault.NotFound
	}
	if err == nil {
		err = json.Unmarshal(rules, &l.Rules)
	}
	return l, err
}

// Reorder moves the named members to the front; unnamed members keep their order.
func (s Store) Reorder(ctx context.Context, a identity.Actor, listID int64, key string, version int, entryIDs []string) (Mutation, error) {
	ids, err := parseIDs(entryIDs, 5000, "list entries")
	if err != nil {
		return Mutation{}, err
	}
	input := struct {
		Version int
		IDs     []int64
	}{version, ids}
	return s.mutate(ctx, a, "lists:write", key, "list.reorder/"+strconv.FormatInt(listID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if err := checkList(ctx, tx, a, listID, version); err != nil {
			return Mutation{}, err
		}
		var members int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM list_entries WHERE list_id=$1 AND entry_id=ANY($2)", listID, ids).Scan(&members); err != nil {
			return Mutation{}, err
		}
		if members != len(ids) || len(dedupeIDs(ids)) != len(ids) {
			return Mutation{}, fault.Invalid("Name existing list entries without duplicates.")
		}
		tag, err := tx.Exec(ctx, `WITH wanted AS (SELECT id,n FROM unnest($2::bigint[]) WITH ORDINALITY o(id,n)), ranked AS (
 SELECT l.entry_id,row_number() OVER(ORDER BY w.n NULLS LAST,l.position,l.entry_id) AS pos FROM list_entries l LEFT JOIN wanted w ON w.id=l.entry_id WHERE l.list_id=$1)
 UPDATE list_entries l SET position=r.pos FROM ranked r WHERE l.list_id=$1 AND l.entry_id=r.entry_id`, listID, ids)
		if err != nil {
			return Mutation{}, err
		}
		if int(tag.RowsAffected()) < len(ids) {
			return Mutation{}, fault.Invalid("Name existing list entries without duplicates.")
		}
		out := Mutation{ID: listID}
		err = tx.QueryRow(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), listID).Scan(&out.Version)
		return out, err
	})
}

// PublicItem is what a shared list reveals about each title: public metadata only,
// never notes, ratings, progress or history.
type PublicItem struct {
	Title       string  `json:"title"`
	MediaType   string  `json:"media_type"`
	Year        *int    `json:"year"`
	ImageURL    string  `json:"image_url"`
	Provider    string  `json:"provider"`
	ProviderID  string  `json:"provider_id"`
	ExternalIDs any     `json:"external_ids"`
	Note        string  `json:"note"`
	CatalogID   *string `json:"catalog_id"`
}

type PublicList struct {
	NextCursor  *string      `json:"next_cursor"`
	Owner       string       `json:"owner"`
	OwnerName   string       `json:"owner_name"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Slug        string       `json:"slug"`
	Kind        string       `json:"kind"`
	UpdatedAt   time.Time    `json:"updated_at"`
	Items       []PublicItem `json:"items"`
}

// Public reads a public or unlisted list by owner and link name, with no authentication.
// Disabled accounts and private lists are indistinguishable from missing ones.
func (s Store) Public(ctx context.Context, username, slug string) (PublicList, error) {
	return s.PublicPage(ctx, username, slug, "", 100)
}

func (s Store) PublicPage(ctx context.Context, username, slug, cursor string, limit int) (PublicList, error) {
	var out PublicList
	var userID, listID int64
	var raw []byte
	var sortName string
	err := s.Pool.QueryRow(ctx, `SELECT l.profile_id,u.username,p.name,l.id,l.name,l.description,l.slug,l.kind,l.rules,l.sort,l.updated_at
		FROM users u JOIN tracking_profiles p ON p.account_id=u.id AND p.archived_at IS NULL JOIN personal_lists l ON l.profile_id=p.id
		WHERE u.username=$1 AND u.active AND ((l.visibility='public' AND l.slug=$2) OR (l.visibility='unlisted' AND l.share_token=$2 AND l.share_expires_at>clock_timestamp()))`, username, slug).
		Scan(&userID, &out.Owner, &out.OwnerName, &listID, &out.Name, &out.Description, &out.Slug, &out.Kind, &raw, &sortName, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	}
	if err != nil {
		return out, err
	}
	out.Slug = slug
	out.Items = []PublicItem{}
	reader, err := identity.ResolveWorkerProfile(ctx, s.Pool, userID, "public", []string{"library:read", "lists:read"}, false)
	if err != nil {
		return out, err
	}
	// Visitors see public metadata only, so the cursor cannot carry the owner's rating
	// or activity time for a list sorted by them.
	f := Filter{ListID: listID, Limit: limit, Cursor: cursor, anchored: true}
	{
		res, err := s.Entries(ctx, reader, f)
		if err != nil {
			return out, err
		}
		ids := make([]int64, len(res.Items))
		for i, e := range res.Items {
			ids[i] = e.ID
		}
		extra, err := s.publicDetails(ctx, userID, listID, ids)
		if err != nil {
			return out, err
		}
		for _, e := range res.Items {
			// Public views use catalog artwork only: a personal override could point
			// visitors at any host.
			item := PublicItem{Title: e.Title, MediaType: e.MediaType, Year: e.Year}
			if d, ok := extra[e.ID]; ok {
				item.Provider, item.ProviderID, item.ExternalIDs, item.Note, item.ImageURL = d.Provider, d.ProviderID, d.ExternalIDs, d.Note, d.ImageURL
			}
			if e.CatalogID != nil {
				v := strconv.FormatInt(*e.CatalogID, 10)
				item.CatalogID = &v
			}
			out.Items = append(out.Items, item)
		}
		out.NextCursor = res.NextCursor
	}
	return out, nil
}

func (s Store) publicDetails(ctx context.Context, userID, listID int64, ids []int64) (map[int64]PublicItem, error) {
	out := map[int64]PublicItem{}
	rows, err := s.Pool.Query(ctx, `SELECT e.id,COALESCE(c.provider,''),COALESCE(c.provider_id,''),COALESCE(c.external_ids,'{}'::jsonb),COALESCE(le.note,''),COALESCE(c.image_url,'')
		FROM library_entries e LEFT JOIN catalog_items c ON c.id=e.catalog_item_id
		LEFT JOIN list_entries le ON le.list_id=$2 AND le.entry_id=e.id
		WHERE e.profile_id=$1 AND e.id=ANY($3)`, userID, listID, ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p PublicItem
		var ext []byte
		if err = rows.Scan(&id, &p.Provider, &p.ProviderID, &ext, &p.Note, &p.ImageURL); err != nil {
			return out, err
		}
		_ = json.Unmarshal(ext, &p.ExternalIDs)
		out[id] = p
	}
	return out, rows.Err()
}

// PublicLists lists an opted-in profile's public lists (unlisted ones stay hidden).
func (s Store) PublicLists(ctx context.Context, username string) (string, []List, error) {
	var userID int64
	var display string
	err := s.Pool.QueryRow(ctx, "SELECT p.id,p.name FROM users u JOIN tracking_profiles p ON p.account_id=u.id AND p.is_default WHERE u.username=$1 AND u.active AND p.public_profile", username).Scan(&userID, &display)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, fault.NotFound
	}
	if err != nil {
		return "", nil, err
	}
	out := []List{}
	rows, err := s.Pool.Query(ctx, `SELECT `+listColumns+` FROM personal_lists l WHERE l.profile_id=$1 AND l.visibility='public' ORDER BY l.updated_at DESC LIMIT 100`, userID)
	if err != nil {
		return display, out, err
	}
	defer rows.Close()
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return display, out, err
		}
		l.Rules = Rules{}
		l.Source = nil // Which outside list it follows is the owner's business.
		out = append(out, l)
	}
	return display, out, rows.Err()
}

// SetListNote attaches a short public-facing note to one member of a manual list.
func (s Store) SetListNote(ctx context.Context, a identity.Actor, listID, entryID int64, key string, version int, note string) (Mutation, error) {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 500 {
		return Mutation{}, fault.Invalid("A list note has at most 500 characters.")
	}
	input := struct {
		Version int
		Note    string
	}{version, note}
	return s.mutate(ctx, a, "lists:write", key, "list.note/"+strconv.FormatInt(listID, 10)+"/"+strconv.FormatInt(entryID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		if err := checkList(ctx, tx, a, listID, version); err != nil {
			return Mutation{}, err
		}
		tag, err := tx.Exec(ctx, "UPDATE list_entries SET note=$3 WHERE list_id=$1 AND entry_id=$2", listID, entryID, note)
		if err != nil {
			return Mutation{}, err
		}
		if tag.RowsAffected() == 0 {
			return Mutation{}, fault.NotFound
		}
		out := Mutation{ID: listID}
		err = tx.QueryRow(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2 RETURNING version", a.TrackingID(), listID).Scan(&out.Version)
		return out, err
	})
}

// MirrorList makes a manual list hold exactly the given entries, in order. It is how a
// list that follows an outside source is kept in step; the source owns its contents.
func (s Store) MirrorList(ctx context.Context, userID, listID int64, entryIDs []int64, expected ...string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR SHARE OF p,u", userID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return fault.Unauthenticated
	}
	var sourceKind, sourceRef *string
	if err = tx.QueryRow(ctx, "SELECT source_kind,source_ref FROM personal_lists WHERE profile_id=$1 AND id=$2 FOR UPDATE", userID, listID).Scan(&sourceKind, &sourceRef); err != nil {
		return err
	}
	if len(expected) == 2 && (sourceKind == nil || sourceRef == nil || *sourceKind != expected[0] || *sourceRef != expected[1]) {
		return fault.Conflict
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT l.kind FROM personal_lists l JOIN tracking_profiles p ON p.id=l.profile_id AND p.archived_at IS NULL JOIN users u ON u.id=p.account_id WHERE l.profile_id=$1 AND l.id=$2 AND u.active FOR UPDATE OF l`, userID, listID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.NotFound
	}
	if err != nil {
		return err
	}
	if kind != "manual" {
		return fault.Invalid("Only manual lists can follow a source.")
	}
	if _, err = tx.Exec(ctx, "DELETE FROM list_entries WHERE profile_id=$1 AND list_id=$2 AND entry_id<>ALL($3)", userID, listID, entryIDs); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO list_entries(profile_id,list_id,entry_id,position)
		SELECT $1,$2,e.id,o.n FROM unnest($3::bigint[]) WITH ORDINALITY o(id,n) JOIN library_entries e ON e.profile_id=$1 AND e.id=o.id
		ON CONFLICT (list_id,entry_id) DO UPDATE SET position=EXCLUDED.position`, userID, listID, entryIDs); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE personal_lists SET synced_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE profile_id=$1 AND id=$2", userID, listID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListSourceURL is the address of a followed list.
func ListSourceURL(kind, ref string) string {
	user, slug, _ := strings.Cut(ref, "/")
	switch kind {
	case "trakt":
		return "https://trakt.tv/users/" + user + "/lists/" + slug
	case "mdblist":
		return "https://mdblist.com/lists/" + user + "/" + slug
	}
	return ""
}
