package bloem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func exportListEntry(e library.ListExportEntry) ListSnapshotEntry {
	out := ListSnapshotEntry{Title: e.Title, Year: e.Year, SourceEntryID: e.ID, ExternalIDs: map[string]string{}}
	switch e.MediaType {
	case "movie", "series", "audiobook":
		out.Kind = e.MediaType
	case "book":
		out.Kind = "ebook"
	default:
		out.Kind = "unsupported"
		out.UnsupportedReason = "unsupported_kind"
		return out
	}
	book := out.Kind == "ebook" || out.Kind == "audiobook"
	if book && e.EditionISBN != "" {
		out.ExternalIDs["isbn"] = e.EditionISBN
		if !listIdentityValid("isbn", e.EditionISBN) {
			out.Kind = "unsupported"
			out.UnsupportedReason = "invalid_identity"
			out.ExternalIDs = map[string]string{}
		}
		return out
	}
	allowed := func(k string) bool {
		if book {
			return k == "isbn" || k == "asin"
		}
		return k == "tmdb" || k == "imdb" || k == "tvdb" && out.Kind == "series"
	}
	for ns, id := range e.ExternalIDs {
		if allowed(ns) && id != "" {
			out.ExternalIDs[ns] = id
		}
	}
	if allowed(e.Provider) && e.ProviderID != "" {
		if previous := out.ExternalIDs[e.Provider]; previous != "" && previous != e.ProviderID {
			out.Kind = "unsupported"
			out.UnsupportedReason = "ambiguous_identity"
			out.ExternalIDs = map[string]string{}
			return out
		}
		out.ExternalIDs[e.Provider] = e.ProviderID
	}
	if len(out.ExternalIDs) == 0 {
		out.Kind = "unsupported"
		out.UnsupportedReason = "missing_identity"
	}
	for ns, id := range out.ExternalIDs {
		if !listIdentityValid(ns, id) {
			out.Kind = "unsupported"
			out.UnsupportedReason = "invalid_identity"
			out.ExternalIDs = map[string]string{}
			break
		}
	}
	return out
}

var listDecimal = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var listIMDb = regexp.MustCompile(`^tt[0-9]{7,12}$`)
var listASIN = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func listIdentityValid(namespace, value string) bool {
	switch namespace {
	case "tmdb", "tvdb":
		if !listDecimal.MatchString(value) {
			return false
		}
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	case "imdb":
		return listIMDb.MatchString(value)
	case "asin":
		return listASIN.MatchString(value)
	case "isbn":
		if len(value) != 10 && len(value) != 13 {
			return false
		}
		if len(value) == 13 && value[:3] != "978" && value[:3] != "979" {
			return false
		}
		sum := 0
		for index, char := range value {
			digit := int(char - '0')
			if len(value) == 10 && index == 9 && char == 'X' {
				digit = 10
			} else if char < '0' || char > '9' {
				return false
			}
			weight := 10 - index
			if len(value) == 13 {
				weight = 1 + 2*(index%2)
			}
			sum += weight * digit
		}
		if len(value) == 13 {
			return sum%10 == 0
		}
		return sum%11 == 0
	}
	return false
}

