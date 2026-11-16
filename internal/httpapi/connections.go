package httpapi

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/connections"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/go-chi/chi/v5"
)

type connectInput struct {
	Kind     string               `json:"kind"`
	Settings connections.Settings `json:"settings"`
}
type connectionUpdate struct {
	Settings connections.Settings `json:"settings"`
}

func (s *Server) connectionRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticatedAs("", browserSession))
		r.Get("/api/v1/connections", s.listConnections)
		r.Post("/api/v1/connections", s.connect)
		r.Patch("/api/v1/connections/{connectionID}", s.updateConnection)
		r.Delete("/api/v1/connections/{connectionID}", s.disconnect)
	})
	s.Router.Post("/webhooks/{kind}/{secret}", s.webhook)
}

func (s *Server) connectionsReady(w http.ResponseWriter, r *http.Request) bool {
	if s.connections.Pool == nil {
		s.problem(w, r, fault.Unavailable)
		return false
	}
	return true
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	if !s.connectionsReady(w, r) {
		return
	}
	out, err := s.connections.List(r.Context(), actor(r))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"items": out})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if !s.connectionsReady(w, r) {
		return
	}
	in, err := decode[connectInput](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if !s.accountLimit(w, r, "connection", 10) {
		return
	}
	c, raw, err := s.connections.Connect(r.Context(), actor(r), in.Kind, in.Settings)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 201, map[string]any{"connection": c, "webhook_url": s.origin + "/webhooks/" + c.Kind + "/" + raw})
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	if !s.connectionsReady(w, r) {
		return
	}
	in, err := decode[connectionUpdate](w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	c, err := s.connections.Update(r.Context(), actor(r), chi.URLParam(r, "connectionID"), in.Settings)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 200, c)
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if !s.connectionsReady(w, r) {
		return
	}
	if err := s.connections.Disconnect(r.Context(), actor(r), chi.URLParam(r, "connectionID")); err != nil {
		s.problem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// webhookBody extracts the JSON document. Plex posts multipart with a "payload" part and
// an optional thumbnail, which is skipped without buffering; others post JSON.
func webhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, fault.Invalid("Send JSON or multipart form data.")
	}
	if mediaType != "multipart/form-data" {
		return io.ReadAll(io.LimitReader(r.Body, 256<<10))
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, fault.Invalid("The payload part is missing.")
		}
		if err != nil {
			return nil, fault.Invalid("Malformed multipart body.")
		}
		if name := part.FormName(); name == "payload" || name == "data" {
			return io.ReadAll(io.LimitReader(part, 256<<10))
		}
	}
}

// webhook authenticates by path secret, filters by the configured server account, and
// durably enqueues completed viewings. It answers 202 for accepted and ignored events
// alike so media servers do not retry deliveries that are intentionally skipped.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.connectionsReady(w, r) || s.accounts == nil {
		s.problem(w, r, fault.Unavailable)
		return
	}
	kind := chi.URLParam(r, "kind")
	if err := s.accounts.Limit(r.Context(), "webhook:"+remoteBucket(r), 600, time.Minute); err != nil {
		s.problem(w, r, err)
		return
	}
	c, err := s.connections.Authenticate(r.Context(), kind, chi.URLParam(r, "secret"))
	if err != nil {
		s.problem(w, r, err)
		return
	}
	body, err := webhookBody(w, r)
	if err != nil {
		s.problem(w, r, err)
		return
	}
	parse := connections.ParseEmbyStyle
	switch kind {
	case "plex":
		parse = connections.ParsePlex
	case "generic":
		parse = connections.ParseGeneric
	case "seerr":
		parse = connections.ParseSeerr
	}
	ev, err := parse(body, time.Now().UTC())
	var ignored connections.Ignored
	if errors.As(err, &ignored) {
		respond(w, 202, map[string]string{"status": "ignored", "reason": string(ignored)})
		return
	}
	if err != nil {
		s.problem(w, r, err)
		return
	}
	if c.Settings.Username != "" && !strings.EqualFold(c.Settings.Username, ev.Account) {
		respond(w, 202, map[string]string{"status": "ignored", "reason": "event is for another media server account"})
		return
	}
	ev.EventID = connections.DeliveryID(body)
	if v := r.Header.Get("X-Event-ID"); v != "" {
		ev.EventID = v
	}
	if err = s.connections.Enqueue(r.Context(), c, ev); err != nil {
		s.problem(w, r, err)
		return
	}
	respond(w, 202, map[string]string{"status": "queued", "reason": ""})
}
