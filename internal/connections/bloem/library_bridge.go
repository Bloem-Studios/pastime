package bloem

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type LibraryQuery struct {
	InstanceID string `json:"instance_id"`
	View       string `json:"view"`
	ParentID   string `json:"parent_id"`
	Cursor     string `json:"cursor"`
	Limit      int    `json:"limit"`
	Query      string `json:"query"`
	Kind       string `json:"kind"`
}
type LibraryRequest struct {
	LibraryQuery
	RequestID string `json:"request_id"`
	AccountID string `json:"account_id"`
	ProfileID string `json:"profile_id"`
}
type LibraryItem struct {
	ContentID   string            `json:"content_id"`
	Kind        string            `json:"kind"`
	Title       string            `json:"title"`
	ExternalIDs map[string]string `json:"external_ids"`
	Year        int               `json:"year"`
	Available   bool              `json:"available"`
	CatalogID   *string           `json:"catalog_id,omitempty"`
	ImageURL    string            `json:"image_url,omitempty"`
	Handoff     *HandoffTarget    `json:"handoff,omitempty"`
}
type LibraryCollection struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Scope          string `json:"scope"`
	Writable       bool   `json:"writable"`
	Revision       int64  `json:"revision"`
	DefinitionKind string `json:"definition_kind"`
}
type LibraryInput struct {
	RequestID   string              `json:"request_id"`
	InstanceID  string              `json:"instance_id"`
	AccountID   string              `json:"account_id"`
	ProfileID   string              `json:"profile_id"`
	View        string              `json:"view"`
	ParentID    string              `json:"parent_id"`
	NextCursor  string              `json:"next_cursor"`
	Complete    bool                `json:"complete"`
	GeneratedAt time.Time           `json:"generated_at"`
	ExpiresAt   time.Time           `json:"expires_at"`
	Items       []LibraryItem       `json:"items"`
	Collections []LibraryCollection `json:"collections"`
	Copies      []LibraryCopy       `json:"copies"`
	BookStates  []BookState         `json:"book_states"`
	Revision    int64               `json:"revision"`
}
type LibraryResult struct {
	RequestID string        `json:"request_id"`
	Status    string        `json:"status"`
	Page      *LibraryInput `json:"page,omitempty"`
}

