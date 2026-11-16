package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

func (s *Server) libraryRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/library", s.entries)
		r.Get("/api/v1/library/facets", s.libraryFacets)
		r.Get("/api/v1/library/{entryID}", s.entry)
		r.Get("/api/v1/library-stats", s.libraryStats)
		r.Get("/api/v1/activity", s.activity)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/library", s.createEntry)
		r.Put("/api/v1/library/{entryID}", s.replaceEntry)
		r.Delete("/api/v1/library/{entryID}", s.deleteEntry)
		r.Post("/api/v1/library/{entryID}/progress", s.entryProgress)
		r.Post("/api/v1/library/{entryID}/restart", s.restartEntry)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:read", false, false))
		r.Get("/api/v1/lists", s.lists)
		r.Get("/api/v1/lists/{listID}", s.list)
		r.Get("/api/v1/library/{entryID}/lists", s.entryLists)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:write", false, false))
		r.Post("/api/v1/lists", s.createList)
		r.Put("/api/v1/lists/{listID}", s.replaceList)
		r.Delete("/api/v1/lists/{listID}", s.deleteList)
		r.Put("/api/v1/lists/{listID}/entries/{entryID}", s.listMembership)
	})
}
func entryID(r *http.Request) (int64, error) { return identity.ParseID(chi.URLParam(r, "entryID")) }
func listID(r *http.Request) (int64, error)  { return identity.ParseID(chi.URLParam(r, "listID")) }
func optionalID(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	id, err := identity.ParseID(v)
	if err != nil {
		return 0, fault.Invalid("Invalid record identifier.")
	}
	return id, nil
}

// libraryFilter reads the filters shared by browsing the library and counting its facets.
func libraryFilter(r *http.Request) (library.Filter, error) {
	q := r.URL.Query()
	list, err := optionalID(q.Get("list_id"))
	if err != nil {
		return library.Filter{}, err
	}
	f := library.Filter{Query: q.Get("q"), ListID: list,
		MediaTypes: splitList(q.Get("media_type")), Statuses: splitList(q.Get("status")), Genres: splitList(q.Get("genre")),
		Provider: q.Get("provider"), ProviderID: q.Get("provider_id")}
	for _, v := range splitList(q.Get("tag_id")) {
		id, err := identity.ParseID(v)
		if err != nil {
			return f, fault.Invalid("Invalid tag identifier.")
		}
		f.TagIDs = append(f.TagIDs, id)
	}
	for name, dst := range map[string]**int{"min_rating": &f.MinRating, "year_from": &f.YearFrom, "year_to": &f.YearTo} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return f, fault.Invalid("Invalid library filters.")
			}
			*dst = &n
		}
	}
	if v := q.Get("owned"); v != "" {
		owned := v == "true"
		if !owned && v != "false" {
			return f, fault.Invalid("owned is true or false.")
		}
		f.Owned = &owned
	}
	return f, nil
}
func (s *Server) entries(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	f, err := libraryFilter(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	f.Cursor, f.Limit, f.Sort = r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("sort")
	out, err := s.library.Entries(r.Context(), actor(r), f)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) libraryFacets(w http.ResponseWriter, r *http.Request) {
	f, err := libraryFilter(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Facets(r.Context(), actor(r), f)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) entry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Get(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) libraryStats(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.Stats(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	id, err := optionalID(r.URL.Query().Get("entry_id"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Activity(r.Context(), actor(r), id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) createEntry(w http.ResponseWriter, r *http.Request) {
	in, err := decode[library.EntryInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Create(r.Context(), actor(r), r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, out)
}
func (s *Server) replaceEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[library.EntryInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Replace(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) entryProgress(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[library.ProgressInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Progress(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

type versionInput struct {
	Version int `json:"version"`
}

func (s *Server) restartEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[versionInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Restart(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in.Version)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[versionInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Delete(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in.Version)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) lists(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Lists(r.Context(), actor(r), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) entryLists(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.EntryLists(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": out})
}
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.List(r.Context(), actor(r), id)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) saveList(w http.ResponseWriter, r *http.Request, id int64, status int) {
	in, err := decode[library.ListInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SaveList(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, status, out)
}
func (s *Server) createList(w http.ResponseWriter, r *http.Request) { s.saveList(w, r, 0, 201) }
func (s *Server) replaceList(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	s.saveList(w, r, id, 200)
}
func (s *Server) deleteList(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[versionInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.DeleteList(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in.Version)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (s *Server) listMembership(w http.ResponseWriter, r *http.Request) {
	lid, err := listID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	eid, err := entryID(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	in, err := decode[struct {
		Version int   `json:"version"`
		Present *bool `json:"present"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if in.Present == nil {
		s.problem(w, r, fault.Invalid("Include whether the item should be present."))
		return
	}
	out, err := s.library.Membership(r.Context(), actor(r), lid, eid, r.Header.Get("Idempotency-Key"), in.Version, *in.Present)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, out)
}

// splitList reads a comma-separated query value; empty items are dropped.
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
