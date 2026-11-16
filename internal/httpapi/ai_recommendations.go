package httpapi

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
	"net/http"
)

type aiRecommendation struct {
	bloem.AIRecommendation
	CatalogID   *string `json:"catalog_id,omitempty"`
	ImageURL    string  `json:"image_url"`
	BackdropURL string  `json:"backdrop_url"`
}

func (s *Server) aiRecommendationsRoutes() {
	s.Router.Post("/api/v1/integrations/bloem/recommendations/feedback:pending", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		out, err := s.bloemStore().PendingRecommendationFeedback(r.Context(), raw)
		s.reply(w, r, map[string]any{"items": out}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/recommendations/feedback:ack", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.RecommendationFeedback](w, r)
		if err == nil {
			err = s.bloemStore().AcknowledgeRecommendationFeedback(r.Context(), raw, in)
		}
		s.reply(w, r, map[string]bool{"accepted": err == nil}, err)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Put("/api/v1/ai-recommendations/feedback", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[bloem.RecommendationChoice](w, r)
			if err == nil {
				err = s.bloemStore().SetRecommendationChoice(r.Context(), actor(r), in)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	s.Router.Post("/api/v1/integrations/bloem/recommendations", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.RecommendationsInput](w, r)
		if err == nil {
			err = s.bloemStore().AcceptRecommendations(r.Context(), raw, in)
		}
		if err != nil {
			s.problem(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, map[string]bool{"accepted": true})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/ai-recommendations/feedback", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.bloemStore().RecommendationChoices(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Get("/api/v1/ai-recommendations", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.aiRecommendations(r.Context(), actor(r))
			w.Header().Set("Cache-Control", "no-store")
			s.reply(w, r, map[string]any{"items": out}, err)
		})
	})
}
func (s *Server) aiRecommendations(ctx context.Context, a identity.Actor) ([]aiRecommendation, error) {
	items, err := s.bloemStore().AIRecommendations(ctx, a)
	if err != nil {
		return nil, err
	}
	// Reuse only the verified-title metadata adapter, never calendar storage/events.
	source := make([]bloem.CalendarEvent, 0, len(items))
	projection := make([]calendarRelease, 0, len(items))
	for _, item := range items {
		event := bloem.CalendarEvent{InstanceID: item.InstanceID, EventID: item.ContentID, ContentID: item.ContentID, Kind: item.Kind, Title: item.Title, ExternalIDs: item.ExternalIDs}
		source = append(source, event)
		projection = append(projection, calendarRelease{SourceInstanceID: item.InstanceID, SourceEventID: item.ContentID})
	}
	if err = s.enrichCalendarTitles(ctx, a, projection, source); err != nil {
		return nil, err
	}
	out := make([]aiRecommendation, 0, len(items))
	for i, item := range items {
		out = append(out, aiRecommendation{AIRecommendation: item, CatalogID: projection[i].ParentCatalog, ImageURL: projection[i].ImageURL, BackdropURL: projection[i].BackdropURL})
	}
	return out, nil
}
