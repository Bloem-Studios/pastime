package bloem

import (
	"strconv"
	"sync"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestListSnapshotPublicRenameAndRevocation(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	lib := library.Store{Pool: f.Pool}
	slug := "shared-synthetic"
	list, err := lib.SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Public", Kind: "manual", Visibility: "public", Slug: &slug})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/u/" + a.User.Username + "/" + slug})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	if _, err := f.Pool.Exec(t.Context(), `UPDATE personal_lists SET slug='renamed-synthetic' WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	p, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || p.SourceID != r.SourceID {
		t.Fatal("rename retargeted binding", p, err)
	}
	if _, err := f.Pool.Exec(t.Context(), `UPDATE personal_lists SET visibility='private' WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: p.SnapshotID}); err == nil {
		t.Fatal("public grant survived private visibility")
	}
	if _, err := f.Pool.Exec(t.Context(), `DELETE FROM personal_lists WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Replacement", Kind: "manual", Visibility: "public", Slug: &slug}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadListSnapshot(t.Context(), raw, q); err == nil {
		t.Fatal("old source grant retargeted replacement")
	}
}

func TestListSnapshotCollaborativeMembershipRevocation(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	lib := library.Store{Pool: f.Pool}
	list, err := lib.CreateCollab(t.Context(), f.Owner, library.CollabInput{Name: "Shared source"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.SetCollabMember(t.Context(), f.Owner, list.ID, a.User.Username, "viewer"); err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/collab/" + strconv.FormatInt(list.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	p, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveCollabMember(t.Context(), f.Owner, list.ID, a.User.Username); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: p.SnapshotID}); err == nil {
		t.Fatal("revoked collaborator exported snapshot")
	}
}

func TestListSnapshotConcurrentAdmissionIsBounded(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	list, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Empty", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { <-start; _, _ = s.ReadListSnapshot(t.Context(), raw, q) })
	}
	close(start)
	wg.Wait()
	var count int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_list_export_snapshots WHERE profile_native_id=$1`, a.TrackingID()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 1 || count > 8 {
		t.Fatal("snapshot admission exceeded bounded storage", count)
	}
}

func TestListSnapshotCleanupPreservesLiveSnapshots(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	list, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Cleanup", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	live, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(t.Context(), `WITH stamp AS (SELECT clock_timestamp() AS t) UPDATE bloem_list_export_snapshots SET generated_at=stamp.t-interval '180 seconds',expires_at=stamp.t-interval '61 seconds' FROM stamp WHERE id=$1`, old.SnapshotID); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepListSnapshots(t.Context(), 1)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: live.SnapshotID}); err != nil {
		t.Fatal("live snapshot swept", err)
	}
}

