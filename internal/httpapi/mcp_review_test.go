package httpapi

import (
	"encoding/json"
	"testing"
)

func TestMCPArgumentsMatchSchema(t *testing.T) {
	spec := schema(map[string]any{"count": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "kind": map[string]any{"type": "string", "enum": []any{"book", "movie"}}}, "kind")
	for _, raw := range []string{`{}`, `{"kind":null}`, `{"kind":"unknown"}`, `{"kind":"book","count":0}`, `{"kind":"book","count":51}`, `{"kind":"book","count":1.5}`, `{"kind":"book","extra":true}`} {
		if validateMCPArgs(json.RawMessage(raw), spec) == nil {
			t.Error("accepted", raw)
		}
	}
	if err := validateMCPArgs(json.RawMessage(`{"kind":"book","count":12}`), spec); err != nil {
		t.Fatal(err)
	}
}
