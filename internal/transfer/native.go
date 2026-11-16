// Package transfer owns the versioned, bounded native library snapshot format.
// Catalog metadata and credentials are deliberately not accepted as writable data.
package transfer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxBytes = 64 << 20
const MaxRows = 100000

type table struct {
	name, columns string
	hasID         bool
}

// Ordering is part of version 2: referenced owner records precede their dependents.
var tables = []table{
	{"tags", "name,color,created_at", true},
	{"collection_fields", "name,kind,choices,position", true},
	{"library_entries", "catalog_item_id,title,media_type,publication_year,unit,total,status,progress,rating,notes,cycle,completed_cycles,next_item_id,created_at,updated_at,started_at,finished_at,last_activity_at,image_override,edition,episode_order,scored_at", true},
	{"personal_lists", "name,description,created_at,visibility,slug,kind,rules,sort,updated_at", true},
	{"owned_copies", "entry_id,catalog_item_id,format,edition,resolution,hdr,audio,bitrate_kbps,acquired_on,price_cents,currency,store,location,notes,custom,source,external_id,created_at,updated_at", true},
	{"plays", "entry_id,catalog_item_id,cycle,played_at,media_type,minutes,source,external_id,created_at,unmapped", true},
	{"skipped_items", "entry_id,catalog_item_id,cycle,created_at", false},
	{"entry_tags", "entry_id,tag_id", false},
	{"list_entries", "entry_id,list_id,added_at,position,note", false},
	{"activity_events", "entry_id,kind,unit,status,progress,cycle,created_at", true},
	{"playback_progress", "catalog_item_id,position_seconds,duration_seconds,source,updated_at", false},
	{"saved_views", "name,rules,sort,layout,position,version,created_at", true},
	{"episode_ratings", "entry_id,catalog_item_id,rating,scored_at,version", false},
	{"entry_status_history", "entry_id,previous_status,status,source,created_at", true},
	{"library_tombstones", "catalog_item_id,deleted_at", false},
}

