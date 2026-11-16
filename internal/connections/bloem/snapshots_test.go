package bloem

import (
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestSnapshotReceiptsRequireTraversedDurablePages(t *testing.T) {
	f := testkit.New(t)
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	ctx := t.Context()
	i, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	main := ProvisionInput{Version: 1, InstanceID: i.ID, AccountID: "house", AccountName: "House", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 5, Revision: 5, Active: true, AccountActive: true}
	child := main
	child.ProfileID = "child"
	child.Name = "Child"
	if _, err = s.Provision(ctx, raw, main); err != nil {
		t.Fatal(err)
	}
	c, err := s.Provision(ctx, raw, child)
	if err != nil {
		t.Fatal(err)
	}
	page := SnapshotPage{SnapshotID: "snapshot", NextCursor: "next", Profiles: []ProvisionInput{main}}
	receipt, err := s.AcceptPage(ctx, raw, page)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteSnapshot(ctx, raw, page.SnapshotID, receipt.Receipt); !errors.Is(err, fault.Conflict) {
		t.Fatal("incomplete snapshot completed", err)
	}
	replay, err := s.AcceptPage(ctx, raw, page)
	if err != nil || replay.Receipt != receipt.Receipt {
		t.Fatal("page replay changed receipt", err)
	}
	forged := page
	forged.Cursor = "forged"
	forged.Complete = true
	forged.NextCursor = ""
	if _, err = s.AcceptPage(ctx, raw, forged); err == nil {
		t.Fatal("forged traversal accepted")
	}
	last := SnapshotPage{SnapshotID: page.SnapshotID, Cursor: "next", Complete: true, Profiles: []ProvisionInput{}}
	final, err := s.AcceptPage(ctx, raw, last)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteSnapshot(ctx, raw, page.SnapshotID, final.Receipt); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err = f.Pool.QueryRow(ctx, `SELECT active FROM bloem_profiles WHERE profile_id=$1`, c.ProfileID).Scan(&active); err != nil || active {
		t.Fatal("missing child not archived", err)
	}
	if err = s.CompleteSnapshot(ctx, raw, page.SnapshotID, final.Receipt); err != nil {
		t.Fatal("completion replay", err)
	}
	if err = s.CompleteSnapshot(ctx, raw, page.SnapshotID, receipt.Receipt); !errors.Is(err, fault.Conflict) {
		t.Fatal("finalized snapshot accepted an earlier page receipt", err)
	}
}
func TestSnapshotCannotArchiveNewerMappingOrAcceptUnprovisionedRevision(t *testing.T) {
	f := testkit.New(t)
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	ctx := t.Context()
	i, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	p := ProvisionInput{Version: 1, InstanceID: i.ID, AccountID: "house", AccountName: "House", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 5, Revision: 5, Active: true, AccountActive: true}
	if _, err = s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "bad", Complete: true, Profiles: []ProvisionInput{p}}); err == nil {
		t.Fatal("unprovisioned page accepted")
	}
	if _, err = s.Provision(ctx, raw, p); err != nil {
		t.Fatal(err)
	}
	r, err := s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "safe", NextCursor: "next", Profiles: []ProvisionInput{p}})
	if err != nil {
		t.Fatal(err)
	}
	_ = r
	child := p
	child.ProfileID = "child"
	child.Name = "Child"
	child.Revision = 10
	child.AccountRevision = 10
	c, err := s.Provision(ctx, raw, child)
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.AcceptPage(ctx, raw, SnapshotPage{SnapshotID: "safe", Cursor: "next", Complete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteSnapshot(ctx, raw, "safe", last.Receipt); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err = f.Pool.QueryRow(ctx, `SELECT active FROM bloem_profiles WHERE profile_id=$1`, c.ProfileID).Scan(&active); err != nil || !active {
		t.Fatal("newer child archived", err)
	}
}
