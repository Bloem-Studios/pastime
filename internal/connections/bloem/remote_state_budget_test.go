package bloem

import (
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestSinglePagePersonalReadsDoNotConsumeContinuationBudget(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://media.example.test", Scope: "read-budget"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Provision(ctx, raw, ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Household", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.IssueCredential(ctx, raw, profile.ProfileID, "read-budget")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 24; n++ {
		out, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite"}, PageSize: 100})
		if err != nil || !out.CompleteSnapshot || out.NextPageToken != "" {
			t.Fatalf("read %d: %+v %v", n, out, err)
		}
	}
	var snapshots int
	if err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM bloem_personal_read_snapshots WHERE instance_id=$1`, instance.ID).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal("single-page reads retained traversal state", snapshots, err)
	}
}
