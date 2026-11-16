package bloem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"github.com/jackc/pgx/v5"
)

type syncCase struct {
	f        *testkit.Fixture
	s        Store
	instance string
	raw      string
}

func newSyncCase(t *testing.T) *syncCase {
	t.Helper()
	f := testkit.New(t)
	box, err := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: f.Pool, Box: box}
	i, raw, err := s.Register(t.Context(), f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://silo.example", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	return &syncCase{f: f, s: s, instance: i.ID, raw: raw}
}

func (c *syncCase) credential(t *testing.T, account, profile, issuance string) (int64, string) {
	t.Helper()
	p, err := c.s.Provision(t.Context(), c.raw, ProvisionInput{Version: 1, InstanceID: c.instance, AccountID: account, AccountName: "Household", ProfileID: profile, DefaultProfileID: "main", Name: "Main", Revision: 1, AccountRevision: 1, Active: true, AccountActive: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := c.s.IssueCredential(t.Context(), c.raw, p.ProfileID, issuance)
	if err != nil {
		t.Fatal(err)
	}
	return p.ProfileID, cred.Credential
}

func (c *syncCase) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := c.f.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func enableServiceAccessPolicy(t *testing.T, f *testkit.Fixture) {
	t.Helper()
	now := time.Now()
	if _, err := f.Pool.Exec(t.Context(), `SELECT pastime_apply_service_access($1,$2,$3,'[]'::jsonb)`, "https://access.example", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func wantFault(t *testing.T, err error, status int, code string) {
	t.Helper()
	var fe *fault.Error
	if !errors.As(err, &fe) || fe.Status != status || fe.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
}

// A valid sync credential refused only for lack of tracking access answers 403
// service_access_required, so the host reports a permission problem instead of asking
// for a new key. Every other refusal stays 401, and the provisioning path is unchanged.
func TestSyncCredentialWithoutServiceAccessIsForbidden(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	profile, current := c.credential(t, "acct", "main", "first")
	kidProfile, kid := c.credential(t, "acct", "kid", "kid")
	if _, err := c.s.AuthenticateSync(ctx, current); err != nil {
		t.Fatal(err)
	}
	enableServiceAccessPolicy(t, c.f)
	_, err := c.s.AuthenticateSync(ctx, current)
	wantFault(t, err, 403, "service_access_required")
	if !errors.Is(err, ErrServiceAccess) {
		t.Fatal("unexpected service access error value", err)
	}
	// Provisioning keeps its 401 for plugin 0.1.14's connector.
	_, err = c.s.IssueCredential(ctx, c.raw, profile, "second")
	wantFault(t, err, 401, "unauthenticated")
	_, err = c.s.AuthenticateSync(ctx, "ptbs_not-a-credential")
	wantFault(t, err, 401, "unauthenticated")
	c.exec(t, `UPDATE bloem_profiles SET excluded=true WHERE instance_id=$1 AND profile_id=$2`, c.instance, kidProfile)
	_, err = c.s.AuthenticateSync(ctx, kid)
	wantFault(t, err, 401, "unauthenticated")
	c.exec(t, `UPDATE bloem_sync_credentials SET revoked_at=clock_timestamp() WHERE instance_id=$1 AND profile_id=$2`, c.instance, profile)
	_, err = c.s.AuthenticateSync(ctx, current)
	wantFault(t, err, 401, "unauthenticated")
}

// Use extends the current sync credential to 90 days, at most once a week. Superseded,
// revoked and expired credentials are never extended.
func TestSyncCredentialExpiryRollsOnUse(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	profile, raw := c.credential(t, "acct", "main", "first")
	expiry := func(issuance string) time.Duration {
		t.Helper()
		var left float64
		if err := c.f.Pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at-clock_timestamp()) FROM bloem_sync_credentials WHERE instance_id=$1 AND profile_id=$2 AND issuance_id=$3`, c.instance, profile, issuance).Scan(&left); err != nil {
			t.Fatal(err)
		}
		return time.Duration(left * float64(time.Second))
	}
	const day = 24 * time.Hour
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '10 days' WHERE issuance_id='first'`)
	if _, err := c.s.AuthenticateSync(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if left := expiry("first"); left < 89*day || left > 90*day {
		t.Fatal("a used credential was not extended to 90 days", left)
	}
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '85 days' WHERE issuance_id='first'`)
	if _, err := c.s.AuthenticateSync(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if left := expiry("first"); left > 85*day {
		t.Fatal("a credential with more than 83 days left was rewritten", left)
	}
	// A superseded credential keeps working until its deadline but is not extended.
	next, err := c.s.IssueCredential(ctx, c.raw, profile, "second")
	if err != nil {
		t.Fatal(err)
	}
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '10 days' WHERE issuance_id='first'`)
	if _, err = c.s.AuthenticateSync(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if left := expiry("first"); left > 10*day {
		t.Fatal("a superseded credential was extended", left)
	}
	// An expired current credential stays expired.
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE issuance_id='second'`)
	_, err = c.s.AuthenticateSync(ctx, next.Credential)
	wantFault(t, err, 401, "unauthenticated")
	if left := expiry("second"); left > 0 {
		t.Fatal("an expired credential was revived", left)
	}
}

// Requests that share one credential never deadlock on the renewal: while another
// transaction holds the credential row, a request skips the renewal and a later use
// renews it.
func TestSyncCredentialRenewalNeverWaits(t *testing.T) {
	c := newSyncCase(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	profile, raw := c.credential(t, "acct", "main", "first")
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '10 days' WHERE issuance_id='first'`)
	var txs []pgx.Tx
	for range 2 {
		tx, err := c.f.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='2s'`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `SELECT 1 FROM bloem_sync_credentials WHERE token_digest=$1 FOR SHARE`, identity.Digest(raw)); err != nil {
			t.Fatal(err)
		}
		txs = append(txs, tx)
	}
	for _, tx := range txs {
		if _, err := authenticateSyncTx(ctx, tx, raw); err != nil {
			t.Fatal("a request waited on another request's credential lock", err)
		}
	}
	for _, tx := range txs {
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var renewed bool
	check := `SELECT expires_at>clock_timestamp()+interval '89 days' FROM bloem_sync_credentials WHERE instance_id=$1 AND profile_id=$2 AND issuance_id='first'`
	if err := c.f.Pool.QueryRow(ctx, check, c.instance, profile).Scan(&renewed); err != nil || renewed {
		t.Fatal("contended requests renewed the credential", renewed, err)
	}
	if _, err := c.s.AuthenticateSync(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if err := c.f.Pool.QueryRow(ctx, check, c.instance, profile).Scan(&renewed); err != nil || !renewed {
		t.Fatal("a later use did not renew the credential", renewed, err)
	}
}
