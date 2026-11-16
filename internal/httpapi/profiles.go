package httpapi

import "net/http"

func (s *Server) trackingProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.accounts.Profiles(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": profiles})
}
