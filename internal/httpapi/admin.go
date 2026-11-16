package httpapi

import (
	"net/http"

	"github.com/Bloem-Studios/pastime/internal/admin"
	"github.com/go-chi/chi/v5"
)

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.Overview(r.Context(), actor(r))
	s.reply(w, r, out, err)
}

func (s *Server) adminJobs(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := s.admin.Jobs(r.Context(), actor(r), admin.JobFilter{Status: q.Get("status"), Kind: q.Get("kind"), Cursor: q.Get("cursor"), Limit: limit})
	s.reply(w, r, out, err)
}

func (s *Server) adminJobKinds(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.JobKinds(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) adminRetryJob(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.RetryJob(r.Context(), actor(r), chi.URLParam(r, "jobID"))
	s.reply(w, r, out, err)
}

func (s *Server) adminCancelJob(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.CancelJob(r.Context(), actor(r), chi.URLParam(r, "jobID"))
	s.reply(w, r, out, err)
}

func (s *Server) adminCatalog(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.CatalogStats(r.Context(), actor(r))
	s.reply(w, r, out, err)
}

func (s *Server) adminRefreshStale(w http.ResponseWriter, r *http.Request) {
	n, err := s.admin.RefreshStale(r.Context(), actor(r))
	s.reply(w, r, map[string]int{"scheduled": n}, err)
}

func (s *Server) adminImports(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	q := r.URL.Query()
	out, err := s.admin.Imports(r.Context(), actor(r), q.Get("status"), q.Get("cursor"), limit)
	s.reply(w, r, out, err)
}

// reply writes out on success or the public problem for err.
func (s *Server) reply(w http.ResponseWriter, r *http.Request, out any, err error) {
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, http.StatusOK, out)
}

func (s *Server) adminChecks(w http.ResponseWriter, r *http.Request) {
	out, err := s.admin.Checks(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}
