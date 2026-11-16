package httpapi

import (
	"net/http"
	"net/url"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/go-chi/chi/v5"
)

// deviceRoutes serve device sign-in. Starting and polling are public: apps on TVs and
// phones send no browser Origin and carry no cookie, so there is nothing to forge, and
// both are limited per address. Looking up and deciding a code need a browser session.
func (s *Server) deviceRoutes() {
	s.Router.Post("/api/v1/device/authorize", s.deviceLimited("device-start", 10, func(w http.ResponseWriter, r *http.Request) {
		in, err := decode[struct {
			ClientName string   `json:"client_name"`
			Scopes     []string `json:"scopes"`
		}](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		out, err := s.accounts.StartDevice(r.Context(), in.ClientName, in.Scopes)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		respond(w, 200, struct {
			identity.DeviceStart
			VerificationURI         string `json:"verification_uri"`
			VerificationURIComplete string `json:"verification_uri_complete"`
		}{out, s.origin + "/device", s.origin + "/device?" + url.Values{"code": {out.UserCode}}.Encode()})
	}))
	s.Router.Post("/api/v1/device/token", s.deviceLimited("device-poll", 60, func(w http.ResponseWriter, r *http.Request) {
		in, err := decode[struct {
			DeviceCode string `json:"device_code"`
		}](w, r)
		if err != nil {
			s.problem(w, r, err)
			return
		}
		out, err := s.accounts.PollDevice(r.Context(), in.DeviceCode)
		s.reply(w, r, out, err)
	}))
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/device", func(w http.ResponseWriter, r *http.Request) {
			if !s.deviceGuessLimit(w, r) {
				return
			}
			out, err := s.accounts.LookupDevice(r.Context(), actor(r), r.URL.Query().Get("code"))
			s.reply(w, r, out, err)
		})
		r.Post("/api/v1/device/decision", func(w http.ResponseWriter, r *http.Request) {
			in, err := decode[struct {
				Code    string `json:"code"`
				Approve bool   `json:"approve"`
			}](w, r)
			if err != nil {
				s.problem(w, r, err)
				return
			}
			if !s.deviceGuessLimit(w, r) {
				return
			}
			if err = s.accounts.DecideDevice(r.Context(), actor(r), in.Code, in.Approve); err != nil {
				s.problem(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
}

// deviceLimited limits a public device endpoint per client address.
func (s *Server) deviceLimited(label string, perMinute int, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.accounts == nil {
			s.problem(w, r, fault.Unavailable)
			return
		}
		if err := s.accounts.Limit(r.Context(), label+":"+remoteBucket(r), perMinute, time.Minute); err != nil {
			s.problem(w, r, err)
			return
		}
		next(w, r)
	}
}

// deviceGuessLimit bounds how many codes one account can try: 10 a minute, 30 an hour.
func (s *Server) deviceGuessLimit(w http.ResponseWriter, r *http.Request) bool {
	if !s.accountLimit(w, r, "device-code", 10) {
		return false
	}
	if err := s.accounts.Limit(r.Context(), "device-code-hour:"+actor(r).PublicUser().Username, 30, time.Hour); err != nil {
		s.problem(w, r, err)
		return false
	}
	return true
}
