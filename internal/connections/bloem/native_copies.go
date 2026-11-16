package bloem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/jackc/pgx/v5"
)

type NativeCopyImport struct {
	RequestID string `json:"request_id"`
	CopyID    string `json:"copy_id"`
	EntryID   int64  `json:"entry_id,string"`
}

// ImportNativeCopy is an explicit viewer action against an unexpired source page.
// Merely browsing availability never creates ownership, and local edits conflict.
func (s Store) ImportNativeCopy(ctx context.Context, a identity.Actor, in NativeCopyImport) (library.Mutation, error) {
	if !opaque(in.RequestID) || !opaque(in.CopyID) || in.EntryID < 1 {
		return library.Mutation{}, fault.Invalid("Choose a source copy and a matching library entry.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return library.Mutation{}, err
	}
	defer tx.Rollback(ctx)
	a, err = identity.AuthorizeTx(ctx, tx, a, "library:write")
	if err != nil {
		return library.Mutation{}, err
	}
	var instance string
	if err = tx.QueryRow(ctx, `SELECT instance_id FROM bloem_library_requests WHERE id=$1 AND profile_id=$2`, in.RequestID, a.TrackingID()).Scan(&instance); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return library.Mutation{}, fault.NotFound
		}
		return library.Mutation{}, err
	}
	if _, err = lockSourceProfile(ctx, tx, instance, a.TrackingID(), false); err != nil {
		return library.Mutation{}, err
	}
	// Match the page to the current source revisions as well as its lease. Holding
	// the request row also serializes imports of the same source projection.
	var payload []byte
	var current bool
	err = tx.QueryRow(ctx, `SELECT r.payload,COALESCE(NOT r.pending AND r.expires_at>clock_timestamp() AND r.account_revision=b.source_revision AND r.profile_revision=p.source_revision,false) FROM bloem_library_requests r JOIN bloem_profiles p ON p.instance_id=r.instance_id AND p.profile_id=r.profile_id JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE r.id=$1 AND r.profile_id=$2 FOR UPDATE OF r`, in.RequestID, a.TrackingID()).Scan(&payload, &current)
	if err != nil {
		return library.Mutation{}, authorityError(err)
	}
	if !current {
		return library.Mutation{}, fault.Conflict
	}
	var page LibraryInput
	if err = json.Unmarshal(payload, &page); err != nil {
		return library.Mutation{}, err
	}
	var copy *LibraryCopy
	if page.View == "copies" {
		for i := range page.Copies {
			if page.Copies[i].ID == in.CopyID {
				copy = &page.Copies[i]
				break
			}
		}
	}
	if copy == nil || copy.Removed {
		return library.Mutation{}, fault.NotFound
	}
	// Keep the native change-feed/entry/copy lock order used by ordinary edits.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.'||$1::bigint::text,0))`, a.TrackingID()); err != nil {
		return library.Mutation{}, err
	}
	var stillCurrent bool
	if err = tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM bloem_library_requests WHERE id=$1`, in.RequestID).Scan(&stillCurrent); err != nil {
		return library.Mutation{}, err
	}
	if !stillCurrent {
		return library.Mutation{}, fault.Conflict
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('pastime.user_mutation','true',true),set_config('pastime.change_source','user:bloem.copy.import',true)`); err != nil {
		return library.Mutation{}, err
	}
	var kind, provider, providerID string
	var ids map[string]string
	var edition []byte
	err = tx.QueryRow(ctx, `SELECT e.media_type,COALESCE(c.provider,''),COALESCE(c.provider_id,''),c.external_ids,e.edition FROM library_entries e LEFT JOIN catalog_items c ON c.id=e.catalog_item_id WHERE e.profile_id=$1 AND e.id=$2 FOR UPDATE OF e`, a.TrackingID(), in.EntryID).Scan(&kind, &provider, &providerID, &ids, &edition)
	if err != nil {
		return library.Mutation{}, authorityError(err)
	}
	want := copy.Kind
	if want == "ebook" {
		want = "book"
	}
	if kind != want {
		return library.Mutation{}, fault.Conflict
	}
	if ids == nil {
		ids = map[string]string{}
	}
	if before := ids[provider]; before != "" && before != providerID {
		return library.Mutation{}, fault.Conflict
	}
	ids[provider] = providerID
	matched := false
	for key, value := range copy.ExternalIDs {
		if value == "" {
			continue
		}
		switch key {
		case "tmdb", "imdb", "tvdb", "isbn", "asin":
		default:
			return library.Mutation{}, fault.Invalid("Unsupported source copy identity.")
		}
		if native := ids[key]; native != "" {
			if native != value {
				return library.Mutation{}, fault.Conflict
			}
			matched = true
		}
	}
	if !matched {
		return library.Mutation{}, fault.Invalid("The source copy has no exact catalog identity match.")
	}
	if len(edition) > 0 && copy.ExternalIDs["isbn"] != "" {
		var e library.Edition
		if err = json.Unmarshal(edition, &e); err != nil {
			return library.Mutation{}, err
		}
		if e.ISBN != "" && e.ISBN != copy.ExternalIDs["isbn"] {
			return library.Mutation{}, fault.Conflict
		}
	}
	var boundCopyID *int64
	var copyID int64
	var version, actual int
	var revision int64
	var old []byte
	err = tx.QueryRow(ctx, `SELECT b.native_copy_id,b.native_version,b.source_revision,b.payload_digest FROM bloem_native_copy_bindings b WHERE b.instance_id=$1 AND b.profile_id=$2 AND b.source_copy_id=$3 FOR UPDATE OF b`, instance, a.TrackingID(), copy.ID).Scan(&boundCopyID, &version, &revision, &old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return library.Mutation{}, err
	}
	if err == nil {
		if boundCopyID == nil {
			return library.Mutation{}, fault.Conflict
		}
		copyID = *boundCopyID
		if err = tx.QueryRow(ctx, `SELECT version FROM owned_copies WHERE id=$1 AND profile_id=$2 FOR UPDATE`, copyID, a.TrackingID()).Scan(&actual); err != nil {
			return library.Mutation{}, authorityError(err)
		}
	}
	raw, err := json.Marshal(copy)
	if err != nil {
		return library.Mutation{}, err
	}
	digest := identity.Digest(string(raw))
	if copyID > 0 {
		var nativeEntry int64
		if err = tx.QueryRow(ctx, `SELECT entry_id FROM owned_copies WHERE id=$1`, copyID).Scan(&nativeEntry); err != nil {
			return library.Mutation{}, err
		}
		if nativeEntry != in.EntryID || version != actual || copy.Revision < revision {
			return library.Mutation{}, fault.Conflict
		}
		if copy.Revision == revision {
			if !bytes.Equal(old, digest) {
				return library.Mutation{}, fault.Conflict
			}
			return library.Mutation{ID: copyID, Version: version, Replayed: true}, commitCurrentCopyPage(ctx, tx, in.RequestID)
		}
	}
	native := library.CopyInput{Format: copy.Format, Edition: copy.Edition, Resolution: copy.Resolution, HDR: copy.HDR, Audio: copy.Audio, Currency: copy.Currency, Store: copy.Store, Location: copy.Location, Notes: copy.Notes, Version: version, Custom: map[string]any{}}
	if copy.PriceCents < 0 || copy.PriceCents > 100000000 {
		return library.Mutation{}, fault.Invalid("Bitrate or price is out of range.")
	}
	price := int(copy.PriceCents)
	native.PriceCents = &price
	if copy.BitrateKbps > 0 {
		rate := int(copy.BitrateKbps)
		native.BitrateKbps = &rate
	}
	if copy.AcquiredOn != "" {
		native.AcquiredOn = &copy.AcquiredOn
	}
	seenNames := map[string]bool{}
	for name, value := range copy.Custom {
		key := strings.ToLower(name)
		if seenNames[key] {
			return library.Mutation{}, fault.Invalid("Source custom field names must be unique ignoring case.")
		}
		seenNames[key] = true
		if len([]rune(name)) < 1 || len([]rune(name)) > 60 {
			return library.Mutation{}, fault.Invalid("A source custom field name is too long.")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO collection_fields(profile_id,name,kind,position) SELECT $1,$2,'text',COALESCE((SELECT max(position)+1 FROM collection_fields WHERE profile_id=$1),0) WHERE (SELECT count(*) FROM collection_fields WHERE profile_id=$1)<100 AND NOT EXISTS(SELECT 1 FROM collection_fields WHERE profile_id=$1 AND lower(name)=lower($2))`, a.TrackingID(), name); err != nil {
			return library.Mutation{}, err
		}
		var field int64
		var fieldKind string
		if err = tx.QueryRow(ctx, `SELECT id,kind FROM collection_fields WHERE profile_id=$1 AND lower(name)=lower($2)`, a.TrackingID(), name).Scan(&field, &fieldKind); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return library.Mutation{}, fault.Invalid("You can define at most 100 collection fields.")
			}
			return library.Mutation{}, err
		}
		if fieldKind != "text" {
			return library.Mutation{}, fault.Invalid("The source custom field cannot be converted without losing its type.")
		}
		native.Custom[strconv.FormatInt(field, 10)] = value
	}
	out, err := (library.Store{Pool: s.Pool}).SaveCopyTx(ctx, tx, a, in.EntryID, copyID, native)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE owned_copies SET source='bloem' WHERE id=$1 AND profile_id=$2`, out.ID, a.TrackingID()); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO bloem_native_copy_bindings(instance_id,profile_id,source_copy_id,native_copy_id,source_revision,native_version,payload_digest) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(instance_id,profile_id,source_copy_id) DO UPDATE SET source_revision=EXCLUDED.source_revision,native_version=EXCLUDED.native_version,payload_digest=EXCLUDED.payload_digest`, instance, a.TrackingID(), copy.ID, out.ID, copy.Revision, out.Version, digest); err != nil {
		return out, err
	}
	return out, commitCurrentCopyPage(ctx, tx, in.RequestID)
}

// Recheck the lease after any native lock wait; expired imports roll back fully.
func commitCurrentCopyPage(ctx context.Context, tx pgx.Tx, requestID string) error {
	var current bool
	if err := tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM bloem_library_requests WHERE id=$1`, requestID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return fault.Conflict
	}
	return tx.Commit(ctx)
}
