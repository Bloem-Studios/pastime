package library

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/page"
	"github.com/jackc/pgx/v5"
)

// Rules is the stored form of a smart list: the filter fields that select entries.
type Rules struct {
	Query      string   `json:"query,omitempty"`
	MediaTypes []string `json:"media_types,omitempty"`
	Statuses   []string `json:"statuses,omitempty"`
	Genres     []string `json:"genres,omitempty"`
	TagIDs     []string `json:"tag_ids,omitempty"`
	MinRating  *int     `json:"min_rating,omitempty"`
	YearFrom   *int     `json:"year_from,omitempty"`
	YearTo     *int     `json:"year_to,omitempty"`
	Owned      *bool    `json:"owned,omitempty"`
}

func (r Rules) apply(f *Filter) error {
	f.Query, f.MediaTypes, f.Statuses, f.Genres, f.MinRating, f.YearFrom, f.YearTo, f.Owned = r.Query, r.MediaTypes, r.Statuses, r.Genres, r.MinRating, r.YearFrom, r.YearTo, r.Owned
	f.TagIDs = nil
	for _, id := range r.TagIDs {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n < 1 {
			return fault.Invalid("A smart list rule names an invalid tag.")
		}
		f.TagIDs = append(f.TagIDs, n)
	}
	return nil
}

// Validate normalizes rules so the same selection always stores identically.
func (r *Rules) Validate() error {
	var f Filter
	if err := r.apply(&f); err != nil {
		return err
	}
	if err := f.normalize(); err != nil {
		return err
	}
	r.Query, r.MediaTypes, r.Statuses, r.Genres = f.Query, f.MediaTypes, f.Statuses, f.Genres
	return nil
}

var sorts = map[string]struct {
	expr string
	asc  bool
}{
	"added":    {"e.id", false},
	"activity": {"COALESCE(e.last_activity_at,e.created_at)", false},
	"title":    {"lower(e.title)", true},
	"rating":   {"COALESCE(e.rating,-1)", false},
	"year":     {"COALESCE(e.publication_year,0)", false},
	"position": {"le.position", true},
}

// playedSort orders by the latest real play or finish, the date the library shows. Only
// browsing offers it: lists and saved views store their sort under a fixed set.
var playedSort = struct {
	expr string
	asc  bool
}{"COALESCE(GREATEST(" + lastPlayedSQL + ",e.finished_at),'0001-01-01 00:00:00+00'::timestamptz)", false}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func (f *Filter) normalize() error {
	f.Query = strings.TrimSpace(f.Query)
	if f.MediaType != "" {
		f.MediaTypes = append(f.MediaTypes, f.MediaType)
	}
	if f.Status != "" {
		f.Statuses = append(f.Statuses, f.Status)
	}
	f.MediaTypes, f.Statuses, f.Genres = dedupe(f.MediaTypes), dedupe(f.Statuses), dedupe(f.Genres)
	if len([]rune(f.Query)) > 100 || len(f.MediaTypes) > 11 || len(f.Statuses) > 6 || len(f.Genres) > 20 || len(f.TagIDs) > 20 {
		return fault.Invalid("Invalid library filters.")
	}
	for _, t := range f.MediaTypes {
		if !validType(t) {
			return fault.Invalid("Invalid library filters.")
		}
	}
	for _, t := range f.Statuses {
		if !validStatus(t) {
			return fault.Invalid("Invalid library filters.")
		}
	}
	for _, g := range f.Genres {
		if len(g) > 60 {
			return fault.Invalid("Invalid library filters.")
		}
	}
	if (f.Provider == "") != (f.ProviderID == "") || len(f.Provider) > 40 || len(f.ProviderID) > 200 {
		return fault.Invalid("provider and provider_id go together.")
	}
	if (f.MinRating != nil && (*f.MinRating < 0 || *f.MinRating > 10)) || (f.YearFrom != nil && (*f.YearFrom < 1 || *f.YearFrom > 9999)) || (f.YearTo != nil && (*f.YearTo < 1 || *f.YearTo > 9999)) {
		return fault.Invalid("Invalid library filters.")
	}
	sort.Slice(f.TagIDs, func(i, j int) bool { return f.TagIDs[i] < f.TagIDs[j] })
	return nil
}

func (f Filter) scope(userID int64) string {
	data, _ := json.Marshal([]any{userID, f.Query, f.MediaTypes, f.Statuses, f.Genres, f.TagIDs, f.MinRating, f.YearFrom, f.YearTo, f.Owned, f.ListID, f.Sort, f.Provider, f.ProviderID})
	return "library|" + string(data)
}

// Entries browses the caller's library. Every order is a keyset over an indexed
// expression, so deep pages cost the same as the first.
type entryReader interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s Store) Entries(ctx context.Context, a identity.Actor, f Filter) (page.Result[Summary], error) {
	return s.entries(ctx, a, f, s.Pool)
}

