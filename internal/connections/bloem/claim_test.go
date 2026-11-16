package bloem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// claimSetup wires the claim exactly as the service does and declares the Pastime
// sign-in issuer as equivalent to the instance identity issuer.
func claimSetup(t *testing.T) (*testkit.Fixture, Store, Instance, string, *identity.Store) {
	t.Helper()
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	if _, err := s.DeclareLoginIssuer(t.Context(), f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	accounts := identity.New(f.Pool)
	accounts.ManagedClaim = s.ClaimForLogin
	return f, s, instance, raw, accounts
}

func login(subject string) identity.OIDCClaims {
	return identity.OIDCClaims{Issuer: loginIssuer, Subject: subject, Email: "person@example.test", EmailVerified: true, PreferredUsername: "person", Name: "Person"}
}

func rows(t *testing.T, f *testkit.Fixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestClaimLandsInManagedAccount(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	in := withIdentity(managed(instance.ID, "claimer"), bloemIssuer, subjectA)
	main, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	child := in
	child.ProfileID, child.Name = "child", "Child"
	if _, err = s.Provision(ctx, raw, child); err != nil {
		t.Fatal(err)
	}
	users := rows(t, f, `SELECT count(*) FROM users`)
	u, session, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, false)
	if err != nil || u.ID != main.AccountID || session == "" {
		t.Fatal("claim did not sign in to the managed account", u.ID, main.AccountID, err)
	}
	if rows(t, f, `SELECT count(*) FROM users`) != users {
		t.Fatal("claim created an account")
	}
	if a := account(t, f, instance.ID, "claimer"); a.Status != "linked" || !a.Claimed || !a.Owns || a.Subject != subjectA || a.BoundIssuer != bloemIssuer || a.BoundSubject != subjectA {
		t.Fatal("claimed account state", a)
	}
	if rows(t, f, `SELECT count(*) FROM oidc_identities WHERE issuer=$1 AND subject=$2 AND user_id=$3`, loginIssuer, subjectA, u.ID) != 1 {
		t.Fatal("claim did not link the sign-in identity")
	}
	if rows(t, f, `SELECT count(*) FROM tracking_profiles WHERE account_id=$1`, u.ID) != 2 {
		t.Fatal("managed profiles are not on the claimed account")
	}
	var actor int64
	var label string
	var raw2 []byte
	if err = f.Pool.QueryRow(ctx, `SELECT actor_id,actor_label,metadata FROM audit_events WHERE action='bloem.account.claim' AND target_kind='user' AND target_id=$1`, fmt.Sprint(u.ID)).Scan(&actor, &label, &raw2); err != nil {
		t.Fatal("claim audit", err)
	}
	var meta map[string]any
	_ = json.Unmarshal(raw2, &meta)
	want := map[string]any{"instance_id": instance.ID, "source_account_id": "claimer", "login_issuer": loginIssuer, "instance_issuer": bloemIssuer}
	if actor != u.ID || label != u.Username || len(meta) != len(want) {
		t.Fatal("claim audit attribution", actor, label, meta)
	}
	for k, v := range want {
		if meta[k] != v {
			t.Fatal("claim audit metadata", meta)
		}
	}
	if strings.Contains(string(raw2), subjectA) || strings.Contains(string(raw2), "person@") {
		t.Fatal("claim audit recorded identity details")
	}
	if rows(t, f, `SELECT count(*) FROM audit_events WHERE action='session.oidc' AND actor_id=$1`, u.ID) != 1 {
		t.Fatal("claim skipped the normal sign-in audit")
	}
	if a, err := accounts.Authenticate(ctx, session, "session"); err != nil || a.User.ID != u.ID {
		t.Fatal("claim session", err)
	}
	// Later sign-ins go straight in through the linked identity.
	again, _, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, false)
	if err != nil || again.ID != u.ID || auditCount(t, f, "bloem.account.claim") != 1 {
		t.Fatal("repeat sign-in", err)
	}
	// The claim does not disturb the source fingerprint.
	if _, err = s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "after-claim", Complete: true, Profiles: []ProvisionInput{in, child}}); err != nil {
		t.Fatal("snapshot after claim", err)
	}
	// A claimed identity cannot move to another subject; a clear keeps the login.
	in.AccountRevision = 2
	if _, err = s.Provision(ctx, raw, withIdentity(in, bloemIssuer, subjectB)); code(err) != "bloem_identity_conflict" {
		t.Fatal("claimed identity re-targeted", err)
	}
	if _, err = s.Provision(ctx, raw, withIdentity(in, "", "")); err != nil {
		t.Fatal(err)
	}
	if again, _, err = accounts.OIDCSignIn(ctx, login(subjectA), nil, false); err != nil || again.ID != u.ID {
		t.Fatal("clear removed the claimed login", err)
	}
}

