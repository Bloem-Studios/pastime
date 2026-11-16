package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) bloemAvailabilityRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/catalog/{catalogID}/bloem-availability", func(w http.ResponseWriter, r *http.Request) {
			if !s.accountLimit(w, r, "bloem-availability", 240) {
				return
			}
			id, err := catalogID(r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.bloemStore().Availability(r.Context(), actor(r), []int64{id})
			s.reply(w, r, map[string]any{"items": out}, err)
		})
	})
	s.Router.Post("/api/v1/integrations/bloem/availability:pending", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		out, err := s.bloemStore().PendingAvailability(r.Context(), raw)
		s.reply(w, r, map[string]any{"items": out}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/availability:apply", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.AvailabilityInput](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		err = s.bloemStore().AcceptAvailability(r.Context(), raw, in)
		s.reply(w, r, map[string]bool{"accepted": err == nil}, err)
	})
}