func (s Store) entries(ctx context.Context, a identity.Actor, f Filter, q entryReader) (page.Result[Summary], error) {
	out := page.Result[Summary]{Items: []Summary{}}
	if !a.Allows("library:read") || (f.ListID > 0 && !a.Allows("lists:read")) {
		return out, fault.Forbidden
	}
	limit, err := page.Limit(f.Limit)
	if err != nil {
		return out, err
	}
	manualList, err := resolveList(ctx, q, a, &f)
	if err != nil {
		return out, err
	}
	if f.Sort == "" {
		f.Sort = "added"
	}
	order, ok := sorts[f.Sort]
	if f.Sort == "played" {
		order, ok = playedSort, true
	}
	if !ok || (f.Sort == "position" && !manualList) {
		return out, fault.Invalid("Unsupported sort order.")
	}
	if err = f.normalize(); err != nil {
		return out, err
	}
	scope := f.scope(a.TrackingID())
	raw, lastID, more, err := page.DecodeValue(scope, f.Cursor)
	if err != nil {
		return out, err
	}
	source := from
	if manualList {
		source += " JOIN list_entries le ON le.profile_id=e.profile_id AND le.entry_id=e.id"
	}
	query := "SELECT " + columns + ", " + order.expr
	if manualList {
		query += ", le.note" + source
	} else {
		query += ", NULL::text" + source
	}
	where, args := entryWhere(f, a.TrackingID(), manualList)
	query += where
	if more {
		cmp := "<"
		if order.asc {
			cmp = ">"
		}
		var value any
		if f.anchored {
			// The anchor must itself match the filters, so a cursor cannot read the
			// sort value of an entry the reader cannot see.
			anchor := append(append([]any{}, args...), lastID)
			err = q.QueryRow(ctx, "SELECT "+order.expr+source+where+" AND e.id=$"+strconv.Itoa(len(anchor)), anchor...).Scan(&value)
			if errors.Is(err, pgx.ErrNoRows) {
				return out, fault.Invalid("The page cursor does not match these filters.")
			}
		} else {
			value, err = keyValue(f.Sort, raw)
		}
		if err != nil {
			return out, err
		}
		args = append(args, value, lastID)
		query += " AND (" + order.expr + ",e.id) " + cmp + " ($" + strconv.Itoa(len(args)-1) + ",$" + strconv.Itoa(len(args)) + ")"
	}
	dir := " DESC"
	if order.asc {
		dir = " ASC"
	}
	args = append(args, limit+1)
	query += " ORDER BY " + order.expr + dir + ",e.id" + dir + " LIMIT $" + strconv.Itoa(len(args))
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var keys []any
	for rows.Next() {
		var e Summary
		var key any
		if err = rows.Scan(append(fields(&e), &key, &e.ListNote)...); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		key := keys[limit-1]
		if f.anchored {
			key = nil
		}
		next := page.EncodeValue(scope, key, out.Items[limit-1].ID)
		out.NextCursor = &next
	}
	return out, nil
}

// resolveList applies a list filter: a smart list replaces the filters with its rules, a
// manual list restricts the entries to its members. It reports which one applies.
func resolveList(ctx context.Context, q entryReader, a identity.Actor, f *Filter) (bool, error) {
	if f.ListID == 0 {
		return false, nil
	}
	var kind, listSort string
	var raw []byte
	err := q.QueryRow(ctx, "SELECT kind,sort,rules FROM personal_lists WHERE profile_id=$1 AND id=$2", a.TrackingID(), f.ListID).Scan(&kind, &listSort, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fault.NotFound
	}
	if err != nil {
		return false, err
	}
	if f.Sort == "" {
		f.Sort = listSort
	}
	if kind != "smart" {
		return true, nil
	}
	var rules Rules
	if err = json.Unmarshal(raw, &rules); err != nil {
		return false, err
	}
	if err = rules.apply(f); err != nil {
		return false, err
	}
	f.ListID = 0
	if f.Sort == "position" {
		f.Sort = "added"
	}
	return false, nil
}

// Facets counts what a library view can narrow to. Each media type is counted under every
// active filter except the media type, each status under every one except the status, and
// the total under all of them. Only values with titles are listed, in a fixed order.
type Facets struct {
	Total      int           `json:"total"`
	MediaTypes []TypeCount   `json:"media_types"`
	Statuses   []StatusCount `json:"statuses"`
}
type TypeCount struct {
	MediaType string `json:"media_type"`
	Count     int    `json:"count"`
}
type StatusCount struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

var statusOrder = []string{"planned", "in_progress", "completed", "paused", "dropped", "rated"}

