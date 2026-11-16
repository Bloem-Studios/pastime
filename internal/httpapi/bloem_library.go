package httpapi

import (
	"context"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (s *Server) bloemLibraryRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/bloem/library/sources", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.bloemStore().LibrarySources(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Post("/api/v1/bloem/library/queries", func(w http.ResponseWriter, r *http.Request) {
			if !s.accountLimit(w, r, "bloem-library", 120) {
				return
			}
			in, err := decode[bloem.LibraryQuery](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.bloemStore().RequestLibrary(r.Context(), actor(r), in)
			w.Header().Set("Cache-Control", "no-store")
			s.reply(w, r, out, err)
		})
		r.Get("/api/v1/bloem/library/queries/{requestID}", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.connectedLibraryResult(r.Context(), actor(r), chi.URLParam(r, "requestID"))
			w.Header().Set("Cache-Control", "no-store")
			s.reply(w, r, out, err)
		})
	})

	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/bloem/library/copies:import", func(w http.ResponseWriter, r *http.Request) {
			if !s.accountLimit(w, r, "bloem-copy-import", 120) {
				return
			}
			in, err := decode[bloem.NativeCopyImport](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.bloemStore().ImportNativeCopy(r.Context(), actor(r), in)
			w.Header().Set("Cache-Control", "no-store")
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:write", false, false))
		r.Post("/api/v1/bloem/library/mutations", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[bloem.LibraryMutationIntent](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			out, err := s.bloemStore().RequestLibraryMutation(r.Context(), actor(r), in)
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:read", false, false))
		r.Get("/api/v1/bloem/library/mutations/{requestID}", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.bloemStore().LibraryMutationResult(r.Context(), actor(r), chi.URLParam(r, "requestID"))
			s.reply(w, r, out, err)
		})
	})
	s.Router.Post("/api/v1/integrations/bloem/library/mutations:pending", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		out, err := s.bloemStore().PendingLibraryMutations(r.Context(), raw)
		s.reply(w, r, map[string]any{"items": out}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/library/mutations:apply", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.LibraryMutationResult](w, r)
		if err == nil {
			err = s.bloemStore().AcceptLibraryMutation(r.Context(), raw, in)
		}
		s.reply(w, r, map[string]bool{"accepted": err == nil}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/library:reject", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.LibraryFailure](w, r)
		if err == nil {
			err = s.bloemStore().RejectLibrary(r.Context(), raw, in)
		}
		s.reply(w, r, map[string]bool{"accepted": err == nil}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/library:pending", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		out, err := s.bloemStore().PendingLibrary(r.Context(), raw)
		s.reply(w, r, map[string]any{"items": out}, err)
	})
	s.Router.Post("/api/v1/integrations/bloem/library:apply", func(w http.ResponseWriter, r *http.Request) {
		raw, ok := s.bloemProvisionToken(w, r)
		if !ok {
			return
		}
		in, err := decode[bloem.LibraryInput](w, r)
		if err == nil {
			err = s.bloemStore().AcceptLibrary(r.Context(), raw, in)
		}
		s.reply(w, r, map[string]bool{"accepted": err == nil}, err)
	})
}

func (s *Server) connectedLibraryResult(ctx context.Context, a identity.Actor, id string) (bloem.LibraryResult, error) {
	out, err := s.bloemStore().LibraryResult(ctx, a, id)
	if err != nil || out.Page == nil {
		return out, err
	}
	source := []bloem.CalendarEvent{}
	projection := []calendarRelease{}
	for _, item := range out.Page.Items {
		source = append(source, bloem.CalendarEvent{InstanceID: out.Page.InstanceID, EventID: item.ContentID, ContentID: item.ContentID, Kind: item.Kind, Title: item.Title, ExternalIDs: item.ExternalIDs})
		projection = append(projection, calendarRelease{SourceInstanceID: out.Page.InstanceID, SourceEventID: item.ContentID})
	}
	if err = s.enrichCalendarTitles(ctx, a, projection, source); err != nil {
		return bloem.LibraryResult{}, err
	}
	for i := range out.Page.Items {
		out.Page.Items[i].CatalogID = projection[i].ParentCatalog
		out.Page.Items[i].ImageURL = projection[i].ImageURL
	}
	return out, nil
}