func libraryQueryValid(q LibraryQuery) bool {
	if !opaque(q.InstanceID) || q.Limit < 1 || q.Limit > 100 || len(q.Query) > 256 || len(q.Cursor) > 16384 || len(q.ParentID) > 256 {
		return false
	}
	if q.View != "catalog" && q.View != "collections" && q.View != "collection_items" && q.View != "copies" && q.View != "book_states" {
		return false
	}
	switch q.Kind {
	case "", "movie", "series", "ebook", "audiobook":
		return true
	}
	return false
}
func (s Store) RequestLibrary(ctx context.Context, a identity.Actor, q LibraryQuery) (LibraryResult, error) {
	if !libraryQueryValid(q) {
		return LibraryResult{}, fault.Invalid("Invalid connected library query.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LibraryResult{}, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return LibraryResult{}, err
	}
	_, err = lockSourceProfile(ctx, tx, q.InstanceID, fresh.TrackingID(), false)
	if err != nil {
		return LibraryResult{}, err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return LibraryResult{}, err
	}
	digest := identity.Digest(string(body))
	id, err := randomRaw("library_")
	if err != nil {
		return LibraryResult{}, err
	}
	// The profile row lock serializes admission, bounding request storage per viewer.
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_library_requests WHERE instance_id=$1 AND profile_id=$2 AND requested_at<clock_timestamp()-interval '5 minutes'`, q.InstanceID, fresh.TrackingID()); err != nil {
		return LibraryResult{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_library_requests WHERE instance_id=$1 AND profile_id=$2 AND query_digest<>$3`, q.InstanceID, fresh.TrackingID(), digest).Scan(&count); err != nil {
		return LibraryResult{}, err
	}
	if count >= 40 {
		return LibraryResult{}, fault.Invalid("Too many pending library pages; retry shortly.")
	}
	err = tx.QueryRow(ctx, `INSERT INTO bloem_library_requests(id,instance_id,profile_id,query_digest,query,account_revision,profile_revision)
 SELECT $1,$2,$3,$4,$5,b.source_revision,p.source_revision FROM bloem_profiles p JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE p.instance_id=$2 AND p.profile_id=$3
 ON CONFLICT(instance_id,profile_id,query_digest) DO UPDATE SET
 id=CASE WHEN bloem_library_requests.account_revision=EXCLUDED.account_revision AND bloem_library_requests.profile_revision=EXCLUDED.profile_revision AND (bloem_library_requests.pending OR bloem_library_requests.expires_at>clock_timestamp()) THEN bloem_library_requests.id ELSE EXCLUDED.id END,
 pending=CASE WHEN bloem_library_requests.account_revision=EXCLUDED.account_revision AND bloem_library_requests.profile_revision=EXCLUDED.profile_revision AND bloem_library_requests.expires_at>clock_timestamp() THEN false ELSE true END,
 payload=CASE WHEN bloem_library_requests.account_revision=EXCLUDED.account_revision AND bloem_library_requests.profile_revision=EXCLUDED.profile_revision AND bloem_library_requests.expires_at>clock_timestamp() THEN bloem_library_requests.payload ELSE NULL END,
 expires_at=CASE WHEN bloem_library_requests.account_revision=EXCLUDED.account_revision AND bloem_library_requests.profile_revision=EXCLUDED.profile_revision AND bloem_library_requests.expires_at>clock_timestamp() THEN bloem_library_requests.expires_at ELSE NULL END,
 account_revision=EXCLUDED.account_revision,profile_revision=EXCLUDED.profile_revision,failure=''
 RETURNING id`, id, q.InstanceID, fresh.TrackingID(), digest, body).Scan(&id)
	if err != nil {
		return LibraryResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryResult{}, err
	}
	return s.LibraryResult(ctx, a, id)
}
func (s Store) LibraryResult(ctx context.Context, a identity.Actor, id string) (LibraryResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LibraryResult{}, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return LibraryResult{}, err
	}
	var instance string
	var pending, current bool
	var failure string
	var body []byte
	var settings HandoffSettings
	err = tx.QueryRow(ctx, `SELECT instance_id FROM bloem_library_requests WHERE id=$1 AND profile_id=$2`, id, fresh.TrackingID()).Scan(&instance)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryResult{}, fault.NotFound
	}
	if err != nil {
		return LibraryResult{}, err
	}
	if _, err = lockSourceProfile(ctx, tx, instance, fresh.TrackingID(), false); err != nil {
		return LibraryResult{}, err
	}
	err = tx.QueryRow(ctx, `SELECT r.instance_id,(r.pending AND r.requested_at>clock_timestamp()-interval '5 minutes'),r.failure,COALESCE(r.expires_at>clock_timestamp(),false),r.payload,i.handoff_enabled,i.handoff_label,COALESCE(i.handoff_base_url,i.url)
 FROM bloem_library_requests r JOIN bloem_instances i ON i.id=r.instance_id AND i.active
 JOIN bloem_profiles p ON p.instance_id=r.instance_id AND p.profile_id=r.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 WHERE r.id=$1 AND r.profile_id=$2 AND r.account_revision=b.source_revision AND r.profile_revision=p.source_revision`, id, fresh.TrackingID()).Scan(&instance, &pending, &failure, &current, &body, &settings.Enabled, &settings.Label, &settings.BaseURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryResult{}, fault.NotFound
	}
	if err != nil {
		return LibraryResult{}, err
	}
	out := LibraryResult{RequestID: id, Status: "expired"}
	if failure != "" {
		out.Status = failure
	} else if pending {
		out.Status = "pending"
	} else if current {
		out.Status = "ready"
		out.Page = new(LibraryInput)
		if err = json.Unmarshal(body, out.Page); err != nil {
			return LibraryResult{}, err
		}
		for index := range out.Page.Items {
			item := &out.Page.Items[index]
			if item.Available {
				item.Handoff = handoffTarget(settings, item.ContentID)
			}
		}
	}
	return out, tx.Commit(ctx)
}
func (s Store) PendingLibrary(ctx context.Context, raw string) ([]LibraryRequest, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var instance string
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return nil, authorityError(err)
	}
	rows, err := tx.Query(ctx, `SELECT r.id,r.query,p.source_account_id,p.source_profile_id FROM bloem_library_requests r
 JOIN bloem_profiles p ON p.instance_id=r.instance_id AND p.profile_id=r.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 JOIN users u ON u.id=p.account_id AND u.active AND pastime_account_has_service_access(u.id)
 WHERE r.instance_id=$1 AND r.pending AND r.requested_at>clock_timestamp()-interval '5 minutes' AND r.account_revision=b.source_revision AND r.profile_revision=p.source_revision
 ORDER BY r.last_attempt_at,r.id LIMIT 100 FOR UPDATE OF r SKIP LOCKED`, instance)
	if err != nil {
		return nil, err
	}
	out := []LibraryRequest{}
	for rows.Next() {
		var in LibraryRequest
		var body []byte
		if err = rows.Scan(&in.RequestID, &body, &in.AccountID, &in.ProfileID); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(body, &in.LibraryQuery); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, in)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, in := range out {
		if _, err = tx.Exec(ctx, `UPDATE bloem_library_requests SET last_attempt_at=clock_timestamp() WHERE id=$1`, in.RequestID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
func (s Store) AcceptLibrary(ctx context.Context, raw string, in LibraryInput) error {
	if !opaque(in.RequestID) || !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || (in.View != "catalog" && in.View != "collections" && in.View != "collection_items" && in.View != "copies" && in.View != "book_states") || in.GeneratedAt.IsZero() || !in.ExpiresAt.After(in.GeneratedAt) || in.ExpiresAt.Sub(in.GeneratedAt) > 120*time.Second || in.Complete != (in.NextCursor == "") || len(in.NextCursor) > 16384 {
		return fault.Invalid("Invalid library page.")
	}
	seen := map[string]bool{}
	for _, item := range in.Items {
		if !opaque(item.ContentID) || !calendarText(item.Title, 4096) || seen[item.ContentID] || item.Handoff != nil || item.CatalogID != nil || item.ImageURL != "" {
			return fault.Invalid("Invalid library title.")
		}
		seen[item.ContentID] = true
		switch item.Kind {
		case "movie", "series", "ebook", "audiobook":
		default:
			return fault.Invalid("Invalid library kind.")
		}
		for key, value := range item.ExternalIDs {
			switch key {
			case "tmdb", "imdb", "tvdb", "isbn", "asin":
			default:
				return fault.Invalid("Invalid library identity.")
			}
			if !calendarText(value, 128) {
				return fault.Invalid("Invalid library identity.")
			}
		}
	}

	if in.View != "catalog" && in.View != "collection_items" && len(in.Items) > 0 || in.View != "collections" && in.View != "collection_items" && len(in.Collections) > 0 || in.View != "copies" && len(in.Copies) > 0 || in.View != "book_states" && len(in.BookStates) > 0 {
		return fault.Invalid("Invalid collection page.")
	}
	for _, c := range in.Collections {
		if !opaque(c.ID) || !calendarText(c.Name, 4096) || len(c.Description) > 10000 || c.Revision < 1 || seen[c.ID] {
			return fault.Invalid("Invalid source collection.")
		}
		seen[c.ID] = true
		switch c.Scope {
		case "personal":
		case "shared", "server":
			if c.Writable {
				return fault.Invalid("Invalid source collection permission.")
			}
		default:
			return fault.Invalid("Invalid source collection permission.")
		}
	}
	for _, copy := range in.Copies {
		if !opaque(copy.ID) || !calendarText(copy.Title, 4096) || copy.Revision < 1 || seen[copy.ID] {
			return fault.Invalid("Invalid source copy.")
		}
		switch copy.Kind {
		case "movie", "series", "ebook", "audiobook":
		default:
			return fault.Invalid("Invalid source copy kind.")
		}
		seen[copy.ID] = true
	}
	for _, state := range in.BookStates {
		if !opaque(state.ContentID) || state.FileID < 1 || !opaque(state.EditionID) || seen[state.ContentID] || len(state.EbookLocation) > 16384 || state.Revision < 0 || state.Revision > 0 && state.ChangedAt == nil {
			return fault.Invalid("Invalid source book state.")
		}
		if state.Kind != "ebook" && state.Kind != "audiobook" {
			return fault.Invalid("Invalid source book kind.")
		}
		for _, value := range []float64{state.AudiobookSeconds, state.AudiobookDurationSeconds, state.EbookFraction} {
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return fault.Invalid("Invalid source book progress.")
			}
		}
		if state.EbookFraction > 1 {
			return fault.Invalid("Invalid source book progress.")
		}
		seen[state.ContentID] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	binding, err := calendarBinding(ctx, tx, raw, in.InstanceID, in.AccountID, in.ProfileID)
	if err != nil {
		return err
	}
	if err = validCalendarLease(ctx, tx, in.ExpiresAt); err != nil {
		return err
	}
	var query []byte
	err = tx.QueryRow(ctx, `SELECT r.query FROM bloem_library_requests r JOIN bloem_profiles p ON p.instance_id=r.instance_id AND p.profile_id=r.profile_id JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id
 WHERE r.id=$1 AND r.instance_id=$2 AND r.profile_id=$3 AND r.requested_at>clock_timestamp()-interval '5 minutes' AND r.account_revision=b.source_revision AND r.profile_revision=p.source_revision FOR UPDATE OF r`, in.RequestID, in.InstanceID, binding.ProfileID).Scan(&query)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict
	}
	if err != nil {
		return err
	}
	var q LibraryQuery
	if err = json.Unmarshal(query, &q); err != nil {
		return err
	}
	if q.View != in.View || q.ParentID != in.ParentID || (len(in.Items) > q.Limit || len(in.Collections) > q.Limit || len(in.Copies) > q.Limit || len(in.BookStates) > q.Limit) {
		return fault.Invalid("Library page does not match its request.")
	}
	if in.View == "collection_items" && (len(in.Collections) != 1 || in.Collections[0].ID != in.ParentID) {
		return fault.Invalid("Invalid parent collection.")
	}
	if in.Items == nil {
		in.Items = []LibraryItem{}
	}
	if in.Copies == nil {
		in.Copies = []LibraryCopy{}
	}
	if in.BookStates == nil {
		in.BookStates = []BookState{}
	}
	if in.Collections == nil {
		in.Collections = []LibraryCollection{}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_library_requests SET pending=false,expires_at=$2,payload=$3 WHERE id=$1 AND (pending OR payload=$3::jsonb)`, in.RequestID, in.ExpiresAt, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return tx.Commit(ctx)
}

type LibrarySourceView struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
}

func (s Store) LibrarySources(ctx context.Context, a identity.Actor) ([]LibrarySourceView, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT i.id,'Silo' FROM bloem_instances i JOIN bloem_profiles p ON p.instance_id=i.id JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE i.active AND p.active AND NOT p.excluded AND b.active AND p.profile_id=$1 ORDER BY i.id LIMIT 10`, fresh.TrackingID())
	if err != nil {
		return nil, err
	}
	out := []LibrarySourceView{}
	for rows.Next() {
		var item LibrarySourceView
		if err = rows.Scan(&item.InstanceID, &item.Name); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

type LibraryFailure struct {
	RequestID string `json:"request_id"`
	Reason    string `json:"reason"`
}

func (s Store) RejectLibrary(ctx context.Context, raw string, in LibraryFailure) error {
	if !opaque(in.RequestID) || (in.Reason != "denied" && in.Reason != "invalid") {
		return fault.Invalid("Invalid library failure.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var instance string
	var profile int64
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return authorityError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT profile_id FROM bloem_library_requests WHERE id=$1 AND instance_id=$2`, in.RequestID, instance).Scan(&profile); err != nil {
		return authorityError(err)
	}
	if _, err = lockSourceProfile(ctx, tx, instance, profile, false); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_library_requests SET pending=false,payload=NULL,expires_at=clock_timestamp(),failure=$2 WHERE id=$1 AND (pending OR failure=$2)`, in.RequestID, in.Reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return tx.Commit(ctx)
}
