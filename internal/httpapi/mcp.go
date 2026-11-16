package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/go-chi/chi/v5"
)

// The MCP endpoint lets assistants read and update a library through a personal token,
// using the Model Context Protocol's streamable HTTP transport with JSON responses.
// Tools call the same stores as the REST API, so scopes and rules are identical.

const mcpProtocol = "2025-06-18"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
	scope       string
	call        func(ctx context.Context, a identity.Actor, args json.RawMessage) (any, error)
}

func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func prop(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

var trackableKinds = []any{"movie", "series", "anime", "manga", "book", "audiobook", "comic", "game", "boardgame", "music", "podcast", "video"}

func idempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (s *Server) mcpTools() []mcpTool {
	readOnly := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	return []mcpTool{
		{Name: "search_catalog", Title: "Search the catalog", scope: "library:read", Annotations: map[string]any{"readOnlyHint": true, "openWorldHint": true},
			Description: "Find movies, shows, books, games, music and more by title. Returns provider records; use their provider, media_type and provider_id with add_to_library.",
			InputSchema: schema(map[string]any{"media_type": map[string]any{"type": "string", "enum": trackableKinds}, "query": prop("string", "Title, or an ISBN for books.")}, "media_type", "query"),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct{ MediaType, Query string }
				if err := strictArgs(raw, &in, "media_type", "query"); err != nil {
					return nil, err
				}
				out, err := s.catalog.Search(ctx, in.MediaType, in.Query)
				if len(out.Items) > 10 {
					out.Items = out.Items[:10]
				}
				return out, err
			}},
		{Name: "list_library", Title: "List your library", scope: "library:read", Annotations: readOnly,
			Description: "Titles you track, newest activity first. Filter by status or media type, or search your own titles.",
			InputSchema: schema(map[string]any{"status": map[string]any{"type": "string", "enum": []any{"planned", "in_progress", "paused", "dropped", "completed", "rated"}},
				"media_type": map[string]any{"type": "string", "enum": trackableKinds}, "query": prop("string", "Words from the title."), "cursor": prop("string", "Continuation cursor returned by the previous page."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct {
					Status, MediaType, Query, Cursor string
					Limit                            int
				}
				if err := strictArgs(raw, &in, "status", "media_type", "query", "limit", "cursor"); err != nil {
					return nil, err
				}
				if in.Limit < 1 || in.Limit > 50 {
					in.Limit = 20
				}
				page, err := s.library.Entries(ctx, a, library.Filter{Status: in.Status, MediaType: in.MediaType, Query: in.Query, Sort: "activity", Limit: in.Limit, Cursor: in.Cursor})
				return page, err
			}},
		{Name: "up_next", Title: "What to watch next", scope: "library:read", Annotations: readOnly,
			Description: "Titles in progress, the next unwatched episode of each show, upcoming releases and your counts.",
			InputSchema: schema(map[string]any{}),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				return s.library.Home(ctx, a, time.Now())
			}},
		{Name: "history", Title: "Play history", scope: "library:read", Annotations: readOnly,
			Description: "What you watched, read, played or listened to, newest first, optionally within dates (YYYY-MM-DD, UTC).",
			InputSchema: schema(map[string]any{"from": prop("string", "Inclusive start date."), "to": prop("string", "Exclusive end date."), "cursor": prop("string", "Continuation cursor returned by the previous page."), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}}),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct {
					From, To, Cursor string
					Limit            int
				}
				if err := strictArgs(raw, &in, "from", "to", "limit", "cursor"); err != nil {
					return nil, err
				}
				f := library.HistoryFilter{Limit: in.Limit, Cursor: in.Cursor}
				if f.Limit < 1 || f.Limit > 100 {
					f.Limit = 30
				}
				for _, d := range []struct {
					raw string
					dst **time.Time
				}{{in.From, &f.From}, {in.To, &f.To}} {
					if d.raw != "" {
						t, err := time.Parse("2006-01-02", d.raw)
						if err != nil {
							return nil, fault.Invalid("Dates are YYYY-MM-DD.")
						}
						*d.dst = &t
					}
				}
				page, err := s.library.History(ctx, a, f)
				return page, err
			}},
		{Name: "stats", Title: "Statistics", scope: "library:read", Annotations: readOnly,
			Description: "Totals and breakdowns for a period (YYYY-MM-DD, UTC); defaults to the last year.",
			InputSchema: schema(map[string]any{"from": prop("string", "Inclusive start date."), "to": prop("string", "Exclusive end date.")}),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct{ From, To string }
				if err := strictArgs(raw, &in, "from", "to"); err != nil {
					return nil, err
				}
				to := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
				from := to.AddDate(-1, 0, 0)
				if in.To != "" {
					parsed, err := time.Parse("2006-01-02", in.To)
					if err != nil {
						return nil, fault.Invalid("Dates are YYYY-MM-DD.")
					}
					to = parsed
				}
				if in.From != "" {
					parsed, err := time.Parse("2006-01-02", in.From)
					if err != nil {
						return nil, fault.Invalid("Dates are YYYY-MM-DD.")
					}
					from = parsed
				}
				out, err := s.library.Insights(ctx, a, from, to)
				out.Days = nil // Daily buckets are too long for a conversation.
				return out, err
			}},
		{Name: "year_in_review", Title: "Year in Review", scope: "library:read", Annotations: readOnly,
			Description: "A calendar year (UTC) in review: totals against the year before, months, streaks, the top titles of each media type, the first play and new titles. Defaults to this year, reviewed so far.",
			InputSchema: schema(map[string]any{"year": prop("integer", "Four-digit year, up to this one.")}),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct{ Year int }
				if err := strictArgs(raw, &in, "year"); err != nil {
					return nil, err
				}
				now := time.Now()
				if in.Year == 0 {
					in.Year = now.UTC().Year()
				}
				out, err := s.library.Review(ctx, a, in.Year, now)
				out.Stats.Days = nil // Daily buckets are too long for a conversation.
				return out, err
			}},
		{Name: "add_to_library", Title: "Add to your library", scope: "library:write", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true},
			Description: "Track a title from search_catalog. A title already tracked is left as it is and returned.",
			InputSchema: schema(map[string]any{"provider": prop("string", "From search_catalog."), "media_type": map[string]any{"type": "string", "enum": trackableKinds}, "provider_id": prop("string", "From search_catalog."),
				"status": map[string]any{"type": "string", "enum": []any{"planned", "in_progress", "completed"}}}, "provider", "media_type", "provider_id"),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct{ Provider, MediaType, ProviderID, Status string }
				if err := strictArgs(raw, &in, "provider", "media_type", "provider_id", "status"); err != nil {
					return nil, err
				}
				item, err := s.catalog.Resolve(ctx, in.Provider, in.MediaType, in.ProviderID)
				if err != nil {
					return nil, err
				}
				if in.Status == "" {
					in.Status = "planned"
				}
				if existing, err := s.library.EntryForCatalog(ctx, a, item.ID); err != nil || existing != nil {
					return map[string]any{"title": item.Title, "entry": existing, "already_tracked": true}, err
				}
				m, err := s.library.Track(ctx, a, idempotencyKey(), library.TrackInput{CatalogID: item.ID, Status: in.Status})
				return map[string]any{"title": item.Title, "entry_id": fmt.Sprint(m.ID), "status": in.Status}, err
			}},
		{Name: "record_play", Title: "Record a play", scope: "library:write", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false},
			Description: "Record that you watched, read, played or listened to a tracked title, optionally one episode by season and episode number. The same viewing within three hours is recorded once.",
			InputSchema: schema(map[string]any{"entry_id": prop("string", "From list_library."), "season": map[string]any{"type": "integer", "minimum": 0}, "episode": map[string]any{"type": "integer", "minimum": 1},
				"played_at": prop("string", "RFC 3339 time; defaults to now.")}, "entry_id"),
			call: func(ctx context.Context, a identity.Actor, raw json.RawMessage) (any, error) {
				var in struct {
					EntryID         string
					Season, Episode *int
					PlayedAt        string
				}
				if err := strictArgs(raw, &in, "entry_id", "season", "episode", "played_at"); err != nil {
					return nil, err
				}
				id, err := identity.ParseID(in.EntryID)
				if err != nil {
					return nil, fault.NotFound
				}
				entry, err := s.library.Get(ctx, a, id)
				if err != nil {
					return nil, err
				}
				if entry.CatalogID == nil {
					return nil, fault.Invalid("Plays can be recorded for catalogued titles only.")
				}
				at := time.Now().UTC()
				if in.PlayedAt != "" {
					if at, err = time.Parse(time.RFC3339, in.PlayedAt); err != nil || at.After(time.Now().Add(time.Minute)) {
						return nil, fault.Invalid("played_at is an RFC 3339 time, not in the future.")
					}
				}
				target := *entry.CatalogID
				if in.Season != nil || in.Episode != nil {
					if in.Season == nil || in.Episode == nil {
						return nil, fault.Invalid("Give both season and episode.")
					}
					episodes, err := s.catalog.EpisodeMap(ctx, *entry.CatalogID)
					if err != nil {
						return nil, err
					}
					var ok bool
					if target, ok = episodes[[2]int{*in.Season, *in.Episode}]; !ok {
						return nil, fault.Invalid("That episode is not listed for this show.")
					}
				}
				recorded, err := s.library.ScrobbleAs(ctx, a, "mcp", *entry.CatalogID, target, at)
				return map[string]any{"title": entry.Title, "recorded": recorded}, err
			}},
	}
}

