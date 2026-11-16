package bloem

import (
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

func TestRevisionDomains(t *testing.T) {
	const legacy, timeRev = 17_057, 1_791_981_296_789_123
	for _, c := range []struct {
		incoming uint64
		stored   int64
		older    bool
	}{
		{5, 0, false},
		{5, 6, true},
		{6, 6, false},
		{timeRev, legacy, false},
		{timeRev - 1, timeRev, true},
		// A return to plugin 0.1.14 after 0.2.0 synced: its sequences are newer.
		{legacy, timeRev, false},
		{1, timeRev, false},
	} {
		if got := olderRevision(c.incoming, c.stored); got != c.older {
			t.Errorf("olderRevision(%d, %d) = %v", c.incoming, c.stored, got)
		}
	}
}

// After plugin 0.2.0 stored microsecond revisions, a return to 0.1.14 sends host
// sequences again. Its changes must apply, not answer stale, which 0.1.14 would record
// as delivered; within its own domain ordering works as before.
func TestReturningSequenceRevisionsAreNotStale(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	credential, err := r.s.IssueCredential(ctx, r.raw, r.profile, "events")
	if err != nil {
		t.Fatal(err)
	}
	title := syntheticTitle(t, r, "rollback-film")
	at := time.Now().UTC().Add(-time.Hour)
	pause := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "media_x", EventID: "pevt_pause", Revision: uint64(at.UnixMicro()), Operation: "pause", OccurredAt: at, PositionSeconds: 100, DurationSeconds: 5400}
	if out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, pause, title, title); err != nil || out.Status != "applied" {
		t.Fatal(out, err)
	}
	back := pause
	back.EventID, back.Revision, back.OccurredAt, back.PositionSeconds = "bloem_evt_back", 17_001, at.Add(time.Minute), 900
	if out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, back, title, title); err != nil || out.Status != "applied" || progress(t, r, title) != 900 {
		t.Fatal("a returning 0.1.14 event was judged stale", out, err)
	}
	older := back
	older.EventID, older.Revision, older.PositionSeconds = "bloem_evt_older", 17_000, 300
	if out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, older, title, title); err != nil || out.Status != "stale" || progress(t, r, title) != 900 {
		t.Fatal("sequence ordering after the return", out, err)
	}

	favourite := PersonalEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "state_x", EventID: "pevt_add", Revision: uint64(at.UnixMicro()), Operation: "favorite_add", OccurredAt: at}
	if out, err := r.s.ApplyMatchedPersonalEvent(ctx, credential.Credential, favourite, title); err != nil || out.Status != "applied" {
		t.Fatal(out, err)
	}
	remove := favourite
	remove.EventID, remove.Revision, remove.Operation, remove.OccurredAt = "bloem_evt_remove", 3, "favorite_remove", at.Add(time.Minute)
	if out, err := r.s.ApplyMatchedPersonalEvent(ctx, credential.Credential, remove, title); err != nil || out.Status != "applied" {
		t.Fatal("a returning 0.1.14 removal was judged stale", out, err)
	}
}

// A completion clears the resume point it finished, but an old completion that reaches
// Pastime after newer progress (a late history export, or the first 0.2.0 event on an
// entity that outranks 0.1.14's sequences) keeps that progress.
func TestOldCompletionKeepsANewerResumePoint(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	credential, err := r.s.IssueCredential(ctx, r.raw, r.profile, "events")
	if err != nil {
		t.Fatal(err)
	}
	title := syntheticTitle(t, r, "resume-film")
	now := time.Now().UTC()
	pause := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "media_y", EventID: "bloem_evt_pause", Revision: 40, Operation: "pause", OccurredAt: now.Add(-time.Minute), PositionSeconds: 1200, DurationSeconds: 5400}
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, pause, title, title); err != nil {
		t.Fatal(err)
	}
	old := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "media_y", EventID: "pevt_old", Revision: uint64(now.Add(-48 * time.Hour).UnixMicro()), Operation: "stop", OccurredAt: now.Add(-48 * time.Hour), Completed: true, ConsumptionID: "play_old", DurationSeconds: 5400}
	out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, old, title, title)
	if err != nil || out.Status != "applied" || !out.PlayRecorded {
		t.Fatal(out, err)
	}
	if progress(t, r, title) != 1200 {
		t.Fatal("an old completion removed a newer resume point")
	}
	live := old
	live.EventID, live.Revision, live.OccurredAt, live.ConsumptionID = "pevt_live", uint64(now.UnixMicro()), now, "play_live"
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, live, title, title); err != nil {
		t.Fatal(err)
	}
	if progress(t, r, title) != -1 {
		t.Fatal("a current completion must clear the resume point")
	}
}

// An imported play count arrives as several completions stamped with the same last
// played date. Each is its own viewing and counts as a play; only a change of state
// that is not a completion conflicts at the same moment.
func TestSameMomentCompletionsAreEachAPlay(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	credential, err := r.s.IssueCredential(ctx, r.raw, r.profile, "events")
	if err != nil {
		t.Fatal(err)
	}
	title := syntheticTitle(t, r, "rewatched-film")
	at := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	first := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "media_z", EventID: "pevt_first", Revision: uint64(at.UnixMicro()), Operation: "stop", OccurredAt: at, Completed: true, ConsumptionID: "play_first", DurationSeconds: 5400}
	if out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, first, title, title); err != nil || out.Status != "applied" || !out.PlayRecorded {
		t.Fatal(out, err)
	}
	second := first
	second.EventID, second.ConsumptionID, second.DurationSeconds = "pevt_second", "play_second", 5300
	out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, second, title, title)
	if err != nil || !out.PlayRecorded {
		t.Fatal("a second viewing at the same moment was refused", out, err)
	}
	var plays int
	if err = r.s.Pool.QueryRow(ctx, `SELECT count(*) FROM plays WHERE catalog_item_id=$1`, title).Scan(&plays); err != nil || plays != 2 {
		t.Fatalf("plays = %d %v, want 2", plays, err)
	}
	if out, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, second, title, title); err != nil || out.PlayRecorded {
		t.Fatal("a replayed viewing counted twice", out, err)
	}
	pause := first
	pause.EventID, pause.Completed, pause.ConsumptionID, pause.Operation, pause.PositionSeconds = "pevt_pause_same", false, "", "pause", 60
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, pause, title, title); !errors.Is(err, fault.Conflict) {
		t.Fatal("a different state at the same moment must conflict", err)
	}
}