func TestListSnapshotSmartMembershipTracksNativeFields(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	lib := library.Store{Pool: f.Pool}
	list, err := lib.SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Planned", Kind: "smart", Sort: "title", Rules: library.Rules{Statuses: []string{"planned"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Zebra", "Aspen"} {
		if _, err := lib.Create(t.Context(), a, testkit.Key(t, f), library.EntryInput{Title: title, MediaType: "movie", Unit: "minutes", Status: "planned"}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	first, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || len(first.Entries) != 2 || first.Entries[0].Title != "Aspen" {
		t.Fatal(first, err)
	}
	if _, err := f.Pool.Exec(t.Context(), `UPDATE library_entries SET status='completed' WHERE profile_id=$1`, a.TrackingID()); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := f.Pool.QueryRow(t.Context(), `SELECT version FROM personal_lists WHERE id=$1`, list.ID).Scan(&version); err != nil || version != list.Version {
		t.Fatal(version, err)
	}
	next, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || next.Total != 0 || !next.Final || next.Digest == first.Digest {
		t.Fatal("smart membership ignored underlying changes", next, err)
	}
	var oldTitle string
	if err := f.Pool.QueryRow(t.Context(), `SELECT payload->>'title' FROM bloem_list_export_rows WHERE snapshot_id=$1 AND ordinal=0`, first.SnapshotID).Scan(&oldTitle); err != nil || oldTitle != "Aspen" {
		t.Fatal("prior snapshot changed", oldTitle, err)
	}
}

func TestListSnapshotRevokedAuthorities(t *testing.T) {
	for _, change := range []string{"UPDATE users SET active=false WHERE id=$1", "UPDATE tracking_profiles SET is_default=false,archived_at=clock_timestamp() WHERE account_id=$1", "UPDATE bloem_profiles SET active=false WHERE account_id=$1", "UPDATE bloem_instances SET active=false"} {
		t.Run(change, func(t *testing.T) {
			f, s, raw, _, a, source := calendarFixture(t)
			list, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Revocation", Kind: "manual"})
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/lists/" + strconv.FormatInt(list.ID, 10)})
			if err != nil {
				t.Fatal(err)
			}
			q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
			page, err := s.ReadListSnapshot(t.Context(), raw, q)
			if err != nil {
				t.Fatal(err)
			}
			if change == "UPDATE bloem_instances SET active=false" {
				_, err = f.Pool.Exec(t.Context(), change)
			} else {
				_, err = f.Pool.Exec(t.Context(), change, a.User.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: page.SnapshotID}); err == nil {
				t.Fatal("revoked authority attested")
			}
		})
	}
}

func TestListSnapshotUnlistedTokenExpires(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	list, err := (library.Store{Pool: f.Pool}).SaveList(t.Context(), a, 0, testkit.Key(t, f), library.ListInput{Name: "Unlisted", Kind: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE personal_lists SET visibility='unlisted',slug='synthetic-unlisted',share_token='synthetic-share',share_expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/u/" + a.User.Username + "/synthetic-share"})
	if err != nil {
		t.Fatal(err)
	}
	q := ListSnapshotInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	page, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE personal_lists SET share_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, list.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttestListSnapshot(t.Context(), raw, ListAttestationInput{ListSnapshotInput: q, SnapshotID: page.SnapshotID}); err == nil {
		t.Fatal("expired share token attested")
	}
}

func TestListSnapshotCountLimitAndMissingPage(t *testing.T) {
	f, s, raw, _, a, source := calendarFixture(t)
	list, err := (library.Store{Pool: f.Pool}).CreateCollab(t.Context(), a, library.CollabInput{Name: "Bounded"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Pool.Exec(t.Context(), `WITH items AS (INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) SELECT 1,'movie','tmdb',i::text,'Synthetic '||i,'minutes' FROM generate_series(1,10000) i RETURNING id) INSERT INTO collab_items(list_id,catalog_item_id) SELECT $1,id FROM items`, list.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := ListResolveInput{InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, URL: "https://pastime.example/collab/" + strconv.FormatInt(list.ID, 10)}
	r, err := s.ResolveList(t.Context(), raw, "https://pastime.example", in)
	if err != nil || r.Count != 10000 {
		t.Fatal(r.Count, err)
	}
	q := ListSnapshotInput{InstanceID: in.InstanceID, AccountID: in.AccountID, ProfileID: in.ProfileID, SourceID: r.SourceID, ResolutionToken: r.ResolutionToken}
	page, err := s.ReadListSnapshot(t.Context(), raw, q)
	if err != nil || page.Total != 10000 {
		t.Fatal(page.Total, err)
	}
	_, err = f.Pool.Exec(t.Context(), `DELETE FROM bloem_list_export_rows WHERE snapshot_id=$1 AND ordinal>=100`, page.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	q.Cursor = page.NextCursor
	if _, err = s.ReadListSnapshot(t.Context(), raw, q); err == nil {
		t.Fatal("missing persisted final page accepted")
	}
	_, err = f.Pool.Exec(t.Context(), `WITH item AS (INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(1,'movie','tmdb','10001','Overflow','minutes') RETURNING id) INSERT INTO collab_items(list_id,catalog_item_id) SELECT $1,id FROM item`, list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResolveList(t.Context(), raw, "https://pastime.example", in); err == nil {
		t.Fatal("10001 titles truncated instead of rejected")
	}
	q.Cursor = ""
	if _, err = s.ReadListSnapshot(t.Context(), raw, q); err == nil {
		t.Fatal("oversized refresh snapshot accepted")
	}
}