// strictArgs decodes tool arguments, refusing unknown names.
func strictArgs(raw json.RawMessage, into any, allowed ...string) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	var names map[string]json.RawMessage
	if err := json.Unmarshal(raw, &names); err != nil {
		return fault.Invalid("Arguments must be an object.")
	}
	for name := range names {
		known := false
		for _, a := range allowed {
			known = known || a == name
		}
		if !known {
			return fault.Invalid("Unknown argument " + name + ".")
		}
	}
	// Names are snake_case in the schema; underscores are dropped so they match Go field
	// names case-insensitively, so target structs must not carry json tags.
	normalized := map[string]json.RawMessage{}
	for name, v := range names {
		normalized[strings.ReplaceAll(name, "_", "")] = v
	}
	b, _ := json.Marshal(normalized)
	if err := json.Unmarshal(b, into); err != nil {
		return fault.Invalid("An argument has the wrong type.")
	}
	return nil
}

func (s *Server) mcpRoutes() {
	s.Router.Group(func(r chi.Router) {
		r.Use(s.authenticated("library:read", false, false))
		r.Post("/mcp", s.mcp) // No server-sent stream: other methods answer 405.
	})
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w, r) || !s.accountLimit(w, r, "mcp", 120) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var req mcpRequest
	if err != nil || json.Unmarshal(body, &req) != nil || req.JSONRPC != "2.0" || req.Method == "" {
		respond(w, 400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": mcpError{Code: -32700, Message: "Send one JSON-RPC 2.0 request."}})
		return
	}
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted) // Notifications need no answer.
		return
	}
	reply := func(result any, e *mcpError) {
		out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if e != nil {
			out["error"] = e
		} else {
			out["result"] = result
		}
		respond(w, 200, out)
	}
	a := actor(r)
	tools := s.mcpTools()
	switch req.Method {
	case "initialize":
		reply(map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":   map[string]any{"name": "pastime", "title": "Pastime", "version": "0.3"},
			"instructions": "Tools read and update this person's media library. Search the catalog before adding titles; record plays against entries from list_library."}, nil)
	case "ping":
		reply(map[string]any{}, nil)
	case "tools/list":
		visible := []mcpTool{}
		for _, t := range tools {
			if a.Allows(t.scope) {
				visible = append(visible, t)
			}
		}
		reply(map[string]any{"tools": visible}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		for _, t := range tools {
			if t.Name != p.Name {
				continue
			}
			if !a.Allows(t.scope) {
				reply(toolResult(nil, &fault.Error{Status: 403, Code: "forbidden", Message: "This token lacks the scope this tool needs."}), nil)
				return
			}
			if err := validateMCPArgs(p.Arguments, t.InputSchema); err != nil {
				reply(toolResult(nil, err), nil)
				return
			}
			out, err := t.call(r.Context(), a, p.Arguments)
			var fe *fault.Error
			if err != nil && !errors.As(err, &fe) {
				s.logger.Error("mcp tool failed", "tool", t.Name)
			}
			reply(toolResult(out, err), nil)
			return
		}
		reply(nil, &mcpError{Code: -32602, Message: "Unknown tool " + p.Name + "."})
	default:
		reply(nil, &mcpError{Code: -32601, Message: "Method not found."})
	}
}