func sourceListDigest(entries []ListSnapshotEntry) (string, error) {
	if entries == nil {
		entries = []ListSnapshotEntry{}
	}
	payload, err := json.Marshal(struct {
		Version int                 `json:"version"`
		Entries []ListSnapshotEntry `json:"entries"`
	}{1, entries})
	if err != nil {
		return "", err
	}
	if len(payload) > 8<<20 {
		return "", fault.Invalid("This list exceeds the 8 MiB sync limit.")
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

func (s Store) createListSnapshot(ctx context.Context, raw string, in ListSnapshotInput) (string, error) {
	for attempt := 0; attempt < 4; attempt++ {
		id, err := s.createListSnapshotOnce(ctx, raw, in)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "40001" && pgerr.Code != "40P01" {
			return id, err
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", fault.Conflict
}

func (s Store) createListSnapshotOnce(ctx context.Context, raw string, in ListSnapshotInput) (string, error) {
	// Serializable catches stale admission predicates when another worker
	// commits while this transaction waits for the profile's advisory lane.
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	a, _, native, reader, err := s.listGrantTx(ctx, tx, raw, in)
	if err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "bloem.list.export:"+strconv.FormatInt(a.ProfileID, 10)); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `WITH old AS (SELECT id FROM bloem_list_export_snapshots WHERE profile_native_id=$1 AND expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 16) DELETE FROM bloem_list_export_snapshots s USING old WHERE s.id=old.id`, a.ProfileID); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_list_export_snapshots WHERE profile_native_id=$1 AND expires_at>clock_timestamp()`, a.ProfileID).Scan(&count); err != nil {
		return "", err
	}
	if count >= 8 {
		return "", fault.Invalid("Too many list snapshots are active. Retry after they expire.")
	}
	nativeEntries, err := (library.Store{Pool: s.Pool}).ExportListTx(ctx, tx, reader, native.ID, native.Family == "collaborative")
	if err != nil {
		return "", err
	}
	entries := make([]ListSnapshotEntry, len(nativeEntries))
	for i, e := range nativeEntries {
		entries[i] = exportListEntry(e)
	}
	digest, err := sourceListDigest(entries)
	if err != nil {
		return "", err
	}
	var id string
	err = tx.QueryRow(ctx, `WITH stamp AS (SELECT clock_timestamp() AS t) INSERT INTO bloem_list_export_snapshots(instance_id,profile_native_id,account_id,profile_id,source_revision,source_id,resolution_token,total,digest,generated_at,expires_at)
	SELECT $1,$2,$3,$4,p.source_revision,$5,$6,$7,$8,stamp.t,stamp.t+interval '120 seconds' FROM bloem_profiles p,stamp WHERE p.instance_id=$1 AND p.profile_id=$2 RETURNING id::text`, a.InstanceID, a.ProfileID, in.AccountID, in.ProfileID, in.SourceID, in.ResolutionToken, len(entries), digest).Scan(&id)
	if err != nil {
		return "", err
	}
	rows := make([][]any, len(entries))
	for i, e := range entries {
		payload, err := json.Marshal(e)
		if err != nil {
			return "", err
		}
		rows[i] = []any{id, i, payload}
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"bloem_list_export_rows"}, []string{"snapshot_id", "ordinal", "payload"}, pgx.CopyFromRows(rows)); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s Store) listSnapshotTx(ctx context.Context, tx pgx.Tx, raw string, in ListSnapshotInput, id string) (ListSnapshotPage, error) {
	var out ListSnapshotPage
	a, _, _, _, err := s.listGrantTx(ctx, tx, raw, in)
	if err != nil {
		return out, err
	}
	var token string
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.source_id,s.total,s.digest,s.generated_at,s.expires_at,s.resolution_token FROM bloem_list_export_snapshots s
	JOIN bloem_profiles p ON p.instance_id=s.instance_id AND p.profile_id=s.profile_native_id AND p.source_revision=s.source_revision
	WHERE s.id=$1::uuid AND s.instance_id=$2 AND s.profile_native_id=$3 AND s.account_id=$4 AND s.profile_id=$5 AND s.expires_at>clock_timestamp() FOR SHARE OF s`, id, a.InstanceID, a.ProfileID, in.AccountID, in.ProfileID).Scan(&out.SnapshotID, &out.SourceID, &out.Total, &out.Digest, &out.GeneratedAt, &out.ExpiresAt, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, fault.Conflict
	}
	if err != nil {
		return out, err
	}
	if token != in.ResolutionToken || out.SourceID != in.SourceID {
		return out, fault.Forbidden
	}
	out.Version = 1
	out.Entries = []ListSnapshotEntry{}
	return out, nil
}

func (s Store) ReadListSnapshot(ctx context.Context, raw string, in ListSnapshotInput) (ListSnapshotPage, error) {
	var cursor listCursor
	if in.Cursor == "" {
		id, err := s.createListSnapshot(ctx, raw, in)
		if err != nil {
			return ListSnapshotPage{}, err
		}
		cursor.ID = id
	} else {
		if err := s.openList(in.Cursor, "cursor", &cursor); err != nil {
			return ListSnapshotPage{}, err
		}
		if cursor.Instance != in.InstanceID || cursor.Account != in.AccountID || cursor.Profile != in.ProfileID || cursor.Source != in.SourceID {
			return ListSnapshotPage{}, fault.Forbidden
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ListSnapshotPage{}, err
	}
	defer tx.Rollback(ctx)
	out, err := s.listSnapshotTx(ctx, tx, raw, in, cursor.ID)
	if err != nil {
		return out, err
	}
	if cursor.Position < 0 || cursor.Position > out.Total || in.Cursor != "" && !cursor.ExpiresAt.Equal(out.ExpiresAt) {
		return out, fault.Conflict
	}
	out.Position = cursor.Position
	rows, err := tx.Query(ctx, `SELECT ordinal,payload FROM bloem_list_export_rows WHERE snapshot_id=$1::uuid AND ordinal>=$2 ORDER BY ordinal LIMIT 100`, out.SnapshotID, out.Position)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ordinal int
		var payload []byte
		var e ListSnapshotEntry
		if err = rows.Scan(&ordinal, &payload); err != nil {
			rows.Close()
			return out, err
		}
		if ordinal != out.Position+len(out.Entries) {
			rows.Close()
			return out, fault.Conflict
		}
		if err = json.Unmarshal(payload, &e); err != nil {
			rows.Close()
			return out, err
		}
		out.Entries = append(out.Entries, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	end := out.Position + len(out.Entries)
	if end > out.Total || end < out.Total && len(out.Entries) == 0 {
		return out, fault.Conflict
	}
	out.Final = end == out.Total
	if out.Final {
		out.SourceAttestation, err = s.signListProofTx(ctx, tx, in, out)
	} else {
		out.NextCursor, err = s.sealList(listCursor{ID: out.SnapshotID, Instance: in.InstanceID, Account: in.AccountID, Profile: in.ProfileID, Source: out.SourceID, Position: end, ExpiresAt: out.ExpiresAt}, "cursor")
	}
	if err != nil {
		return out, err
	}
	if err = checkListDeadline(ctx, tx, out.SnapshotID); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func checkListDeadline(ctx context.Context, tx pgx.Tx, id string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM bloem_list_export_snapshots WHERE id=$1::uuid`, id).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fault.Conflict
	}
	return nil
}

// AttestListSnapshot renews proof of current source read rights immediately
// before host publication or request delivery. It does not extend snapshot life.
func (s Store) AttestListSnapshot(ctx context.Context, raw string, in ListAttestationInput) (ListAttestation, error) {
	var out ListAttestation
	if !listSnapshotUUID.MatchString(in.SnapshotID) {
		return out, fault.Invalid("Invalid list snapshot identifier.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	page, err := s.listSnapshotTx(ctx, tx, raw, in.ListSnapshotInput, in.SnapshotID)
	if err != nil {
		return out, err
	}
	out.SnapshotID = page.SnapshotID
	out.Digest = page.Digest
	out.ExpiresAt = page.ExpiresAt
	out.SourceAttestation, err = s.signListProofTx(ctx, tx, in.ListSnapshotInput, page)
	if err != nil {
		return out, err
	}
	if err = checkListDeadline(ctx, tx, page.SnapshotID); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

var listSnapshotUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s Store) SweepListSnapshots(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 16 {
		return 0, fault.Invalid("List snapshot cleanup batches contain 1 to 16 snapshots.")
	}
	result, err := s.Pool.Exec(ctx, `DELETE FROM bloem_list_export_snapshots WHERE id IN (
	SELECT id FROM bloem_list_export_snapshots WHERE expires_at<=clock_timestamp() ORDER BY expires_at,id LIMIT $1 FOR UPDATE SKIP LOCKED)`, limit)
	return result.RowsAffected(), err
}
