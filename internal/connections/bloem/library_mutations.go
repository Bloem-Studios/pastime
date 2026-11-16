package bloem

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"strings"
)

type LibraryMutationIntent struct {
	InstanceID       string        `json:"instance_id"`
	EventID          string        `json:"event_id"`
	Operation        string        `json:"operation"`
	TargetID         string        `json:"target_id"`
	ExpectedRevision int64         `json:"expected_revision"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Items            []LibraryItem `json:"items"`
	Copy             *LibraryCopy  `json:"copy,omitempty"`
	BookState        *BookState    `json:"book_state,omitempty"`
}
type LibraryMutationRequest struct {
	LibraryMutationIntent
	RequestID string `json:"request_id"`
	AccountID string `json:"account_id"`
	ProfileID string `json:"profile_id"`
}
type LibraryMutationResult struct {
	RequestID string `json:"request_id"`
	EventID   string `json:"event_id"`
	TargetID  string `json:"target_id"`
	Revision  int64  `json:"revision"`
	Status    string `json:"status"`
}

func (s Store) RequestLibraryMutation(ctx context.Context, a identity.Actor, in LibraryMutationIntent) (LibraryMutationResult, error) {
	if !opaque(in.InstanceID) || !opaque(in.EventID) || len(in.Name) > 200 || len(in.Description) > 10000 || in.ExpectedRevision < 0 {
		return LibraryMutationResult{}, fault.Invalid("Invalid collection change.")
	}
	switch in.Operation {
	case "copy.upsert", "copy.remove", "book.progress", "book.completed", "book.reset":
		if !opaque(in.TargetID) {
			return LibraryMutationResult{}, fault.Invalid("Choose a source record.")
		}
	case "collection.create":
		if in.TargetID != "" || in.ExpectedRevision != 0 || strings.TrimSpace(in.Name) == "" || len(in.Items) != 0 {
			return LibraryMutationResult{}, fault.Invalid("Invalid collection creation.")
		}
	case "collection.update", "collection.delete", "collection.add", "collection.remove":
		if !opaque(in.TargetID) || in.ExpectedRevision < 1 {
			return LibraryMutationResult{}, fault.Invalid("Refresh the collection before editing it.")
		}
	default:
		return LibraryMutationResult{}, fault.Invalid("Unsupported collection change.")
	}

	if in.Operation == "collection.add" || in.Operation == "collection.remove" {
		if len(in.Items) != 1 || !opaque(in.Items[0].ContentID) {
			return LibraryMutationResult{}, fault.Invalid("Choose one source title.")
		}
	} else if len(in.Items) != 0 {
		return LibraryMutationResult{}, fault.Invalid("Invalid collection change.")
	}
	if in.Operation == "collection.update" && strings.TrimSpace(in.Name) == "" {
		return LibraryMutationResult{}, fault.Invalid("Enter a collection name.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LibraryMutationResult{}, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "library:write")
	if err != nil {
		return LibraryMutationResult{}, err
	}
	if _, err = lockSourceProfile(ctx, tx, in.InstanceID, fresh.TrackingID(), false); err != nil {
		return LibraryMutationResult{}, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return LibraryMutationResult{}, err
	}
	var priorID string
	var same bool
	err = tx.QueryRow(ctx, `SELECT id,intent=$4::jsonb FROM bloem_library_mutations WHERE instance_id=$1 AND profile_id=$2 AND event_id=$3`, in.InstanceID, fresh.TrackingID(), in.EventID, body).Scan(&priorID, &same)
	if err == nil {
		if !same {
			return LibraryMutationResult{}, fault.Conflict
		}
		if err = tx.Commit(ctx); err != nil {
			return LibraryMutationResult{}, err
		}
		return s.LibraryMutationResult(ctx, a, priorID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LibraryMutationResult{}, err
	}
	if strings.HasPrefix(in.Operation, "collection.") && in.Operation != "collection.create" {
		var writable bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_library_requests r CROSS JOIN LATERAL jsonb_array_elements(r.payload->'collections') c WHERE r.instance_id=$1 AND r.profile_id=$2 AND r.expires_at>clock_timestamp() AND NOT r.pending AND c->>'id'=$3 AND (c->>'writable')::boolean AND (c->>'revision')::bigint=$4)`, in.InstanceID, fresh.TrackingID(), in.TargetID, in.ExpectedRevision).Scan(&writable); err != nil {
			return LibraryMutationResult{}, err
		}
		if !writable {
			return LibraryMutationResult{}, fault.Forbidden
		}
	}
	id, err := randomRaw("mutation_")
	if err != nil {
		return LibraryMutationResult{}, err
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM bloem_library_mutations WHERE instance_id=$1 AND profile_id=$2 AND status='pending'`, in.InstanceID, fresh.TrackingID()).Scan(&count); err != nil {
		return LibraryMutationResult{}, err
	}
	if count >= 100 {
		return LibraryMutationResult{}, fault.Invalid("Too many pending collection changes.")
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_library_mutations(id,instance_id,profile_id,event_id,intent,account_revision,profile_revision)
 SELECT $1,$2,$3,$4,$5,b.source_revision,p.source_revision FROM bloem_profiles p JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE p.instance_id=$2 AND p.profile_id=$3`, id, in.InstanceID, fresh.TrackingID(), in.EventID, body)
	if err != nil {
		return LibraryMutationResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LibraryMutationResult{}, err
	}
	return s.LibraryMutationResult(ctx, a, id)
}
func (s Store) LibraryMutationResult(ctx context.Context, a identity.Actor, id string) (LibraryMutationResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LibraryMutationResult{}, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "lists:read")
	if err != nil {
		return LibraryMutationResult{}, err
	}
	var instance string
	if err = tx.QueryRow(ctx, `SELECT instance_id FROM bloem_library_mutations WHERE id=$1 AND profile_id=$2`, id, fresh.TrackingID()).Scan(&instance); err != nil {
		return LibraryMutationResult{}, authorityError(err)
	}
	if _, err = lockSourceProfile(ctx, tx, instance, fresh.TrackingID(), false); err != nil {
		return LibraryMutationResult{}, err
	}
	var receipt []byte
	out := LibraryMutationResult{RequestID: id}
	err = tx.QueryRow(ctx, `SELECT m.event_id,m.status,m.receipt FROM bloem_library_mutations m JOIN bloem_profiles p ON p.instance_id=m.instance_id AND p.profile_id=m.profile_id JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE m.id=$1 AND m.account_revision=b.source_revision AND m.profile_revision=p.source_revision`, id).Scan(&out.EventID, &out.Status, &receipt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LibraryMutationResult{}, fault.NotFound
	}
	if err != nil {
		return LibraryMutationResult{}, err
	}
	if receipt != nil {
		if err = json.Unmarshal(receipt, &out); err != nil {
			return LibraryMutationResult{}, err
		}
	}
	return out, tx.Commit(ctx)
}
func (s Store) PendingLibraryMutations(ctx context.Context, raw string) ([]LibraryMutationRequest, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var instance string
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return nil, authorityError(err)
	}
	rows, err := tx.Query(ctx, `SELECT m.id,m.intent,p.source_account_id,p.source_profile_id FROM bloem_library_mutations m
 JOIN bloem_profiles p ON p.instance_id=m.instance_id AND p.profile_id=m.profile_id AND p.active AND NOT p.excluded
 JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id AND b.active
 JOIN users u ON u.id=p.account_id AND u.active AND pastime_account_has_service_access(u.id)
 WHERE m.instance_id=$1 AND m.status='pending' AND m.account_revision=b.source_revision AND m.profile_revision=p.source_revision ORDER BY m.last_attempt_at,m.id LIMIT 100 FOR UPDATE OF m SKIP LOCKED`, instance)
	if err != nil {
		return nil, err
	}
	out := []LibraryMutationRequest{}
	for rows.Next() {
		var request LibraryMutationRequest
		var body []byte
		if err = rows.Scan(&request.RequestID, &body, &request.AccountID, &request.ProfileID); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(body, &request.LibraryMutationIntent); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, request)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, request := range out {
		if _, err = tx.Exec(ctx, `UPDATE bloem_library_mutations SET last_attempt_at=clock_timestamp() WHERE id=$1`, request.RequestID); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
func (s Store) AcceptLibraryMutation(ctx context.Context, raw string, in LibraryMutationResult) error {
	if !opaque(in.RequestID) || !opaque(in.EventID) || in.Revision < 0 {
		return fault.Invalid("Invalid collection receipt.")
	}
	success := false
	switch in.Status {
	case "applied", "no_change", "removed":
		success = true
		if !opaque(in.TargetID) || in.Revision < 1 {
			return fault.Invalid("Invalid collection receipt.")
		}
	case "conflict", "denied", "invalid":
		if in.Revision != 0 {
			return fault.Invalid("Invalid collection failure.")
		}
	default:
		return fault.Invalid("Invalid collection receipt.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var instance string
	if err = tx.QueryRow(ctx, `SELECT id FROM bloem_instances WHERE provision_digest=$1 AND active FOR SHARE`, identity.Digest(raw)).Scan(&instance); err != nil {
		return authorityError(err)
	}
	var profile int64
	if err = tx.QueryRow(ctx, `SELECT profile_id FROM bloem_library_mutations WHERE id=$1 AND instance_id=$2`, in.RequestID, instance).Scan(&profile); err != nil {
		return authorityError(err)
	}
	if _, err = lockSourceProfile(ctx, tx, instance, profile, false); err != nil {
		return err
	}
	var intent []byte
	err = tx.QueryRow(ctx, `SELECT m.intent FROM bloem_library_mutations m JOIN bloem_profiles p ON p.instance_id=m.instance_id AND p.profile_id=m.profile_id JOIN bloem_accounts b ON b.instance_id=p.instance_id AND b.source_account_id=p.source_account_id WHERE m.id=$1 AND m.event_id=$2 AND m.account_revision=b.source_revision AND m.profile_revision=p.source_revision FOR UPDATE OF m`, in.RequestID, in.EventID).Scan(&intent)
	if errors.Is(err, pgx.ErrNoRows) {
		return fault.Conflict
	}
	if err != nil {
		return err
	}
	var request LibraryMutationIntent
	if err = json.Unmarshal(intent, &request); err != nil {
		return err
	}
	if request.TargetID != "" && in.TargetID != request.TargetID || success && in.Revision < request.ExpectedRevision || request.Operation == "collection.create" && success && !strings.HasPrefix(in.TargetID, "personal:") {
		return fault.Invalid("Collection receipt does not match its change.")
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_library_mutations SET status=$2,receipt=$3 WHERE id=$1 AND (status='pending' OR receipt=$3::jsonb)`, in.RequestID, in.Status, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	// A confirmed effect invalidates only this profile's connected pages. It never
	// replaces independent lists or personal tracking state.
	if success {
		if _, err = tx.Exec(ctx, `UPDATE bloem_library_requests SET expires_at=clock_timestamp() WHERE instance_id=$1 AND profile_id=$2 AND NOT pending`, instance, profile); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
