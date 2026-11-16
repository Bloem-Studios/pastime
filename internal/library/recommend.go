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

// Person is another account as recommendations show it.
type Person struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// Recommendation is a title one person suggested to another.
type Recommendation struct {
	ID        int64     `json:"id,string"`
	From      Person    `json:"from"`
	To        Person    `json:"to"`
	CatalogID int64     `json:"catalog_id,string"`
	Title     string    `json:"title"`
	MediaType string    `json:"media_type"`
	Year      *int      `json:"year"`
	ImageURL  string    `json:"image_url"`
	Note      string    `json:"note"`
	Status    string    `json:"status"`
	InLibrary bool      `json:"in_library"`
	CreatedAt time.Time `json:"created_at"`
}

type RecommendInput struct {
	To        string `json:"to"`
	CatalogID string `json:"catalog_id"`
	Note      string `json:"note"`
}

const recommendationColumns = `r.id,f.username,f.display_name,t.username,t.display_name,c.id,c.title,c.kind,extract(year FROM c.release_date)::int,c.image_url,r.note,r.status,
	EXISTS(SELECT 1 FROM library_entries e WHERE e.profile_id=$2 AND e.user_id=$1 AND e.catalog_item_id=c.id),r.created_at
	FROM recommendations r JOIN users f ON f.id=r.from_user JOIN users t ON t.id=r.to_user JOIN catalog_items c ON c.id=r.catalog_item_id`

func scanRecommendation(row pgx.Row) (Recommendation, error) {
	var r Recommendation
	err := row.Scan(&r.ID, &r.From.Username, &r.From.DisplayName, &r.To.Username, &r.To.DisplayName, &r.CatalogID, &r.Title, &r.MediaType, &r.Year, &r.ImageURL, &r.Note, &r.Status, &r.InLibrary, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = fault.NotFound
	}
	return r, err
}

// Recipients finds other active accounts by username or name prefix.
func (s Store) Recipients(ctx context.Context, a identity.Actor, q string) ([]Person, error) {
	out := []Person{}
	if !a.Household() {
		return out, fault.Forbidden
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if len([]rune(q)) < 2 || len(q) > 80 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT username,display_name FROM users WHERE active AND id<>$1 AND last_login_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM user_preferences p WHERE p.user_id=users.id AND p.profile_id=(SELECT id FROM tracking_profiles WHERE account_id=users.id AND is_default) AND p.prefs->>'decline_recommendations'='true')
		AND (username LIKE $2 || '%' OR lower(display_name) LIKE $2 || '%') ORDER BY username LIMIT 10`, a.User.ID, strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.Username, &p.DisplayName); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Recommend suggests a catalogued title to another active account. Suggesting it again
// updates the note while preserving the recipient’s decision and inbox order.
func (s Store) Recommend(ctx context.Context, a identity.Actor, in RecommendInput) (Recommendation, error) {
	if !a.Household() {
		return Recommendation{}, fault.Forbidden
	}
	catalogID, err := identity.ParseID(in.CatalogID)
	if err != nil {
		return Recommendation{}, fault.Invalid("Choose a title to recommend.")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 500 {
		return Recommendation{}, fault.Invalid("Keep the note under 500 characters.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Recommendation{}, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return Recommendation{}, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO recommendations(from_user,to_user,catalog_item_id,note)
		SELECT $1,u.id,c.id,$4 FROM users u, catalog_items c WHERE u.username=lower($2) AND u.active AND u.id<>$1 AND c.id=$3 AND c.parent_id IS NULL
 AND NOT EXISTS(SELECT 1 FROM user_preferences p WHERE p.user_id=u.id AND p.profile_id=(SELECT id FROM tracking_profiles WHERE account_id=u.id AND is_default) AND p.prefs->>'decline_recommendations'='true')
		ON CONFLICT (from_user,to_user,catalog_item_id) DO UPDATE SET note=EXCLUDED.note
		RETURNING id`, a.User.ID, strings.TrimSpace(in.To), catalogID, in.Note).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recommendation{}, fault.Invalid("Choose another person on this server and a title from the catalog.")
	}
	if err != nil {
		return Recommendation{}, err
	}
	out, err := scanRecommendation(tx.QueryRow(ctx, "SELECT "+recommendationColumns+" WHERE r.id=$3", a.User.ID, a.TrackingID(), id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// Recommendations lists the inbox (received) or sent recommendations, newest first.
func (s Store) Recommendations(ctx context.Context, a identity.Actor, box string) ([]Recommendation, error) {
	out := []Recommendation{}
	if !a.Household() {
		return out, fault.Forbidden
	}
	where := "r.to_user=$1 AND r.status='pending'"
	switch box {
	case "", "inbox":
	case "sent":
		where = "r.from_user=$1"
	case "all":
		where = "r.to_user=$1"
	default:
		return out, fault.Invalid("box must be inbox, all or sent.")
	}
	rows, err := s.Pool.Query(ctx, "SELECT "+recommendationColumns+" WHERE "+where+" AND f.active ORDER BY r.created_at DESC LIMIT 100", a.User.ID, a.TrackingID())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRecommendation(rows)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Respond accepts a recommendation, adding the title as planned unless it is already
// tracked, or dismisses it.
func (s Store) Respond(ctx context.Context, a identity.Actor, id int64, accept bool) (Recommendation, error) {
	if !a.Household() {
		return Recommendation{}, fault.Forbidden
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Recommendation{}, err
	}
	defer tx.Rollback(ctx)
	if a, err = identity.AuthorizeTx(ctx, tx, a, "library:write"); err != nil {
		return Recommendation{}, err
	}
	if err = lockChanges(ctx, tx, a.TrackingID()); err != nil {
		return Recommendation{}, err
	}
	status := "dismissed"
	if accept {
		status = "accepted"
	}
	var catalogID int64
	err = tx.QueryRow(ctx, "UPDATE recommendations SET status=$3 WHERE id=$2 AND to_user=$1 RETURNING catalog_item_id", a.User.ID, id, status).Scan(&catalogID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Recommendation{}, fault.NotFound
	}
	if err != nil {
		return Recommendation{}, err
	}
	if accept {
		var entryID int64
		err = tx.QueryRow(ctx, `INSERT INTO library_entries(profile_id,title,media_type,publication_year,unit,total,status,catalog_item_id)
			SELECT $1,left(c.title,200),c.kind,extract(year FROM c.release_date)::int,c.unit,c.total,'planned',c.id FROM catalog_items c WHERE c.id=$2
			ON CONFLICT DO NOTHING RETURNING id`, a.TrackingID(), catalogID).Scan(&entryID)
		if err == nil {
			err = event(ctx, tx, a, entryID, "added")
			if err == nil {
				err = initializeTracking(ctx, tx, a, entryID, catalogID)
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		}
		if err != nil {
			return Recommendation{}, err
		}
	}
	out, err := scanRecommendation(tx.QueryRow(ctx, "SELECT "+recommendationColumns+" WHERE r.id=$3", a.User.ID, a.TrackingID(), id))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
