package bloem

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"time"
)

type AIRecommendation struct {
	ContentID    string            `json:"content_id"`
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	Reason       string            `json:"reason"`
	ReasonDetail string            `json:"reason_detail"`
	ExternalIDs  map[string]string `json:"external_ids"`
	InstanceID   string            `json:"source_instance_id,omitempty"`
	ExpiresAt    *time.Time        `json:"source_authority_expires_at,omitempty"`
}
type RecommendationsInput struct {
	Version     int                `json:"version"`
	InstanceID  string             `json:"instance_id"`
	AccountID   string             `json:"account_id"`
	ProfileID   string             `json:"profile_id"`
	GeneratedAt time.Time          `json:"generated_at"`
	ExpiresAt   time.Time          `json:"expires_at"`
	Items       []AIRecommendation `json:"items"`
}

func (s Store) AcceptRecommendations(ctx context.Context, raw string, in RecommendationsInput) error {
	if in.Version != 1 || !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || in.GeneratedAt.IsZero() || in.ExpiresAt.Before(in.GeneratedAt) || in.ExpiresAt.Sub(in.GeneratedAt) > 120*time.Second || len(in.Items) > 100 || in.Items == nil {
		return fault.Invalid("Invalid AI recommendations projection.")
	}
	seen := map[string]bool{}
	for _, item := range in.Items {
		if !opaque(item.ContentID) || !calendarText(item.Title, 4096) || (item.Kind != "movie" && item.Kind != "series") || len(item.Reason) > 4096 || len(item.ReasonDetail) > 4096 || item.InstanceID != "" || item.ExpiresAt != nil || seen[item.ContentID] {
			return fault.Invalid("Invalid AI recommendation.")
		}
		seen[item.ContentID] = true
		if len(item.ExternalIDs) > 3 {
			return fault.Invalid("Invalid recommendation identifiers.")
		}
		for scheme, id := range item.ExternalIDs {
			if (scheme != "tmdb" && scheme != "imdb" && scheme != "tvdb") || !calendarText(id, 128) {
				return fault.Invalid("Invalid recommendation identifiers.")
			}
		}
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
	payload, err := json.Marshal(in.Items)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO bloem_ai_recommendations(instance_id,profile_id,generated_at,expires_at,payload) VALUES($1,$2,$3,$4,$5) ON CONFLICT(instance_id,profile_id) DO UPDATE SET generated_at=EXCLUDED.generated_at,expires_at=EXCLUDED.expires_at,payload=EXCLUDED.payload WHERE bloem_ai_recommendations.generated_at<EXCLUDED.generated_at OR (bloem_ai_recommendations.generated_at=EXCLUDED.generated_at AND bloem_ai_recommendations.payload=EXCLUDED.payload AND bloem_ai_recommendations.expires_at=EXCLUDED.expires_at)`, in.InstanceID, binding.ProfileID, in.GeneratedAt, in.ExpiresAt, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return tx.Commit(ctx)
}
func (s Store) AIRecommendations(ctx context.Context, a identity.Actor) ([]AIRecommendation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:read")
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT COALESCE((SELECT jsonb_agg(item) FROM jsonb_array_elements(r.payload) item WHERE NOT EXISTS(SELECT 1 FROM bloem_recommendation_feedback f WHERE f.instance_id=r.instance_id AND f.profile_id=r.profile_id AND f.content_id=item->>'content_id' AND f.dismissed)),'[]'::jsonb),r.instance_id,r.expires_at FROM bloem_ai_recommendations r
 JOIN bloem_instances i ON i.id=r.instance_id AND i.active
 JOIN bloem_profiles p ON p.instance_id=r.instance_id AND p.profile_id=r.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 WHERE r.profile_id=$1 AND r.expires_at>clock_timestamp() ORDER BY r.instance_id LIMIT 10`, fresh.TrackingID())
	if err != nil {
		return nil, err
	}
	out := []AIRecommendation{}
	for rows.Next() {
		var raw []byte
		var instance string
		var expires time.Time
		if err = rows.Scan(&raw, &instance, &expires); err != nil {
			rows.Close()
			return nil, err
		}
		var items []AIRecommendation
		if err = json.Unmarshal(raw, &items); err != nil {
			rows.Close()
			return nil, err
		}
		for _, item := range items {
			item.InstanceID = instance
			item.ExpiresAt = &expires
			out = append(out, item)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}
