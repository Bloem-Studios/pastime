package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
)

// episodeDescriber is a provider that can describe one of its episodes (TheTVDB).
type episodeDescriber interface {
	Episode(ctx context.Context, id string) (provider.EpisodeFacts, error)
}

// An episode's air date and title rarely change, so TheTVDB's answer is kept a month;
// an id TheTVDB does not know is asked again after a day.
const (
	episodeFactsTTL   = 30 * 24 * time.Hour
	episodeUnknownTTL = 24 * time.Hour
)

// describe fills a reference's missing air date and title from TheTVDB when the
// reference names a TheTVDB episode. An episode of another TheTVDB show, or a lookup
// that fails, leaves the reference as it was: the facts only help a match, and their
// absence never refuses one. failed reports a lookup that failed for now (TheTVDB
// unavailable or rate limited), so a caller can ask for a retry instead of refusing.
func (s *Service) describe(ctx context.Context, ref Reference) (_ Reference, failed bool) {
	if ref.TVDB == "" || ref.AirDate != "" && ref.Title != "" {
		return ref, false
	}
	p, ok := s.Providers["tvdb"].(episodeDescriber)
	if !ok {
		return ref, false
	}
	key := "tvdb-episode:v1:" + ref.TVDB
	var facts provider.EpisodeFacts
	cached := false
	if s.Cache != nil {
		if data, hit := s.Cache.Get(ctx, key); hit && json.Unmarshal(data, &facts) == nil {
			cached = true
		}
	}
	if !cached {
		found, err := p.Episode(ctx, ref.TVDB)
		switch {
		case errors.Is(err, provider.ErrNotFound):
			s.keepFacts(ctx, key, provider.EpisodeFacts{}, episodeUnknownTTL)
		case err != nil:
			return ref, true
		default:
			facts = found
			s.keepFacts(ctx, key, facts, episodeFactsTTL)
		}
	}
	if facts.SeriesID == "" || ref.ShowTVDB != "" && facts.SeriesID != ref.ShowTVDB {
		return ref, false
	}
	if ref.AirDate == "" {
		ref.AirDate = facts.Aired
	}
	if ref.Title == "" {
		ref.Title = facts.Name
	}
	return ref, false
}

func (s *Service) keepFacts(ctx context.Context, key string, facts provider.EpisodeFacts, ttl time.Duration) {
	if s.Cache == nil {
		return
	}
	if data, err := json.Marshal(facts); err == nil {
		s.Cache.Set(ctx, key, data, ttl)
	}
}
