package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
)

// checkinRoutes serve check-ins: start an activity on any title, list what is
// running, finish it with the real time spent, or cancel it.
func (s *Server) checkinRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/checkins", s.listCheckins)
		r.Get("/api/v1/checkins/{checkinID}", s.getCheckin)
		r.Get("/api/v1/library/{entryID}/play-history", s.playHistory)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/checkins", s.checkIn)
		r.Post("/api/v1/checkins/{checkinID}/finish", s.finishCheckin)
		r.Post("/api/v1/checkins/{checkinID}/cancel", s.cancelCheckin)
	})
}

func checkinID(r *http.Request) (int64, error) { return identity.ParseID(chi.URLParam(r, "checkinID")) }

type checkinPage struct {
	Items []library.Checkin `json:"items"`
}

func (s *Server) listCheckins(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.ActiveCheckins(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, checkinPage{Items: out})
}

func (s *Server) getCheckin(w http.ResponseWriter, r *http.Request) {
	id, err := checkinID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.GetCheckin(r.Context(), actor(r), id)
	s.reply(w, r, out, err)
}

// answerCheckin returns the check-in a mutation touched, as it stands now.
func (s *Server) answerCheckin(w http.ResponseWriter, r *http.Request, status int, m library.Mutation, err error) {
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.GetCheckin(r.Context(), actor(r), m.ID)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, status, out)
}

func (s *Server) checkIn(w http.ResponseWriter, r *http.Request) {
	in, err := decode[library.CheckinInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	m, err := s.library.CheckIn(r.Context(), actor(r), r.Header.Get("Idempotency-Key"), in)
	s.answerCheckin(w, r, 201, m, err)
}

func (s *Server) finishCheckin(w http.ResponseWriter, r *http.Request) {
	id, err := checkinID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[library.FinishInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	m, err := s.library.FinishCheckin(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in)
	s.answerCheckin(w, r, 200, m, err)
}

func (s *Server) cancelCheckin(w http.ResponseWriter, r *http.Request) {
	id, err := checkinID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	m, err := s.library.CancelCheckin(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"))
	s.answerCheckin(w, r, 200, m, err)
}

func (s *Server) playHistory(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.PlayHistory(r.Context(), actor(r), id)
	s.reply(w, r, out, err)
}
