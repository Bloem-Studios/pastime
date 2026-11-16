package library_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func ptr[T any](v T) *T { return &v }
func input(title string) library.EntryInput {
	return library.EntryInput{Title: title, MediaType: "book", Unit: "pages", Total: ptr(240), Status: "planned", Notes: "private synthetic note"}
}
func create(t *testing.T, f *testkit.Fixture, s library.Store, a identity.Actor, title string) library.Mutation {
	t.Helper()
	v, e := s.Create(context.Background(), a, testkit.Key(t, f), input(title))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestLibraryOwnershipAndScopeBoundaries(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	member := f.User(t, "library_member", "member")
	v := create(t, f, s, member, "Private Member Atlas")
	if _, e := s.Get(ctx, f.Owner, v.ID); !errors.Is(e, fault.NotFound) {
		t.Fatal("owner read member notes", e)
	}
	if _, e := s.Delete(ctx, f.Owner, v.ID, testkit.Key(t, f), 1); !errors.Is(e, fault.NotFound) {
		t.Fatal("owner deleted member item", e)
	}
	for _, a := range []identity.Actor{f.Owner, member} {
		page, e := s.Entries(ctx, a, library.Filter{Query: "Atlas"})
		if e != nil {
			t.Fatal(e)
		}
		expected := 0
		if a.User.ID == member.User.ID {
			expected = 1
		}
		if len(page.Items) != expected {
			t.Fatal("cross-account search")
		}
	}
	_, raw, e := f.Accounts.MintToken(ctx, member, identity.TokenInput{Name: "Read only", Scopes: []string{"library:read"}, ExpiresInDays: 1, Password: testkit.Password})
	if e != nil {
		t.Fatal(e)
	}
	a, e := f.Accounts.Authenticate(ctx, raw, "token")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, a, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, a, testkit.Key(t, f), input("Forbidden")); !errors.Is(e, fault.Forbidden) {
		t.Fatal("read-only token wrote", e)
	}
}
func TestConcurrentReplayCreatesOneRecordAndRejectsChangedIntent(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	key := testkit.Key(t, f)
	results := make(chan library.Mutation, 8)
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { v, e := s.Create(ctx, f.Owner, key, input("Concurrent Atlas")); results <- v; errs <- e })
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var id int64
	fresh := 0
	for v := range results {
		if id == 0 {
			id = v.ID
		}
		if v.ID != id {
			t.Fatal("replay returned different identity")
		}
		if !v.Replayed {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal("multiple executions", fresh)
	}
	if _, e := s.Create(ctx, f.Owner, key, input("Changed Atlas")); !errors.Is(e, fault.Conflict) {
		t.Fatal("changed replay intent accepted", e)
	}
	for _, table := range []string{"library_entries", "activity_events", "mutation_receipts"} {
		var count int
		if e := f.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); e != nil || count != 1 {
			t.Fatal("duplicate/missing rows", table, count, e)
		}
	}
}
func TestProgressFencingRepeatsAndCompletionCorrection(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	v := create(t, f, s, f.Owner, "Repeat Atlas")
	errOut := make(chan error, 2)
	var wg sync.WaitGroup
	for _, n := range []int{20, 30} {
		key := testkit.Key(t, f)
		wg.Go(func() {
			_, e := s.Progress(ctx, f.Owner, v.ID, key, library.ProgressInput{Version: 1, Progress: ptr(n), Status: "in_progress"})
			errOut <- e
		})
	}
	wg.Wait()
	close(errOut)
	success, conflicts := 0, 0
	for e := range errOut {
		if e == nil {
			success++
		} else if errors.Is(e, fault.Conflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("version fencing failed")
	}
	key := testkit.Key(t, f)
	completed, e := s.Progress(ctx, f.Owner, v.ID, key, library.ProgressInput{Version: 2, Progress: ptr(30), Status: "completed"})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.Progress(ctx, f.Owner, v.ID, key, library.ProgressInput{Version: 2, Progress: ptr(30), Status: "completed"})
	if e != nil || !replay.Replayed || replay.Version != completed.Version {
		t.Fatal("completion retry", e)
	}
	entry, e := s.Get(ctx, f.Owner, v.ID)
	if e != nil || entry.Progress != 240 || entry.CompletedCycles != 1 {
		t.Fatal("completion state", entry, e)
	}
	if _, e = s.Restart(ctx, f.Owner, v.ID, testkit.Key(t, f), entry.Version); e != nil {
		t.Fatal(e)
	}
	entry, e = s.Get(ctx, f.Owner, v.ID)
	if e != nil || entry.Cycle != 2 || entry.Progress != 0 || entry.CompletedCycles != 1 {
		t.Fatal("repeat erased history", entry, e)
	}
	if _, e = s.Progress(ctx, f.Owner, v.ID, testkit.Key(t, f), library.ProgressInput{Version: entry.Version, Progress: ptr(0), Status: "completed"}); e != nil {
		t.Fatal(e)
	}
	entry, _ = s.Get(ctx, f.Owner, v.ID)
	if entry.CompletedCycles != 2 {
		t.Fatal("repeat completion missing")
	}
	if _, e = s.Progress(ctx, f.Owner, v.ID, testkit.Key(t, f), library.ProgressInput{Version: entry.Version, Progress: ptr(100), Status: "in_progress"}); e != nil {
		t.Fatal(e)
	}
	entry, _ = s.Get(ctx, f.Owner, v.ID)
	if entry.CompletedCycles != 1 {
		t.Fatal("correction changed prior completed cycle")
	}
}
func TestAtomicFailureAndPrivateReceiptPayload(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	if _, e := f.Pool.Exec(ctx, "ALTER TABLE activity_events ADD CONSTRAINT reject_added CHECK(kind<>'added')"); e != nil {
		t.Fatal(e)
	}
	key := testkit.Key(t, f)
	if _, e := s.Create(ctx, f.Owner, key, input("Rollback Atlas")); e == nil {
		t.Fatal("fault was not triggered")
	}
	for _, table := range []string{"library_entries", "activity_events", "mutation_receipts"} {
		var n int
		if e := f.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial commit", table, n, e)
		}
	}
	if _, e := f.Pool.Exec(ctx, "ALTER TABLE activity_events DROP CONSTRAINT reject_added"); e != nil {
		t.Fatal(e)
	}
	v, e := s.Create(ctx, f.Owner, key, input("Rollback Atlas"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Delete(ctx, f.Owner, v.ID, testkit.Key(t, f), v.Version); e != nil {
		t.Fatal(e)
	}
	replayed, e := s.Create(ctx, f.Owner, key, input("Rollback Atlas"))
	if e != nil || !replayed.Replayed {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, f.Owner, v.ID); !errors.Is(e, fault.NotFound) {
		t.Fatal("retry restored deleted entry", e)
	}
	var receipt string
	if e = f.Pool.QueryRow(ctx, "SELECT response::text FROM mutation_receipts WHERE idempotency_key=$1", key).Scan(&receipt); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(receipt, "private") || strings.Contains(receipt, "Atlas") {
		t.Fatal("receipt retained private content")
	}
}
func TestPrivateListsAndCascadingRemoval(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	other := f.User(t, "other_reader", "member")
	entry := create(t, f, s, f.Owner, "List Atlas")
	foreign := create(t, f, s, other, "Foreign Atlas")
	list, e := s.SaveList(ctx, f.Owner, 0, testkit.Key(t, f), library.ListInput{Name: "Night shelf"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Membership(ctx, f.Owner, list.ID, foreign.ID, testkit.Key(t, f), list.Version, true); !errors.Is(e, fault.NotFound) {
		t.Fatal("cross-account membership", e)
	}
	list, e = s.Membership(ctx, f.Owner, list.ID, entry.ID, testkit.Key(t, f), list.Version, true)
	if e != nil {
		t.Fatal(e)
	}
	got, e := s.Entries(ctx, f.Owner, library.Filter{ListID: list.ID})
	if e != nil || len(got.Items) != 1 {
		t.Fatal("membership missing", e)
	}
	if _, e = s.List(ctx, other, list.ID); !errors.Is(e, fault.NotFound) {
		t.Fatal("private list disclosed", e)
	}
	if _, e = s.Delete(ctx, f.Owner, entry.ID, testkit.Key(t, f), entry.Version); e != nil {
		t.Fatal(e)
	}
	metadata, e := s.List(ctx, f.Owner, list.ID)
	if e != nil || metadata.ItemCount != 0 {
		t.Fatal("orphan membership", e)
	}
	activity, e := s.Activity(ctx, f.Owner, entry.ID, "", 30)
	if e != nil || len(activity.Items) != 0 {
		t.Fatal("deleted activity retained", e)
	}
}
func TestCursorOwnershipFiltersAndBoundedPages(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	other := f.User(t, "cursor_reader", "member")
	for _, name := range []string{"North Atlas", "South Atlas", "East Atlas"} {
		create(t, f, s, f.Owner, name)
	}
	first, e := s.Entries(ctx, f.Owner, library.Filter{Limit: 2, Query: "Atlas"})
	if e != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal("first page", e)
	}
	second, e := s.Entries(ctx, f.Owner, library.Filter{Limit: 2, Query: "Atlas", Cursor: *first.NextCursor})
	if e != nil || len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].ID >= first.Items[1].ID {
		t.Fatal("second page", e)
	}
	for _, v := range []struct {
		a      identity.Actor
		filter library.Filter
	}{{other, library.Filter{Query: "Atlas", Cursor: *first.NextCursor}}, {f.Owner, library.Filter{Query: "North", Cursor: *first.NextCursor}}, {f.Owner, library.Filter{Limit: 101}}} {
		if _, e := s.Entries(ctx, v.a, v.filter); fault.Public(e).Status != 422 {
			t.Fatal("invalid cursor/filter/limit accepted", e)
		}
	}
}
func TestRevocationFencesAdmittedTransactionAndBlocksReplay(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	member := f.User(t, "fenced_member", "member")
	key := testkit.Key(t, f)
	if _, e := s.Create(ctx, member, key, input("Fence Atlas")); e != nil {
		t.Fatal(e)
	}
	tx, e := f.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = identity.AuthorizeTx(ctx, tx, member, "library:write"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := f.Accounts.UpdateUser(ctx, f.Owner, member.User.ID, identity.UserPatch{Version: member.User.Version, Active: ptr(false)}, false)
		done <- e
	}()
	select {
	case e := <-done:
		t.Fatal("revocation passed the in-flight fence", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revocation did not unblock")
	}
	if _, e = s.Create(ctx, member, key, input("Fence Atlas")); !errors.Is(e, fault.Unauthenticated) {
		t.Fatal("disabled actor replayed receipt", e)
	}
}

func TestSearchIgnoresAccentsCaseAndTypos(t *testing.T) {
	f := testkit.New(t)
	s := library.Store{Pool: f.Pool}
	ctx := context.Background()
	create(t, f, s, f.Owner, "Le Fabuleux Destin d'Amélie Poulain")
	create(t, f, s, f.Owner, "Pokémon Adventures")
	create(t, f, s, f.Owner, "The Remains of the Day")
	other := f.User(t, "search_other", "member")
	create(t, f, s, other, "Amélie")
	for query, want := range map[string]string{
		"amelie":             "Le Fabuleux Destin d'Amélie Poulain",
		"AMÉLIE":             "Le Fabuleux Destin d'Amélie Poulain",
		"pokemon":            "Pokémon Adventures",
		"remains of teh day": "The Remains of the Day",
		"adventres":          "Pokémon Adventures",
		"zebra":              "",
	} {
		page, e := s.Entries(ctx, f.Owner, library.Filter{Query: query})
		if e != nil {
			t.Fatal(e)
		}
		if want == "" {
			if len(page.Items) != 0 {
				t.Fatalf("%q matched %d titles", query, len(page.Items))
			}
			continue
		}
		if len(page.Items) != 1 || page.Items[0].Title != want {
			t.Fatalf("%q: %+v", query, page.Items)
		}
	}
}
