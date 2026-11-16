package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/jobs"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
)

// ListSyncJob refreshes a list that follows a Trakt or MDBList list.
const ListSyncJob = "list_sync"

type listSyncPayload struct {
	ProfileID int64 `json:"profile_id,string"`
	ListID    int64 `json:"list_id,string"`
}

// ParseListSource reads a public Trakt or MDBList list address.
func ParseListSource(raw string) (kind, ref string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	bad := fault.Invalid("Paste the address of a public Trakt or MDBList list.")
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", "", bad
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	valid := func(s string) bool {
		return s != "" && len(s) <= 100 && strings.Trim(strings.ToLower(s), "abcdefghijklmnopqrstuvwxyz0123456789-_.") == ""
	}
	switch strings.TrimPrefix(strings.ToLower(u.Host), "www.") {
	case "trakt.tv", "app.trakt.tv":
		if len(parts) == 4 && parts[0] == "users" && parts[2] == "lists" && valid(parts[1]) && valid(parts[3]) {
			return "trakt", parts[1] + "/" + parts[3], nil
		}
	case "mdblist.com":
		if len(parts) == 3 && parts[0] == "lists" && valid(parts[1]) && valid(parts[2]) {
			return "mdblist", parts[1] + "/" + parts[2], nil
		}
	}
	return "", "", bad
}

