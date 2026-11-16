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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ---------------------------------------------------------------- tags

type Tag struct {
	ID      int64  `json:"id,string"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Entries int    `json:"entries"`
}

type TagInput struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

var colorPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func (in *TagInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Color = strings.ToLower(in.Color)
	if n := len([]rune(in.Name)); n < 1 || n > 50 || strings.ContainsAny(in.Name, "\n\r\t") {
		return fault.Invalid("A tag name has 1 to 50 characters on one line.")
	}
	if in.Color != "" && !colorPattern.MatchString(in.Color) {
		return fault.Invalid("A tag colour is a hex value such as #5f74ee.")
	}
	return nil
}

func uniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

var duplicateTag = &fault.Error{Status: 409, Code: "duplicate_tag", Message: "You already have a tag with that name."}

// Tags lists every tag with how many entries carry it. People keep dozens, not
// thousands, so the list is unpaged but capped.
func (s Store) Tags(ctx context.Context, a identity.Actor) ([]Tag, error) {
	out := []Tag{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.name,t.color,(SELECT count(*) FROM entry_tags x WHERE x.tag_id=t.id)
		FROM tags t WHERE t.profile_id=$1 ORDER BY lower(t.name) LIMIT 500`, a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Tag
		if err = rows.Scan(&t.ID, &t.Name, &t.Color, &t.Entries); err != nil {
			return out, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s Store) SaveTag(ctx context.Context, a identity.Actor, id int64, key string, in TagInput) (Mutation, error) {
	if err := in.validate(); err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "library:write", key, "tag.save/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		out := Mutation{ID: id, Version: 1}
		var err error
		if id == 0 {
			var n int
			if err = tx.QueryRow(ctx, "SELECT count(*) FROM tags WHERE profile_id=$1", a.TrackingID()).Scan(&n); err != nil {
				return out, err
			}
			if n >= 500 {
				return out, fault.Invalid("You can keep up to 500 tags.")
			}
			err = tx.QueryRow(ctx, "INSERT INTO tags(profile_id,name,color) VALUES($1,$2,$3) RETURNING id", a.TrackingID(), in.Name, in.Color).Scan(&out.ID)
		} else {
			var tag pgconn.CommandTag
			tag, err = tx.Exec(ctx, "UPDATE tags SET name=$3,color=$4 WHERE profile_id=$1 AND id=$2", a.TrackingID(), id, in.Name, in.Color)
			if err == nil && tag.RowsAffected() == 0 {
				err = fault.NotFound
			}
		}
		if uniqueViolation(err) {
			err = duplicateTag
		}
		return out, err
	})
}

func (s Store) DeleteTag(ctx context.Context, a identity.Actor, id int64, key string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "tag.delete/"+strconv.FormatInt(id, 10), id, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		tag, err := tx.Exec(ctx, "DELETE FROM tags WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return Mutation{ID: id, Version: 1, Deleted: true}, err
	})
}

