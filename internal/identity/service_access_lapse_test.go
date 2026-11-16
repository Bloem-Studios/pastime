package identity_test

import (
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// Maintainer decision, 2026-11-11: "Pastime should not sign people out." A lapse in the access
// feed denies access while it lasts, but it must never sign out or delete the
// calendar feed of a member the next snapshot still grants. Only a member the
// new snapshot leaves out is revoked.

const lapseIssuer = "https://sso.example.test/application/o/pastime/"

type accessMember struct {
	actor   identity.Actor
	raw     string
	subject string
}

type authState struct{ epoch, version int64 }

func linkMember(t *testing.T, f *testkit.Fixture, username, subject string) accessMember {
	t.Helper()
	a := f.User(t, username, "member")
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, lapseIssuer, subject, a.User.ID); err != nil {
		t.Fatal(err)
	}
	return accessMember{actor: a, subject: subject}
}

func grantsFor(subjects ...string) string {
	body := "["
	for i, s := range subjects {
		if i > 0 {
			body += ","
		}
		body += `{"subject":"` + s + `","expires_at":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano) + `"}`
	}
	return body + "]"
}

func applyAccess(t *testing.T, f *testkit.Fixture, observed, validUntil time.Time, grants string) int {
	t.Helper()
	var revoked int
	if err := f.Pool.QueryRow(t.Context(), `SELECT pastime_apply_service_access($1,$2,$3,$4::jsonb)`, lapseIssuer, observed, validUntil, grants).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

func applyNow(t *testing.T, f *testkit.Fixture, grants string) int {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	return applyAccess(t, f, now, now.Add(time.Minute), grants)
}

func stateOf(t *testing.T, f *testkit.Fixture, id int64) authState {
	t.Helper()
	var s authState
	if err := f.Pool.QueryRow(t.Context(), `SELECT auth_epoch,version FROM users WHERE id=$1`, id).Scan(&s.epoch, &s.version); err != nil {
		t.Fatal(err)
	}
	return s
}

func feedCount(t *testing.T, f *testkit.Fixture, id int64) int {
	t.Helper()
	var n int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM calendar_feeds WHERE profile_id IN (SELECT id FROM tracking_profiles WHERE account_id=$1)`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// signInWithFeed opens a session and a calendar feed while the member has access.
func signInWithFeed(t *testing.T, f *testkit.Fixture, m *accessMember) string {
	t.Helper()
	m.actor, m.raw = f.Login(t, m.actor.User.Username)
	feed, err := f.Accounts.RotateFeed(t.Context(), m.actor)
	if err != nil {
		t.Fatal(err)
	}
	return feed
}

// lapse moves the published snapshot three minutes into the past, as if the
// publisher had been unreachable that long.
func lapse(t *testing.T, f *testkit.Fixture) {
	t.Helper()
	if _, err := f.Pool.Exec(t.Context(), `UPDATE service_access_policy SET generated_at=clock_timestamp()-interval '4 minutes',fresh_until=clock_timestamp()-interval '3 minutes';
		UPDATE service_access_grants SET expires_at=clock_timestamp()-interval '3 minutes'`); err != nil {
		t.Fatal(err)
	}
}

func adminAndOwner(t *testing.T, f *testkit.Fixture) (identity.Actor, map[int64]authState) {
	t.Helper()
	admin := f.User(t, "lapse_admin", "admin")
	return admin, map[int64]authState{f.Owner.User.ID: stateOf(t, f, f.Owner.User.ID), admin.User.ID: stateOf(t, f, admin.User.ID)}
}

func assertUntouched(t *testing.T, f *testkit.Fixture, want map[int64]authState) {
	t.Helper()
	for id, s := range want {
		if got := stateOf(t, f, id); got != s {
			t.Fatalf("owner/admin %d changed: %+v -> %+v", id, s, got)
		}
	}
}

func TestServiceAccessLapseKeepsGrantedMembersSignedIn(t *testing.T) {
	f := testkit.New(t)
	m := linkMember(t, f, "lapsed_paid", "00000000-0000-4000-8000-000000000101")
	_, privileged := adminAndOwner(t, f)
	if got := applyNow(t, f, grantsFor(m.subject)); got != 0 {
		t.Fatal("first grant revoked", got)
	}
	feed := signInWithFeed(t, f, &m)
	before := stateOf(t, f, m.actor.User.ID)

	lapse(t, f)
	if _, err := f.Accounts.Authenticate(t.Context(), m.raw, "session"); err == nil {
		t.Fatal("a stale snapshot still granted access; the check must fail closed")
	}

	if got := applyNow(t, f, grantsFor(m.subject)); got != 0 {
		t.Fatal("the apply after a lapse reported revocations", got)
	}
	if got := stateOf(t, f, m.actor.User.ID); got != before {
		t.Fatalf("the lapse signed the member out: %+v -> %+v", before, got)
	}
	if feedCount(t, f, m.actor.User.ID) != 1 {
		t.Fatal("the lapse deleted the member's calendar feed")
	}
	if _, err := f.Accounts.Authenticate(t.Context(), m.raw, "session"); err != nil {
		t.Fatal("the session did not come back after the lapse", err)
	}
	if _, err := f.Accounts.FeedActor(t.Context(), feed); err != nil {
		t.Fatal("the calendar feed did not come back after the lapse", err)
	}
	assertUntouched(t, f, privileged)
}

func TestServiceAccessGrantAlreadyPastExpiryIsNotRevocation(t *testing.T) {
	f := testkit.New(t)
	m := linkMember(t, f, "slow_apply", "00000000-0000-4000-8000-000000000102")
	if got := applyNow(t, f, grantsFor(m.subject)); got != 0 {
		t.Fatal("first grant revoked", got)
	}
	signInWithFeed(t, f, &m)
	before := stateOf(t, f, m.actor.User.ID)
	lapse(t, f)

	// A snapshot observed 100 s ago whose grant ran out 10 s ago still lists
	// the member: access waits for the next snapshot, the session survives.
	now := time.Now().UTC().Truncate(time.Microsecond)
	grant := `[{"subject":"` + m.subject + `","expires_at":"` + now.Add(-10*time.Second).Format(time.RFC3339Nano) + `"}]`
	if got := applyAccess(t, f, now.Add(-100*time.Second), now.Add(15*time.Second), grant); got != 0 {
		t.Fatal("a listed member was revoked", got)
	}
	if got := stateOf(t, f, m.actor.User.ID); got != before {
		t.Fatalf("a listed member was signed out: %+v -> %+v", before, got)
	}
	if feedCount(t, f, m.actor.User.ID) != 1 {
		t.Fatal("a listed member lost their calendar feed")
	}
	if _, err := f.Accounts.Authenticate(t.Context(), m.raw, "session"); err == nil {
		t.Fatal("an expired grant still granted access; the check must fail closed")
	}
	if got := applyNow(t, f, grantsFor(m.subject)); got != 0 {
		t.Fatal("renewal reported revocations", got)
	}
	if _, err := f.Accounts.Authenticate(t.Context(), m.raw, "session"); err != nil {
		t.Fatal("the session did not come back with a fresh grant", err)
	}
}

func TestServiceAccessRevokesMembersMissingFromTheNewSet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lapse bool
	}{{"fresh", false}, {"after a lapse", true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := testkit.New(t)
			kept := linkMember(t, f, "kept_paid", "00000000-0000-4000-8000-000000000201")
			gone := linkMember(t, f, "cancelled", "00000000-0000-4000-8000-000000000202")
			_, privileged := adminAndOwner(t, f)
			if got := applyNow(t, f, grantsFor(kept.subject, gone.subject)); got != 0 {
				t.Fatal("first grant revoked", got)
			}
			keptFeed := signInWithFeed(t, f, &kept)
			goneFeed := signInWithFeed(t, f, &gone)
			keptBefore, goneBefore := stateOf(t, f, kept.actor.User.ID), stateOf(t, f, gone.actor.User.ID)
			if tc.lapse {
				lapse(t, f)
			}

			if got := applyNow(t, f, grantsFor(kept.subject)); got != 1 {
				t.Fatal("revoked count", got)
			}
			if got := stateOf(t, f, gone.actor.User.ID); got != (authState{goneBefore.epoch + 1, goneBefore.version + 1}) {
				t.Fatalf("the cancelled member was not signed out: %+v -> %+v", goneBefore, got)
			}
			if feedCount(t, f, gone.actor.User.ID) != 0 {
				t.Fatal("the cancelled member kept a calendar feed")
			}
			if _, err := f.Accounts.FeedActor(t.Context(), goneFeed); err == nil {
				t.Fatal("the cancelled feed still resolves")
			}
			if got := stateOf(t, f, kept.actor.User.ID); got != keptBefore {
				t.Fatalf("a still-paid member was signed out: %+v -> %+v", keptBefore, got)
			}
			if feedCount(t, f, kept.actor.User.ID) != 1 {
				t.Fatal("a still-paid member lost their feed")
			}
			if _, err := f.Accounts.FeedActor(t.Context(), keptFeed); err != nil {
				t.Fatal(err)
			}

			// Returning later does not bring back the revoked session.
			if got := applyNow(t, f, grantsFor(kept.subject, gone.subject)); got != 0 {
				t.Fatal("renewal reported revocations", got)
			}
			if _, err := f.Accounts.Authenticate(t.Context(), gone.raw, "session"); err == nil {
				t.Fatal("renewal restored a revoked session")
			}
			assertUntouched(t, f, privileged)
		})
	}
}

func TestServiceAccessNeverTouchesOwnerOrAdmin(t *testing.T) {
	f := testkit.New(t)
	m := linkMember(t, f, "plain_member", "00000000-0000-4000-8000-000000000301")
	admin, privileged := adminAndOwner(t, f)
	// Owner and admin carry grant-set identities too, then drop out of the set.
	for i, id := range []int64{f.Owner.User.ID, admin.User.ID} {
		subject := []string{"00000000-0000-4000-8000-000000000302", "00000000-0000-4000-8000-000000000303"}[i]
		if _, err := f.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, lapseIssuer, subject, id); err != nil {
			t.Fatal(err)
		}
	}
	applyNow(t, f, grantsFor(m.subject, "00000000-0000-4000-8000-000000000302", "00000000-0000-4000-8000-000000000303"))
	lapse(t, f)
	if got := applyNow(t, f, `[]`); got != 1 {
		t.Fatal("revoked count", got)
	}
	assertUntouched(t, f, privileged)
	if _, err := f.Accounts.Authenticate(t.Context(), f.OwnerRaw, "session"); err != nil {
		t.Fatal("owner lost access", err)
	}
}

func TestServiceAccessRepeatedAppliesDoNotBump(t *testing.T) {
	f := testkit.New(t)
	m := linkMember(t, f, "steady_paid", "00000000-0000-4000-8000-000000000401")
	_, privileged := adminAndOwner(t, f)
	applyNow(t, f, grantsFor(m.subject))
	signInWithFeed(t, f, &m)
	before := stateOf(t, f, m.actor.User.ID)
	for i := range 5 {
		if got := applyNow(t, f, grantsFor(m.subject)); got != 0 {
			t.Fatal("unchanged apply revoked", i, got)
		}
	}
	if got := stateOf(t, f, m.actor.User.ID); got != before {
		t.Fatalf("unchanged applies bumped the member: %+v -> %+v", before, got)
	}
	if feedCount(t, f, m.actor.User.ID) != 1 {
		t.Fatal("unchanged applies deleted the feed")
	}
	assertUntouched(t, f, privileged)
}