// FollowList makes a manual list follow an outside list and queues the first sync.
func (s Store) FollowList(ctx context.Context, a identity.Actor, listID int64, address string) error {
	if !a.Allows("lists:write") || !a.Allows("library:write") {
		return fault.Forbidden
	}
	kind, ref, err := ParseListSource(address)
	if err != nil {
		return err
	}
	if kind == "trakt" && s.Creds.TraktClientID == "" {
		return &fault.Error{Status: 503, Code: "service_unavailable", Message: "Trakt is not set up on this server."}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "lists:write"); err != nil {
		return err
	}
	if !a.Allows("library:write") {
		return fault.Forbidden
	}
	tag, err := tx.Exec(ctx, "UPDATE personal_lists SET source_kind=$3,source_ref=$4,synced_at=NULL,updated_at=clock_timestamp(),version=version+1 WHERE profile_id=$1 AND id=$2 AND kind='manual'", a.TrackingID(), listID, kind, ref)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fault.Invalid("Choose one of your manual lists.")
	}
	if err = queueListSync(ctx, tx, a.TrackingID(), listID, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UnfollowList stops following; the list keeps its current titles.
func (s Store) UnfollowList(ctx context.Context, a identity.Actor, listID int64) error {
	return identity.WriteOnly(ctx, s.Pool, a, "lists:write", func(tx pgx.Tx, a identity.Actor) error {
		if !a.Allows("lists:write") {
			return fault.Forbidden
		}
		tag, err := tx.Exec(ctx, "UPDATE personal_lists SET source_kind=NULL,source_ref=NULL,synced_at=NULL,updated_at=clock_timestamp(),version=version+1 WHERE profile_id=$1 AND id=$2", a.TrackingID(), listID)
		if err == nil && tag.RowsAffected() == 0 {
			err = fault.NotFound
		}
		return err
	})
}

// queueListSync queues one sync; day makes scheduled syncs once a day, and an empty day
// (following a list now) always queues.
func queueListSync(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, listID int64, day string) error {
	payload, _ := json.Marshal(listSyncPayload{ProfileID: userID, ListID: listID})
	if day == "" {
		day = "now-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	key := fmt.Sprintf("list_sync:%d:%d:%s", userID, listID, day)
	_, err := jobs.Enqueue(ctx, q, ListSyncJob, key, "idempotent", payload, 3)
	return err
}

// EnqueueListSyncs queues a daily sync for every followed list.
func (s Store) EnqueueListSyncs(ctx context.Context, day string) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT l.profile_id,l.id FROM personal_lists l JOIN tracking_profiles tracking ON tracking.id=l.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
		WHERE l.source_kind IS NOT NULL AND u.active AND (l.synced_at IS NULL OR l.synced_at < clock_timestamp()-interval '20 hours') LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var due [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range due {
		if err := queueListSync(ctx, s.Pool, p[0], p[1], day); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}

// ListSyncHandler mirrors the outside list: its titles are added to the library as
// planned when new, and the list is set to exactly those titles in the source's order.
func (s Store) ListSyncHandler(ctx context.Context, raw json.RawMessage) error {
	var p listSyncPayload
	if json.Unmarshal(raw, &p) != nil || p.ProfileID == 0 {
		return jobs.ErrInvalidPayload
	}
	var kind, ref string
	err := s.Pool.QueryRow(ctx, `SELECT l.source_kind,l.source_ref FROM personal_lists l JOIN tracking_profiles tracking ON tracking.id=l.profile_id AND tracking.archived_at IS NULL JOIN users u ON u.id=tracking.account_id
		WHERE l.profile_id=$1 AND l.id=$2 AND l.source_kind IS NOT NULL AND u.active`, p.ProfileID, p.ListID).Scan(&kind, &ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	records, err := s.fetchListSource(ctx, kind, ref)
	if errors.Is(err, provider.ErrNotFound) || errors.Is(err, ErrCredentials) || errors.Is(err, provider.ErrNotConfigured) {
		return nil // The list is gone, private, or the server lost its key; try again tomorrow.
	}
	if err != nil {
		return err
	}
	var ids []int64
	seen := map[int64]bool{}
	for _, rec := range records {
		item, err := s.resolve(ctx, rec)
		var fe *fault.Error
		if errors.As(err, &fe) && fe.Status == 404 {
			continue
		}
		if err != nil {
			return err
		}
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		if _, err = s.Library.ApplyImport(ctx, p.ProfileID, "list:"+kind, library.ImportItem{TitleID: item.ID, Status: "planned", ListID: p.ListID, ListSourceKind: kind, ListSourceRef: ref}); err != nil {
			return err
		}
		var entryID int64
		if err = s.Pool.QueryRow(ctx, "SELECT id FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2", p.ProfileID, item.ID).Scan(&entryID); err != nil {
			return err
		}
		ids = append(ids, entryID)
	}
	err = s.Library.MirrorList(ctx, p.ProfileID, p.ListID, ids, kind, ref)
	if errors.Is(err, fault.NotFound) {
		return nil
	}
	return err
}

func (s Store) fetchListSource(ctx context.Context, kind, ref string) ([]Record, error) {
	user, slug, _ := strings.Cut(ref, "/")
	c := s.httpClient(false)
	var out []Record
	switch kind {
	case "trakt":
		if s.Creds.TraktClientID == "" {
			return nil, provider.ErrNotConfigured
		}
		var items []struct {
			Type  string     `json:"type"`
			Movie *traktWork `json:"movie"`
			Show  *traktWork `json:"show"`
		}
		header := http.Header{"Trakt-Api-Version": {"2"}, "Trakt-Api-Key": {s.Creds.TraktClientID}}
		if _, err := getJSON(ctx, c, "GET", traktAPI+"/users/"+url.PathEscape(user)+"/lists/"+url.PathEscape(slug)+"/items/movie,show", header, nil, &items); err != nil {
			return nil, err
		}
		for _, it := range items {
			switch {
			case it.Movie != nil:
				w := it.Movie
				out = append(out, Record{Label: w.Title, Kind: "movie", Video: &catalog.Reference{Kind: "movie", TMDB: idString(w.IDs.TMDB), IMDB: w.IDs.IMDB, Title: w.Title, Year: year(strconv.Itoa(w.Year))}})
			case it.Show != nil:
				w := it.Show
				out = append(out, Record{Label: w.Title, Kind: "series", Video: &catalog.Reference{Kind: "series", ShowTMDB: idString(w.IDs.TMDB), ShowIMDB: w.IDs.IMDB, ShowTVDB: idString(w.IDs.TVDB), ShowTitle: w.Title}})
			}
		}
	case "mdblist":
		var items []struct {
			ID        int64  `json:"id"`
			Title     string `json:"title"`
			IMDB      string `json:"imdb_id"`
			TVDB      int64  `json:"tvdb_id"`
			MediaType string `json:"mediatype"`
			Year      int    `json:"release_year"`
		}
		if _, err := getJSON(ctx, c, "GET", "https://mdblist.com/lists/"+url.PathEscape(user)+"/"+url.PathEscape(slug)+"/json", nil, nil, &items); err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.MediaType == "show" {
				out = append(out, Record{Label: it.Title, Kind: "series", Video: &catalog.Reference{Kind: "series", ShowTMDB: idString(it.ID), ShowIMDB: it.IMDB, ShowTVDB: idString(it.TVDB), ShowTitle: it.Title}})
			} else {
				out = append(out, Record{Label: it.Title, Kind: "movie", Video: &catalog.Reference{Kind: "movie", TMDB: idString(it.ID), IMDB: it.IMDB, Title: it.Title, Year: year(strconv.Itoa(it.Year))}})
			}
		}
	default:
		return nil, provider.ErrNotFound
	}
	if len(out) > 2000 {
		out = out[:2000]
	}
	return out, nil
}

// TraktExport reports what exporting a list to Trakt did.
type TraktExport struct {
	Added    int    `json:"added"`
	Existing int    `json:"existing"`
	Skipped  int    `json:"skipped"`
	URL      string `json:"url"`
}

// ExportListToTrakt copies a list's movies and shows to a private Trakt list of the
// same name on the linked Trakt account, creating it when needed. Titles already there
// stay; nothing is removed from Trakt.
func (s Store) ExportListToTrakt(ctx context.Context, a identity.Actor, listID int64) (TraktExport, error) {
	var out TraktExport
	if !a.Browser() {
		return out, fault.Forbidden
	}
	if s.Creds.TraktClientID == "" || !s.Box.Available() {
		return out, &fault.Error{Status: 503, Code: "service_unavailable", Message: "Trakt is not set up on this server."}
	}
	var linkID int64
	err := s.Pool.QueryRow(ctx, "SELECT id FROM service_links WHERE profile_id=$1 AND kind='trakt'", a.TrackingID()).Scan(&linkID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, &fault.Error{Status: 409, Code: "trakt_not_linked", Message: "Link your Trakt account under Integrations first."}
	}
	if err != nil {
		return out, err
	}
	var name, description string
	if err = s.Pool.QueryRow(ctx, "SELECT name,description FROM personal_lists WHERE profile_id=$1 AND id=$2", a.TrackingID(), listID).Scan(&name, &description); errors.Is(err, pgx.ErrNoRows) {
		return out, fault.NotFound
	} else if err != nil {
		return out, err
	}
	type traktID struct {
		IDs map[string]any `json:"ids"`
	}
	var movies, shows []traktID
	rows, err := s.Pool.Query(ctx, `SELECT c.kind,c.provider_id,COALESCE(c.external_ids->>'imdb','') FROM list_entries le JOIN library_entries e ON e.profile_id=le.profile_id AND e.id=le.entry_id
		JOIN catalog_items c ON c.id=e.catalog_item_id WHERE le.profile_id=$1 AND le.list_id=$2 ORDER BY le.position LIMIT 2000`, a.TrackingID(), listID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var kind, tmdb, imdb string
		if err := rows.Scan(&kind, &tmdb, &imdb); err != nil {
			rows.Close()
			return out, err
		}
		id, _ := strconv.ParseInt(tmdb, 10, 64)
		ids := traktID{IDs: map[string]any{"tmdb": id}}
		if imdb != "" {
			ids.IDs["imdb"] = imdb
		}
		switch kind {
		case "movie":
			movies = append(movies, ids)
		case "series":
			shows = append(shows, ids)
		default:
			out.Skipped++
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	_, cfg, state, _, err := s.loadLink(ctx, a.TrackingID(), linkID)
	if err != nil {
		return out, err
	}
	c := s.httpClient(false)
	if err = s.refreshTrakt(ctx, c, &cfg); err != nil {
		return out, err
	}
	if err = s.saveLinkState(ctx, a.TrackingID(), linkID, cfg, state); err != nil {
		return out, err
	}
	var lists []struct {
		Name string `json:"name"`
		IDs  struct {
			Trakt int64  `json:"trakt"`
			Slug  string `json:"slug"`
		} `json:"ids"`
		User struct {
			IDs struct {
				Slug string `json:"slug"`
			} `json:"ids"`
		} `json:"user"`
	}
	if _, err = getJSON(ctx, c, "GET", traktAPI+"/users/me/lists", s.traktHeader(&cfg), nil, &lists); err != nil {
		return out, err
	}
	var target int64
	var slug, owner string
	for _, l := range lists {
		if strings.EqualFold(l.Name, name) {
			target, slug, owner = l.IDs.Trakt, l.IDs.Slug, l.User.IDs.Slug
		}
	}
	if target == 0 {
		body, _ := json.Marshal(map[string]any{"name": name, "description": description, "privacy": "private"})
		var created struct {
			IDs struct {
				Trakt int64  `json:"trakt"`
				Slug  string `json:"slug"`
			} `json:"ids"`
			User struct {
				IDs struct {
					Slug string `json:"slug"`
				} `json:"ids"`
			} `json:"user"`
		}
		if _, err = getJSON(ctx, c, "POST", traktAPI+"/users/me/lists", s.traktHeader(&cfg), strings.NewReader(string(body)), &created); err != nil {
			return out, err
		}
		target, slug, owner = created.IDs.Trakt, created.IDs.Slug, created.User.IDs.Slug
	}
	body, _ := json.Marshal(map[string]any{"movies": movies, "shows": shows})
	var result struct {
		Added    map[string]int   `json:"added"`
		Existing map[string]int   `json:"existing"`
		NotFound map[string][]any `json:"not_found"`
	}
	if _, err = getJSON(ctx, c, "POST", fmt.Sprintf("%s/users/me/lists/%d/items", traktAPI, target), s.traktHeader(&cfg), strings.NewReader(string(body)), &result); err != nil {
		return out, err
	}
	for _, n := range result.Added {
		out.Added += n
	}
	for _, n := range result.Existing {
		out.Existing += n
	}
	for _, v := range result.NotFound {
		out.Skipped += len(v)
	}
	if owner != "" && slug != "" {
		out.URL = "https://trakt.tv/users/" + url.PathEscape(owner) + "/lists/" + url.PathEscape(slug)
	}
	return out, nil
}
