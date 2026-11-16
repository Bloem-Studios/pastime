package identity_test

import (
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestServiceAccessRevokesOldSessionsAndPreservesHistory(t *testing.T) {
	f := testkit.New(t)
	member := f.User(t, "paid_viewer", "member")
	const issuer = "https://sso.example.test/application/o/pastime/"
	const subject = "00000000-0000-4000-8000-000000000042"
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, issuer, subject, member.User.ID); err != nil {
		t.Fatal(err)
	}
	_, raw := f.Login(t, "paid_viewer")
	publish := func(grants string) {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := f.Pool.Exec(t.Context(), `SELECT pastime_apply_service_access($1,$2,$3,$4::jsonb)`, issuer, now, now.Add(time.Minute), grants); err != nil {
			t.Fatal(err)
		}
	}
	publish(`[{"subject":"` + subject + `","expires_at":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano) + `"}]`)
	if _, err := f.Accounts.Authenticate(t.Context(), raw, "session"); err != nil {
		t.Fatal("paid access denied", err)
	}
	paidActor, err := f.Accounts.Authenticate(t.Context(), raw, "session")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := f.Accounts.MintToken(t.Context(), paidActor, identity.TokenInput{Name: "Synthetic reader", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := f.Accounts.RotateFeed(t.Context(), paidActor)
	if err != nil {
		t.Fatal(err)
	}

	publish(`[]`)
	if _, err := f.Accounts.Authenticate(t.Context(), token, "token"); err == nil {
		t.Fatal("token retained unpaid access")
	}
	if _, err := f.Accounts.FeedActor(t.Context(), feed); err == nil {
		t.Fatal("feed retained unpaid access")
	}
	if _, err := identity.ResolveWorkerProfile(t.Context(), f.Pool, paidActor.TrackingID(), "worker", nil, false); err == nil {
		t.Fatal("worker retained unpaid access")
	}
	if _, _, err := f.Accounts.OIDCSignIn(t.Context(), identity.OIDCClaims{Issuer: issuer, Subject: subject}, nil, true); !errors.Is(err, identity.ErrServiceAccessRequired) {
		t.Fatal("SSO bypassed paid access", err)
	}

	if _, err := f.Accounts.Authenticate(t.Context(), raw, "session"); !errors.Is(err, fault.Unauthenticated) {
		t.Fatal("expired access retained", err)
	}
	if _, _, err := f.Accounts.Login(t.Context(), "paid_viewer", testkit.Password); err == nil {
		t.Fatal("password bypassed paid access")
	}
	publish(`[{"subject":"` + subject + `","expires_at":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano) + `"}]`)
	if _, err := f.Accounts.Authenticate(t.Context(), raw, "session"); err == nil {
		t.Fatal("renewal restored revoked session")
	}
	if _, _, err := f.Accounts.Login(t.Context(), "paid_viewer", testkit.Password); err != nil {
		t.Fatal("renewed account cannot sign in", err)
	}
	_, renewedRaw := f.Login(t, "paid_viewer")
	if _, err := f.Pool.Exec(t.Context(), `UPDATE service_access_grants SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Accounts.Authenticate(t.Context(), renewedRaw, "session"); err == nil {
		t.Fatal("expired grant retained session")
	}
	// A grant that ran out is a lapse, not a cancellation: the next snapshot
	// that still lists the member restores the same session (2026-11-11).
	publish(`[{"subject":"` + subject + `","expires_at":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano) + `"}]`)
	if _, err := f.Accounts.Authenticate(t.Context(), renewedRaw, "session"); err != nil {
		t.Fatal("renewal after a lapse signed the member out", err)
	}

	var active, password, linked bool
	if err := f.Pool.QueryRow(t.Context(), `SELECT active,password_hash<>'',EXISTS(SELECT 1 FROM oidc_identities o WHERE o.user_id=users.id) FROM users WHERE id=$1`, member.User.ID).Scan(&active, &password, &linked); err != nil || !active || !password || !linked {
		t.Fatal("retained identity/data destroyed", err)
	}
	if _, err := f.Accounts.Authenticate(t.Context(), f.OwnerRaw, "session"); err != nil {
		t.Fatal("owner lost management access", err)
	}
}
