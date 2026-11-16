package page

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

type Result[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func Limit(v int) (int, error) {
	if v == 0 {
		return 30, nil
	}
	if v < 1 || v > 100 {
		return 0, fault.Invalid("Page size must be between 1 and 100.")
	}
	return v, nil
}
func stamp(scope string) string { h := sha256.Sum256([]byte(scope)); return hex.EncodeToString(h[:8]) }
func Encode(scope string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(stamp(scope) + ":" + strconv.FormatInt(id, 10)))
}

// Cursors are pagination hints, never authorization or synchronization checkpoints.
func Decode(scope, cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 100 {
		return 0, fault.Invalid("Invalid page cursor.")
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fault.Invalid("Invalid page cursor.")
	}
	prefix, n, ok := strings.Cut(string(data), ":")
	id, e := strconv.ParseInt(n, 10, 64)
	if !ok || e != nil || id < 1 || prefix != stamp(scope) {
		return 0, fault.Invalid("The page cursor does not match these filters.")
	}
	return id, nil
}

// EncodeAt is a keyset cursor for lists ordered by a timestamp then id, both descending.
func EncodeAt(scope string, at time.Time, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(stamp(scope) + ":" + strconv.FormatInt(at.UnixMicro(), 10) + ":" + strconv.FormatInt(id, 10)))
}

// DecodeAt reverses EncodeAt. An empty cursor returns ok=false.
func DecodeAt(scope, cursor string) (time.Time, int64, bool, error) {
	if cursor == "" {
		return time.Time{}, 0, false, nil
	}
	invalid := fault.Invalid("The page cursor does not match these filters.")
	if len(cursor) > 120 {
		return time.Time{}, 0, false, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, 0, false, invalid
	}
	parts := strings.Split(string(data), ":")
	if len(parts) != 3 || parts[0] != stamp(scope) {
		return time.Time{}, 0, false, invalid
	}
	micros, e1 := strconv.ParseInt(parts[1], 10, 64)
	id, e2 := strconv.ParseInt(parts[2], 10, 64)
	if e1 != nil || e2 != nil || id < 1 {
		return time.Time{}, 0, false, invalid
	}
	return time.UnixMicro(micros).UTC(), id, true, nil
}

// EncodeKey is a keyset cursor for lists ordered by a timestamp then a text key
// (such as a UUID), both descending.
func EncodeKey(scope string, at time.Time, key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(stamp(scope) + ":" + strconv.FormatInt(at.UnixMicro(), 10) + ":" + key))
}

// DecodeKey reverses EncodeKey. An empty cursor returns ok=false.
func DecodeKey(scope, cursor string) (time.Time, string, bool, error) {
	if cursor == "" {
		return time.Time{}, "", false, nil
	}
	invalid := fault.Invalid("The page cursor does not match these filters.")
	if len(cursor) > 160 {
		return time.Time{}, "", false, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", false, invalid
	}
	parts := strings.SplitN(string(data), ":", 3)
	if len(parts) != 3 || parts[0] != stamp(scope) || parts[2] == "" {
		return time.Time{}, "", false, invalid
	}
	micros, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil {
		return time.Time{}, "", false, invalid
	}
	return time.UnixMicro(micros).UTC(), parts[2], true, nil
}

// EncodeValue is a keyset cursor for lists ordered by an arbitrary scalar then an id.
func EncodeValue(scope string, value any, id int64) string {
	data, _ := json.Marshal([]any{stamp(scope), value, id})
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeValue reverses EncodeValue into the raw sort value and id. An empty cursor
// returns ok=false.
func DecodeValue(scope, cursor string) (json.RawMessage, int64, bool, error) {
	if cursor == "" {
		return nil, 0, false, nil
	}
	invalid := fault.Invalid("The page cursor does not match these filters.")
	if len(cursor) > 8192 {
		return nil, 0, false, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, 0, false, invalid
	}
	var parts []json.RawMessage
	var prefix string
	var id int64
	if json.Unmarshal(data, &parts) != nil || len(parts) != 3 || json.Unmarshal(parts[0], &prefix) != nil || prefix != stamp(scope) || json.Unmarshal(parts[2], &id) != nil || id < 1 {
		return nil, 0, false, invalid
	}
	return parts[1], id, true, nil
}
