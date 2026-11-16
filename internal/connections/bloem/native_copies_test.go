package bloem

import (
	"context"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestConnectedCopyImportPreservesNativeOwnershipAndLocalEdits(t *testing.T) {
	f, s, raw, _, member, source := calendarFixture(t)
	ctx := t.Context()
	var catalog int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,external_ids) VALUES(0,'movie','tmdb',$1,'Synthetic film','minutes','{}') RETURNING id`, testkit.Key(t, f)).Scan(&catalog); err != nil {
		t.Fatal(err)
	}
	var providerID string
	if err := f.Pool.QueryRow(ctx, `SELECT provider_id FROM catalog_items WHERE id=$1`, catalog).Scan(&providerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `UPDATE catalog_items SET root_id=id WHERE id=$1`, catalog); err != nil {
		t.Fatal(err)
	}
	ls := library.Store{Pool: f.Pool}
	tracked, err := ls.Track(ctx, member, testkit.Key(t, f), library.TrackInput{CatalogID: catalog})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ls.SaveCopy(ctx, member, tracked.ID, 0, testkit.Key(t, f), library.CopyInput{Format: "dvd", Notes: "Keep native"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := s.RequestLibrary(ctx, member, LibraryQuery{InstanceID: source.InstanceID, View: "copies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	page := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "copies", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Copies: []LibraryCopy{{ID: "source-copy", ContentID: "film", Kind: "movie", Title: "Synthetic film", ExternalIDs: map[string]string{"tmdb": providerID}, Format: "bluray", PriceCents: 1234, Currency: "EUR", Audio: "DTS", BitrateKbps: 7000, Custom: map[string]string{"Shelf": "A"}, Revision: 1}}}
	if err = s.AcceptLibrary(ctx, raw, page); err != nil {
		t.Fatal(err)
	}
	in := NativeCopyImport{RequestID: request.RequestID, CopyID: "source-copy", EntryID: tracked.ID}
	imported, err := s.ImportNativeCopy(ctx, member, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ImportNativeCopy(ctx, member, in)
	if err != nil || again.ID != imported.ID {
		t.Fatal("duplicate native import", again, err)
	}
	copies, err := ls.Copies(ctx, member, tracked.ID, "")
	if err != nil || len(copies) != 2 {
		t.Fatal(copies, err)
	}
	for _, copy := range copies {
		if copy.ID == local.ID && copy.Notes != "Keep native" {
			t.Fatal("native copy overwritten")
		}
		if copy.ID == imported.ID && (copy.Audio != "DTS" || copy.PriceCents == nil || *copy.PriceCents != 1234 || copy.BitrateKbps == nil || *copy.BitrateKbps != 7000) {
			t.Fatal("source fields lost", copy)
		}
		if copy.ID == imported.ID {
			fields, e := ls.Fields(ctx, member)
			if e != nil || len(fields) != 1 || copy.Custom[strconv.FormatInt(fields[0].ID, 10)] != "A" {
				t.Fatal("custom value lost", fields, copy, e)
			}
		}
	}
	if _, err = ls.SaveCopy(ctx, member, tracked.ID, imported.ID, testkit.Key(t, f), library.CopyInput{Format: "bluray", Version: imported.Version, Notes: "Local edit"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportNativeCopy(ctx, member, in); err == nil {
		t.Fatal("native edit overwritten by replay")
	}
	other := f.User(t, "copy_other", "member")
	if _, err = s.ImportNativeCopy(ctx, other, in); err == nil {
		t.Fatal("source copy crossed profile")
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE bloem_library_requests SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, request.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportNativeCopy(ctx, member, in); !errors.Is(err, fault.Conflict) {
		t.Fatal("expired source imported or treated as lost login")
	}
}

func TestConnectedCopyImportRejectsLossAndResurrection(t *testing.T) {
	for _, scenario := range []string{"deleted", "case-collision", "identity", "kind", "format", "custom-type", "field-limit", "manual-target", "source-update", "concurrent"} {
		t.Run(scenario, func(t *testing.T) {
			f, s, raw, _, member, source := calendarFixture(t)
			ctx := t.Context()
			ls := library.Store{Pool: f.Pool}
			var catalog int64
			providerID := testkit.Key(t, f)
			if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,external_ids) VALUES(0,'movie','tmdb',$1,'Synthetic film','minutes','{}') RETURNING id`, providerID).Scan(&catalog); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Pool.Exec(ctx, `UPDATE catalog_items SET root_id=id WHERE id=$1`, catalog); err != nil {
				t.Fatal(err)
			}
			tracked, err := ls.Track(ctx, member, testkit.Key(t, f), library.TrackInput{CatalogID: catalog})
			if err != nil {
				t.Fatal(err)
			}
			copy := LibraryCopy{ID: "source-copy", ContentID: "film", Kind: "movie", Title: "Synthetic film", ExternalIDs: map[string]string{"tmdb": providerID}, Format: "bluray", Custom: map[string]string{"Shelf": "A"}, Revision: 1}
			switch scenario {
			case "field-limit":
				if _, err = f.Pool.Exec(ctx, `INSERT INTO collection_fields(profile_id,name,kind,position) SELECT $1,'Field '||n,'text',n FROM generate_series(1,100) n`, member.TrackingID()); err != nil {
					t.Fatal(err)
				}
			case "manual-target":
				if _, err = f.Pool.Exec(ctx, `UPDATE library_entries SET catalog_item_id=NULL WHERE id=$1`, tracked.ID); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				copy.Notes = "Synthetic private source note"
			case "case-collision":
				copy.Custom["shelf"] = "B"
			case "identity":
				copy.ExternalIDs["tmdb"] = "different"
			case "kind":
				copy.Kind = "ebook"
			case "format":
				copy.Format = "laserdisc"
			case "custom-type":
				if _, err = ls.SaveField(ctx, member, 0, testkit.Key(t, f), library.FieldInput{Name: "Shelf", Kind: "number"}); err != nil {
					t.Fatal(err)
				}
			}
			publish := func() NativeCopyImport {
				t.Helper()
				request, e := s.RequestLibrary(ctx, member, LibraryQuery{InstanceID: source.InstanceID, View: "copies", Cursor: testkit.Key(t, f), Limit: 20})
				if e != nil {
					t.Fatal(e)
				}
				e = s.AcceptLibrary(ctx, raw, LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "copies", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Copies: []LibraryCopy{copy}})
				if e != nil {
					t.Fatal(e)
				}
				return NativeCopyImport{RequestID: request.RequestID, CopyID: copy.ID, EntryID: tracked.ID}
			}
			in := publish()
			if scenario == "concurrent" {
				other := publish()
				start := make(chan struct{})
				var wg sync.WaitGroup
				results := make(chan library.Mutation, 2)
				errs := make(chan error, 2)
				for _, request := range []NativeCopyImport{in, other} {
					wg.Go(func() { <-start; out, e := s.ImportNativeCopy(ctx, member, request); results <- out; errs <- e })
				}
				close(start)
				wg.Wait()
				close(results)
				close(errs)
				var id int64
				for e := range errs {
					if e != nil {
						t.Fatal(e)
					}
				}
				for out := range results {
					if id != 0 && id != out.ID {
						t.Fatal("duplicate import", id, out)
					}
					id = out.ID
				}
			} else if scenario == "deleted" || scenario == "source-update" {
				out, e := s.ImportNativeCopy(ctx, member, in)
				if e != nil {
					t.Fatal(e)
				}
				if scenario == "deleted" {
					if _, e = ls.DeleteCopy(ctx, member, tracked.ID, out.ID, testkit.Key(t, f)); e != nil {
						t.Fatal(e)
					}
					if _, e = s.ImportNativeCopy(ctx, member, in); !errors.Is(e, fault.Conflict) {
						t.Fatal("deleted native copy resurrected", e)
					}
					var retained string
					if e = f.Pool.QueryRow(ctx, `SELECT to_jsonb(b)::text FROM bloem_native_copy_bindings b WHERE native_copy_id IS NULL`).Scan(&retained); e != nil || strings.Contains(retained, copy.Notes) {
						t.Fatal("deleted private note retained in receipt", e)
					}
				} else {
					copy.Revision = 2
					copy.Notes = "Source updated"
					newer := publish()
					updated, e := s.ImportNativeCopy(ctx, member, newer)
					if e != nil || updated.ID != out.ID || updated.Version != out.Version+1 {
						t.Fatal(updated, e)
					}
					if _, e = s.ImportNativeCopy(ctx, member, in); !errors.Is(e, fault.Conflict) {
						t.Fatal("stale revision replaced source update", e)
					}
				}
			} else {
				_, e := s.ImportNativeCopy(ctx, member, in)
				if e == nil {
					t.Fatal("unrepresentable import accepted")
				}
				if (scenario == "field-limit" || scenario == "manual-target") && fault.Public(e).Status != 422 {
					t.Fatal("invalid import treated as lost login", e)
				}
			}
			copies, e := ls.Copies(ctx, member, tracked.ID, "")
			if e != nil {
				t.Fatal(e)
			}
			want := 0
			if scenario == "source-update" || scenario == "concurrent" {
				want = 1
			}
			if len(copies) != want {
				t.Fatal("non-atomic import", copies)
			}
			if scenario != "custom-type" && scenario != "field-limit" && want == 0 {
				fields, e := ls.Fields(ctx, member)
				if e != nil || len(fields) != 0 && scenario != "deleted" {
					t.Fatal("field leaked on rollback", fields, e)
				}
			}
		})
	}
}

