package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
)

func (s *Server) bloemListSnapshotRoutes() {
	s.Router.Post("/api/v1/integrations/bloem/lists:resolve", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.ListResolveInput](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		out, err := s.bloemStore().ResolveList(r.Context(), raw, s.origin, in)
		w.Header().Set("Cache-Control", "no-store")
		s.reply(w, r, out, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/lists:page", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.ListSnapshotInput](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		out, err := s.bloemStore().ReadListSnapshot(r.Context(), raw, in)
		w.Header().Set("Cache-Control", "no-store")
		s.reply(w, r, out, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/lists:attest", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.ListAttestationInput](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		out, err := s.bloemStore().AttestListSnapshot(r.Context(), raw, in)
		w.Header().Set("Cache-Control", "no-store")
		s.reply(w, r, out, err)
	})
}
