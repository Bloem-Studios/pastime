package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/imports"
	"github.com/go-chi/chi/v5"
)

type scheduleInput struct {
	Schedule string `json:"schedule"`
}

func (s *Server) linkRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/service-kinds", func(w http.ResponseWriter, r *http.Request) {
			s.reply(w, r, map[string]any{"items": s.imports.Kinds()}, nil)
		})
		r.Get("/api/v1/me/services", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.imports.Links(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Post("/api/v1/me/services", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[imports.LinkInput](w, r)
			if err == nil && !s.accountLimit(w, r, "service-link", 10) {
				return
			}
			var out imports.Link
			if err == nil {
				out, err = s.imports.SaveLink(r.Context(), actor(r), in)
			}
			s.replyStatus(w, r, http.StatusCreated, out, err)
		})
		r.Put("/api/v1/me/services/{serviceID}/schedule", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "serviceID")
			var in scheduleInput
			if err == nil {
				in, err = decode[scheduleInput](w, r)
			}
			var out imports.Link
			if err == nil {
				out, err = s.imports.UpdateSchedule(r.Context(), actor(r), id, in.Schedule)
			}
			s.reply(w, r, out, err)
		})
		r.Delete("/api/v1/me/services/{serviceID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "serviceID")
			if err == nil {
				err = s.imports.DeleteLink(r.Context(), actor(r), id)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Post("/api/v1/me/service-authorizations", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Kind string `json:"kind"`
				URL  string `json:"url"`
			}](w, r)
			if err == nil && !s.accountLimit(w, r, "service-auth", 10) {
				return
			}
			var out imports.Authorization
			if err == nil {
				out, err = s.imports.BeginAuthorization(r.Context(), actor(r), in.Kind, in.URL)
			}
			s.replyStatus(w, r, http.StatusCreated, out, err)
		})
		r.Post("/api/v1/me/service-authorizations/{authorizationID}/poll", func(w http.ResponseWriter, r *http.Request) {
			if !s.accountLimit(w, r, "service-auth-poll", 30) {
				return
			}
			out, err := s.imports.PollAuthorization(r.Context(), actor(r), chi.URLParam(r, "authorizationID"))
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/me/services/{serviceID}/sync", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "serviceID")
			var out imports.Run
			if err == nil {
				out, err = s.imports.SyncNow(r.Context(), actor(r), id)
			}
			s.replyStatus(w, r, http.StatusAccepted, out, err)
		})
	})
}