func TestClaimThroughInstanceIssuerNeedsNoDeclaration(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	r, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "direct"), bloemIssuer, "opaque-subject-1"))
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := accounts.OIDCSignIn(ctx, identity.OIDCClaims{Issuer: bloemIssuer, Subject: "opaque-subject-1"}, nil, false)
	if err != nil || u.ID != r.AccountID {
		t.Fatal("instance issuer sign-in did not claim", err)
	}
	if meta := lastAudit(t, f, "bloem.account.claim"); meta["login_issuer"] != bloemIssuer || meta["instance_issuer"] != bloemIssuer {
		t.Fatal(meta)
	}
}

func TestClaimRequiresDeclaredIssuerAndSubjectShape(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	unlinked := func(c identity.OIDCClaims, auto bool) identity.User {
		t.Helper()
		u, _, err := accounts.OIDCSignIn(ctx, c, nil, auto)
		if !auto {
			if code(err) != "identity_unlinked" {
				t.Fatal("expected an unlinked sign-in", err)
			}
			return u
		}
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	// A subject that is not a canonical UUID never matches a user_uuid declaration.
	opaque, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "opaque"), bloemIssuer, "opaque-subject-2"))
	if err != nil {
		t.Fatal(err)
	}
	unlinked(login("opaque-subject-2"), false)
	if created := unlinked(login("opaque-subject-2"), true); created.ID == opaque.AccountID {
		t.Fatal("shape mismatch claimed")
	}
	if a := account(t, f, instance.ID, "opaque"); a.Status != "pending" || a.Claimed {
		t.Fatal("shape mismatch changed the managed account", a)
	}
	if rows(t, f, `SELECT count(*) FROM oidc_identities WHERE user_id=$1`, opaque.AccountID) != 0 {
		t.Fatal("managed account gained a sign-in")
	}
	if _, err = s.Provision(ctx, raw, withIdentity(managed(instance.ID, "uuid"), bloemIssuer, subjectA)); err != nil {
		t.Fatal(err)
	}
	upper := login(strings.ToUpper(subjectA))
	unlinked(upper, false)
	// An issuer that was never declared is not equivalent.
	other := login(subjectA)
	other.Issuer = "https://sso.example.test/application/o/other/"
	unlinked(other, false)
	// Removing the declaration stops new claims.
	if err = s.RemoveLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, nil); err != nil {
		t.Fatal(err)
	}
	unlinked(login(subjectA), false)
	if _, err = s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	// An inactive grant offers nothing to claim.
	if _, err = f.Pool.Exec(ctx, `UPDATE bloem_instances SET active=false WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	unlinked(login(subjectA), false)
	if _, err = f.Pool.Exec(ctx, `UPDATE bloem_instances SET active=true WHERE id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	// Email is never evidence: a matching address without the subject claims nothing.
	in := managed(instance.ID, "email-only")
	in.Email = "person@example.test"
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	unlinked(login(subjectC), false)
	if auditCount(t, f, "bloem.account.claim") != 0 {
		t.Fatal("an unmatched sign-in claimed an account")
	}
}

