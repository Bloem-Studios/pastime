package bloem

import (
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func calendarFixture(t *testing.T) (*testkit.Fixture, Store, string, Instance, identity.Actor, CalendarInput) {
	t.Helper()
	f, s, instance, raw := identityFixture(t, bloemIssuer)
	member := f.User(t, "calendar_member", "member")
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, bloemIssuer, subjectA, member.User.ID); err != nil {
		t.Fatal(err)
	}
	in := withIdentity(managed(instance.ID, "source-account"), bloemIssuer, subjectA)
	mapping, err := s.Provision(t.Context(), raw, in)
	if err != nil || mapping.AccountID != member.User.ID || mapping.ProfileID != member.TrackingID() {
		t.Fatalf("verified fixture mapping: %v %v", mapping, err)
	}
	page := CalendarInput{Version: 1, InstanceID: instance.ID, AccountID: in.AccountID, ProfileID: in.ProfileID, SnapshotID: testkit.Key(t, f), Start: "2026-10-12", End: "2026-10-13", VisibilityDigest: "digest", Revision: 1, ExpiresAt: time.Now().Add(time.Minute), NextCursor: "1", Events: []CalendarEvent{{EventID: "first", ContentID: "first", Kind: "movie", Title: "First", Date: "2026-10-12", Favourite: true}}}
	return f, s, raw, instance, member, page
}
func TestCalendarPublicationRequiresCompletePages(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	if _, err := s.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, page.SnapshotID); err == nil {
		t.Fatal("incomplete snapshot published")
	}
	before, err := s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(before) != 0 {
		t.Fatalf("staging leaked: %v %v", before, err)
	}
	if _, err = s.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatalf("exact replay failed: %v", err)
	}
	changed := page
	changed.Events = append([]CalendarEvent(nil), page.Events...)
	changed.Events[0].Title = "Changed"
	if _, err = s.AcceptCalendarPage(t.Context(), raw, changed); !errors.Is(err, fault.Conflict) {
		t.Fatalf("changed receipt accepted: %v", err)
	}
	next := page
	next.Cursor = "1"
	next.NextCursor = ""
	next.Complete = true
	next.Events = []CalendarEvent{{EventID: "second", ContentID: "second", Kind: "episode", Title: "Second", Date: "2026-10-13", Followed: true}}
	if _, err = s.AcceptCalendarPage(t.Context(), raw, next); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	out, err := s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(out) != 2 {
		t.Fatalf("complete view: %v %v", out, err)
	}
	favourite, err := s.CalendarEvents(t.Context(), member, page.Start, page.End, "favourites")
	if err != nil || len(favourite) != 1 || favourite[0].ContentID != "first" {
		t.Fatalf("favourite filter: %v %v", favourite, err)
	}
	other := f.User(t, "calendar_other", "member")
	hidden, err := s.CalendarEvents(t.Context(), other, page.Start, page.End, "all")
	if err != nil || len(hidden) != 0 {
		t.Fatalf("calendar crossed profiles: %v %v", hidden, err)
	}
	if _, err = s.AcceptCalendarPage(t.Context(), f.OwnerRaw, page); !errors.Is(err, fault.Unauthenticated) {
		t.Fatalf("UI session granted machine authority: %v", err)
	}
}
func TestCalendarLeaseExpiryAndNewerSnapshot(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	page.Complete = true
	page.NextCursor = ""
	if _, err := s.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	newer := page
	newer.SnapshotID = testkit.Key(t, f)
	newer.Revision = 2
	newer.Events = nil
	if _, err := s.AcceptCalendarPage(t.Context(), raw, newer); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, newer.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, page.SnapshotID); !errors.Is(err, fault.Conflict) {
		t.Fatalf("old completion overwrote new snapshot: %v", err)
	}
	out, err := s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(out) != 0 {
		t.Fatalf("removed events retained: %v %v", out, err)
	}
	newer.SnapshotID = testkit.Key(t, f)
	newer.Revision = 3
	newer.Events = page.Events
	if _, err = s.AcceptCalendarPage(t.Context(), raw, newer); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishCalendar(t.Context(), raw, newer.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_calendar_snapshots SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, newer.SnapshotID); err != nil {
		t.Fatal(err)
	}
	out, err = s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(out) != 0 {
		t.Fatalf("expired private calendar visible: %v %v", out, err)
	}
}

func TestCalendarLeaseRenewalAndInvalidationStayBoundToProfile(t *testing.T) {
	f, s, raw, instance, member, page := calendarFixture(t)
	page.Complete = true
	page.NextCursor = ""
	if _, err := s.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	lease := CalendarLease{InstanceID: instance.ID, AccountID: page.AccountID, ProfileID: page.ProfileID, VisibilityDigest: page.VisibilityDigest, ExpiresAt: time.Now().Add(90 * time.Second)}
	if err := s.RenewCalendarLease(t.Context(), raw, lease); err != nil {
		t.Fatal(err)
	}
	events, err := s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(events) != 1 || events[0].AuthorityExpiresAt == nil || !events[0].AuthorityExpiresAt.After(page.ExpiresAt) {
		t.Fatalf("renewed lease missing: %v %v", events, err)
	}
	foreign := lease
	foreign.ProfileID = "foreign"
	if err = s.InvalidateCalendar(t.Context(), raw, foreign); err == nil {
		t.Fatal("foreign profile invalidation accepted")
	}
	if err = s.InvalidateCalendar(t.Context(), raw, lease); err != nil {
		t.Fatal(err)
	}
	events, err = s.CalendarEvents(t.Context(), member, page.Start, page.End, "all")
	if err != nil || len(events) != 0 {
		t.Fatalf("invalidated source remained visible: %v %v", events, err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_profiles SET active=false WHERE instance_id=$1`, instance.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.RenewCalendarLease(t.Context(), raw, lease); err == nil {
		t.Fatal("inactive mapping renewed calendar")
	}
}

func TestCalendarSweepKeepsPublishedWindowsAndDeletesOnlyOldStaging(t *testing.T) {
	f, s, raw, _, _, page := calendarFixture(t)
	page.Complete = true
	page.NextCursor = ""
	if _, err := s.AcceptCalendarPage(t.Context(), raw, page); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishCalendar(t.Context(), raw, page.SnapshotID); err != nil {
		t.Fatal(err)
	}
	staged := page
	staged.SnapshotID = testkit.Key(t, f)
	staged.Revision++
	if _, err := s.AcceptCalendarPage(t.Context(), raw, staged); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(t.Context(), `UPDATE bloem_calendar_snapshots SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '2 days' WHERE id=ANY($1::text[])`, []string{page.SnapshotID, staged.SnapshotID}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepCalendar(t.Context()); err != nil || n != 1 {
		t.Fatalf("sweep %d %v", n, err)
	}
	var exists bool
	if err := f.Pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM bloem_calendar_snapshots WHERE id=$1)`, page.SnapshotID).Scan(&exists); err != nil || !exists {
		t.Fatalf("current window deleted: %v", err)
	}
}
