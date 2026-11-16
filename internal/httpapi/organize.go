package httpapi

import (
	"encoding/csv"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/exports"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

func (s *Server) organizeRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/tags", s.tags)
		r.Get("/api/v1/library/{entryID}/tags", s.entryTags)
		r.Get("/api/v1/library/{entryID}/copies", s.entryCopies)
		r.Get("/api/v1/library/{entryID}/skipped", s.skipped)
		r.Get("/api/v1/collection", s.collection)
		r.Get("/api/v1/collection-fields", s.fields)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Post("/api/v1/tags", func(w http.ResponseWriter, r *http.Request) { s.saveTag(w, r, 0) })
		r.Put("/api/v1/tags/{tagID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "tagID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			s.saveTag(w, r, id)
		})
		r.Delete("/api/v1/tags/{tagID}", s.deleteTag)
		r.Put("/api/v1/library/{entryID}/tags", s.setEntryTags)
		r.Post("/api/v1/library/{entryID}/copies", func(w http.ResponseWriter, r *http.Request) { s.saveCopy(w, r, false) })
		r.Put("/api/v1/library/{entryID}/copies/{copyID}", func(w http.ResponseWriter, r *http.Request) { s.saveCopy(w, r, true) })
		r.Delete("/api/v1/library/{entryID}/copies/{copyID}", s.deleteCopy)
		r.Post("/api/v1/library/{entryID}/skips", s.skip)
		r.Put("/api/v1/library/{entryID}/artwork", s.setArtwork)
		r.Post("/api/v1/library/{entryID}/remap", s.remap)
		r.Post("/api/v1/library/{entryID}/remap/preview", s.remap)
		r.Post("/api/v1/collection-fields", func(w http.ResponseWriter, r *http.Request) { s.saveField(w, r, 0) })
		r.Put("/api/v1/collection-fields/{fieldID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "fieldID")
			if err != nil {
				s.problem(w, r, err)
				return
			}
			s.saveField(w, r, id)
		})
		r.Delete("/api/v1/collection-fields/{fieldID}", s.deleteField)
	})
	// Bulk list actions need lists:write instead of library:write; the store checks the scope.
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("", false, false))
		r.Post("/api/v1/library/bulk", s.bulk)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:read", false, false))
		r.Get("/api/v1/lists/{listID}/tiers", s.tiers)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("lists:write", false, false))
		r.Put("/api/v1/lists/{listID}/order", s.reorderList)
		r.Put("/api/v1/lists/{listID}/tiers", s.saveTiers)
		r.Put("/api/v1/lists/{listID}/entries/{entryID}/note", s.listNote)
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/preferences", s.preferences)
		r.Put("/api/v1/me/preferences", s.savePreferences)
	})
	s.Router.Get("/api/v1/public/{username}/lists", s.publicProfile)
	s.Router.Get("/api/v1/public/{username}/lists/{slug}", s.publicList)
	s.Router.Get("/lists/{username}/{slug}.rss", s.publicListRSS)
	s.Router.Get("/lists/{username}/{slug}.csv", s.publicListCSV)
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := identity.ParseID(chi.URLParam(r, name))
	if err != nil {
		return 0, fault.NotFound
	}
	return id, nil
}

func key(r *http.Request) string { return r.Header.Get("Idempotency-Key") }

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.Tags(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) saveTag(w http.ResponseWriter, r *http.Request, id int64) {
	in, err := decode[library.TagInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SaveTag(r.Context(), actor(r), id, key(r), in)
	status := 200
	if id == 0 {
		status = 201
	}
	s.replyStatus(w, r, status, out, err)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "tagID")
	if err == nil {
		var out library.Mutation
		out, err = s.library.DeleteTag(r.Context(), actor(r), id, key(r))
		s.reply(w, r, out, err)
		return
	}
	s.problem(w, r, err)
}

