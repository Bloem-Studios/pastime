package httpapi

import (
	"net/http"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

// The Connect Silo page: a signed-in person creates the Silo key for one of their own
// tracking profiles and pastes it into Silo (Settings, Watch Providers). Session only; the
// keys themselves never authorize these routes.
func (s *Server) siloConnectRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/integrations/silo/connect", s.siloConnect)
		r.Post("/api/v1/integrations/silo/profiles/{profileID}/credential", s.createSiloCredential)
		r.Delete("/api/v1/integrations/silo/profiles/{profileID}/credential", s.revokeSiloCredential)
	})
}

func (s *Server) siloConnect(w http.ResponseWriter, r *http.Request) {
	if s.library.Pool == nil {
		s.problem(w, r, fault.Unavailable)
		return
	}
	out, err := s.bloemStore().ConnectView(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

// siloConnectLimit bounds key creation and revocation per account.
func (s *Server) siloConnectLimit(w http.ResponseWriter, r *http.Request) bool {
	if err := s.accounts.Limit(r.Context(), "silo-connect:"+actor(r).PublicUser().Username, 10, time.Minute); err != nil {
		s.problem(w, r, err)
		return false
	}
	return true
}

func (s *Server) createSiloCredential(w http.ResponseWriter, r *http.Request) {
	profileID, err := identity.ParseID(chi.URLParam(r, "profileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	var in struct {
		InstanceID string `json:"instance_id"`
	}
	// An empty body means "the only registered Silo server".
	if r.ContentLength != 0 || r.Header.Get("Content-Type") != "" {
		if in, err = decode[struct {
			InstanceID string `json:"instance_id"`
		}](w, r); err != nil {
			s.problem(w, r, err)
			return
		}
	}
	if !s.siloConnectLimit(w, r) {
		return
	}
	out, err := s.bloemStore().IssueSelfServiceCredential(r.Context(), actor(r), profileID, in.InstanceID)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}

func (s *Server) revokeSiloCredential(w http.ResponseWriter, r *http.Request) {
	profileID, err := identity.ParseID(chi.URLParam(r, "profileID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if !s.siloConnectLimit(w, r) {
		return
	}
	if err = s.bloemStore().RevokeSelfServiceCredentials(r.Context(), actor(r), profileID); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
