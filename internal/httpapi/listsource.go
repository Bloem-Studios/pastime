package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

func (s *Server) listSourceRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:write", false, false))
		r.Put("/api/v1/lists/{listID}/source", func(w http.ResponseWriter, r *http.Request) {
			id, err := listID(r)
			var in struct {
				URL string `json:"url"`
			}
			if err == nil {
				in, err = decode[struct {
					URL string `json:"url"`
				}](w, r)
			}
			if err == nil && !s.accountLimit(w, r, "list-follow", 20) {
				return
			}
			if err == nil {
				err = s.imports.FollowList(r.Context(), actor(r), id, in.URL)
			}
			var out library.List
			if err == nil {
				out, err = s.library.List(r.Context(), actor(r), id)
			}
			s.reply(w, r, out, err)
		})
		r.Delete("/api/v1/lists/{listID}/source", func(w http.ResponseWriter, r *http.Request) {
			id, err := listID(r)
			if err == nil {
				err = s.imports.UnfollowList(r.Context(), actor(r), id)
			}
			var out library.List
			if err == nil {
				out, err = s.library.List(r.Context(), actor(r), id)
			}
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Post("/api/v1/lists/{listID}/trakt-export", func(w http.ResponseWriter, r *http.Request) {
			id, err := listID(r)
			if err == nil && !s.accountLimit(w, r, "trakt-export", 6) {
				return
			}
			var out imports.TraktExport
			if err == nil {
				out, err = s.imports.ExportListToTrakt(r.Context(), actor(r), id)
			}
			s.reply(w, r, out, err)
		})
	})
}
