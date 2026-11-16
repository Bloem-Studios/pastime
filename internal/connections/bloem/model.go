// Package bloem owns the backend-only native managed-tracking authority boundary.
package bloem

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"errors"
	"github.com/Bloem-Studios/pastime/internal/config"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
	Box  *sealed.Box
}
type InstanceInput struct {
	ID             string `json:"id"`
	URL            string `json:"url"`
	Scope          string `json:"scope"`
	IdentityIssuer string `json:"identity_issuer"`
}
type Instance struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Scope string `json:"scope"`
}
type ProvisionInput struct {
	Version          int    `json:"version"`
	InstanceID       string `json:"instance_id"`
	AccountID        string `json:"account_id"`
	AccountName      string `json:"account_name"`
	Email            string `json:"email,omitempty"`
	ProfileID        string `json:"profile_id"`
	DefaultProfileID string `json:"default_profile_id"`
	Name             string `json:"name"`
	AccountRevision  uint64 `json:"account_revision"`
	Revision         uint64 `json:"revision"`
	AccountActive    bool   `json:"account_active"`
	Active           bool   `json:"active"`
	IdentityIssuer   string `json:"identity_issuer,omitempty"`
	IdentitySubject  string `json:"identity_subject,omitempty"`
}
type ProvisionResult struct {
	AccountID       int64  `json:"account_id,string"`
	ProfileID       int64  `json:"profile_id,string"`
	LinkStatus      string `json:"link_status"`
	ConnectionReady bool   `json:"connection_ready"`
}

func opaque(v string) bool {
	return utf8.ValidString(v) && len(v) > 0 && len(v) <= 128 && !strings.ContainsFunc(v, unicode.IsControl)
}
func name(v string) bool {
	return utf8.ValidString(v) && strings.TrimSpace(v) == v && utf8.RuneCountInString(v) > 0 && utf8.RuneCountInString(v) <= 80 && !strings.ContainsFunc(v, unicode.IsControl)
}
func fingerprint(v any) []byte { b, _ := json.Marshal(v); h := sha256.Sum256(b); return h[:] }
func randomRaw(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}
func constraint(err error) error {
	var e *pgconn.PgError
	if errors.As(err, &e) && e.Code == "23505" {
		return fault.Conflict
	}
	return err
}

func (s Store) Register(ctx context.Context, a identity.Actor, in InstanceInput) (Instance, string, error) {
	var raw string
	instance, err := s.RegisterAndPublish(ctx, a, in, nil, func(_ Instance, credential string) error {
		raw = credential
		return nil
	})
	if err != nil {
		return Instance{}, "", err
	}
	return instance, raw, nil
}

// RegisterAndPublish hands the new provisioning credential to publish before the
// registration commits. The credential has no other copy, so an operator entry
// point makes it durable first: a publish failure rolls the registration back
// instead of leaving a registered instance whose only credential was lost. The
// audit metadata must not contain secrets.
func (s Store) RegisterAndPublish(ctx context.Context, a identity.Actor, in InstanceInput, audit map[string]any, publish func(Instance, string) error) (Instance, error) {
	if !opaque(in.ID) || !opaque(in.Scope) {
		return Instance{}, fault.Invalid("Invalid integration instance or scope.")
	}
	origin, err := config.NormalizeOrigin(in.URL)
	if err != nil {
		return Instance{}, fault.Invalid("Enter a valid integration server origin.")
	}
	if in.IdentityIssuer != "" && !validIssuer(in.IdentityIssuer) {
		return Instance{}, fault.Invalid("Enter a valid integration identity issuer.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer tx.Rollback(ctx)
	fresh, err := identity.AuthorizeTx(ctx, tx, a, "")
	if err != nil {
		return Instance{}, err
	}
	if !fresh.IsAdmin() {
		return Instance{}, fault.Forbidden
	}
	raw, err := randomRaw("ptbp_")
	if err != nil {
		return Instance{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_instances(id,url,enrollment_scope,identity_issuer,provision_digest) VALUES($1,$2,$3,$4,$5)`, in.ID, origin, in.Scope, in.IdentityIssuer, identity.Digest(raw))
	if err != nil {
		return Instance{}, constraint(err)
	}
	if err = identity.Audit(ctx, tx, fresh, "bloem.instance.register", "bloem_instance", in.ID, audit); err != nil {
		return Instance{}, err
	}
	instance := Instance{ID: in.ID, URL: origin, Scope: in.Scope}
	if err = publish(instance, raw); err != nil {
		return Instance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	return instance, nil
}
func validate(in ProvisionInput) error {
	if in.Version != 1 || !opaque(in.InstanceID) || !opaque(in.AccountID) || !opaque(in.ProfileID) || !opaque(in.DefaultProfileID) || !name(in.Name) || !name(in.AccountName) || in.Revision == 0 || in.Revision > math.MaxInt64 || in.AccountRevision == 0 || in.AccountRevision > math.MaxInt64 {
		return fault.Invalid("Invalid managed profile request.")
	}
	if err := identity.ValidateEmail(in.Email); err != nil {
		return err
	}
	if (in.IdentityIssuer == "") != (in.IdentitySubject == "") || len(in.IdentitySubject) > 255 || strings.ContainsFunc(in.IdentitySubject, unicode.IsControl) {
		return fault.Invalid("Invalid managed identity request.")
	}
	return nil
}

// AuthenticateProvision validates backend enrollment authority, independently
// of personal sessions and profile tracking credentials.
func (s Store) AuthenticateProvision(ctx context.Context, raw string) (Instance, error) {
	var out Instance
	err := s.Pool.QueryRow(ctx, `SELECT id,url,enrollment_scope FROM bloem_instances WHERE provision_digest=$1 AND active`, identity.Digest(raw)).Scan(&out.ID, &out.URL, &out.Scope)
	return out, authorityError(err)
}
