package bloem

import (
	"testing"
	"time"
)

func TestLibraryBrowseIsBoundToRequestAndProfile(t *testing.T) {
	f, s, raw, _, member, source := calendarFixture(t)
	q := LibraryQuery{InstanceID: source.InstanceID, View: "catalog", Limit: 20, Kind: "ebook"}
	queued, err := s.RequestLibrary(t.Context(), member, q)
	if err != nil || queued.Status != "pending" || queued.RequestID == "" {
		t.Fatal(queued, err)
	}
	pending, err := s.PendingLibrary(t.Context(), raw)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	in := LibraryInput{RequestID: pending[0].RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "catalog", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Items: []LibraryItem{{ContentID: "ebook-1", Kind: "ebook", Title: "Synthetic book", ExternalIDs: map[string]string{"isbn": "9780000000002"}}}}
	wrong := in
	wrong.RequestID = "foreign"
	if err = s.AcceptLibrary(t.Context(), raw, wrong); err == nil {
		t.Fatal("unrequested publication")
	}
	if err = s.AcceptLibrary(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.LibraryResult(t.Context(), member, queued.RequestID)
	if err != nil || got.Status != "ready" || got.Page == nil || len(got.Page.Items) != 1 {
		t.Fatal(got, err)
	}
	other := f.User(t, "library_other", "member")
	if _, err = s.LibraryResult(t.Context(), other, queued.RequestID); err == nil {
		t.Fatal("profile leak")
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_library_requests SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, queued.RequestID); err != nil {
		t.Fatal(err)
	}
	got, err = s.LibraryResult(t.Context(), member, queued.RequestID)
	if err != nil || got.Status != "expired" || got.Page != nil {
		t.Fatal("expired metadata leak", got, err)
	}
	if err = s.AcceptLibrary(t.Context(), f.OwnerRaw, in); err == nil {
		t.Fatal("viewer session published")
	}
}
func TestLibraryBrowseNeverRebindsCachedMetadataToNewSourceRevision(t *testing.T) {
	f, s, raw, _, member, source := calendarFixture(t)
	q := LibraryQuery{InstanceID: source.InstanceID, View: "catalog", Limit: 20}
	request, err := s.RequestLibrary(t.Context(), member, q)
	if err != nil {
		t.Fatal(err)
	}
	in := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "catalog", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Items: []LibraryItem{{ContentID: "old", Kind: "movie", Title: "Old permissions"}}}
	if err = s.AcceptLibrary(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_profiles SET source_revision=source_revision+1 WHERE instance_id=$1`, source.InstanceID); err != nil {
		t.Fatal(err)
	}
	next, err := s.RequestLibrary(t.Context(), member, q)
	if err != nil || next.Status != "pending" || next.RequestID == request.RequestID || next.Page != nil {
		t.Fatal("stale metadata rebound", next, err)
	}
	if err = s.AcceptLibrary(t.Context(), raw, in); err == nil {
		t.Fatal("late old request accepted")
	}
}
func TestCollectionMutationQueueIsScopedAndRequiresWritableSource(t *testing.T) {
	_, s, raw, _, member, source := calendarFixture(t)
	q := LibraryQuery{InstanceID: source.InstanceID, View: "collections", Limit: 20}
	request, err := s.RequestLibrary(t.Context(), member, q)
	if err != nil {
		t.Fatal(err)
	}
	input := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: "collections", GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Collections: []LibraryCollection{{ID: "personal:own", Name: "Mine", Scope: "personal", Writable: true, Revision: 1, DefinitionKind: "manual"}, {ID: "personal:shared", Name: "Shared", Scope: "shared", Revision: 1, DefinitionKind: "manual"}}}
	if err = s.AcceptLibrary(t.Context(), raw, input); err != nil {
		t.Fatal(err)
	}
	intent := LibraryMutationIntent{InstanceID: source.InstanceID, EventID: "change-1", Operation: "collection.update", TargetID: "personal:own", ExpectedRevision: 1, Name: "New name"}
	result, err := s.RequestLibraryMutation(t.Context(), member, intent)
	if err != nil || result.Status != "pending" {
		t.Fatal(result, err)
	}
	if _, err = s.RequestLibraryMutation(t.Context(), member, intent); err != nil {
		t.Fatal("same request rejected", err)
	}
	intent.Name = "Different"
	if _, err = s.RequestLibraryMutation(t.Context(), member, intent); err == nil {
		t.Fatal("changed replay accepted")
	}
	intent.EventID = "shared-write"
	intent.TargetID = "personal:shared"
	if _, err = s.RequestLibraryMutation(t.Context(), member, intent); err == nil {
		t.Fatal("shared viewer queued write")
	}
	pending, err := s.PendingLibraryMutations(t.Context(), raw)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	receipt := LibraryMutationResult{RequestID: result.RequestID, EventID: "change-1", TargetID: "personal:own", Revision: 2, Status: "applied"}
	if err = s.AcceptLibraryMutation(t.Context(), raw, receipt); err != nil {
		t.Fatal(err)
	}
	confirmed, err := s.LibraryMutationResult(t.Context(), member, result.RequestID)
	if err != nil || confirmed.Status != "applied" || confirmed.Revision != 2 {
		t.Fatal(confirmed, err)
	}
}
func TestLibraryLookupRefusalEndsPendingWithoutMetadata(t *testing.T) {
	_, s, raw, _, member, source := calendarFixture(t)
	request, err := s.RequestLibrary(t.Context(), member, LibraryQuery{InstanceID: source.InstanceID, View: "catalog", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RejectLibrary(t.Context(), raw, LibraryFailure{RequestID: request.RequestID, Reason: "denied"}); err != nil {
		t.Fatal(err)
	}
	out, err := s.LibraryResult(t.Context(), member, request.RequestID)
	if err != nil || out.Status != "denied" || out.Page != nil {
		t.Fatal("denial stayed pending or leaked", out, err)
	}
}

func TestCopyAndBookPagesRejectForeignPayloadFamilies(t *testing.T) {
	_, s, raw, _, member, source := calendarFixture(t)
	for _, view := range []string{"copies", "book_states"} {
		t.Run(view, func(t *testing.T) {
			request, err := s.RequestLibrary(t.Context(), member, LibraryQuery{InstanceID: source.InstanceID, View: view, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			in := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: view, GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true, Items: []LibraryItem{{ContentID: "foreign", Kind: "movie", Title: "Wrong family"}}}
			if err = s.AcceptLibrary(t.Context(), raw, in); err == nil {
				t.Fatal("foreign payload family accepted")
			}
		})
	}
}
func TestCopyAndBookPagesValidateRecordsBeforePublication(t *testing.T) {
	_, s, raw, _, member, source := calendarFixture(t)
	for _, view := range []string{"copies", "book_states"} {
		t.Run(view, func(t *testing.T) {
			request, err := s.RequestLibrary(t.Context(), member, LibraryQuery{InstanceID: source.InstanceID, View: view, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			in := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: view, GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Complete: true}
			if view == "copies" {
				in.Copies = []LibraryCopy{{ID: "copy-one", Title: "Synthetic copy", Kind: "ebook", Revision: 1}, {ID: "copy-one", Title: "Duplicate", Kind: "ebook", Revision: 1}}
			} else {
				in.BookStates = []BookState{{ContentID: "book-one", Kind: "ebook", EditionID: "edition-one", FileID: 1, EbookFraction: 1.5}}
			}
			if err = s.AcceptLibrary(t.Context(), raw, in); err == nil {
				t.Fatal("invalid records published")
			}
		})
	}
}

func TestCopyAndBookPagesPreserveExactValuesAndQueueEdits(t *testing.T) {
	_, s, raw, _, member, source := calendarFixture(t)
	for _, view := range []string{"copies", "book_states"} {
		t.Run(view, func(t *testing.T) {
			request, err := s.RequestLibrary(t.Context(), member, LibraryQuery{InstanceID: source.InstanceID, View: view, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Truncate(time.Microsecond)
			in := LibraryInput{RequestID: request.RequestID, InstanceID: source.InstanceID, AccountID: source.AccountID, ProfileID: source.ProfileID, View: view, GeneratedAt: at, ExpiresAt: at.Add(time.Minute), Complete: true}
			intent := LibraryMutationIntent{InstanceID: source.InstanceID, EventID: "edit-" + view, ExpectedRevision: 1}
			if view == "copies" {
				in.Copies = []LibraryCopy{{ID: "owned-one", Kind: "ebook", Title: "Synthetic owned book", Format: "hardcover", Edition: "First", PriceCents: 1234, Currency: "EUR", Notes: "Keep exact", Custom: map[string]string{"shelf": "A"}, Revision: 1}}
				intent.Operation = "copy.upsert"
				intent.TargetID = "owned-one"
				intent.Copy = &in.Copies[0]
			} else {
				in.BookStates = []BookState{{ContentID: "book-one", Kind: "ebook", Title: "Synthetic book", EditionID: "edition-one", FileID: 42, EbookLocation: "epubcfi(/6/4!/4/2:13)", EbookFraction: 0.123456789, ChangedAt: &at, Revision: 1}}
				intent.Operation = "book.progress"
				intent.TargetID = "book-one"
				intent.BookState = &in.BookStates[0]
			}
			if err = s.AcceptLibrary(t.Context(), raw, in); err != nil {
				t.Fatal(err)
			}
			got, err := s.LibraryResult(t.Context(), member, request.RequestID)
			if err != nil || got.Page == nil {
				t.Fatal(got, err)
			}
			if view == "copies" {
				if got.Page.Copies[0].PriceCents != 1234 || got.Page.Copies[0].Custom["shelf"] != "A" {
					t.Fatal("copy values lost", got.Page.Copies)
				}
			} else {
				if got.Page.BookStates[0].EbookFraction != in.BookStates[0].EbookFraction || got.Page.BookStates[0].EbookLocation != in.BookStates[0].EbookLocation {
					t.Fatal("reader values changed", got.Page.BookStates)
				}
			}
			receipt, err := s.RequestLibraryMutation(t.Context(), member, intent)
			if err != nil || receipt.Status != "pending" {
				t.Fatal(receipt, err)
			}
			pending, err := s.PendingLibraryMutations(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, q := range pending {
				if q.EventID == intent.EventID {
					found = true
					if view == "copies" && (q.Copy == nil || q.Copy.PriceCents != 1234) || view == "book_states" && (q.BookState == nil || q.BookState.EbookLocation != in.BookStates[0].EbookLocation) {
						t.Fatal("mutation payload lost", q)
					}
				}
			}
			if !found {
				t.Fatal("mutation not deliverable")
			}
		})
	}
}
