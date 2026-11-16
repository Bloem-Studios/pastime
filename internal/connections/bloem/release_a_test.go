package bloem

import (
	"testing"
	"time"
)

// A personal-state write the profile already reflects is recorded as no_change. The
// receipt CHECK rejected that result before migration 57, failing every repeated
// favourite, watchlist or rating change with a server error.
func TestConvergentPersonalWriteRecordsNoChange(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	profile, raw := c.credential(t, "acct", "main", "first")
	var title int64
	if err := c.f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','42','Film','minutes',90) RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	e := PersonalEvent{Version: 1, InstanceID: c.instance, AccountID: "acct", ProfileID: "main", EntityID: "state-favorite", EventID: "add-1", Revision: 1, Operation: "favorite_add", OccurredAt: time.Now().UTC().Add(-time.Minute)}
	out, err := c.s.ApplyMatchedPersonalEvent(ctx, raw, e, title)
	if err != nil || out.Status != "applied" {
		t.Fatal("first favourite", out, err)
	}
	e.EventID, e.Revision, e.OccurredAt = "add-2", 2, e.OccurredAt.Add(time.Second)
	out, err = c.s.ApplyMatchedPersonalEvent(ctx, raw, e, title)
	if err != nil || out.Status != "no_change" {
		t.Fatal("a repeated favourite must answer no_change", out, err)
	}
	// The receipt replays its recorded result.
	out, err = c.s.ApplyMatchedPersonalEvent(ctx, raw, e, title)
	if err != nil || out.Status != "no_change" {
		t.Fatal("no_change replay", out, err)
	}
	var receipts int
	if err = c.f.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_event_receipts WHERE profile_id=$1 AND result='no_change'`, profile).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("no_change receipt", receipts, err)
	}
}

// Plugin 0.1.14's provisioning snapshots and source account disables manage only the
// bindings provisioning created, so resuming it never archives or revokes a binding a
// person created on the Connect Silo page.
func TestProvisioningLeavesSelfServiceBindingsAlone(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	mainProfile, _ := c.credential(t, "house", "main", "main-key")
	selfProfile, selfKey := c.credential(t, "house", "kid", "kid-key")
	c.exec(t, `UPDATE bloem_profiles SET origin='self_service' WHERE instance_id=$1 AND profile_id=$2`, c.instance, selfProfile)
	main := ProvisionInput{Version: 1, InstanceID: c.instance, AccountID: "house", AccountName: "Household", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, Active: true, AccountActive: true}
	page, err := c.s.AcceptPage(ctx, c.raw, SnapshotPage{SnapshotID: "only-main", Complete: true, Profiles: []ProvisionInput{main}})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.s.CompleteSnapshot(ctx, c.raw, "only-main", page.Receipt); err != nil {
		t.Fatal(err)
	}
	if _, err = c.s.AuthenticateSync(ctx, selfKey); err != nil {
		t.Fatal("a provisioning snapshot archived a self-service binding", err)
	}
	disabled := main
	disabled.AccountRevision, disabled.AccountActive = 2, false
	if _, err = c.s.Provision(ctx, c.raw, disabled); err != nil {
		t.Fatal(err)
	}
	var revokedMain, revokedSelf bool
	if err = c.f.Pool.QueryRow(ctx, `SELECT bool_or(revoked_at IS NOT NULL) FILTER (WHERE profile_id=$2), bool_or(revoked_at IS NOT NULL) FILTER (WHERE profile_id=$3) FROM bloem_sync_credentials WHERE instance_id=$1`, c.instance, mainProfile, selfProfile).Scan(&revokedMain, &revokedSelf); err != nil {
		t.Fatal(err)
	}
	if !revokedMain || revokedSelf {
		t.Fatal("an account disable must revoke only provisioned keys", revokedMain, revokedSelf)
	}
}

// The worker reports current keys that are about to expire or already expired: a key
// Silo keeps using renews itself, so these belong to connections that stopped syncing.
func TestSyncKeyExpiryCountsKeysNeedingAttention(t *testing.T) {
	c := newSyncCase(t)
	ctx := t.Context()
	soon, _ := c.credential(t, "acct", "soon", "k-soon")
	expired, _ := c.credential(t, "acct", "expired", "k-expired")
	c.credential(t, "acct", "fresh", "k-fresh")
	superseded, _ := c.credential(t, "acct", "superseded", "k-old")
	if _, err := c.s.IssueCredential(ctx, c.raw, superseded, "k-new"); err != nil {
		t.Fatal(err)
	}
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '10 days' WHERE profile_id=$1`, soon)
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()-interval '1 day' WHERE profile_id=$1`, expired)
	c.exec(t, `UPDATE bloem_sync_credentials SET expires_at=clock_timestamp()+interval '5 days' WHERE profile_id=$1 AND revoked_at IS NOT NULL`, superseded)
	keys, err := c.s.SyncKeyExpiry(ctx, 30*24*time.Hour)
	if err != nil || keys.ExpiringSoon != 1 || keys.Expired != 1 || keys.FirstExpiry == nil || time.Until(*keys.FirstExpiry) > 11*24*time.Hour {
		t.Fatalf("%+v %v", keys, err)
	}
	// An excluded binding needs no key.
	c.exec(t, `UPDATE bloem_profiles SET excluded=true WHERE profile_id=$1`, soon)
	if keys, err = c.s.SyncKeyExpiry(ctx, 30*24*time.Hour); err != nil || keys.ExpiringSoon != 0 || keys.FirstExpiry != nil {
		t.Fatalf("%+v %v", keys, err)
	}
}
