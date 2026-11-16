package library

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
	"unicode/utf8"
)

func validateCustomFields(ctx context.Context, tx pgx.Tx, userID, copyID int64, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	// Deleted fields may retain their existing value, but cannot introduce new keys.
	old := map[string]any{}
	if copyID > 0 {
		if err := tx.QueryRow(ctx, "SELECT custom FROM owned_copies WHERE profile_id=$1 AND id=$2", userID, copyID).Scan(&old); err != nil {
			return err
		}
	}
	for key, value := range values {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id < 1 {
			return fault.Invalid("Choose one of your collection fields.")
		}
		var kind string
		var choices []string
		err = tx.QueryRow(ctx, "SELECT kind,choices FROM collection_fields WHERE profile_id=$1 AND id=$2 FOR SHARE", userID, id).Scan(&kind, &choices)
		if err == pgx.ErrNoRows {
			before, present := old[key]
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(value)
			if present && string(a) == string(b) {
				continue
			}
			return fault.Invalid("Choose one of your collection fields.")
		}
		if err != nil {
			return err
		}
		if value == nil || value == "" {
			continue
		}
		valid := false
		switch kind {
		case "text":
			v, ok := value.(string)
			valid = ok && utf8.RuneCountInString(v) <= 2000
		case "number":
			raw, _ := json.Marshal(value)
			var n float64
			valid = json.Unmarshal(raw, &n) == nil
		case "boolean":
			_, valid = value.(bool)
		case "date":
			v, ok := value.(string)
			_, err := time.Parse("2006-01-02", v)
			valid = ok && err == nil
		case "choice":
			v, ok := value.(string)
			if ok {
				for _, choice := range choices {
					valid = valid || v == choice
				}
			}
		}
		if !valid {
			return fault.Invalid("A collection field has an invalid value.")
		}
	}
	return nil
}
