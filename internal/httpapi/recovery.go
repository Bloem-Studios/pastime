package httpapi

import (
	"net/http"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"

	"github.com/go-chi/chi/v5"
)

func (s *Server) recoveryRoutes() {
	s.Router.Post("/api/v1/password-recovery", s.publicPost("password-recovery", 5, func(w http.ResponseWriter, r *http.Request) {
		in, err := decode[struct {
			Username string `json:"username"`
			Code     string `json:"code"`
			Password string `json:"password"`
		}](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		if len(in.Password) < 12 || len(in.Password) > 128 || len(in.Username) > 48 || len(in.Code) > 40 {
			s.problem(w, r, fault.Invalid("Use a password of 12 to 128 characters."))
			return
		}
		// The per-address budget alone lets many addresses share the guessing of one
		// account's six-digit code; this caps attempts per account at five an hour.
		if err = s.accounts.Limit(r.Context(), accountBucket("recovery-account", in.Username), 5, time.Hour); err != nil {
			s.problem(w, r, err)
			return
		}
		if err = s.accounts.RecoverPassword(r.Context(), in.Username, in.Code, in.Password); err != nil {
			s.problem(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", householdSession))
		r.Get("/api/v1/me/totp", func(w http.ResponseWriter, r *http.Request) {
			out, err := s.accounts.TOTPStatus(r.Context(), actor(r))
			s.reply(w, r, out, err)
		})
		r.Post("/api/v1/me/totp", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Password string `json:"password"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !s.accountLimit(w, r, "totp", 10) || !s.passwordLimit(w, r) {
				return
			}
			out, err := s.accounts.BeginTOTP(r.Context(), actor(r), in.Password, "Pastime")
			s.replyStatus(w, r, 201, out, err)
		})
		r.Post("/api/v1/me/totp/confirm", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Code string `json:"code"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !s.accountLimit(w, r, "totp", 10) {
				return
			}
			codes, err := s.accounts.ConfirmTOTP(r.Context(), actor(r), in.Code)
			s.reply(w, r, map[string]any{"recovery_codes": codes}, err)
		})
		r.Delete("/api/v1/me/totp", func(w http.ResponseWriter, r *http.Request) {
			if !s.passwordLimit(w, r) {
				return
			}
			in, err := decode[struct {
				Password string `json:"password"`
			}](w, r)
			if err == nil {
				err = s.accounts.DisableTOTP(r.Context(), actor(r), in.Password)
			}
			if err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
}
