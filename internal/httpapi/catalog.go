package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

type resolveInput struct {
	Provider   string `json:"provider"`
	MediaType  string `json:"media_type"`
	ProviderID string `json:"provider_id"`
}

// catalogDetail pairs shared metadata with the caller's own entry, if tracked.
type catalogDetail struct {
	Item  catalog.Item   `json:"item"`
	Entry *library.Entry `json:"entry"`
}

func (s *Server) catalogRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/catalog/search", s.catalogSearch)
		r.Get("/api/v1/catalog/browse", s.catalogBrowse)
		r.Get("/api/v1/catalog/providers", s.catalogProviders)
		r.Post("/api/v1/catalog/resolve", s.catalogResolve)
		r.Get("/api/v1/catalog/{catalogID}", s.catalogItem)
		r.Get("/api/v1/catalog/{catalogID}/personal-state", s.personalMemberships)
		r.Get("/api/v1/catalog/{catalogID}/children", s.catalogChildren)
		r.Get("/api/v1/catalog/{catalogID}/critic", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) {
				return
			}
			id, err := catalogID(r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.catalog.Critic(r.Context(), id)
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/library/{entryID}/plays", s.plays)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/library/track", s.track)
		r.Put("/api/v1/catalog/{catalogID}/personal-state", s.setPersonalMembership)
		r.Post("/api/v1/library/{entryID}/plays", s.addPlays)
		r.Delete("/api/v1/library/{entryID}/plays/{playID}", s.removePlay)
	})
}

func (s *Server) catalogReady(w http.ResponseWriter, r *http.Request) bool {
	if s.catalog == nil {
		s.problem(w, r, fault.Unavailable)
		return false
	}
	return true
}

func (s *Server) catalogSearch(w http.ResponseWriter, r *http.Request) { s.catalogQuery(w, r, false) }
func (s *Server) catalogBrowse(w http.ResponseWriter, r *http.Request) { s.catalogQuery(w, r, true) }
func (s *Server) catalogQuery(w http.ResponseWriter, r *http.Request, browse bool) {
	if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-search", 60) {
		return
	}
	q := r.URL.Query()
	o := provider.QueryOptions{Query: q.Get("q"), Genre: q.Get("genre"), Sort: q.Get("sort")}
	if browse && o.Query != "" {
		s.problem(w, r, fault.Invalid("Use catalog search to search for a title."))
		return
	}
	if !browse && len(strings.TrimSpace(o.Query)) == 0 {
		s.problem(w, r, fault.Invalid("Search for 1 to 100 characters."))
		return
	}
	for _, v := range []struct {
		name string
		dst  *int
	}{{"page", &o.Page}, {"year", &o.Year}} {
		if raw := q.Get(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				s.problem(w, r, fault.Invalid("Choose a positive catalog page and year."))
				return
			}
			*v.dst = n
		}
	}
	out, err := s.catalog.Query(r.Context(), q.Get("media_type"), q.Get("provider"), o)
	if err == nil {
		err = s.catalog.Membership(r.Context(), actor(r), out.Items)
	}
	s.reply(w, r, out, err)
}

func (s *Server) catalogProviders(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w, r) {
		return
	}
	respond(w, 200, map[string]any{"items": s.catalog.Choices()})
}

func (s *Server) catalogResolve(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w, r) {
		return
	}
	in, err := decode[resolveInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if !s.accountLimit(w, r, "catalog-resolve", 60) {
		return
	}
	out, err := s.catalog.Resolve(r.Context(), in.Provider, in.MediaType, in.ProviderID)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func catalogID(r *http.Request) (int64, error) { return identity.ParseID(chi.URLParam(r, "catalogID")) }

func (s *Server) catalogItem(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w, r) {
		return
	}
	id, err := catalogID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	item, err := s.catalog.Get(r.Context(), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	entry, err := s.library.EntryForCatalog(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, catalogDetail{Item: item, Entry: entry})
}

func (s *Server) catalogChildren(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w, r) {
		return
	}
	id, err := catalogID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	items, err := s.catalog.Children(r.Context(), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": items})
}

func (s *Server) track(w http.ResponseWriter, r *http.Request) {
	in, err := decode[library.TrackInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Track(r.Context(), actor(r), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, out)
}

func (s *Server) plays(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	cycle := 0
	if raw := r.URL.Query().Get("cycle"); raw != "" {
		if cycle, err = strconv.Atoi(raw); err != nil || cycle < 1 {
			s.problem(w, r, fault.Invalid("Cycle must be a positive number."))
			return
		}
	}
	out, err := s.library.Plays(r.Context(), actor(r), id, cycle)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": out})
}

func (s *Server) addPlays(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[library.PlayInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.AddPlays(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (s *Server) removePlay(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	playID, err := identity.ParseID(chi.URLParam(r, "playID"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[versionInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.RemovePlay(r.Context(), actor(r), id, playID, r.Header.Get("Idempotency-Key"), in.Version)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