type row struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
	Version   int             `json:"version,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Rows      int             `json:"rows,omitempty"`
	Digest    string          `json:"sha256,omitempty"`
}
type Reference struct {
	ID           int64  `json:"id"`
	Provider     string `json:"provider"`
	Kind         string `json:"kind"`
	ProviderID   string `json:"provider_id"`
	RootProvider string `json:"root_provider"`
	RootKind     string `json:"root_kind"`
	RootID       string `json:"root_id"`
	Season       *int   `json:"season_number"`
}

func IsNative(data []byte) bool {
	first, _, _ := bytes.Cut(data, []byte{'\n'})
	var r row
	return json.Unmarshal(first, &r) == nil && r.Type == "pastime"
}

// Write emits individual rows, never an entry-sized history array. A trailer makes
// a truncated successful HTTP response detectably incomplete on restore.
func Write(ctx context.Context, pool *pgxpool.Pool, a identity.Actor, w io.Writer) (int, error) {
	if !a.Allows("library:read") || !a.Allows("lists:read") {
		return 0, fault.Forbidden
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var ns string
	if err = tx.QueryRow(ctx, "SELECT u.transfer_namespace::text FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL AND u.active", a.TrackingID()).Scan(&ns); err != nil {
		return 0, err
	}
	h := sha256.New()
	size, count, entries := 0, 0, 0
	emit := func(r row, hash bool) error {
		b, e := json.Marshal(r)
		if e != nil {
			return e
		}
		b = append(b, '\n')
		size += len(b)
		if size > MaxBytes {
			return fault.Invalid("Native snapshot exceeds 64 MiB.")
		}
		if _, e = w.Write(b); e != nil {
			return e
		}
		if hash {
			h.Write(b)
		}
		return nil
	}
	if err = emit(row{Type: "pastime", Version: 3, Namespace: ns}, true); err != nil {
		return 0, err
	}
	refs := `WITH referenced AS (SELECT catalog_item_id id FROM library_entries WHERE profile_id=$1 UNION SELECT next_item_id FROM library_entries WHERE profile_id=$1 UNION SELECT catalog_item_id FROM plays WHERE profile_id=$1 UNION SELECT catalog_item_id FROM owned_copies WHERE profile_id=$1 UNION SELECT catalog_item_id FROM skipped_items WHERE profile_id=$1 UNION SELECT catalog_item_id FROM playback_progress WHERE profile_id=$1 UNION SELECT catalog_item_id FROM episode_ratings WHERE profile_id=$1 UNION SELECT catalog_item_id FROM library_tombstones WHERE profile_id=$1)
 SELECT jsonb_build_object('id',c.id,'provider',c.provider,'kind',c.kind,'provider_id',c.provider_id,'root_provider',r.provider,'root_kind',r.kind,'root_id',r.provider_id,'season_number',c.season_number) FROM catalog_items c JOIN referenced x ON x.id=c.id JOIN catalog_items r ON r.id=c.root_id ORDER BY c.id`
	stream := func(kind, q string) error {
		rs, e := tx.Query(ctx, q, a.TrackingID())
		if e != nil {
			return e
		}
		defer rs.Close()
		for rs.Next() {
			var b []byte
			if e = rs.Scan(&b); e != nil {
				return e
			}
			count++
			if count > MaxRows {
				return fault.Invalid("Native snapshot exceeds 100,000 records.")
			}
			if e = emit(row{Type: kind, Data: b}, true); e != nil {
				return e
			}
			if kind == "library_entries" {
				entries++
			}
		}
		return rs.Err()
	}
	if err = stream("catalog", refs); err != nil {
		return entries, err
	}
	for _, t := range tables {
		cols := t.columns
		if t.hasID {
			cols = "id," + cols
		}
		if err = stream(t.name, "SELECT row_to_json(r) FROM (SELECT "+cols+" FROM "+t.name+" WHERE profile_id=$1 ORDER BY "+strings.Split(cols, ",")[0]+") r"); err != nil {
			return entries, err
		}
	}
	if err = emit(row{Type: "end", Rows: count, Digest: hex.EncodeToString(h.Sum(nil))}, false); err != nil {
		return entries, err
	}
	return entries, tx.Commit(ctx)
}

func parse(data []byte) ([]row, string, error) {
	invalid := fault.Invalid("Invalid, incomplete or unsupported native snapshot.")
	if len(data) > MaxBytes {
		return nil, "", invalid
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 4096), 1<<20)
	var rows []row
	h := sha256.New()
	ns := ""
	ended := false
	line := 0
	for sc.Scan() {
		b := sc.Bytes()
		var r row
		if ended || json.Unmarshal(b, &r) != nil {
			return nil, "", invalid
		}
		line++
		if line == 1 {
			if r.Type != "pastime" || (r.Version != 2 && r.Version != 3) || !identity.ValidUUID(r.Namespace) {
				return nil, "", invalid
			}
			ns = r.Namespace
		} else if r.Type == "end" {
			if r.Rows != len(rows) || r.Digest != hex.EncodeToString(h.Sum(nil)) {
				return nil, "", invalid
			}
			ended = true
			continue
		} else {
			rows = append(rows, r)
			if len(rows) > MaxRows {
				return nil, "", invalid
			}
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	if sc.Err() != nil || !ended {
		return nil, "", invalid
	}
	return rows, ns, nil
}

// Restore is atomic and intentionally requires an empty target library/organization.
// Exact file retries are no-ops, including after later user edits. It never overwrites
// an existing library or publishes restored shares. Resolve validates catalog identities
// through normal provider paths before this transaction starts.
func Restore(ctx context.Context, pool *pgxpool.Pool, userID int64, runID string, data []byte, resolve func(context.Context, Reference) (int64, error)) (int, int, error) {
	rows, ns, err := parse(data)
	if err != nil {
		return 0, 0, err
	}
	sum := sha256.Sum256(data)
	catalog := map[string]int64{}
	for _, r := range rows {
		if r.Type == "catalog" {
			var ref Reference
			if json.Unmarshal(r.Data, &ref) != nil || ref.ID < 1 {
				return 0, 0, fault.Invalid("Invalid catalog reference.")
			}
			id, e := resolve(ctx, ref)
			if e != nil {
				return 0, 0, e
			}
			catalog[strconv.FormatInt(ref.ID, 10)] = id
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	var active bool
	if err = tx.QueryRow(ctx, "SELECT u.active FROM tracking_profiles p JOIN users u ON u.id=p.account_id WHERE p.id=$1 AND p.archived_at IS NULL FOR UPDATE OF p,u", userID).Scan(&active); err != nil {
		return 0, 0, err
	}
	if !active {
		return 0, 0, fault.Unauthenticated
	}
	var replay bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM native_restore_receipts WHERE profile_id=$1 AND digest=$2)", userID, sum[:]).Scan(&replay); err != nil {
		return 0, 0, err
	}
	if replay {
		if err = finishNativeRun(ctx, tx, userID, runID, 0, 0); err != nil {
			return 0, 0, err
		}
		return 0, 0, tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.'||$1::bigint::text,0))", userID); err != nil {
		return 0, 0, err
	}
	for _, t := range tables {
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+t.name+" WHERE profile_id=$1)", userID).Scan(&exists); err != nil {
			return 0, 0, err
		}
		if exists {
			return 0, 0, fault.Invalid("Native restore requires an empty library, lists, tags and collection fields. Use service imports to merge existing libraries.")
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL pastime.native_restore='on'"); err != nil {
		return 0, 0, err
	}
	maps := map[string]map[string]int64{"catalog": catalog}
	for _, t := range tables {
		maps[t.name] = map[string]int64{}
	}
	counts := map[string]int{}
	last := -1
	for _, r := range rows {
		if r.Type == "catalog" {
			if last >= 0 {
				return 0, 0, fault.Invalid("Catalog references must precede library records.")
			}
			continue
		}
		index := -1
		var t table
		for i, v := range tables {
			if v.name == r.Type {
				index = i
				t = v
				break
			}
		}
		if index < last || index < 0 {
			return 0, 0, fault.Invalid("Unknown or out-of-order native record.")
		}
		last = index
		var values map[string]any
		dec := json.NewDecoder(bytes.NewReader(r.Data))
		dec.UseNumber()
		if dec.Decode(&values) != nil || values == nil {
			return 0, 0, fault.Invalid("Invalid native record.")
		}
		allowed := map[string]bool{"id": t.hasID}
		for _, c := range strings.Split(t.columns, ",") {
			allowed[c] = true
		}
		for k := range values {
			if !allowed[k] {
				return 0, 0, fault.Invalid("Unknown native field.")
			}
		}
		original := fmt.Sprint(values["id"])
		delete(values, "id")
		if t.hasID {
			if _, e := strconv.ParseInt(original, 10, 64); e != nil {
				return 0, 0, fault.Invalid("Invalid source identity.")
			}
			if maps[t.name][original] != 0 {
				return 0, 0, fault.Invalid("Duplicate source identity.")
			}
		}
		remap := func(value any, target string) (any, error) {
			if value == nil {
				return nil, nil
			}
			id := maps[target][fmt.Sprint(value)]
			if id == 0 {
				return nil, fault.Invalid("Unresolved native reference.")
			}
			return id, nil
		}
		for field, target := range map[string]string{"catalog_item_id": "catalog", "next_item_id": "catalog", "entry_id": "library_entries", "list_id": "personal_lists", "tag_id": "tags"} {
			if v, ok := values[field]; ok {
				values[field], err = remap(v, target)
				if err != nil {
					return 0, 0, err
				}
			}
		}
		if t.name == "owned_copies" {
			if custom, ok := values["custom"].(map[string]any); ok {
				mapped := map[string]any{}
				for k, v := range custom {
					id, e := remap(k, "collection_fields")
					if e != nil {
						// Earlier collection imports used field names rather than decimal IDs.
						var named bool
						if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM collection_fields WHERE profile_id=$1 AND name=$2)", userID, k).Scan(&named); err != nil {
							return 0, 0, err
						}
						if !named {
							return 0, 0, e
						}
						mapped[k] = v
					} else {
						mapped[fmt.Sprint(id)] = v
					}
				}
				values["custom"] = mapped
			}
		}
		if t.name == "personal_lists" {
			values["visibility"] = "private"
			values["slug"] = nil
		}
		if t.name == "personal_lists" || t.name == "saved_views" {
			if rules, ok := values["rules"].(map[string]any); ok {
				if tags, ok := rules["tag_ids"].([]any); ok {
					for i, v := range tags {
						id, e := remap(v, "tags")
						if e != nil {
							return 0, 0, e
						}
						tags[i] = fmt.Sprint(id)
					}
				}
			}
		}
		if t.name == "library_entries" && values["episode_order"] != nil {
			var valid bool
			if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM episode_orders WHERE id=$1 AND show_id=$2)", values["episode_order"], values["catalog_item_id"]).Scan(&valid); err != nil {
				return 0, 0, err
			}
			if !valid {
				return 0, 0, fault.Invalid("An episode order is unavailable on this server.")
			}
		}
		values["profile_id"] = userID
		cols := t.columns + ",profile_id"
		if (t.name == "library_entries" || t.name == "plays") && runID != "" {
			values["import_run"] = runID
			cols += ",import_run"
		}
		b, e := json.Marshal(values)
		if e != nil {
			return 0, 0, e
		}
		q := "INSERT INTO " + t.name + " (" + cols + ") SELECT " + cols + " FROM jsonb_populate_record(NULL::" + t.name + ",$1::jsonb)"
		if t.hasID {
			var id int64
			err = tx.QueryRow(ctx, q+" RETURNING id", b).Scan(&id)
			maps[t.name][original] = id
		} else {
			_, err = tx.Exec(ctx, q, b)
		}
		if err != nil {
			return 0, 0, fmt.Errorf("restore %s: %w", t.name, err)
		}
		counts[t.name]++
	}
	if _, err = tx.Exec(ctx, "INSERT INTO native_restore_receipts(profile_id,namespace,digest) VALUES($1,$2,$3)", userID, ns, sum[:]); err != nil {
		return 0, 0, err
	}
	if err = finishNativeRun(ctx, tx, userID, runID, counts["library_entries"], counts["plays"]); err != nil {
		return 0, 0, err
	}
	return counts["library_entries"], counts["plays"], tx.Commit(ctx)
}

// The run outcome shares the restore transaction: a crash cannot leave committed
// data with a running import that loses its counts on replay.
func finishNativeRun(ctx context.Context, tx pgx.Tx, userID int64, runID string, entries, plays int) error {
	if runID == "" {
		return nil
	}
	tag, err := tx.Exec(ctx, `UPDATE import_runs SET status='succeeded',total=$3,processed=$3,imported=$3,plays_added=$4,unchanged=0,unmatched=0,failed=0,unmatched_sample='[]',error='',upload=NULL,last_applied_at=clock_timestamp(),finished_at=clock_timestamp() WHERE id=$1 AND profile_id=$2 AND status IN ('queued','running') AND NOT cancel_requested`, runID, userID, entries, plays)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Invalid("The native import is no longer active.")
	}
	return nil
}
