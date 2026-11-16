package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) savedViewRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/saved-views", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.library.SavedViews(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Put("/api/v1/saved-views/order", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				IDs []string `json:"ids"`
			}](w, r)
			if err == nil {
				err = s.library.ReorderViews(r.Context(), actor(r), in.IDs)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		save := func(w http.ResponseWriter, r *http.Request, id int64) {
			in, err := decode[library.SavedViewInput](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.library.SaveView(r.Context(), actor(r), id, in)
			s.reply(w, r, out, err)
		}
		r.Post("/api/v1/saved-views", func(w http.ResponseWriter, r *http.Request) { save(w, r, 0) })
		r.Put("/api/v1/saved-views/{viewID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "viewID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			save(w, r, id)
		})
		r.Delete("/api/v1/saved-views/{viewID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "viewID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			in, err := decode[struct {
				Version int `json:"version"`
			}](w, r)
			if err == nil {
				err = s.library.DeleteView(r.Context(), actor(r), id, in.Version)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
}