func (s *Server) entryTags(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	out, err := s.library.EntryTags(r.Context(), actor(r), id)
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) setEntryTags(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		TagIDs []string `json:"tag_ids"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SetEntryTags(r.Context(), actor(r), id, key(r), in.TagIDs)
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) bulk(w http.ResponseWriter, r *http.Request) {
	in, err := decode[library.BulkInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Bulk(r.Context(), actor(r), key(r), in)
	s.reply(w, r, out, err)
}

func (s *Server) entryCopies(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.CopyPage(r.Context(), actor(r), id, "", r.URL.Query().Get("cursor"), limit)
	s.reply(w, r, out, err)
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.CopyPage(r.Context(), actor(r), 0, r.URL.Query().Get("format"), r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("q"))
	s.reply(w, r, out, err)
}

func (s *Server) saveCopy(w http.ResponseWriter, r *http.Request, existing bool) {
	entry, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	var copyID int64
	if existing {
		if copyID, err = pathID(r, "copyID"); err != nil {
			s.problem(w, r, err)
			return
		}
	}
	in, err := decode[library.CopyInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SaveCopy(r.Context(), actor(r), entry, copyID, key(r), in)
	status := 200
	if !existing {
		status = 201
	}
	s.replyStatus(w, r, status, out, err)
}

func (s *Server) deleteCopy(w http.ResponseWriter, r *http.Request) {
	entry, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	copyID, err := pathID(r, "copyID")
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.DeleteCopy(r.Context(), actor(r), entry, copyID, key(r))
	s.reply(w, r, out, err)
}

func (s *Server) fields(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.Fields(r.Context(), actor(r))
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) saveField(w http.ResponseWriter, r *http.Request, id int64) {
	in, err := decode[library.FieldInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SaveField(r.Context(), actor(r), id, key(r), in)
	status := 200
	if id == 0 {
		status = 201
	}
	s.replyStatus(w, r, status, out, err)
}

func (s *Server) deleteField(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "fieldID")
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.DeleteField(r.Context(), actor(r), id, key(r))
	s.reply(w, r, out, err)
}

func (s *Server) skipped(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	cycle := 0
	if v := r.URL.Query().Get("cycle"); v != "" {
		if cycle, err = strconv.Atoi(v); err != nil || cycle < 1 {
			s.problem(w, r, fault.Invalid("Cycle must be a positive number."))
			return
		}
	}
	out, err := s.library.Skipped(r.Context(), actor(r), id, cycle)
	s.reply(w, r, map[string]any{"items": out}, err)
}

func (s *Server) skip(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		Version    int      `json:"version"`
		CatalogIDs []string `json:"catalog_ids"`
		Skip       bool     `json:"skip"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Skip(r.Context(), actor(r), id, key(r), in.Version, in.CatalogIDs, in.Skip)
	s.reply(w, r, out, err)
}

func (s *Server) reorderList(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		Version  int      `json:"version"`
		EntryIDs []string `json:"entry_ids"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.Reorder(r.Context(), actor(r), id, key(r), in.Version, in.EntryIDs)
	s.reply(w, r, out, err)
}

func (s *Server) tiers(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	out, err := s.library.Tiers(r.Context(), actor(r), id)
	s.reply(w, r, out, err)
}

func (s *Server) saveTiers(w http.ResponseWriter, r *http.Request) {
	id, err := listID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[library.TierInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SaveTiers(r.Context(), actor(r), id, key(r), in)
	s.reply(w, r, out, err)
}

func (s *Server) listNote(w http.ResponseWriter, r *http.Request) {
	list, err := listID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	entry, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		Version int    `json:"version"`
		Note    string `json:"note"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SetListNote(r.Context(), actor(r), list, entry, key(r), in.Version, in.Note)
	s.reply(w, r, out, err)
}

func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	out, err := s.library.Preferences(r.Context(), actor(r))
	s.reply(w, r, out, err)
}

func (s *Server) savePreferences(w http.ResponseWriter, r *http.Request) {
	in, err := decode[library.Preferences](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SavePreferences(r.Context(), actor(r), in)
	s.reply(w, r, out, err)
}

// ---------------------------------------------------------------- public lists

func (s *Server) publicLimit(w http.ResponseWriter, r *http.Request) bool {
	if s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return false
	}
	if err := s.accounts.Limit(r.Context(), "public-list:"+remoteBucket(r), 60, time.Minute); err != nil {
		s.problem(w, r, err)
		return false
	}
	return true
}