func TestClaimConflictsAreRefused(t *testing.T) {
	for _, reason := range []string{"source_inactive", "not_pending", "disabled", "privileged", "has_password", "has_identity", "multiple_matches"} {
		t.Run(reason, func(t *testing.T) {
			f, s, instance, raw, accounts := claimSetup(t)
			ctx := t.Context()
			in := withIdentity(managed(instance.ID, "conflict"), bloemIssuer, subjectA)
			in.AccountActive = reason != "source_inactive"
			r, err := s.Provision(ctx, raw, in)
			if err != nil {
				t.Fatal(err)
			}
			change := map[string]string{
				"not_pending":  `UPDATE bloem_accounts SET link_status='linked' WHERE account_id=$1`,
				"disabled":     `UPDATE users SET active=false WHERE id=$1`,
				"privileged":   `UPDATE users SET role='admin' WHERE id=$1`,
				"has_password": `UPDATE users SET password_hash='synthetic-hash' WHERE id=$1`,
				"has_identity": `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://other.example.test','other-subject',$1)`,
			}[reason]
			if change != "" {
				if _, err = f.Pool.Exec(ctx, change, r.AccountID); err != nil {
					t.Fatal(err)
				}
			}
			if reason == "multiple_matches" {
				second, secondRaw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib2.example.test", Scope: "pilot", IdentityIssuer: bloemIssuer})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.DeclareLoginIssuer(ctx, f.Owner, second.ID, loginIssuer, SubjectUserUUID, nil); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Provision(ctx, secondRaw, withIdentity(managed(second.ID, "conflict"), bloemIssuer, subjectA)); err != nil {
					t.Fatal(err)
				}
			}
			before := account(t, f, instance.ID, "conflict")
			users, sessions := rows(t, f, `SELECT count(*) FROM users`), rows(t, f, `SELECT count(*) FROM sessions`)
			_, session, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, true)
			var conflict *identity.ClaimConflict
			if !errors.As(err, &conflict) || conflict.Reason != reason || code(err) != "identity_claim_conflict" || fault.Public(err).Status != 403 || session != "" {
				t.Fatalf("got %v, want conflict %s", err, reason)
			}
			if reason != "multiple_matches" && (conflict.InstanceID != instance.ID || conflict.SourceAccountID != "conflict" || conflict.UserID != r.AccountID) {
				t.Fatal("conflict identifiers", conflict)
			}
			if strings.Contains(err.Error(), subjectA) || strings.Contains(err.Error(), "person@") {
				t.Fatal("conflict error carries identity details")
			}
			if rows(t, f, `SELECT count(*) FROM users`) != users || rows(t, f, `SELECT count(*) FROM sessions`) != sessions {
				t.Fatal("refused claim created an account or session")
			}
			if rows(t, f, `SELECT count(*) FROM oidc_identities WHERE issuer=$1`, loginIssuer) != 0 || auditCount(t, f, "bloem.account.claim") != 0 {
				t.Fatal("refused claim linked an identity")
			}
			if after := account(t, f, instance.ID, "conflict"); after != before {
				t.Fatal("refused claim changed the account", before, after)
			}
		})
	}
}

func TestClaimIsNotUsedForLinkRequests(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	if _, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "linking"), bloemIssuer, subjectA)); err != nil {
		t.Fatal(err)
	}
	u, _, err := accounts.OIDCSignIn(ctx, login(subjectA), &f.Owner, false)
	if err != nil || u.ID != f.Owner.User.ID {
		t.Fatal("link request", err)
	}
	if a := account(t, f, instance.ID, "linking"); a.Status != "pending" || a.Claimed {
		t.Fatal("link request claimed a managed account", a)
	}
}

