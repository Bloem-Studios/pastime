package httpapi

import (
	"net/http"
	"time"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

type collabMemberInput struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type collabItemInput struct {
	CatalogID string `json:"catalog_id"`
	Note      string `json:"note"`
}

func (s *Server) collabRoutes() {
	noContent := func(w http.ResponseWriter, r *http.Request, err error) {
		if err != nil {
			s.problem(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
	detail := func(w http.ResponseWriter, r *http.Request, id int64, err error) {
		var out library.CollabDetail
		if err == nil {
			out, err = s.library.Collab(r.Context(), actor(r), id)
		}
		s.reply(w, r, out, err)
	}
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("lists:read", householdCredential))
		r.Get("/api/v1/collab-lists", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.library.CollabLists(r.Context(), actor(r))
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Get("/api/v1/collab-lists/{collabID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			detail(w, r, id, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("lists:write", householdCredential))
		r.Post("/api/v1/collab-lists", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[library.CollabInput](w, r)
			var out library.CollabList
			if err == nil {
				out, err = s.library.CreateCollab(r.Context(), actor(r), in)
			}
			s.replyStatus(w, r, http.StatusCreated, out, err)
		})
		r.Patch("/api/v1/collab-lists/{collabID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			var in library.CollabInput
			if err == nil {
				in, err = decode[library.CollabInput](w, r)
			}
			if err == nil {
				err = s.library.UpdateCollab(r.Context(), actor(r), id, in)
			}
			detail(w, r, id, err)
		})
		r.Delete("/api/v1/collab-lists/{collabID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			if err == nil {
				err = s.library.DeleteCollab(r.Context(), actor(r), id)
			}
			noContent(w, r, err)
		})
		r.Put("/api/v1/collab-lists/{collabID}/members", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			var in collabMemberInput
			if err == nil {
				in, err = decode[collabMemberInput](w, r)
			}
			if err == nil && !s.accountLimit(w, r, "collab-members", 30) {
				return
			}
			// Adding by username reveals whether the name exists, so also bound how many
			// names one account can try in an hour.
			if err == nil {
				if err = s.accounts.Limit(r.Context(), "collab-members-hour:"+actor(r).User.Username, 60, time.Hour); err != nil {
					s.problem(w, r, err)
					return
				}
				err = s.library.SetCollabMember(r.Context(), actor(r), id, in.Username, in.Role)
			}
			detail(w, r, id, err)
		})
		r.Delete("/api/v1/collab-lists/{collabID}/members/{username}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			if err == nil {
				err = s.library.RemoveCollabMember(r.Context(), actor(r), id, chi.URLParam(r, "username"))
			}
			noContent(w, r, err)
		})
		r.Post("/api/v1/collab-lists/{collabID}/items", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			var in collabItemInput
			if err == nil {
				in, err = decode[collabItemInput](w, r)
			}
			if err == nil {
				var catalogID int64
				if catalogID, err = identity.ParseID(in.CatalogID); err == nil {
					err = s.library.AddCollabItem(r.Context(), actor(r), id, catalogID, in.Note)
				}
			}
			detail(w, r, id, err)
		})
		r.Delete("/api/v1/collab-lists/{collabID}/items/{catalogID}", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			var catalogID int64
			if err == nil {
				catalogID, err = pathID(r, "catalogID")
			}
			if err == nil {
				err = s.library.RemoveCollabItem(r.Context(), actor(r), id, catalogID)
			}
			noContent(w, r, err)
		})
		r.Put("/api/v1/collab-lists/{collabID}/order", func(w http.ResponseWriter, r *http.Request) {
			id, err := pathID(r, "collabID")
			var in struct {
				CatalogIDs []string `json:"catalog_ids"`
			}
			if err == nil {
				in, err = decode[struct {
					CatalogIDs []string `json:"catalog_ids"`
				}](w, r)
			}
			ids := make([]int64, 0, len(in.CatalogIDs))
			for _, raw := range in.CatalogIDs {
				if err != nil {
					break
				}
				var n int64
				if n, err = identity.ParseID(raw); err == nil {
					ids = append(ids, n)
				}
			}
			if err == nil {
				err = s.library.ReorderCollab(r.Context(), actor(r), id, ids)
			}
			detail(w, r, id, err)
		})
	})
}
