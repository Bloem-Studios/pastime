package imports

import (
	"context"
	"encoding/json"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestLinkGenerationFencesOldCheckpoint(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	s := Store{Pool: f.Pool, Box: box}
	link, err := s.storeLink(ctx, f.Owner.User.ID, "radarr", "Synthetic", "manual", LinkConfig{Token: "old"})
	if err != nil {
		t.Fatal(err)
	}
	_, old, state, _, err := s.loadLink(ctx, f.Owner.User.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	state["cursor"] = "old-account"
	_, err = s.storeLink(ctx, f.Owner.User.ID, "radarr", "Synthetic", "manual", LinkConfig{Token: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.saveLinkState(ctx, f.Owner.User.ID, link.ID, old, state); err == nil {
		t.Error("old generation overwrote replacement")
	}
	_, current, currentState, _, err := s.loadLink(ctx, f.Owner.User.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Token != "new" || len(currentState) != 0 {
		t.Fatal("replacement retained old state")
	}
}
func TestLinkedBatchIsDurableBeforeCursorAdvance(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	s := Store{Pool: f.Pool, Box: box}
	link, err := s.storeLink(ctx, f.Owner.User.ID, "radarr", "Synthetic", "manual", LinkConfig{Token: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	_, cfg, _, _, err := s.loadLink(ctx, f.Owner.User.ID, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	var run string
	if err = f.Pool.QueryRow(ctx, "INSERT INTO import_runs(user_id,source) VALUES($1,'radarr') RETURNING id", f.Owner.User.ID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	records := []Record{{Label: "Durable synthetic record", Kind: "movie"}}
	if err = s.stageLinked(ctx, run, f.Owner.User.ID, link.ID, cfg, map[string]any{"cursor": "next"}, records); err != nil {
		t.Fatal(err)
	}
	options, _ := json.Marshal(map[string]any{"link_id": link.ID})
	got, err := s.parseLinked(ctx, run, f.Owner.User.ID, options)
	if err != nil || len(got) != 1 || got[0].Label != records[0].Label {
		t.Fatal("retry lost fetched batch", got, err)
	}
}

func TestRevokedSessionCannotChangeLinkedService(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	box, _ := sealed.New("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	s := Store{Pool: f.Pool, Box: box}
	link, err := s.storeLink(ctx, f.Owner.User.ID, "radarr", "Synthetic", "manual", LinkConfig{Token: "old"}, f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Accounts.Logout(ctx, f.Owner); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateSchedule(ctx, f.Owner, link.ID, "daily"); err == nil {
		t.Fatal("revoked schedule write")
	}
	if err = s.DeleteLink(ctx, f.Owner, link.ID); err == nil {
		t.Fatal("revoked deletion")
	}
	if _, err = s.SyncNow(ctx, f.Owner, link.ID); err == nil {
		t.Fatal("revoked sync")
	}
	if _, err = s.storeLink(ctx, f.Owner.User.ID, "radarr", "Replacement", "manual", LinkConfig{Token: "new"}, f.Owner); err == nil {
		t.Fatal("revoked replacement")
	}
}
