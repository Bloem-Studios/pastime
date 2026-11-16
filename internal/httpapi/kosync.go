package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/kosync"
	"github.com/go-chi/chi/v5"
)

func (s *Server) kosync() kosync.Store { return kosync.Store{Pool: s.imports.Pool, Library: s.library} }

// kosyncRoutes serve KOReader's progress sync protocol under /kosync, so a device's
// custom sync server can be this address plus /kosync.
func (s *Server) kosyncRoutes() {
	r := s.Router
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	unauthorized := func(w http.ResponseWriter) { reply(w, 401, map[string]any{"code": 2001, "message": "Unauthorized"}) }
	auth := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		if s.imports.Pool == nil || s.accounts == nil {
			s.problem(w, r, fault.Unavailable)
			return 0, false
		}
		if err := s.accounts.Limit(r.Context(), "kosync:"+remoteBucket(r), 300, time.Minute); err != nil {
			s.problem(w, r, err)
			return 0, false
		}
		// Bound guessing per account as well as per address: one client gets a small
		// budget per name, and the account-wide ceiling covers many addresses.
		account := accountBucket("kosync-account", r.Header.Get("x-auth-user"))
		if err := s.accounts.Limit(r.Context(), account+":"+remoteBucket(r), 60, time.Minute); err != nil {
			s.problem(w, r, err)
			return 0, false
		}
		if err := s.accounts.Limit(r.Context(), account, 120, time.Minute); err != nil {
			s.problem(w, r, err)
			return 0, false
		}
		id, err := s.kosync().Authenticate(r.Context(), r.Header.Get("x-auth-user"), r.Header.Get("x-auth-key"))
		if errors.Is(err, kosync.ErrUnauthorized) {
			unauthorized(w)
			return 0, false
		}
		if err != nil {
			s.problem(w, r, err)
			return 0, false
		}
		return id, true
	}
	r.Get("/kosync/healthcheck", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]string{"state": "OK"}) })
	r.Post("/kosync/users/create", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 402, map[string]any{"code": 2002, "message": "Accounts are not created from KOReader. Set a KOReader password under Integrations, then sign in."})
	})
	r.Get("/kosync/users/auth", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth(w, r); ok {
			reply(w, 200, map[string]string{"authorized": "OK"})
		}
	})
	r.Put("/kosync/syncs/progress", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := auth(w, r)
		if !ok {
			return
		}
		var in kosync.Progress
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		if err != nil || json.Unmarshal(body, &in) != nil {
			reply(w, 400, map[string]any{"code": 2003, "message": "Invalid request"})
			return
		}
		out, err := s.kosync().Push(r.Context(), userID, in, r.Header.Get("x-auth-key"))
		if errors.Is(err, kosync.ErrUnauthorized) {
			unauthorized(w)
			return
		}
		var fe *fault.Error
		if errors.As(err, &fe) && fe.Status == 422 {
			reply(w, 400, map[string]any{"code": 2003, "message": fe.Message})
			return
		}
		if err != nil {
			s.problem(w, r, err)
			return
		}
		reply(w, 200, map[string]any{"document": out.Document, "timestamp": out.Timestamp})
	})
	r.Get("/kosync/syncs/progress/{document}", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := auth(w, r)
		if !ok {
			return
		}
		p, err := s.kosync().Pull(r.Context(), userID, chi.URLParam(r, "document"), r.Header.Get("x-auth-key"))
		if errors.Is(err, kosync.ErrUnauthorized) {
			unauthorized(w)
			return
		}
		if err != nil {
			s.problem(w, r, err)
			return
		}
		if p == nil {
			reply(w, 200, map[string]any{})
			return
		}
		reply(w, 200, p)
	})

	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/me/koreader", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.kosync().Status(r.Context(), actor(r))
			s.reply(w, r, out, err)
		})
		r.Put("/api/v1/me/koreader", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Password string `json:"password"`
			}](w, r)
			if err == nil && !s.accountLimit(w, r, "koreader-password", 10) {
				return
			}
			if err == nil {
				err = s.kosync().SetPassword(r.Context(), actor(r), in.Password)
			}
			var out kosync.Status
			if err == nil {
				out, err = s.kosync().Status(r.Context(), actor(r))
			}
			s.reply(w, r, out, err)
		})
		r.Delete("/api/v1/me/koreader", func(w http.ResponseWriter, r *http.Request) {
			if err := s.kosync().Disable(r.Context(), actor(r)); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
		r.Put("/api/v1/me/koreader/documents/{document}", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				EntryID *string `json:"entry_id"`
			}](w, r)
			if err == nil {
				entry := ""
				if in.EntryID != nil {
					entry = strings.TrimSpace(*in.EntryID)
				}
				err = s.kosync().Link(r.Context(), actor(r), chi.URLParam(r, "document"), entry)
			}
			var out kosync.Status
			if err == nil {
				out, err = s.kosync().Status(r.Context(), actor(r))
			}
			s.reply(w, r, out, err)
		})
	})
}
