package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/go-chi/chi/v5"
)

func (s *Server) notifyRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/notification-targets", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.notify.Targets(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Post("/api/v1/me/notification-targets", func(w http.ResponseWriter, r *http.Request) { s.saveTarget(w, r, 0) })
		r.Put("/api/v1/me/notification-targets/{targetID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "targetID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			s.saveTarget(w, r, id)
		})
		r.Delete("/api/v1/me/notification-targets/{targetID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "targetID")
			if err == nil {
				err = s.notify.DeleteTarget(r.Context(), actor(r), id)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Post("/api/v1/me/notification-targets/{targetID}/test", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "targetID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !s.accountLimit(w, r, "notify-test", 10) {
				return
			}
			out, err := s.notify.Test(r.Context(), actor(r), id)
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/me/notification-settings", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.notify.Settings(r.Context(), actor(r))
			s.reply(w, r, out, err)
		})
		r.Put("/api/v1/me/notification-settings", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[notify.Settings](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.notify.SaveSettings(r.Context(), actor(r), in)
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/library/{entryID}/notifications", func(w http.ResponseWriter, r *http.Request) {
			id, err := entryID(r)
			if err != nil {
				s.problem(w, r, fault.NotFound)
				return
			}
			if _, err = s.library.Get(r.Context(), actor(r), id); err != nil {
				s.problem(w, r, err)
				return
			}
			excluded, err := s.notify.Excluded(r.Context(), actor(r), id)
			s.reply(w, r, map[string]bool{"excluded": excluded}, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Put("/api/v1/library/{entryID}/notifications", func(w http.ResponseWriter, r *http.Request) {
			id, err := entryID(r)
			if err != nil {
				s.problem(w, r, fault.NotFound)
				return
			}
			in, err := decode[struct {
				Excluded bool `json:"excluded"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if err = s.notify.SetExcluded(r.Context(), actor(r), id, in.Excluded); err != nil {
				s.problem(w, r, err)
				return
			}
			respond(w, 200, map[string]bool{"excluded": in.Excluded})
		})
	})
}

func (s *Server) saveTarget(w http.ResponseWriter, r *http.Request, id int64) {
	in, err := decode[notify.TargetInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.notify.SaveTarget(r.Context(), actor(r), id, in)
	status := 200
	if id == 0 {
		status = 201
	}
	s.replyStatus(w, r, status, out, err)
}
