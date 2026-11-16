package httpapi

import (
	"net/http"
	"net/url"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

func (s *Server) editionRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Get("/api/v1/catalog/{catalogID}/editions", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-editions", 60) {
				return
			}
			id, err := pathID(r, "catalogID")
			var out []catalog.Edition
			if err == nil {
				out, err = s.catalog.Editions(r.Context(), id)
			}
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Get("/api/v1/catalog/{catalogID}/episode-orders", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-orders", 60) {
				return
			}
			id, err := pathID(r, "catalogID")
			var out []catalog.EpisodeOrderSummary
			if err == nil {
				out, err = s.catalog.EpisodeOrders(r.Context(), id)
			}
			s.reply(w, r, map[string]any{"items": out}, err)
		})
		r.Get("/api/v1/catalog/{catalogID}/episode-orders/{orderID}", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) || !s.accountLimit(w, r, "catalog-orders", 60) {
				return
			}
			id, err := pathID(r, "catalogID")
			var out catalog.EpisodeOrderDetail
			if err == nil {
				// Clients may escape the ':' in order ids.
				orderID, _ := url.PathUnescape(chi.URLParam(r, "orderID"))
				out, err = s.catalog.EpisodeOrder(r.Context(), id, orderID)
			}
			s.reply(w, r, out, err)
		})
	})
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:write", false, false))
		r.Put("/api/v1/library/{entryID}/edition", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) {
				return
			}
			id, err := entryID(r)
			if err != nil {
				s.problem(w, r, fault.NotFound)
				return
			}
			in, err := decode[struct {
				Version   int     `json:"version"`
				EditionID *string `json:"edition_id"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			var chosen *library.Edition
			if in.EditionID != nil {
				// The edition is looked up again rather than taken from the request, so
				// only real provider editions are recorded.
				entry, err := s.library.Get(r.Context(), actor(r), id)
				if err != nil {
					s.problem(w, r, err)
					return
				}
				if entry.CatalogID == nil {
					s.problem(w, r, fault.Invalid("Editions apply to books and audiobooks from the catalog."))
					return
				}
				editions, err := s.catalog.Editions(r.Context(), *entry.CatalogID)
				if err != nil {
					s.problem(w, r, err)
					return
				}
				for _, ed := range editions {
					if ed.ID == *in.EditionID {
						chosen = &library.Edition{ID: ed.ID, Title: ed.Title, Publisher: ed.Publisher, Year: ed.Year, Pages: ed.Pages, ISBN: ed.ISBN, Format: ed.Format, Language: ed.Language, ImageURL: ed.ImageURL}
					}
				}
				if chosen == nil {
					s.problem(w, r, fault.Invalid("Choose one of this title's editions."))
					return
				}
			}
			out, err := s.library.SetEdition(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in.Version, chosen)
			s.reply(w, r, out, err)
		})
		r.Put("/api/v1/library/{entryID}/episode-order", func(w http.ResponseWriter, r *http.Request) {
			if !s.catalogReady(w, r) {
				return
			}
			id, err := entryID(r)
			if err != nil {
				s.problem(w, r, fault.NotFound)
				return
			}
			in, err := decode[struct {
				Version int     `json:"version"`
				OrderID *string `json:"order_id"`
			}](w, r)
			if err == nil && in.OrderID != nil {
				// Loading the order stores it, so the next episode can follow it.
				var entry library.Entry
				if entry, err = s.library.Get(r.Context(), actor(r), id); err == nil {
					if entry.CatalogID == nil {
						err = fault.Invalid("Episode orders apply to shows from the catalog.")
					} else {
						_, err = s.catalog.EpisodeOrder(r.Context(), *entry.CatalogID, *in.OrderID)
					}
				}
			}
			var out library.Mutation
			if err == nil {
				out, err = s.library.SetEpisodeOrder(r.Context(), actor(r), id, r.Header.Get("Idempotency-Key"), in.Version, in.OrderID)
			}
			s.reply(w, r, out, err)
		})
	})
}