func TestClaimRechecksIdentityAfterAccountLock(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := s.Provision(ctx, raw, withIdentity(managed(instance.ID, "locked"), bloemIssuer, subjectA)); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var pid int
	if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `UPDATE bloem_accounts SET identity_subject=$2 WHERE instance_id=$1 AND source_account_id='locked'`, instance.ID, subjectB); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, false); done <- err }()
	for {
		var waiting bool
		if err = f.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err = <-done:
			t.Fatal("claim did not wait for the account lock", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; code(err) != "identity_unlinked" {
		t.Fatal("claim used a stale identity", err)
	}
	if a := account(t, f, instance.ID, "locked"); a.Claimed || a.Status != "pending" {
		t.Fatal("stale identity claimed", a)
	}
}

func TestClaimRacesWithProvision(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	claims := 0
	for i := range 8 {
		source := fmt.Sprintf("race-%d", i)
		old := fmt.Sprintf("2d2c3b4a-0000-4000-8000-%012d", i)
		next := fmt.Sprintf("3d2c3b4a-0000-4000-8000-%012d", i)
		in := withIdentity(managed(instance.ID, source), bloemIssuer, old)
		if _, err := s.Provision(ctx, raw, in); err != nil {
			t.Fatal(err)
		}
		in.AccountRevision = 2
		in = withIdentity(in, bloemIssuer, next)
		start := make(chan struct{})
		var claimErr, provisionErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _, _, claimErr = accounts.OIDCSignIn(ctx, login(old), nil, false) }()
		go func() { defer wg.Done(); <-start; _, provisionErr = s.Provision(ctx, raw, in) }()
		close(start)
		wg.Wait()
		a := account(t, f, instance.ID, source)
		switch {
		case claimErr == nil:
			claims++
			if code(provisionErr) != "bloem_identity_conflict" || a.Subject != old || !a.Claimed {
				t.Fatal("claim and re-target both applied", provisionErr, a)
			}
		case provisionErr == nil:
			if code(claimErr) != "identity_unlinked" || a.Subject != next || a.Claimed || a.Status != "pending" {
				t.Fatal("re-target and claim both applied", claimErr, a)
			}
		default:
			t.Fatal("both operations failed", claimErr, provisionErr)
		}
	}
	if rows(t, f, `SELECT count(*) FROM bloem_accounts WHERE claimed_at IS NOT NULL`) != claims {
		t.Fatal("claim count")
	}
}

func exec(t *testing.T, f *testkit.Fixture, query string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// A Bloem account linked to the wrong person and claimed by them must not follow a
// correction in Bloem silently: the claim stays bound to the identity it was made
// under, whether the correction arrives as one revision or as a clear and a link.
func TestClaimedAccountRefusesReassignment(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	in := withIdentity(managed(instance.ID, "reassigned"), bloemIssuer, subjectA)
	r, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = accounts.OIDCSignIn(ctx, login(subjectA), nil, false); err != nil {
		t.Fatal(err)
	}
	in.AccountRevision = 2
	in = withIdentity(in, "", "")
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal("clear", err)
	}
	sum, err := s.IdentitySummary(ctx, f.Owner, instance.ID)
	if err != nil || sum.Claimed != 1 || sum.ClearedAfterLink != 1 {
		t.Fatalf("summary does not flag the cleared claim: %+v %v", sum, err)
	}
	in.AccountRevision = 3
	in = withIdentity(in, bloemIssuer, subjectB)
	if _, err = s.Provision(ctx, raw, in); code(err) != "bloem_identity_conflict" {
		t.Fatal("claimed account took another identity after a clear", err)
	}
	if a := account(t, f, instance.ID, "reassigned"); a.Subject != "" || !a.Claimed || a.BoundSubject != subjectA || a.UserID != r.AccountID {
		t.Fatal("refused reassignment changed the account", a)
	}
	// The other person cannot claim it, and the claimer keeps signing in until an
	// operator revokes the claim.
	if _, _, err = accounts.OIDCSignIn(ctx, login(subjectB), nil, false); code(err) != "identity_unlinked" {
		t.Fatal("refused identity claimed the account", err)
	}
	if u, _, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, false); err != nil || u.ID != r.AccountID {
		t.Fatal("claimer sign-in", err)
	}
	// The identity issuer stays fixed while a claim is bound under it.
	if err = s.RemoveLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.SetIdentityIssuer(ctx, f.Owner, instance.ID, hashedIssuer, nil); code(err) != "identity_issuer_in_use" {
		t.Fatal("identity issuer changed under a bound claim", err)
	}
}

