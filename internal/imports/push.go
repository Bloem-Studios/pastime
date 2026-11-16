package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/jobs"
)

const PushJob = "playback_push"

type pushPayload struct {
	ProfileID  int64 `json:"profile_id"`
	LinkID     int64 `json:"link_id"`
	Generation int64 `json:"generation"`
}

// Pull and push have separate durable jobs and outcomes. A failed remote destination
// must not discard a successfully fetched inbound batch.
func (s Store) PushHandler(ctx context.Context, raw json.RawMessage) error {
	var p pushPayload
	if json.Unmarshal(raw, &p) != nil || p.ProfileID < 1 || p.LinkID < 1 || p.Generation < 1 {
		return jobs.ErrInvalidPayload
	}
	kind, cfg, state, allow, err := s.loadLink(ctx, p.ProfileID, p.LinkID)
	if errors.Is(err, fault.NotFound) || errors.Is(err, fault.Unauthenticated) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg.Generation != p.Generation || !cfg.Push {
		return nil
	}
	if kind != "jellyfin" && kind != "emby" {
		return jobs.ErrInvalidPayload
	}
	if err = s.pushJellyfin(ctx, s.httpClient(allow), kind, p.ProfileID, p.LinkID, &cfg, state); err != nil {
		return fmt.Errorf("playback delivery pending: %w", err)
	}
	// A claimed delivery may be waiting for its lease, or a bounded batch may leave
	// more work. Keep the job retryable until all eligible facts are acknowledged.
	var pending bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plays p JOIN catalog_items c ON c.id=p.catalog_item_id JOIN catalog_items r ON r.id=c.root_id WHERE p.profile_id=$1 AND p.created_at>$2::timestamptz AND p.source NOT IN ('jellyfin','emby') AND c.kind IN ('movie','episode') AND r.provider='tmdb' AND NOT EXISTS(SELECT 1 FROM playback_deliveries d WHERE d.link_id=$3 AND d.generation=$4 AND d.play_id=p.id AND d.delivered_at IS NOT NULL))`, p.ProfileID, state["push_started"], p.LinkID, p.Generation).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return fault.Unavailable
	}
	return nil
}
