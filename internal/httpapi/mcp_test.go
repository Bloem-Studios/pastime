package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/catalog"
	"github.com/Bloem-Studios/pastime/internal/catalog/provider"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

type searchableTMDB struct{ syntheticTMDB }

func (searchableTMDB) Search(_ context.Context, kind, q string) ([]provider.Record, error) {
	if kind == "series" {
		return []provider.Record{{Provider: "tmdb", Kind: "series", ProviderID: "100", Title: "Synthetic Harbour"}}, nil
	}
	return []provider.Record{{Provider: "tmdb", Kind: "movie", ProviderID: "42", Title: "Synthetic Film"}}, nil
}

func TestMCPTools(t *testing.T) {
	f := testkit.New(t)
	cat := &catalog.Service{Pool: f.Pool, Providers: map[string]provider.Provider{"tmdb": searchableTMDB{}}, Defaults: map[string]string{"movie": "tmdb", "series": "tmdb"}}
	s, err := New(ready, nil, "test", Options{Pool: f.Pool, PublicOrigin: "http://127.0.0.1:8080", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, write, _ := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Assistant", Scopes: []string{"library:read", "library:write"}, ExpiresInDays: 30, Password: testkit.Password})
	_, read, _ := f.Accounts.MintToken(ctx, f.Owner, identity.TokenInput{Name: "Reader", Scopes: []string{"library:read"}, ExpiresInDays: 30, Password: testkit.Password})
	id := 0
	call := func(token, method string, params any) map[string]any {
		t.Helper()
		id++
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(body)))
		req.RemoteAddr = "127.0.0.1:51234"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		s.Router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatal(method, rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	tool := func(token, name string, args any) (string, bool) {
		t.Helper()
		out := call(token, "tools/call", map[string]any{"name": name, "arguments": args})
		result, _ := out["result"].(map[string]any)
		if result == nil {
			t.Fatalf("%s: %v", name, out)
		}
		content := result["content"].([]any)[0].(map[string]any)["text"].(string)
		isErr, _ := result["isError"].(bool)
		return content, isErr
	}
	if init := call(write, "initialize", map[string]any{"protocolVersion": mcpProtocol}); init["result"].(map[string]any)["protocolVersion"] != mcpProtocol {
		t.Fatal(init)
	}
	if names := call(read, "tools/list", nil); strings.Contains(mustJSON(names), "record_play") {
		t.Fatal("write tools listed for a read-only token")
	}
	if text, isErr := tool(read, "add_to_library", map[string]any{"provider": "tmdb", "media_type": "movie", "provider_id": "42"}); !isErr || !strings.Contains(text, "scope") {
		t.Fatal("read-only token added a title", text)
	}
	if text, _ := tool(write, "search_catalog", map[string]any{"media_type": "series", "query": "harbour"}); !strings.Contains(text, "Synthetic Harbour") {
		t.Fatal(text)
	}
	if text, isErr := tool(write, "search_catalog", map[string]any{"media_type": "series", "query": "x", "extra": 1}); !isErr || !strings.Contains(text, "Unknown argument") {
		t.Fatal("unknown argument accepted", text)
	}
	text, isErr := tool(write, "add_to_library", map[string]any{"provider": "tmdb", "media_type": "series", "provider_id": "100", "status": "in_progress"})
	if isErr {
		t.Fatal(text)
	}
	var added struct {
		EntryID string `json:"entry_id"`
	}
	_ = json.Unmarshal([]byte(text), &added)
	if text, isErr = tool(write, "record_play", map[string]any{"entry_id": added.EntryID, "season": 1, "episode": 2}); isErr || !strings.Contains(text, `"recorded":true`) {
		t.Fatal(text)
	}
	if text, _ = tool(write, "history", map[string]any{"limit": 5}); !strings.Contains(text, "Episode 2") {
		t.Fatal(text)
	}
	if text, isErr = tool(write, "record_play", map[string]any{"entry_id": added.EntryID, "season": 9, "episode": 9}); !isErr {
		t.Fatal("missing episode recorded", text)
	}
	if text, _ = tool(read, "up_next", map[string]any{}); !strings.Contains(text, "Episode 1") {
		t.Fatal(text)
	}
	if out := call(write, "nope", nil); out["error"] == nil {
		t.Fatal("unknown method answered")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