func TestRevokeClaimRemovesClaimerAccess(t *testing.T) {
	f, s, instance, raw, accounts := claimSetup(t)
	ctx := t.Context()
	in := withIdentity(managed(instance.ID, "mistaken"), bloemIssuer, subjectA)
	main, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Provision(ctx, raw, withIdentity(managed(instance.ID, "unclaimed"), bloemIssuer, subjectC)); err != nil {
		t.Fatal(err)
	}
	// The wrong person claims the account and makes it their own.
	u, session, err := accounts.OIDCSignIn(ctx, login(subjectA), nil, false)
	if err != nil || u.ID != main.AccountID {
		t.Fatal("claim", err)
	}
	exec(t, f, `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, loginIssuer, strings.Repeat("ef", 32), u.ID)
	exec(t, f, `UPDATE users SET password_hash='synthetic-hash',public_profile=true WHERE id=$1`, u.ID)
	exec(t, f, `INSERT INTO user_totp(user_id,sealed_secret,confirmed_at) VALUES($1,'\x00',clock_timestamp())`, u.ID)
	exec(t, f, `INSERT INTO connections(user_id,kind,token_hash) VALUES($1,'plex',decode(repeat('ab',32),'hex'))`, u.ID)
	exec(t, f, `INSERT INTO notification_targets(user_id,kind,name,sealed_config) VALUES($1,'webhook','Claimer hook','\x00')`, u.ID)
	exec(t, f, `INSERT INTO service_links(user_id,kind,name,sealed_config) VALUES($1,'trakt','Claimer link','\x00')`, u.ID)
	exec(t, f, `INSERT INTO service_authorizations(user_id,kind,sealed_state,expires_at) VALUES($1,'trakt','\x00',clock_timestamp()+interval '1 hour')`, u.ID)
	exec(t, f, `INSERT INTO kosync_accounts(user_id,key_digest) VALUES($1,decode(repeat('cd',32),'hex'))`, u.ID)
	var epoch int64
	if err = f.Pool.QueryRow(ctx, `SELECT auth_epoch FROM users WHERE id=$1`, u.ID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	profiles := rows(t, f, `SELECT count(*) FROM tracking_profiles WHERE account_id=$1`, u.ID)

	// Only an administrator revokes, and only a claim.
	member := f.User(t, "plain_member", "member")
	if _, err = s.RevokeClaim(ctx, member, instance.ID, "mistaken", nil); !errors.Is(err, fault.Forbidden) {
		t.Fatal("member revoked a claim", err)
	}
	if _, err = s.RevokeClaim(ctx, f.Owner, instance.ID, "unclaimed", nil); code(err) != "bloem_account_not_claimed" {
		t.Fatal("revoked an unclaimed account", err)
	}
	if _, err = s.RevokeClaim(ctx, f.Owner, instance.ID, "missing", nil); !errors.Is(err, fault.NotFound) {
		t.Fatal("revoked a missing account", err)
	}
	exec(t, f, `UPDATE users SET role='admin' WHERE id=$1`, u.ID)
	if _, err = s.RevokeClaim(ctx, f.Owner, instance.ID, "mistaken", nil); code(err) != "claimed_account_privileged" {
		t.Fatal("revoked a privileged account", err)
	}
	exec(t, f, `UPDATE users SET role='member' WHERE id=$1`, u.ID)

	revoked, err := s.RevokeClaim(ctx, f.Owner, instance.ID, "mistaken", map[string]any{"entry_point": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if revoked.UserID != u.ID || revoked.Username != u.Username || revoked.SignIns != 2 || !revoked.Password || !revoked.Authenticator || !revoked.PublicProfile || revoked.Integrations != 4 {
		t.Fatalf("revoked: %+v", revoked)
	}
	if a := account(t, f, instance.ID, "mistaken"); a.Status != "pending" || a.Claimed || !a.Owns || a.Subject != "" || a.Issuer != "" || a.BoundSubject != "" || a.BoundIssuer != "" || a.UserID != u.ID {
		t.Fatal("revoked account state", a)
	}
	if _, err = accounts.Authenticate(ctx, session, "session"); err == nil {
		t.Fatal("claimer session survived")
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM oidc_identities WHERE user_id=$1`:                              0,
		`SELECT count(*) FROM users WHERE id=$1 AND password_hash='' AND NOT public_profile`: 1,
		`SELECT count(*) FROM user_totp WHERE user_id=$1`:                                    0,
		`SELECT count(*) FROM connections WHERE user_id=$1`:                                  0,
		`SELECT count(*) FROM notification_targets WHERE user_id=$1`:                         0,
		`SELECT count(*) FROM service_links WHERE user_id=$1`:                                0,
		`SELECT count(*) FROM service_authorizations WHERE user_id=$1`:                       0,
		`SELECT count(*) FROM kosync_accounts WHERE user_id=$1`:                              0,
		`SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`:              0,
		`SELECT count(*) FROM users WHERE id=$1 AND auth_epoch>` + fmt.Sprint(epoch):         1,
		`SELECT count(*) FROM tracking_profiles WHERE account_id=$1`:                         profiles,
	} {
		if got := rows(t, f, query, u.ID); got != want {
			t.Fatalf("%s: got %d, want %d", query, got, want)
		}
	}
	var actor int64
	var data []byte
	if err = f.Pool.QueryRow(ctx, `SELECT actor_id,metadata FROM audit_events WHERE action='bloem.account.unclaim' AND target_kind='user' AND target_id=$1`, fmt.Sprint(u.ID)).Scan(&actor, &data); err != nil || actor != f.Owner.User.ID {
		t.Fatal("unclaim audit", actor, err)
	}
	meta := map[string]any{}
	_ = json.Unmarshal(data, &meta)
	if meta["instance_id"] != instance.ID || meta["source_account_id"] != "mistaken" || meta["entry_point"] != "test" || meta["sign_ins_removed"] != float64(2) {
		t.Fatal("unclaim audit metadata", meta)
	}
	if strings.Contains(string(data), subjectA) || strings.Contains(string(data), bloemIssuer) || strings.Contains(string(data), "person@") {
		t.Fatal("unclaim audit recorded identity details")
	}
	if _, err = s.RevokeClaim(ctx, f.Owner, instance.ID, "mistaken", nil); code(err) != "bloem_account_not_claimed" {
		t.Fatal("revoked twice", err)
	}
	// The claimer cannot claim it back, even when the plugin replays the revision
	// it already delivered...
	if _, _, err = accounts.OIDCSignIn(ctx, login(subjectA), nil, false); code(err) != "identity_unlinked" {
		t.Fatal("claimer claimed the account again", err)
	}
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal("replay after revoke", err)
	}
	if a := account(t, f, instance.ID, "mistaken"); a.Subject != "" {
		t.Fatal("replay restored the revoked identity", a)
	}
	// ...while the corrected link arrives as a newer revision and its person claims
	// the account with its history.
	in.AccountRevision = 2
	in = withIdentity(in, bloemIssuer, subjectB)
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal("corrected identity", err)
	}
	x, _, err := accounts.OIDCSignIn(ctx, login(subjectB), nil, false)
	if err != nil || x.ID != main.AccountID || auditCount(t, f, "bloem.account.claim") != 2 {
		t.Fatal("rightful claim", err)
	}
}
