package page

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnicodeValueCursor(t *testing.T) {
	for _, title := range []string{strings.Repeat("界", 200), strings.Repeat("😀", 200), strings.Repeat("\u0001", 200)} {
		c := EncodeValue("titles", title, 9223372036854775807)
		raw, id, ok, err := DecodeValue("titles", c)
		if err != nil || !ok || id != 9223372036854775807 {
			t.Fatalf("round trip failed for %d-byte cursor: %v", len(c), err)
		}
		var got string
		if json.Unmarshal(raw, &got) != nil || got != title {
			t.Fatal("sort value changed")
		}
	}
	if _, _, _, err := DecodeValue("titles", strings.Repeat("x", 8193)); err == nil {
		t.Fatal("unbounded cursor accepted")
	}
}
