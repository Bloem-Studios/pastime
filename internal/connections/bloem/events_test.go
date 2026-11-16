package bloem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestNativePlaybackReplayOrderingAndHouseholdIsolation(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	in := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "household", AccountName: "Household", ProfileID: "child", DefaultProfileID: "main", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true}
	child, err := s.Provision(ctx, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.IssueCredential(ctx, raw, child.ProfileID, "event-test")
	if err != nil {
		t.Fatal(err)
	}
	var titleID int64
	if err = f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','synthetic','native-film','Native film','minutes',90) RETURNING id`).Scan(&titleID); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	event := PlaybackEvent{Version: 1, InstanceID: instance.ID, AccountID: "household", ProfileID: "child", EntityID: "content-one", EventID: "pause-new", Revision: 20, Operation: "pause", OccurredAt: at, PositionSeconds: 200, DurationSeconds: 5400}
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	event.EventID = "old-completion"
	event.Revision = 10
	event.Operation = "stop"
	event.Completed = true
	event.ConsumptionID = "viewing-one"
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	var position, plays int
	if err = f.Pool.QueryRow(ctx, "SELECT position_seconds FROM playback_progress WHERE profile_id=$1", child.ProfileID).Scan(&position); err != nil || position != 200 {
		t.Fatal("stale completion removed newer resume", position, err)
	}
	event.EventID = "import-overlap" // Initial-history import preserves source viewing provenance.
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	event.EventID = "distinct-rewatch"
	event.ConsumptionID = "viewing-two"
	event.OccurredAt = at.Add(time.Minute)
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1", child.ProfileID).Scan(&plays); err != nil || plays != 2 {
		t.Fatal("play facts deduplicated by time or duplicated by replay", plays, err)
	}
	event.EventID = "distinct-rewatch"
	event.PositionSeconds = 400
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); !errors.Is(err, fault.Conflict) {
		t.Fatal("event ID reused with different payload", err)
	}
	event.EventID = "forged-main"
	event.ProfileID = "main"
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); !errors.Is(err, fault.Forbidden) {
		t.Fatal("cross-profile event", err)
	}
	var foreign int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id<>$1", child.ProfileID).Scan(&foreign); err != nil || foreign != 0 {
		t.Fatal("cross-profile effects", foreign, err)
	}
	event.ProfileID = "child"
	event.EventID = "incomplete-stop"
	event.Revision = 30
	event.Completed = false
	event.ConsumptionID = ""
	event.PositionSeconds = 500
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err != nil {
		t.Fatal(err)
	}
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1", child.ProfileID).Scan(&plays); err != nil || plays != 2 {
		t.Fatal("incomplete playback counted as consumption", plays, err)
	}
	// Fail the final durable receipt after the transaction has changed state and
	// inserted a viewing. Neither effect may survive a failed receipt write.
	if _, err = f.Pool.Exec(ctx, `ALTER TABLE bloem_event_receipts ADD CONSTRAINT synthetic_receipt_failure CHECK(event_id<>'faulted-event')`); err != nil {
		t.Fatal(err)
	}
	event.EventID = "faulted-event"
	event.Revision = 31
	event.Completed = true
	event.ConsumptionID = "viewing-three"
	event.OccurredAt = at.Add(2 * time.Minute)
	if _, err = s.ApplyMatchedEvent(ctx, credential.Credential, event, titleID, titleID); err == nil {
		t.Fatal("receipt failure reported success")
	}
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1", child.ProfileID).Scan(&plays); err != nil || plays != 2 {
		t.Fatal("play escaped receipt failure", plays, err)
	}
	if err = f.Pool.QueryRow(ctx, "SELECT position_seconds FROM playback_progress WHERE profile_id=$1", child.ProfileID).Scan(&position); err != nil || position != 500 {
		t.Fatal("resume deletion escaped receipt failure", position, err)
	}
	var storedRevision int64
	if err = f.Pool.QueryRow(ctx, "SELECT source_revision FROM bloem_entity_states WHERE instance_id=$1 AND profile_id=$2", instance.ID, child.ProfileID).Scan(&storedRevision); err != nil || storedRevision != 30 {
		t.Fatal("ordering cursor escaped receipt failure", storedRevision, err)
	}
}
