package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) bloemCalendarRoutes() {
	s.Router.Post("/api/v1/integrations/bloem/calendar/pages", s.bloemCalendarPage)
	s.Router.Post("/api/v1/integrations/bloem/calendar/{snapshotID}/publish", s.bloemCalendarPublish)
	s.Router.Post("/api/v1/integrations/bloem/calendar/leases", s.bloemCalendarLease)
	s.Router.Post("/api/v1/integrations/bloem/calendar/invalidate", s.bloemCalendarInvalidate)
}
func (s *Server) bloemCalendarPage(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	in, err := decode[bloem.CalendarInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.bloemStore().AcceptCalendarPage(r.Context(), raw, in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}
func (s *Server) bloemCalendarPublish(w http.ResponseWriter, r *http.Request) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	snapshot := chi.URLParam(r, "snapshotID")
	if err := s.bloemStore().PublishCalendar(r.Context(), raw, snapshot); err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, map[string]any{"snapshot_id": snapshot, "published": true})
}
func (s *Server) bloemCalendarLease(w http.ResponseWriter, r *http.Request) {
	s.bloemCalendarAuthority(w, r, false)
}
func (s *Server) bloemCalendarInvalidate(w http.ResponseWriter, r *http.Request) {
	s.bloemCalendarAuthority(w, r, true)
}
func (s *Server) bloemCalendarAuthority(w http.ResponseWriter, r *http.Request, invalidate bool) {
	raw, ok := s.bloemProvisionToken(w, r)
	if !ok {
		return
	}
	in, err := decode[bloem.CalendarLease](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if invalidate {
		err = s.bloemStore().InvalidateCalendar(r.Context(), raw, in)
	} else {
		err = s.bloemStore().RenewCalendarLease(r.Context(), raw, in)
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, map[string]bool{"accepted": true})
}