func (s *Server) publicProfile(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit(w, r) {
		return
	}
	name, lists, err := s.library.PublicLists(r.Context(), chi.URLParam(r, "username"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, map[string]any{"username": chi.URLParam(r, "username"), "display_name": name, "items": lists})
}

func (s *Server) publicListData(w http.ResponseWriter, r *http.Request) (library.PublicList, error) {
	limit, err := queryLimit(r)
	if err != nil {
		return library.PublicList{}, err
	}
	out, err := s.library.PublicPage(r.Context(), chi.URLParam(r, "username"), chi.URLParam(r, "slug"), r.URL.Query().Get("cursor"), limit)
	if err == nil && out.NextCursor != nil {
		next := *r.URL
		q := next.Query()
		q.Set("cursor", *out.NextCursor)
		next.RawQuery = q.Encode()
		w.Header().Set("Link", "<"+next.RequestURI()+">; rel=\"next\"")
	}
	return out, err
}

func (s *Server) publicList(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit(w, r) {
		return
	}
	out, err := s.publicListData(w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, 200, out)
}

func (s *Server) publicListRSS(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit(w, r) {
		return
	}
	list, err := s.publicListData(w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	type item struct {
		Title       string `xml:"title"`
		Description string `xml:"description,omitempty"`
		GUID        string `xml:"guid"`
		Category    string `xml:"category"`
	}
	feed := struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			Updated     string `xml:"lastBuildDate"`
			Items       []item `xml:"item"`
		} `xml:"channel"`
	}{Version: "2.0"}
	feed.Channel.Title = list.Name
	feed.Channel.Link = s.origin + "/api/v1/public/" + list.Owner + "/lists/" + list.Slug
	feed.Channel.Description = list.Description
	feed.Channel.Updated = list.UpdatedAt.UTC().Format(time.RFC1123Z)
	for _, it := range list.Items {
		title := it.Title
		if it.Year != nil {
			title += " (" + strconv.Itoa(*it.Year) + ")"
		}
		guid := it.Provider + ":" + it.ProviderID
		if it.ProviderID == "" {
			guid = "manual:" + strings.ToLower(title)
		}
		feed.Channel.Items = append(feed.Channel.Items, item{Title: title, Description: it.Note, GUID: guid, Category: it.MediaType})
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(feed)
}

func (s *Server) publicListCSV(w http.ResponseWriter, r *http.Request) {
	if !s.publicLimit(w, r) {
		return
	}
	list, err := s.publicListData(w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+list.Slug+`.csv"`)
	c := csv.NewWriter(w)
	_ = c.Write([]string{"title", "year", "media_type", "provider", "provider_id", "note"})
	for _, it := range list.Items {
		year := ""
		if it.Year != nil {
			year = strconv.Itoa(*it.Year)
		}
		// A leading =, +, - or @ would run as a formula in a spreadsheet.
		_ = c.Write([]string{safeCell(it.Title), year, it.MediaType, it.Provider, it.ProviderID, safeCell(it.Note)})
	}
	c.Flush()
}

func safeCell(v string) string { return exports.SafeCell(v) }

// replyStatus writes out with status on success or the public problem for err.
func (s *Server) replyStatus(w http.ResponseWriter, r *http.Request, status int, out any, err error) {
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, status, out)
}

func (s *Server) setArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		Version  int    `json:"version"`
		ImageURL string `json:"image_url"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	out, err := s.library.SetArtwork(r.Context(), actor(r), id, key(r), in.Version, in.ImageURL)
	s.reply(w, r, out, err)
}

func (s *Server) remap(w http.ResponseWriter, r *http.Request) {
	id, err := entryID(r)
	if err != nil {
		s.problem(w, r, fault.NotFound)
		return
	}
	in, err := decode[struct {
		Version   int    `json:"version"`
		CatalogID string `json:"catalog_id"`
	}](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	catalogID, err := identity.ParseID(in.CatalogID)
	if err != nil {
		s.problem(w, r, fault.Invalid("Choose a catalog title."))
		return
	}
	// Episode history is matched by season and number, so the new record's episodes for
	// the seasons this entry has history in are loaded first.
	if s.catalog != nil {
		seasons, err := s.library.RemapSeasons(r.Context(), actor(r), id)
		if err == nil {
			err = s.catalog.LoadSeasons(r.Context(), catalogID, seasons)
		}
		if err != nil {
			s.problem(w, r, err)
			return
		}
	}
	if strings.HasSuffix(r.URL.Path, "/preview") {
		out, err := s.library.PreviewRemap(r.Context(), actor(r), id, in.Version, catalogID)
		s.reply(w, r, out, err)
		return
	}
	out, err := s.library.Remap(r.Context(), actor(r), id, key(r), in.Version, catalogID)
	s.reply(w, r, out, err)
}
