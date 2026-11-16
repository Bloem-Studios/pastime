package bloem

import (
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
)

// One viewing has one stable consumption ID. A live stop and a history export of
// it carry different timestamps; the later delivery is an idempotent replay.
func TestConsumptionReplayIsIdempotentAcrossTimestamps(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	credential, err := r.s.IssueCredential(ctx, r.raw, r.profile, "events")
	if err != nil {
		t.Fatal(err)
	}
	title := syntheticTitle(t, r, "replay-film")
	live := time.Now().UTC().Add(-time.Minute)
	stop := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "content-one", EventID: "live-stop", Revision: 10, Operation: "stop", OccurredAt: live, Completed: true, ConsumptionID: "viewing-one", PositionSeconds: 5300, DurationSeconds: 5400}
	first, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, stop, title, title)
	if err != nil || !first.PlayRecorded {
		t.Fatal("first delivery", first, err)
	}
	history := stop
	history.EventID = "history-export"
	history.Revision = 11
	history.OccurredAt = live.Truncate(time.Second).Add(-time.Second)
	history.PositionSeconds = 0
	second, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, history, title, title)
	if err != nil {
		t.Fatal("second delivery of the same viewing rejected", err)
	}
	if second.PlayRecorded {
		t.Fatal("second delivery reported a new play")
	}
	if plays(t, r, title) != 1 {
		t.Fatal("one viewing recorded more than once")
	}
}

// A metadata correction moves an entity to a new catalog target at a newer
// source revision. Later events must apply instead of being rejected forever.
func TestCorrectedEntityTargetFollowsNewerRevision(t *testing.T) {
	r := newReviewFixture(t)
	ctx := t.Context()
	credential, err := r.s.IssueCredential(ctx, r.raw, r.profile, "events")
	if err != nil {
		t.Fatal(err)
	}
	wrong := syntheticTitle(t, r, "misidentified-film")
	right := syntheticTitle(t, r, "corrected-film")
	shared := syntheticTitle(t, r, "shared-film")
	at := time.Now().UTC().Add(-time.Hour)
	pause := PlaybackEvent{Version: 1, InstanceID: r.instance.ID, AccountID: "household", ProfileID: "child", EntityID: "content-one", EventID: "pause-wrong", Revision: 10, Operation: "pause", OccurredAt: at, PositionSeconds: 200, DurationSeconds: 5400}
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, pause, wrong, wrong); err != nil {
		t.Fatal(err)
	}
	// A second entity on another target shows the cleanup keeps shared progress.
	other := pause
	other.EntityID, other.EventID, other.Revision = "content-two", "pause-shared-a", 11
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, other, shared, shared); err != nil {
		t.Fatal(err)
	}
	third := other
	third.EntityID, third.EventID, third.Revision = "content-three", "pause-shared-b", 12
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, third, shared, shared); err != nil {
		t.Fatal(err)
	}
	corrected := pause
	corrected.EventID, corrected.Revision, corrected.PositionSeconds = "pause-right", 20, 300
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, corrected, right, right); err != nil {
		t.Fatal("event after a metadata correction rejected", err)
	}
	if progress(t, r, right) != 300 {
		t.Fatal("corrected target lacks resume progress")
	}
	if progress(t, r, wrong) != -1 {
		t.Fatal("misidentified target kept this entity's resume progress")
	}
	stale := pause
	stale.EventID, stale.Revision = "pause-late", 15
	out, err := r.s.ApplyMatchedEvent(ctx, credential.Credential, stale, wrong, wrong)
	if err != nil || out.Status != "stale" || progress(t, r, wrong) != -1 {
		t.Fatal("older event for the previous target was not treated as stale", out, err)
	}
	same := corrected
	same.EventID = "same-revision-other-target"
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, same, wrong, wrong); !errors.Is(err, fault.Conflict) {
		t.Fatal("same revision resolved to another target", err)
	}
	moveShared := third
	moveShared.EventID, moveShared.Revision = "move-shared", 21
	if _, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, moveShared, right, right); err != nil {
		t.Fatal(err)
	}
	if progress(t, r, shared) != 200 {
		t.Fatal("moving one entity removed progress another entity still uses")
	}
	done := corrected
	done.EventID, done.Revision, done.Operation, done.Completed, done.ConsumptionID, done.OccurredAt = "complete-right", 30, "stop", true, "viewing-right", at.Add(time.Minute)
	if out, err = r.s.ApplyMatchedEvent(ctx, credential.Credential, done, right, right); err != nil || !out.PlayRecorded {
		t.Fatal("completion after correction", out, err)
	}
	if plays(t, r, right) != 1 || plays(t, r, wrong) != 0 {
		t.Fatal("completion recorded on the wrong target")
	}
}

func syntheticTitle(t *testing.T, r reviewFixture, providerID string) int64 {
	t.Helper()
	var id int64
	if err := r.f.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','synthetic',$1,$1,'minutes',90) RETURNING id`, providerID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func plays(t *testing.T, r reviewFixture, title int64) int {
	t.Helper()
	var n int
	if err := r.f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM plays WHERE profile_id=$1 AND catalog_item_id=$2`, r.profile, title).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func progress(t *testing.T, r reviewFixture, target int64) int {
	t.Helper()
	var position int
	err := r.f.Pool.QueryRow(t.Context(), `SELECT position_seconds FROM playback_progress WHERE profile_id=$1 AND catalog_item_id=$2`, r.profile, target).Scan(&position)
	if err != nil {
		return -1
	}
	return position
}
