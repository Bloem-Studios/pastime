package identity_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

// A session keeps the browser it signed in with and, to within a few minutes, when it
// was last used.
func TestSessionsRecordClientAndLastUse(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	agent := "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0\x07" + strings.Repeat("x", 600)
	_, raw, err := f.Accounts.Login(identity.WithClient(ctx, agent), "fixture_owner", testkit.Password)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Accounts.Authenticate(ctx, raw, "session")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := f.Accounts.Sessions(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	var current, fixture *identity.Session
	for i := range sessions {
		if sessions[i].Current {
			current = &sessions[i]
		} else if sessions[i].ID == f.Owner.CredentialID {
			fixture = &sessions[i]
		}
	}
	if current == nil || !strings.HasPrefix(current.UserAgent, "Mozilla/5.0 (X11; Linux x86_64") || len([]rune(current.UserAgent)) != 512 || strings.ContainsRune(current.UserAgent, '\x07') || current.LastUsedAt == nil {
		t.Fatalf("current session %+v", current)
	}
	if fixture == nil || fixture.UserAgent != "" {
		t.Fatalf("a sign-in without a client %+v", fixture)
	}

	lastUse := func() time.Time {
		t.Helper()
		var at time.Time
		if err := f.Pool.QueryRow(ctx, "SELECT last_used_at FROM sessions WHERE id=$1::uuid", a.CredentialID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	// A recent use is not written again.
	recent := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	if _, err = f.Pool.Exec(ctx, "UPDATE sessions SET last_used_at=$2 WHERE id=$1::uuid", a.CredentialID, recent); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(ctx, raw, "session"); err != nil {
		t.Fatal(err)
	}
	if got := lastUse(); !got.Equal(recent) {
		t.Fatalf("a use a minute later was written: %v", got)
	}
	// An older one is.
	if _, err = f.Pool.Exec(ctx, "UPDATE sessions SET last_used_at=clock_timestamp()-interval '10 minutes' WHERE id=$1::uuid", a.CredentialID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Accounts.Authenticate(context.Background(), raw, "session"); err != nil {
		t.Fatal(err)
	}
	if got := lastUse(); time.Since(got) > time.Minute {
		t.Fatalf("last use not recorded: %v", got)
	}
}

// The audit log names an account target by its username.
func TestAuditNamesAccountTargets(t *testing.T) {
	f := testkit.New(t)
	member := f.User(t, "audited_member", "member")
	if _, err := f.Accounts.UpdateUser(t.Context(), f.Owner, member.User.ID, identity.UserPatch{Version: member.User.Version, Role: ptr("admin")}, false); err != nil {
		t.Fatal(err)
	}
	page, err := f.Accounts.AuditLog(t.Context(), f.Owner, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	named := 0
	for _, e := range page.Items {
		if e.TargetKind == "user" && e.TargetID == strconv.FormatInt(member.User.ID, 10) {
			if e.TargetLabel != "audited_member" {
				t.Fatalf("member event %+v", e)
			}
			named++
		}
		if e.TargetKind == "invitation" && e.TargetLabel != "" {
			t.Fatalf("an invitation target got a label: %+v", e)
		}
	}
	if named == 0 {
		t.Fatalf("no labelled event for the member in %+v", page.Items)
	}
}