// Facets takes the filters of Entries; the cursor, limit and sort do not apply.
func (s Store) Facets(ctx context.Context, a identity.Actor, f Filter) (Facets, error) {
	out := Facets{MediaTypes: []TypeCount{}, Statuses: []StatusCount{}}
	if !a.Allows("library:read") || (f.ListID > 0 && !a.Allows("lists:read")) {
		return out, fault.Forbidden
	}
	manualList, err := resolveList(ctx, s.Pool, a, &f)
	if err != nil {
		return out, err
	}
	if err = f.normalize(); err != nil {
		return out, err
	}
	// One pass groups by both dimensions; each count then drops its own filter.
	types, statuses := f.MediaTypes, f.Statuses
	f.MediaTypes, f.Statuses = nil, nil
	query := "SELECT e.media_type,e.status,count(*)" + from
	if manualList {
		query += " JOIN list_entries le ON le.profile_id=e.profile_id AND le.entry_id=e.id"
	}
	where, args := entryWhere(f, a.TrackingID(), manualList)
	rows, err := s.Pool.Query(ctx, query+where+" GROUP BY 1,2", args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	byType, byStatus := map[string]int{}, map[string]int{}
	for rows.Next() {
		var mediaType, status string
		var n int
		if err = rows.Scan(&mediaType, &status, &n); err != nil {
			return out, err
		}
		typeOK, statusOK := len(types) == 0 || oneOf(mediaType, types...), len(statuses) == 0 || oneOf(status, statuses...)
		if statusOK {
			byType[mediaType] += n
		}
		if typeOK {
			byStatus[status] += n
		}
		if typeOK && statusOK {
			out.Total += n
		}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, v := range mediaTypes {
		if byType[v] > 0 {
			out.MediaTypes = append(out.MediaTypes, TypeCount{MediaType: v, Count: byType[v]})
		}
	}
	for _, v := range statusOrder {
		if byStatus[v] > 0 {
			out.Statuses = append(out.Statuses, StatusCount{Status: v, Count: byStatus[v]})
		}
	}
	return out, nil
}

// keyValue restores a cursor's sort value with the type the sort expression compares.
func keyValue(sortName string, raw json.RawMessage) (any, error) {
	invalid := fault.Invalid("The page cursor does not match these filters.")
	switch sortName {
	case "title":
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, invalid
		}
		return v, nil
	case "activity", "played":
		var v time.Time
		if json.Unmarshal(raw, &v) != nil {
			return nil, invalid
		}
		return v, nil
	case "position":
		var v float64
		if json.Unmarshal(raw, &v) != nil {
			return nil, invalid
		}
		return v, nil
	default:
		var v int64
		if json.Unmarshal(raw, &v) != nil {
			return nil, invalid
		}
		return v, nil
	}
}

// entryWhere is shared by interactive browsing and frozen list snapshots.
func entryWhere(f Filter, userID int64, manualList bool) (string, []any) {
	query := " WHERE e.profile_id=$1"
	args := []any{userID}
	add := func(clause string, v any) {
		args = append(args, v)
		query += " AND " + strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args)))
	}
	if manualList {
		add("le.list_id=?", f.ListID)
	}
	if len(f.MediaTypes) > 0 {
		add("e.media_type=ANY(?)", f.MediaTypes)
	}
	if len(f.Statuses) > 0 {
		add("e.status=ANY(?)", f.Statuses)
	}
	if f.Query != "" {
		// Whole words match through the text index; a misspelled word still matches a
		// title containing a close enough word.
		add("(e.search_document @@ plainto_tsquery('simple',public.search_fold(?)) OR public.search_fold(?) OPERATOR(public.<%) public.search_fold(e.title))", f.Query)
	}
	if len(f.Genres) > 0 {
		add("c.genres && ?::text[]", f.Genres)
	}
	for _, tag := range f.TagIDs {
		add("EXISTS(SELECT 1 FROM entry_tags t WHERE t.profile_id=e.profile_id AND t.entry_id=e.id AND t.tag_id=?)", tag)
	}
	if f.MinRating != nil {
		add("e.rating>=?", *f.MinRating)
	}
	if f.Provider != "" {
		add("((c.provider=(?::text[])[1] AND c.provider_id=(?::text[])[2]) OR c.external_ids->>((?::text[])[1])=(?::text[])[2])", []string{f.Provider, f.ProviderID})
	}
	if f.YearFrom != nil {
		add("e.publication_year>=?", *f.YearFrom)
	}
	if f.YearTo != nil {
		add("e.publication_year<=?", *f.YearTo)
	}
	if f.Owned != nil {
		clause := "EXISTS(SELECT 1 FROM owned_copies o WHERE o.profile_id=e.profile_id AND o.entry_id=e.id)"
		if !*f.Owned {
			clause = "NOT " + clause
		}
		query += " AND " + clause
	}

	return query, args
}
