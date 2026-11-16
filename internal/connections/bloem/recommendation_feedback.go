package bloem

import (
	"context"
	"errors"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
)

type RecommendationChoice struct {
	InstanceID string `json:"source_instance_id"`
	ContentID  string `json:"content_id"`
	Dismissed  bool   `json:"dismissed"`
}
type RecommendationFeedback struct {
	RecommendationChoice
	AccountID string `json:"account_id"`
	ProfileID string `json:"profile_id"`
	Revision  int64  `json:"revision"`
	Title     string `json:"title"`
}

func (s Store) SetRecommendationChoice(ctx context.Context, a identity.Actor, in RecommendationChoice) error {
	if !opaque(in.InstanceID) || !opaque(in.ContentID) {
		return fault.Invalid("Invalid recommendation choice.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:write")
	if err != nil {
		return err
	}
	if _, err = lockSourceProfile(ctx, tx, in.InstanceID, fresh.TrackingID(), false); err != nil {
		return err
	}
	var title string
	err = tx.QueryRow(ctx, `SELECT item->>'title' FROM bloem_ai_recommendations r CROSS JOIN LATERAL jsonb_array_elements(r.payload) item WHERE r.instance_id=$1 AND r.profile_id=$2 AND r.expires_at>clock_timestamp() AND item->>'content_id'=$3`, in.InstanceID, fresh.TrackingID(), in.ContentID).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		// Undo remains usable after the recommendation lease expires.
		err = tx.QueryRow(ctx, `SELECT title FROM bloem_recommendation_feedback WHERE instance_id=$1 AND profile_id=$2 AND content_id=$3 FOR UPDATE`, in.InstanceID, fresh.TrackingID(), in.ContentID).Scan(&title)
	}
	if err != nil {
		return authorityError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_recommendation_feedback(instance_id,profile_id,content_id,title,dismissed,revision) VALUES($1,$2,$3,$4,$5,1) ON CONFLICT(instance_id,profile_id,content_id) DO UPDATE SET title=EXCLUDED.title,dismissed=EXCLUDED.dismissed,revision=bloem_recommendation_feedback.revision+1,changed_at=clock_timestamp() WHERE bloem_recommendation_feedback.dismissed IS DISTINCT FROM EXCLUDED.dismissed`, in.InstanceID, fresh.TrackingID(), in.ContentID, title, in.Dismissed)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s Store) PendingRecommendationFeedback(ctx context.Context, raw string) ([]RecommendationFeedback, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var instance string
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return nil, authorityError(err)
	}
	rows, err := tx.Query(ctx, `WITH selected AS (SELECT f.instance_id,f.profile_id,f.content_id,p.source_account_id,p.source_profile_id FROM bloem_recommendation_feedback f JOIN bloem_profiles p ON p.instance_id=f.instance_id AND p.profile_id=f.profile_id AND p.active AND NOT p.excluded JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active JOIN users u ON u.id=p.account_id AND u.active AND pastime_account_has_service_access(u.id) WHERE f.instance_id=$1 AND f.acknowledged_revision<f.revision ORDER BY f.last_attempt_at,f.changed_at,f.content_id LIMIT 100 FOR UPDATE OF f SKIP LOCKED) UPDATE bloem_recommendation_feedback f SET last_attempt_at=clock_timestamp() FROM selected s WHERE f.instance_id=s.instance_id AND f.profile_id=s.profile_id AND f.content_id=s.content_id RETURNING f.instance_id,f.content_id,f.dismissed,s.source_account_id,s.source_profile_id,f.revision,f.title`, instance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecommendationFeedback{}
	for rows.Next() {
		var r RecommendationFeedback
		if err = rows.Scan(&r.InstanceID, &r.ContentID, &r.Dismissed, &r.AccountID, &r.ProfileID, &r.Revision, &r.Title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
func (s Store) AcknowledgeRecommendationFeedback(ctx context.Context, raw string, in RecommendationFeedback) error {
	if !opaque(in.InstanceID) || !opaque(in.ContentID) || in.Revision < 1 {
		return fault.Invalid("Invalid recommendation receipt.")
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
	// A delivery started before Undo cannot acknowledge the newer choice.
	_, err = tx.Exec(ctx, `UPDATE bloem_recommendation_feedback SET acknowledged_revision=$4 WHERE instance_id=$1 AND profile_id=$2 AND content_id=$3 AND revision=$4 AND dismissed=$5`, in.InstanceID, binding.ProfileID, in.ContentID, in.Revision, in.Dismissed)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) RecommendationChoices(ctx context.Context, a identity.Actor) ([]RecommendationFeedback, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT f.instance_id,f.content_id,f.dismissed,p.source_account_id,p.source_profile_id,f.revision,f.title FROM bloem_recommendation_feedback f JOIN bloem_profiles p ON p.instance_id=f.instance_id AND p.profile_id=f.profile_id AND p.active AND NOT p.excluded JOIN bloem_instances i ON i.id=p.instance_id AND i.active JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active WHERE f.profile_id=$1 AND f.dismissed ORDER BY f.changed_at DESC LIMIT 100`, fresh.TrackingID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecommendationFeedback{}
	for rows.Next() {
		var r RecommendationFeedback
		if err = rows.Scan(&r.InstanceID, &r.ContentID, &r.Dismissed, &r.AccountID, &r.ProfileID, &r.Revision, &r.Title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
