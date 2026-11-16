package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/library"
	"net/http"
)

func (s *Server) personalMemberships(w http.ResponseWriter, r *http.Request) {
	id, err := catalogID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.PersonalMemberships(r.Context(), actor(r), id)
	s.reply(w, r, out, err)
}
func (s *Server) setPersonalMembership(w http.ResponseWriter, r *http.Request) {
	id, err := catalogID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[library.PersonalMembershipInput](w, r)
	if err == nil {
		err = s.library.SetMembership(r.Context(), actor(r), r.Header.Get("Idempotency-Key"), id, in)
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