func TestConnectedCopyImportRechecksLeaseAfterNativeLockWait(t *testing.T) {
	f, s, raw, _, member, source := calendarFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var catalog int64
	providerID := testkit.Key(t, f)
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit) VALUES(0,'movie','tmdb',$1,'Synthetic locked film','minutes') RETURNING id`, providerID).Scan(&catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(ctx, `UPDATE catalog_items SET root_id=id WHERE id=$1`, catalog); err != nil {
		t.Fatal(err)
	}
	ls := library.Store{Pool: f.Pool}
	entry, err := ls.Track(ctx, member, testkit.Key(t, f), library.TrackInput{CatalogID: catalog})
	if err != nil {
		t.Fatal(err)
	}
	request, err := s.RequestLibrary(ctx, member, LibraryQuery{InstanceID: source.InstanceID, View: "copies", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptLibrary(ctx, raw, LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "copies", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Copies: []LibraryCopy{{ID: "locked-copy", Kind: "movie", Title: "Synthetic locked film", ExternalIDs: map[string]string{"tmdb": providerID}, Format: "dvd", Revision: 1}}}); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var pid int
	if err = blocker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('pastime.change.'||$1::bigint::text,0))`, member.TrackingID()); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE bloem_library_requests SET expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, request.RequestID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.ImportNativeCopy(ctx, member, NativeCopyImport{RequestID: request.RequestID, CopyID: "locked-copy", EntryID: entry.ID})
		done <- e
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = f.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case e := <-done:
			t.Fatal("import did not wait", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	for {
		var expired bool
		if err = f.Pool.QueryRow(ctx, `SELECT expires_at<=clock_timestamp() FROM bloem_library_requests WHERE id=$1`, request.RequestID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, fault.Conflict) {
		t.Fatal("expired lease accepted after lock wait", err)
	}
	copies, err := ls.Copies(ctx, member, entry.ID, "")
	if err != nil || len(copies) != 0 {
		t.Fatal("expired import left ownership", copies, err)
	}
}

func TestConnectedBookCopyImportKeepsNativeReadingState(t *testing.T) {
	for _, scenario := range []string{"exact-edition", "wrong-edition", "audiobook"} {
		t.Run(scenario, func(t *testing.T) {
			f, s, raw, _, member, source := calendarFixture(t)
			ctx := t.Context()
			ls := library.Store{Pool: f.Pool}
			kind, unit, sourceKind, format := "book", "pages", "ebook", "hardcover"
			if scenario == "audiobook" {
				kind, unit, sourceKind, format = "audiobook", "minutes", "audiobook", "audiobook"
			}
			var catalog int64
			if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,external_ids) VALUES(0,$1,'openlibrary',$2,'Synthetic exact book',$3,123,'{"isbn":"9780000000000"}') RETURNING id`, kind, testkit.Key(t, f), unit).Scan(&catalog); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Pool.Exec(ctx, `UPDATE catalog_items SET root_id=id WHERE id=$1`, catalog); err != nil {
				t.Fatal(err)
			}
			entry, err := ls.Track(ctx, member, testkit.Key(t, f), library.TrackInput{CatalogID: catalog})
			if err != nil {
				t.Fatal(err)
			}
			isbn := "9780000000000"
			if scenario == "wrong-edition" {
				isbn = "9781111111111"
			}
			if _, err = f.Pool.Exec(ctx, `UPDATE library_entries SET progress=17,notes='Keep native reading notes',edition=jsonb_build_object('id','native-edition','isbn',$2::text,'pages',123) WHERE id=$1`, entry.ID, isbn); err != nil {
				t.Fatal(err)
			}
			request, err := s.RequestLibrary(ctx, member, LibraryQuery{InstanceID: source.InstanceID, View: "copies", Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.AcceptLibrary(ctx, raw, LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "copies", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Copies: []LibraryCopy{{ID: "book-copy", Kind: sourceKind, Title: "Synthetic exact book", ExternalIDs: map[string]string{"isbn": "9780000000000"}, Format: format, Revision: 1}}}); err != nil {
				t.Fatal(err)
			}
			_, err = s.ImportNativeCopy(ctx, member, NativeCopyImport{RequestID: request.RequestID, CopyID: "book-copy", EntryID: entry.ID})
			if scenario == "wrong-edition" {
				if !errors.Is(err, fault.Conflict) {
					t.Fatal("conflicting chosen edition imported", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			got, err := ls.Get(ctx, member, entry.ID)
			if err != nil || got.Progress != 17 || got.Total == nil || *got.Total != 123 || got.Notes != "Keep native reading notes" || got.Edition == nil || got.Edition.ISBN != isbn {
				t.Fatal("native reading state changed", got, err)
			}
			copies, err := ls.Copies(ctx, member, entry.ID, "")
			want := 1
			if scenario == "wrong-edition" {
				want = 0
			}
			if err != nil || len(copies) != want {
				t.Fatal(copies, err)
			}
		})
	}
}
