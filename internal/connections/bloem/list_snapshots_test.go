package bloem

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestListSnapshotIsImmutableAndSourceBound(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	libraryStore := library.Store{Pool: f.Pool}
	list, err := libraryStore.SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Synthetic source", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		entry, err := libraryStore.Create(t.Context(), a, testkit.Key(t, f), library.EntryInput{Title: fmt.Sprintf("Title %03d", i), MediaType: "movie", Unit: "minutes", Status: "planned", Notes: "PRIVATE_SENTINEL"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Pool.Exec(t.Context(), `INSERT INTO list_entries(list_id,profile_id,entry_id,position) VALUES($1,$2,$3,$4)`, list.ID, a.TrackingID(), entry.ID, i); err != nil {
			t.Fatal(err)
		}
	}
	in := ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)}
	resolved, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
	if err != nil || resolved.Count != 101 {
		t.Fatal(resolved, err)
	}
	q := ListSnapshotInput{InstanceID: in.InstanceID, AccountID: in.AccountID, ProfileID: in.ProfileID, SourceID: resolved.SourceID, ResolutionToken: resolved.ResolutionToken}
	first, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || first.Final || len(first.Entries) != 100 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	if _, err := f.Pool.Exec(t.Context(), `UPDATE library_entries SET title='Changed after snapshot' WHERE profile_id=$1`, a.TrackingID()); err != nil {
		t.Fatal(err)
	}
	q.Cursor = first.NextCursor
	last, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || !last.Final || len(last.Entries) != 1 || last.Entries[0].Title != "Title 100" || last.Digest != first.Digest || last.SourceAttestation == "" {
		t.Fatal(last, err)
	}
	encoded, _ := json.Marshal(last)
	if strings.Contains(string(encoded), "PRIVATE_SENTINEL") {
		t.Fatal("private notes leaked")
	}
	q.ProfileID = "foreign"
	if _, err := s.ReadListSnapshot(t.Context(), raw, q); err == nil {
		t.Fatal("snapshot crossed profile")
	}
	q.ProfileID = in.ProfileID
	if _, err := f.Pool.Exec(t.Context(), `DELETE FROM personal_lists WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadListSnapshot(t.Context(), raw, q); err == nil {
		t.Fatal("deleted source snapshot still readable")
	}
}

func TestListSnapshotPrivateAuthorityAndExpiry(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	libraryStore := library.Store{Pool: f.Pool}
	list, err := libraryStore.SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Empty source", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	in := ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)}
	resolved, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: in.InstanceID, AccountID: in.AccountID, ProfileID: in.ProfileID, SourceID: resolved.SourceID, ResolutionToken: resolved.ResolutionToken}
	empty, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || !empty.Final || empty.Total != 0 || len(empty.Entries) != 0 {
		t.Fatal(empty, err)
	}
	if _, err := f.Pool.Exec(t.Context(), `WITH stamp AS (SELECT clock_timestamp() AS t) UPDATE bloem_list_export_snapshots SET generated_at=stamp.t-interval '180 seconds',expires_at=stamp.t-interval '61 seconds' FROM stamp WHERE id=$1`, empty.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: empty.SnapshotID}); err == nil {
		t.Fatal("expired snapshot attested")
	}
	other := f.User(t, "list_other", "member")
	foreign, err := libraryStore.SaveList(t.Context(), other, 0, testkit.Key(t, f), library.ListInput{Name: "Other private source", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	in.URL = "https://pastime.example/lists/" + strconv.FormatInt(foreign.ID, 10)
	if _, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in); err == nil {
		t.Fatal("foreign private list resolved")
	}
	if _, err := s.ResolveList(t.Context(), f.OwnerRaw, "https://pastime.example", in); err == nil {
		t.Fatal("viewer session exported machine source")
	}
}
