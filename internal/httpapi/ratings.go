package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
)

func (s *Server) ratingRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/library/{entryID}/status-history", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "entryID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.library.StatusHistory(r.Context(), actor(r), id, r.URL.Query().Get("cursor"), 30)
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/library/{entryID}/episode-ratings", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "entryID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.library.EpisodeRatings(r.Context(), actor(r), id, r.URL.Query().Get("cursor"), 30)
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/library/{entryID}/derived-rating", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "entryID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			var season *int
			if raw := r.URL.Query().Get("season"); raw != "" {
				n, e := strconv.Atoi(raw)
				if e != nil || n < 0 || n > 10000 {
					s.problem(w, r, fault.Invalid("Invalid season."))
					return
				}
				season = &n
			}
			out, err := s.library.DerivedRating(r.Context(), actor(r), id, season)
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Put("/api/v1/library/{entryID}/episode-ratings/{catalogID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "entryID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			child, err := pathID(r, "catalogID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			in, err := decode[library.EpisodeRatingInput](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.library.RateEpisode(r.Context(), actor(r), id, child, in)
			s.reply(w, r, out, err)
		})
	})
}
