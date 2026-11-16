package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/go-chi/chi/v5"
)

func (s *Server) bloemHandoffRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("", true, true))
		r.Get("/api/admin/v1/bloem/instances", s.bloemHandoffInstances)
		r.Get("/api/admin/v1/bloem/instances/{instanceID}/handoff", s.bloemHandoffGet)
		r.Put("/api/admin/v1/bloem/instances/{instanceID}/handoff", s.bloemHandoffSet)
	})
}
func (s *Server) bloemHandoffGet(w http.ResponseWriter, r *http.Request) {
	out, err := s.bloemStore().HandoffSettings(r.Context(), actor(r), chi.URLParam(r, "instanceID"))
	w.Header().Set("Cache-Control", "private, no-store")
	s.reply(w, r, out, err)
}
func (s *Server) bloemHandoffSet(w http.ResponseWriter, r *http.Request) {
	in, err := decode[struct {
		Enabled *bool  `json:"enabled"`
		Label   string `json:"label"`
		BaseURL string `json:"base_url"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.Enabled == nil {
		s.problem(w, r, fault.Invalid("Send explicit playback enablement."))
		return
	}
	out, err := s.bloemStore().SetHandoffSettings(r.Context(), actor(r), chi.URLParam(r, "instanceID"), bloem.HandoffSettings{Enabled: *in.Enabled, Label: in.Label, BaseURL: in.BaseURL})
	s.reply(w, r, out, err)
}

func (s *Server) bloemHandoffInstances(w http.ResponseWriter, r *http.Request) {
	out, err := s.bloemStore().HandoffInstances(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}