// EntryTags returns the tags on one entry.
func (s Store) EntryTags(ctx context.Context, a identity.Actor, entryID int64) ([]Tag, error) {
	out := []Tag{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id,t.name,t.color FROM entry_tags x JOIN tags t ON t.id=x.tag_id
		WHERE x.profile_id=$1 AND x.entry_id=$2 ORDER BY lower(t.name)`, a.TrackingID(), entryID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Tag
		if err = rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return out, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- bulk actions

type BulkInput struct {
	EntryIDs []string `json:"entry_ids"`
	Action   string   `json:"action"` // add_tags, remove_tags, set_status, add_to_list, remove_from_list, delete
	TagIDs   []string `json:"tag_ids,omitempty"`
	Status   string   `json:"status,omitempty"`
	ListID   string   `json:"list_id,omitempty"`
}

type BulkResult struct {
	Changed int  `json:"changed"`
	Replay  bool `json:"replayed"`
}

func parseIDs(values []string, max int, what string) ([]int64, error) {
	if len(values) == 0 || len(values) > max {
		return nil, fault.Invalid("Choose 1 to " + strconv.Itoa(max) + " " + what + ".")
	}
	out := make([]int64, 0, len(values))
	for _, v := range values {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return nil, fault.Invalid("Invalid " + what + ".")
		}
		out = append(out, n)
	}
	return out, nil
}

// Bulk applies one action to up to 200 of the caller's entries in a single transaction.
// Entries that already have the requested state are left untouched and not counted.
func (s Store) Bulk(ctx context.Context, a identity.Actor, key string, in BulkInput) (BulkResult, error) {
	var out BulkResult
	ids, err := parseIDs(in.EntryIDs, 200, "entries")
	if err != nil {
		return out, err
	}
	scope := "library:write"
	var tags []int64
	var list int64
	switch in.Action {
	case "add_tags", "remove_tags":
		if tags, err = parseIDs(in.TagIDs, 20, "tags"); err != nil {
			return out, err
		}
	case "set_status":
		if !validStatus(in.Status) {
			return out, fault.Invalid("Choose a supported status.")
		}
	case "add_to_list", "remove_from_list":
		scope = "lists:write"
		if list, err = strconv.ParseInt(in.ListID, 10, 64); err != nil || list < 1 {
			return out, fault.Invalid("Choose a list.")
		}
	case "delete":
	default:
		return out, fault.Invalid("Unsupported bulk action.")
	}
	m, err := s.mutate(ctx, a, scope, key, "library.bulk", in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		// Lock the caller's entries in a stable order; any foreign or missing id fails the batch.
		var owned []int64
		rows, err := tx.Query(ctx, "SELECT id FROM library_entries WHERE profile_id=$1 AND id=ANY($2) ORDER BY id FOR UPDATE", a.TrackingID(), ids)
		if err != nil {
			return Mutation{}, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return Mutation{}, err
			}
			owned = append(owned, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return Mutation{}, err
		}
		if len(owned) != len(dedupeIDs(ids)) {
			return Mutation{}, fault.NotFound
		}
		var tag pgconn.CommandTag
		switch in.Action {
		case "add_tags", "remove_tags":
			var n int
			if err = tx.QueryRow(ctx, "SELECT count(*) FROM tags WHERE profile_id=$1 AND id=ANY($2)", a.TrackingID(), tags).Scan(&n); err != nil {
				return Mutation{}, err
			}
			if n != len(dedupeIDs(tags)) {
				return Mutation{}, fault.NotFound
			}
			if in.Action == "add_tags" {
				tag, err = tx.Exec(ctx, `INSERT INTO entry_tags(profile_id,tag_id,entry_id) SELECT $1,t,e FROM unnest($2::bigint[]) t, unnest($3::bigint[]) e ON CONFLICT DO NOTHING`, a.TrackingID(), tags, owned)
			} else {
				tag, err = tx.Exec(ctx, `DELETE FROM entry_tags WHERE profile_id=$1 AND tag_id=ANY($2) AND entry_id=ANY($3)`, a.TrackingID(), tags, owned)
			}
		case "set_status":
			rows, err := tx.Query(ctx, `UPDATE library_entries SET status=$3,
				progress=CASE WHEN $3='completed' AND total IS NOT NULL THEN total WHEN $3='rated' THEN 0 ELSE progress END,
				completed_cycles=completed_cycles + CASE WHEN status<>'completed' AND $3='completed' THEN 1 WHEN status='completed' AND $3<>'completed' AND completed_cycles>0 THEN -1 ELSE 0 END,
				version=version+1, updated_at=clock_timestamp()
				WHERE profile_id=$1 AND id=ANY($2) AND status<>$3 RETURNING id`, a.TrackingID(), owned, in.Status)
			if err != nil {
				return Mutation{}, err
			}
			var changed []int64
			for rows.Next() {
				var id int64
				if err = rows.Scan(&id); err != nil {
					rows.Close()
					return Mutation{}, err
				}
				changed = append(changed, id)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return Mutation{}, err
			}
			for _, id := range changed {
				if err = event(ctx, tx, a, id, "updated"); err != nil {
					return Mutation{}, err
				}
				if in.Status == "completed" {
					if err = completionPlay(ctx, tx, a, id); err != nil {
						return Mutation{}, err
					}
				}
			}
			return Mutation{ID: int64(len(changed)), Version: 1}, nil
		case "add_to_list", "remove_from_list":
			var kind string
			err = tx.QueryRow(ctx, "SELECT kind FROM personal_lists WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), list).Scan(&kind)
			if errors.Is(err, pgx.ErrNoRows) {
				return Mutation{}, fault.NotFound
			}
			if err != nil {
				return Mutation{}, err
			}
			if kind != "manual" {
				return Mutation{}, fault.Invalid("Smart lists choose their own titles.")
			}
			if in.Action == "add_to_list" {
				tag, err = tx.Exec(ctx, `INSERT INTO list_entries(profile_id,list_id,entry_id,position)
					SELECT $1,$2,e,COALESCE((SELECT max(position) FROM list_entries WHERE list_id=$2),0)+row_number() OVER (ORDER BY e)
					FROM unnest($3::bigint[]) e ON CONFLICT DO NOTHING`, a.TrackingID(), list, owned)
			} else {
				tag, err = tx.Exec(ctx, "DELETE FROM list_entries WHERE profile_id=$1 AND list_id=$2 AND entry_id=ANY($3)", a.TrackingID(), list, owned)
			}
			if err == nil && tag.RowsAffected() > 0 {
				_, err = tx.Exec(ctx, "UPDATE personal_lists SET version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND id=$2", a.TrackingID(), list)
			}
		case "delete":
			tag, err = tx.Exec(ctx, "DELETE FROM library_entries WHERE profile_id=$1 AND id=ANY($2)", a.TrackingID(), owned)
		}
		if err != nil {
			return Mutation{}, err
		}
		return Mutation{ID: tag.RowsAffected(), Version: 1}, nil
	})
	return BulkResult{Changed: int(m.ID), Replay: m.Replayed}, err
}

func dedupeIDs(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------- owned copies

type Copy struct {
	ID            int64          `json:"id,string"`
	EntryID       int64          `json:"entry_id,string"`
	EntryTitle    string         `json:"entry_title"`
	MediaType     string         `json:"media_type"`
	ImageURL      string         `json:"image_url"`
	CatalogItemID *int64         `json:"catalog_item_id,string"`
	ScopeTitle    string         `json:"scope_title"`
	Format        string         `json:"format"`
	Edition       string         `json:"edition"`
	Resolution    string         `json:"resolution"`
	HDR           string         `json:"hdr"`
	Audio         string         `json:"audio"`
	BitrateKbps   *int           `json:"bitrate_kbps"`
	AcquiredOn    *string        `json:"acquired_on"`
	PriceCents    *int           `json:"price_cents"`
	Currency      string         `json:"currency"`
	Store         string         `json:"store"`
	Location      string         `json:"location"`
	Notes         string         `json:"notes"`
	Custom        map[string]any `json:"custom"`
	Source        string         `json:"source"`
	Version       int            `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type CopyInput struct {
	CatalogItemID *string        `json:"catalog_item_id"`
	Format        string         `json:"format"`
	Edition       string         `json:"edition"`
	Resolution    string         `json:"resolution"`
	HDR           string         `json:"hdr"`
	Audio         string         `json:"audio"`
	BitrateKbps   *int           `json:"bitrate_kbps"`
	AcquiredOn    *string        `json:"acquired_on"`
	PriceCents    *int           `json:"price_cents"`
	Currency      string         `json:"currency"`
	Store         string         `json:"store"`
	Location      string         `json:"location"`
	Notes         string         `json:"notes"`
	Custom        map[string]any `json:"custom"`
	Version       int            `json:"version"`
}

var formats = []string{"digital", "uhd_bluray", "bluray", "dvd", "vhs", "hardcover", "paperback", "ebook", "audiobook", "vinyl", "cd", "cassette", "game_physical", "game_digital", "boxed", "other"}

func (in *CopyInput) validate() error {
	if !oneOf(in.Format, formats...) {
		return fault.Invalid("Choose a supported format.")
	}
	if !oneOf(in.Resolution, "", "sd", "720p", "1080p", "2160p", "4320p") || !oneOf(in.HDR, "", "hdr10", "hdr10plus", "dolby_vision", "hlg") {
		return fault.Invalid("Choose a supported resolution and HDR format.")
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency != "" && !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(in.Currency) {
		return fault.Invalid("Currency is a three-letter code such as EUR.")
	}
	if in.AcquiredOn != nil {
		if _, err := time.Parse("2006-01-02", *in.AcquiredOn); err != nil {
			return fault.Invalid("Acquired date must be YYYY-MM-DD.")
		}
	}
	for _, f := range []struct {
		v   string
		max int
	}{{in.Edition, 200}, {in.Audio, 100}, {in.Store, 200}, {in.Location, 200}, {in.Notes, 2000}} {
		if len([]rune(f.v)) > f.max {
			return fault.Invalid("A copy field is too long.")
		}
	}
	if (in.BitrateKbps != nil && (*in.BitrateKbps < 1 || *in.BitrateKbps > 1000000)) || (in.PriceCents != nil && (*in.PriceCents < 0 || *in.PriceCents > 100000000)) {
		return fault.Invalid("Bitrate or price is out of range.")
	}
	if in.Custom == nil {
		in.Custom = map[string]any{}
	}
	if len(in.Custom) > 50 {
		return fault.Invalid("A copy has at most 50 custom values.")
	}
	return nil
}

const copyColumns = `o.id,o.entry_id,e.title,e.media_type,COALESCE(c.image_url,''),o.catalog_item_id,COALESCE(s.title,''),o.format,o.edition,o.resolution,o.hdr,o.audio,
o.bitrate_kbps,to_char(o.acquired_on,'YYYY-MM-DD'),o.price_cents,o.currency,o.store,o.location,o.notes,o.custom,o.source,o.version,o.created_at,o.updated_at
FROM owned_copies o JOIN library_entries e ON e.profile_id=o.profile_id AND e.id=o.entry_id LEFT JOIN catalog_items c ON c.id=e.catalog_item_id LEFT JOIN catalog_items s ON s.id=o.catalog_item_id`

func scanCopy(row pgx.Row) (Copy, error) {
	var c Copy
	err := row.Scan(&c.ID, &c.EntryID, &c.EntryTitle, &c.MediaType, &c.ImageURL, &c.CatalogItemID, &c.ScopeTitle, &c.Format, &c.Edition, &c.Resolution, &c.HDR, &c.Audio,
		&c.BitrateKbps, &c.AcquiredOn, &c.PriceCents, &c.Currency, &c.Store, &c.Location, &c.Notes, &c.Custom, &c.Source, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.NotFound
	}
	return c, err
}

// Copies lists owned copies: of one entry when entryID is set, else the whole collection
// newest first, capped at 500 for one request.
func (s Store) Copies(ctx context.Context, a identity.Actor, entryID int64, format string) ([]Copy, error) {
	out := []Copy{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	if format != "" && !oneOf(format, formats...) {
		return out, fault.Invalid("Choose a supported format.")
	}
	query := "SELECT " + copyColumns + " WHERE o.profile_id=$1"
	args := []any{a.TrackingID()}
	if entryID > 0 {
		args = append(args, entryID)
		query += " AND o.entry_id=$2"
	}
	if format != "" {
		args = append(args, format)
		query += " AND o.format=$" + strconv.Itoa(len(args))
	}
	rows, err := s.Pool.Query(ctx, query+" ORDER BY o.created_at DESC, o.id DESC LIMIT 500", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCopy(rows)
		if err != nil {
			return out, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveCopy creates a copy for an entry (copyID 0) or replaces one with version fencing.
// A season or volume scope must belong to the entry's own catalog title.
func (s Store) SaveCopy(ctx context.Context, a identity.Actor, entryID, copyID int64, key string, in CopyInput) (Mutation, error) {
	if err := in.validate(); err != nil {
		return Mutation{}, err
	}
	return s.mutate(ctx, a, "library:write", key, "copy.save/"+strconv.FormatInt(entryID, 10)+"/"+strconv.FormatInt(copyID, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		return s.SaveCopyTx(ctx, tx, a, entryID, copyID, in)
	})
}

// SaveCopyTx shares native validation and ownership checks with an atomic source import.
// The caller owns the transaction; current profile authority is rechecked here.
func (s Store) SaveCopyTx(ctx context.Context, tx pgx.Tx, a identity.Actor, entryID, copyID int64, in CopyInput) (Mutation, error) {
	if err := in.validate(); err != nil {
		return Mutation{}, err
	}
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:write")
	if err != nil {
		return Mutation{}, err
	}
	a = fresh
	if err = lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return Mutation{}, err
	}
	var root *int64
	err = tx.QueryRow(ctx, "SELECT catalog_item_id FROM library_entries WHERE profile_id=$1 AND id=$2 FOR UPDATE", a.TrackingID(), entryID).Scan(&root)
	if errors.Is(err, pgx.ErrNoRows) {
		return Mutation{}, fault.NotFound
	}
	if err != nil {
		return Mutation{}, err
	}
	var scope *int64
	if in.CatalogItemID != nil && *in.CatalogItemID != "" {
		id, err := strconv.ParseInt(*in.CatalogItemID, 10, 64)
		if err != nil || root == nil {
			return Mutation{}, fault.Invalid("A copy can cover part of a catalog title only.")
		}
		var ok bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM catalog_items WHERE id=$1 AND root_id=$2)", id, *root).Scan(&ok); err != nil {
			return Mutation{}, err
		}
		if !ok {
			return Mutation{}, fault.Invalid("That season or volume belongs to another title.")
		}
		scope = &id
	}
	if err := validateCustomFields(ctx, tx, a.TrackingID(), copyID, in.Custom); err != nil {
		return Mutation{}, err
	}
	custom, err := json.Marshal(in.Custom)
	if err != nil {
		return Mutation{}, err
	}
	out := Mutation{ID: copyID}
	if copyID == 0 {
		var n int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM owned_copies WHERE profile_id=$1 AND entry_id=$2", a.TrackingID(), entryID).Scan(&n); err != nil {
			return out, err
		}
		if n >= 100 {
			return out, fault.Invalid("A title can have at most 100 copies.")
		}
		err = tx.QueryRow(ctx, `INSERT INTO owned_copies(profile_id,entry_id,catalog_item_id,format,edition,resolution,hdr,audio,bitrate_kbps,acquired_on,price_cents,currency,store,location,notes,custom)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::date,$11,$12,$13,$14,$15,$16) RETURNING id,version`,
			a.TrackingID(), entryID, scope, in.Format, in.Edition, in.Resolution, in.HDR, in.Audio, in.BitrateKbps, in.AcquiredOn, in.PriceCents, in.Currency, in.Store, in.Location, in.Notes, custom).Scan(&out.ID, &out.Version)
		return out, err
	}
	err = tx.QueryRow(ctx, `UPDATE owned_copies SET catalog_item_id=$4,format=$5,edition=$6,resolution=$7,hdr=$8,audio=$9,bitrate_kbps=$10,acquired_on=$11::date,price_cents=$12,
		currency=$13,store=$14,location=$15,notes=$16,custom=$17,version=version+1,updated_at=clock_timestamp()
		WHERE profile_id=$1 AND entry_id=$2 AND id=$3 AND version=$18 RETURNING version`,
		a.TrackingID(), entryID, copyID, scope, in.Format, in.Edition, in.Resolution, in.HDR, in.Audio, in.BitrateKbps, in.AcquiredOn, in.PriceCents, in.Currency, in.Store, in.Location, in.Notes, custom, in.Version).Scan(&out.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if e := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM owned_copies WHERE profile_id=$1 AND entry_id=$2 AND id=$3)", a.TrackingID(), entryID, copyID).Scan(&exists); e != nil {
			return out, e
		}
		if exists {
			return out, fault.Conflict
		}
		return out, fault.NotFound
	}
	return out, err
}

func (s Store) DeleteCopy(ctx context.Context, a identity.Actor, entryID, copyID int64, key string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "copy.delete/"+strconv.FormatInt(entryID, 10)+"/"+strconv.FormatInt(copyID, 10), copyID, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		tag, err := tx.Exec(ctx, "DELETE FROM owned_copies WHERE profile_id=$1 AND entry_id=$2 AND id=$3", a.TrackingID(), entryID, copyID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return Mutation{ID: copyID, Version: 1, Deleted: true}, err
	})
}

