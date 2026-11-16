package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

// sessionView is what a browser session learns about itself. A profile
// session sees its own login and profile name, never the household's account.
func sessionView(a identity.Actor) map[string]any {
	var login any
	if a.ProfileLoginID() != 0 {
		login = map[string]any{"profile_id": strconv.FormatInt(a.TrackingID(), 10), "login_name": a.PublicUser().Username}
	}
	return map[string]any{"user": a.PublicUser(), "csrf_token": a.CSRF, "profile_login": login}
}

func (s *Server) profileLogins(w http.ResponseWriter, r *http.Request) {
	out, err := s.accounts.ProfileLogins(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) setProfileLogin(w http.ResponseWriter, r *http.Request) {
	id, err := identity.ParseID(chi.URLParam(r, "profileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if !s.accountLimit(w, r, "profile-login", 10) {
		return
	}
	in, err := decode[identity.ProfileLoginInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.accounts.SetProfileLogin(r.Context(), actor(r), id, in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, http.StatusOK, out)
}

func (s *Server) removeProfileLogin(w http.ResponseWriter, r *http.Request) {
	id, err := identity.ParseID(chi.URLParam(r, "profileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if err = s.accounts.RemoveProfileLogin(r.Context(), actor(r), id); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
