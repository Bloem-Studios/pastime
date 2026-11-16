package httpapi

import (
	"github.com/Bloem-Studios/pastime/internal/fault"
	"net/http"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/discover"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

type dismissInput struct {
	Provider   string `json:"provider"`
	MediaType  string `json:"media_type"`
	ProviderID string `json:"provider_id"`
}

func (s *Server) discovery() discover.Store {
	return discover.Store{Pool: s.library.Pool, Catalog: s.catalog}
}

func (s *Server) discoveryRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/discover", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "discover", 60) {
				return
			}
			out, err := s.discovery().Feed(r.Context(), actor(r), r.URL.Query().Get("media_type"))
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/discover/dismissals", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) {
				return
			}
			q := r.URL.Query()
			limit := 0
			if raw := q.Get("limit"); raw != "" {
				n, err := strconv.Atoi(raw)
				if err != nil || n < 1 {
					s.problem(w, r, fault.Invalid("Page size must be between 1 and 100."))
					return
				}
				limit = n
			}
			out, err := s.discovery().Dismissals(r.Context(), actor(r), q.Get("media_type"), q.Get("cursor"), limit)
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/people/{provider}/{personID}", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-people", 60) {
				return
			}
			out, err := s.catalog.Person(r.Context(), chi.URLParam(r, "provider"), chi.URLParam(r, "personID"))
			if err == nil {
				refs := make([]catalog.Result, len(out.Credits))
				for i := range out.Credits {
					refs[i] = out.Credits[i].Result
				}
				err = s.catalog.Membership(r.Context(), actor(r), refs)
				for i := range out.Credits {
					out.Credits[i].Result = refs[i]
				}
			}
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/studios/{provider}/{studioKind}/{studioID}", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-people", 60) {
				return
			}
			out, err := s.catalog.Studio(r.Context(), chi.URLParam(r, "provider"), chi.URLParam(r, "studioKind"), chi.URLParam(r, "studioID"))
			if err == nil {
				err = s.catalog.Membership(r.Context(), actor(r), out.Titles)
			}
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/discover/dismissals", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[dismissInput](w, r)
			if err == nil {
				err = s.discovery().Dismiss(r.Context(), actor(r), in.Provider, in.MediaType, in.ProviderID, false)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Delete("/api/v1/discover/dismissals", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if err := s.discovery().Dismiss(r.Context(), actor(r), q.Get("provider"), q.Get("media_type"), q.Get("provider_id"), true); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", householdSession))
		r.Get("/api/v1/recommendations", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.library.Recommendations(r.Context(), actor(r), r.URL.Query().Get("box"))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Get("/api/v1/recommendation-recipients", func(w http.ResponseWriter, r *http.Request) {
			if !s.accountLimit(w, r, "recipients", 60) {
				return
			}
			out, err := s.library.Recipients(r.Context(), actor(r), r.URL.Query().Get("q"))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Post("/api/v1/recommendations", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[library.RecommendInput](w, r)
			if err == nil && !s.accountLimit(w, r, "recommend", 30) {
				return
			}
			var out library.Recommendation
			if err == nil {
				out, err = s.library.Recommend(r.Context(), actor(r), in)
			}
			s.replyStatus(w, r, http.StatusCreated, out, err)
		})
		for _, action := range []string{"accept", "dismiss"} {
			r.Post("/api/v1/recommendations/{recommendationID}/"+action, func(w http.ResponseWriter, r *http.Request) {
				id, err := pathID(r, "recommendationID")
				var out library.Recommendation
				if err == nil {
					out, err = s.library.Respond(r.Context(), actor(r), id, action == "accept")
				}
				s.reply(w, r, out, err)
			})
		}
	})
}