// ---------------------------------------------------------------- collection fields

type Field struct {
	ID       int64    `json:"id,string"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Choices  []string `json:"choices"`
	Position int      `json:"position"`
}

type FieldInput struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Choices  []string `json:"choices"`
	Position int      `json:"position"`
}

func (s Store) Fields(ctx context.Context, a identity.Actor) ([]Field, error) {
	out := []Field{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, "SELECT id,name,kind,choices,position FROM collection_fields WHERE profile_id=$1 ORDER BY position,id LIMIT 100", a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var f Field
		if err = rows.Scan(&f.ID, &f.Name, &f.Kind, &f.Choices, &f.Position); err != nil {
			return out, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s Store) SaveField(ctx context.Context, a identity.Actor, id int64, key string, in FieldInput) (Mutation, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := len([]rune(in.Name)); n < 1 || n > 60 || !oneOf(in.Kind, "text", "number", "date", "boolean", "choice") || len(in.Choices) > 50 || in.Position < 0 || in.Position > 1000 {
		return Mutation{}, fault.Invalid("A field has a 1 to 60 character name, a supported type and at most 50 choices.")
	}
	in.Choices = dedupe(in.Choices)
	if in.Kind != "choice" {
		in.Choices = []string{}
	}
	return s.mutate(ctx, a, "library:write", key, "field.save/"+strconv.FormatInt(id, 10), in, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		out := Mutation{ID: id, Version: 1}
		var err error
		if id == 0 {
			err = tx.QueryRow(ctx, "INSERT INTO collection_fields(profile_id,name,kind,choices,position) VALUES($1,$2,$3,$4,$5) RETURNING id", a.TrackingID(), in.Name, in.Kind, in.Choices, in.Position).Scan(&out.ID)
		} else {
			var tag pgconn.CommandTag
			tag, err = tx.Exec(ctx, "UPDATE collection_fields SET name=$3,kind=$4,choices=$5,position=$6 WHERE profile_id=$1 AND id=$2", a.TrackingID(), id, in.Name, in.Kind, in.Choices, in.Position)
			if err == nil && tag.RowsAffected() == 0 {
				err = fault.NotFound
			}
		}
		if uniqueViolation(err) {
			err = &fault.Error{Status: 409, Code: "duplicate_field", Message: "You already have a field with that name."}
		}
		return out, err
	})
}

func (s Store) DeleteField(ctx context.Context, a identity.Actor, id int64, key string) (Mutation, error) {
	return s.mutate(ctx, a, "library:write", key, "field.delete/"+strconv.FormatInt(id, 10), id, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		tag, err := tx.Exec(ctx, "DELETE FROM collection_fields WHERE profile_id=$1 AND id=$2", a.TrackingID(), id)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		// Values stay on copies under their key; removing a field hides it without losing data.
		return Mutation{ID: id, Version: 1, Deleted: true}, err
	})
}

// ---------------------------------------------------------------- preferences

// Preferences are per-account display choices. Unknown keys are rejected so the stored
// document stays a known shape.
type Preferences struct {
	DeclineRecommendations bool      `json:"decline_recommendations"`
	HomeRows               []HomeRow `json:"home_rows"`
	// CardFields chooses the details shown under posters, per media type. A type left
	// out uses the default.
	CardFields map[string][]string `json:"card_fields,omitempty"`
	// ReleaseRegion (a two-letter country code) and CalendarReleaseTypes (theatrical,
	// digital, physical) show a movie's regional release dates in the calendar. Empty
	// keeps the single primary release date.
	ReleaseRegion        string   `json:"release_region,omitempty"`
	CalendarReleaseTypes []string `json:"calendar_release_types,omitempty"`
	RatingScale          string   `json:"rating_scale"`     // ten, five, hundred, off
	TitleLanguage        string   `json:"title_language"`   // localized, original
	ProgressDisplay      string   `json:"progress_display"` // count, percent
	LibraryView          string   `json:"library_view"`     // grid, list
	LibrarySort          string   `json:"library_sort"`
	WeekStart            string   `json:"week_start"` // monday, sunday
	// AnimeShape is how anime is found and tracked: flat (each season on its own, from
	// AniList) or grouped (whole shows with seasons, from TMDB).
	AnimeShape string `json:"anime_shape"`
	// Language is the interface language; empty follows the browser.
	Language      string `json:"language"`
	PublicProfile bool   `json:"public_profile"`
	// Onboarded is set once the person finishes or skips first-run setup.
	Onboarded bool `json:"onboarded"`
	Version   int  `json:"version"`
}

// HomeRow is one configurable row: a built-in row, a list, or a mix of library titles
// across media types, shown or hidden.
type HomeRow struct {
	Kind    string `json:"kind"` // up_next, continue, upcoming, planned, recently_finished, list, mix
	ListID  string `json:"list_id,omitempty"`
	Visible bool   `json:"visible"`
	// A mix row names its own title and selects library titles by type and status.
	ID         string   `json:"id,omitempty"`
	Title      string   `json:"title,omitempty"`
	MediaTypes []string `json:"media_types,omitempty"`
	Statuses   []string `json:"statuses,omitempty"`
	Sort       string   `json:"sort,omitempty"`
	Limit      int      `json:"limit,omitempty"`
}

var cardFieldNames = []string{"type", "year", "status", "progress", "rating"}

var builtInRows = []string{"up_next", "continue", "upcoming", "planned", "recently_finished"}

func defaultPreferences() Preferences {
	p := Preferences{RatingScale: "ten", TitleLanguage: "localized", ProgressDisplay: "count", LibraryView: "grid", LibrarySort: "added", WeekStart: "monday", AnimeShape: "flat", Version: 0}
	for _, r := range builtInRows {
		p.HomeRows = append(p.HomeRows, HomeRow{Kind: r, Visible: true})
	}
	return p
}

func (p *Preferences) validate() error {
	invalid := fault.Invalid("Unsupported preference value.")
	if p.AnimeShape == "" {
		p.AnimeShape = "flat"
	}
	if !oneOf(p.Language, "", "en", "de", "fr", "nl", "es") {
		return fault.Invalid("language must be empty or one of en, de, fr, nl and es.")
	}
	if !oneOf(p.AnimeShape, "flat", "grouped") {
		return fault.Invalid("anime_shape must be flat or grouped.")
	}
	if !oneOf(p.RatingScale, "ten", "five", "hundred", "off") || !(oneOf(p.TitleLanguage, "localized", "original") || languageCode(p.TitleLanguage)) || !oneOf(p.ProgressDisplay, "count", "percent") ||
		!oneOf(p.LibraryView, "grid", "list") || !oneOf(p.WeekStart, "monday", "sunday") {
		return invalid
	}
	if _, ok := sorts[p.LibrarySort]; !ok || p.LibrarySort == "position" {
		return invalid
	}
	if len(p.HomeRows) > 20 {
		return fault.Invalid("Home has at most 20 rows.")
	}
	seen := map[string]bool{}
	for i := range p.HomeRows {
		r := &p.HomeRows[i]
		id := r.Kind + ":" + r.ListID + ":" + r.ID
		if seen[id] || (r.Kind == "list") == (r.ListID == "") || !oneOf(r.Kind, append(builtInRows, "list", "mix")...) {
			return fault.Invalid("Each home row is a known row, one list or one mix, listed once.")
		}
		seen[id] = true
		if r.Kind != "mix" {
			if r.ID != "" || r.Title != "" || len(r.MediaTypes) > 0 || len(r.Statuses) > 0 || r.Sort != "" || r.Limit != 0 {
				return fault.Invalid("Only mix rows have a title, types, statuses, sort and length.")
			}
			continue
		}
		if err := r.validateMix(); err != nil {
			return err
		}
	}
	p.ReleaseRegion = strings.ToUpper(strings.TrimSpace(p.ReleaseRegion))
	if p.ReleaseRegion != "" && !regionPattern.MatchString(p.ReleaseRegion) {
		return fault.Invalid("release_region is a two-letter country code.")
	}
	have := map[string]bool{}
	for _, t := range p.CalendarReleaseTypes {
		if have[t] || !oneOf(t, "theatrical", "digital", "physical") {
			return fault.Invalid("calendar_release_types are theatrical, digital and physical, each once.")
		}
		have[t] = true
	}
	if len(p.CardFields) > len(mediaTypes) {
		return fault.Invalid("Card details are set per media type.")
	}
	for kind, fields := range p.CardFields {
		if !oneOf(kind, mediaTypes...) || len(fields) > len(cardFieldNames) {
			return fault.Invalid("Card details are set per media type.")
		}
		have := map[string]bool{}
		for _, f := range fields {
			if have[f] || !oneOf(f, cardFieldNames...) {
				return fault.Invalid("Card details are type, year, status, progress and rating, each once.")
			}
			have[f] = true
		}
	}
	return nil
}

func (s Store) Preferences(ctx context.Context, a identity.Actor) (Preferences, error) {
	p := defaultPreferences()
	if !a.Allows("profile:read") && !a.Browser() {
		return p, fault.Forbidden
	}
	var raw []byte
	var version int
	err := s.Pool.QueryRow(ctx, "SELECT prefs,version FROM user_preferences WHERE profile_id=$1", a.TrackingID()).Scan(&raw, &version)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return p, err
	}
	if err == nil {
		_ = json.Unmarshal(raw, &p)
		p.Version = version
	}
	if err = s.Pool.QueryRow(ctx, "SELECT public_profile FROM tracking_profiles WHERE id=$1", a.TrackingID()).Scan(&p.PublicProfile); err != nil {
		return p, err
	}
	return p, nil
}

// SavePreferences replaces the document. Version 0 means "no saved preferences yet".
func (s Store) SavePreferences(ctx context.Context, a identity.Actor, in Preferences) (Preferences, error) {
	if !a.Browser() {
		return in, fault.Forbidden
	}
	if err := in.validate(); err != nil {
		return in, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return in, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, ""); err != nil {
		return in, err
	}

	doc := in
	doc.Version, doc.PublicProfile = 0, false
	raw, err := json.Marshal(doc)
	if err != nil {
		return in, err
	}
	var version int
	if in.Version == 0 {
		err = tx.QueryRow(ctx, "INSERT INTO user_preferences(profile_id,prefs) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING version", a.TrackingID(), raw).Scan(&version)
	} else {
		err = tx.QueryRow(ctx, "UPDATE user_preferences SET prefs=$2,version=version+1,updated_at=clock_timestamp() WHERE profile_id=$1 AND version=$3 RETURNING version", a.TrackingID(), raw, in.Version).Scan(&version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return in, fault.Conflict
	}
	if err != nil {
		return in, err
	}
	if _, err = tx.Exec(ctx, "UPDATE tracking_profiles SET public_profile=$2 WHERE id=$1", a.TrackingID(), in.PublicProfile); err != nil {
		return in, err
	}
	in.Version = version
	return in, tx.Commit(ctx)
}

// ---------------------------------------------------------------- skipped episodes

// Skip marks catalog items of a series as deliberately skipped in the current cycle, or
// clears them. Skips count toward completion but never appear as consumption.
func (s Store) Skip(ctx context.Context, a identity.Actor, entryID int64, key string, version int, itemIDs []string, skip bool) (Mutation, error) {
	ids, err := parseIDs(itemIDs, 500, "episodes")
	if err != nil {
		return Mutation{}, err
	}
	input := struct {
		Version int
		Items   []int64
		Skip    bool
	}{version, ids, skip}
	return s.mutate(ctx, a, "library:write", key, "entry.skip/"+strconv.FormatInt(entryID, 10), input, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		e, err := current(ctx, tx, a, entryID, version)
		if err != nil {
			return Mutation{}, err
		}
		if e.CatalogID == nil {
			return Mutation{}, fault.Invalid("Only catalog titles have episodes to skip.")
		}
		var valid int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM catalog_items WHERE id=ANY($1) AND root_id=$2 AND kind IN ('episode','podcast_episode','comic_issue')", dedupeIDs(ids), *e.CatalogID).Scan(&valid); err != nil {
			return Mutation{}, err
		}
		if valid != len(dedupeIDs(ids)) {
			return Mutation{}, fault.Invalid("Every skipped item must be an episode of this title.")
		}
		if skip {
			_, err = tx.Exec(ctx, `INSERT INTO skipped_items(profile_id,entry_id,catalog_item_id,cycle) SELECT $1,$2,x,$3 FROM unnest($4::bigint[]) x ON CONFLICT DO NOTHING`, a.TrackingID(), entryID, e.Cycle, ids)
		} else {
			_, err = tx.Exec(ctx, "DELETE FROM skipped_items WHERE profile_id=$1 AND entry_id=$2 AND cycle=$3 AND catalog_item_id=ANY($4)", a.TrackingID(), entryID, e.Cycle, ids)
		}
		if err != nil {
			return Mutation{}, err
		}
		return s.reproject(ctx, tx, a, e, "updated")
	})
}

// Skipped lists the skipped items of the current or a given cycle.
func (s Store) Skipped(ctx context.Context, a identity.Actor, entryID int64, cycle int) ([]string, error) {
	out := []string{}
	if !a.Allows("library:read") {
		return out, fault.Forbidden
	}
	rows, err := s.Pool.Query(ctx, `SELECT x.catalog_item_id::text FROM skipped_items x JOIN library_entries e ON e.profile_id=x.profile_id AND e.id=x.entry_id
		WHERE x.profile_id=$1 AND x.entry_id=$2 AND x.cycle=CASE WHEN $3>0 THEN $3 ELSE e.cycle END ORDER BY x.catalog_item_id`, a.TrackingID(), entryID, cycle)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return out, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetEntryTags makes an entry carry exactly the given tags.
func (s Store) SetEntryTags(ctx context.Context, a identity.Actor, entryID int64, key string, tagIDs []string) ([]Tag, error) {
	ids := []int64{}
	if len(tagIDs) > 0 {
		var err error
		if ids, err = parseIDs(tagIDs, 50, "tags"); err != nil {
			return nil, err
		}
	}
	ids = dedupeIDs(ids)
	_, err := s.mutate(ctx, a, "library:write", key, "entry.tags/"+strconv.FormatInt(entryID, 10), ids, func(tx pgx.Tx, a identity.Actor) (Mutation, error) {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM library_entries WHERE profile_id=$1 AND id=$2)", a.TrackingID(), entryID).Scan(&exists); err != nil {
			return Mutation{}, err
		}
		if !exists {
			return Mutation{}, fault.NotFound
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM tags WHERE profile_id=$1 AND id=ANY($2)", a.TrackingID(), ids).Scan(&n); err != nil {
			return Mutation{}, err
		}
		if n != len(ids) {
			return Mutation{}, fault.NotFound
		}
		if _, err := tx.Exec(ctx, "DELETE FROM entry_tags WHERE profile_id=$1 AND entry_id=$2 AND NOT tag_id=ANY($3)", a.TrackingID(), entryID, ids); err != nil {
			return Mutation{}, err
		}
		_, err := tx.Exec(ctx, "INSERT INTO entry_tags(profile_id,tag_id,entry_id) SELECT $1,t,$2 FROM unnest($3::bigint[]) t ON CONFLICT DO NOTHING", a.TrackingID(), entryID, ids)
		return Mutation{ID: entryID, Version: 1}, err
	})
	if err != nil {
		return nil, err
	}
	return s.EntryTags(ctx, a, entryID)
}

// languageCode accepts a two-letter ISO 639-1 code such as "de".
func languageCode(s string) bool {
	return len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z'
}

// validateMix checks a mix row: a short title, one or more media types, optional
// statuses, a library sort and a length of up to 30 titles.
func (r *HomeRow) validateMix() error {
	r.Title = strings.TrimSpace(r.Title)
	if n := len([]rune(r.Title)); n < 1 || n > 60 || !idPattern.MatchString(r.ID) {
		return fault.Invalid("Give each mix row an id and a title of 1 to 60 characters.")
	}
	if len(r.MediaTypes) < 1 || len(r.MediaTypes) > len(mediaTypes) || len(r.Statuses) > 6 {
		return fault.Invalid("A mix row shows one or more media types.")
	}
	for _, t := range r.MediaTypes {
		if !oneOf(t, mediaTypes...) {
			return fault.Invalid("A mix row shows one or more media types.")
		}
	}
	for _, s := range r.Statuses {
		if !validStatus(s) {
			return fault.Invalid("Choose supported statuses for the mix row.")
		}
	}
	if r.Sort == "" {
		r.Sort = "activity"
	}
	if _, ok := sorts[r.Sort]; !ok || r.Sort == "position" {
		return fault.Invalid("Choose a supported order for the mix row.")
	}
	if r.Limit == 0 {
		r.Limit = 20
	}
	if r.Limit < 1 || r.Limit > 30 {
		return fault.Invalid("A mix row shows 1 to 30 titles.")
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
var regionPattern = regexp.MustCompile(`^[A-Z]{2}$`)