// toolResult reports tool failures inside the result, as MCP expects, so the model can
// read the reason and adjust.
func toolResult(out any, err error) map[string]any {
	if err != nil {
		message := "The request failed; try again later."
		var fe *fault.Error
		if errors.As(err, &fe) {
			message = fe.Message
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": message}}, "isError": true}
	}
	text, _ := json.Marshal(out)
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}}
	if m, ok := out.(map[string]any); ok {
		result["structuredContent"] = m
	}
	return result
}

// Validate the published tool contract before any tool can run.
func validateMCPArgs(raw json.RawMessage, spec map[string]any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return fault.Invalid("Arguments must be an object.")
	}
	props := spec["properties"].(map[string]any)
	for _, name := range spec["required"].([]string) {
		if v, ok := args[name]; !ok || v == nil {
			return fault.Invalid("Missing required argument " + name + ".")
		}
	}
	for name, value := range args {
		p, ok := props[name].(map[string]any)
		if !ok {
			return fault.Invalid("Unknown argument " + name + ".")
		}
		valid := false
		switch p["type"] {
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "integer", "number":
			n, ok := value.(float64)
			valid = ok
			if ok {
				if p["type"] == "integer" && math.Trunc(n) != n {
					valid = false
				}
				if min, ok := p["minimum"].(int); ok && n < float64(min) {
					valid = false
				}
				if max, ok := p["maximum"].(int); ok && n > float64(max) {
					valid = false
				}
			}
		}
		if !valid {
			return fault.Invalid("An argument has the wrong type or range.")
		}
		if values, ok := p["enum"].([]any); ok {
			found := false
			for _, allowed := range values {
				if value == allowed {
					found = true
				}
			}
			if !found {
				return fault.Invalid("Unsupported value for " + name + ".")
			}
		}
	}
	return nil
}
