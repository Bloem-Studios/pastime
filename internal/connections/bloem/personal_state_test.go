package bloem

import (
	"errors"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/sealed"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestPersonalStatePreservesTrackingMetadataAndRejectsReplayChanges(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://example.invalid", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Provision(ctx, raw, ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Account", ProfileID: "child", DefaultProfileID: "child", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.IssueCredential(ctx, raw, child.ProfileID, "state-test")
	if err != nil {
		t.Fatal(err)
	}
	var title int64
	if err = f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','tmdb','42','Film','minutes',90) RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, `INSERT INTO library_entries(profile_id,title,media_type,unit,total,status,progress,notes,catalog_item_id) VALUES($1,'Film','movie','minutes',90,'in_progress',12,'Keep my notes',$2)`, child.ProfileID, title); err != nil {
		t.Fatal(err)
	}
	e := PersonalEvent{Version: 1, InstanceID: instance.ID, AccountID: "account", ProfileID: "child", EntityID: "favorite-film", EventID: "favorite-add", Revision: 1, Operation: "favorite_add", OccurredAt: time.Now().UTC()}
	apply := func() {
		t.Helper()
		if _, err = s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title); err != nil {
			t.Fatal(err)
		}
	}
	apply()
	apply()
	var count int
	if err = f.Pool.QueryRow(ctx, `SELECT count(*) FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2 AND kind='favorite' AND present`, child.ProfileID, title).Scan(&count); err != nil || count != 1 {
		t.Fatal("favorite membership", count, err)
	}
	e.Operation = "favorite_remove"
	if _, err = s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title); !errors.Is(err, fault.Conflict) {
		t.Fatal("changed replay accepted", err)
	}
	e.EventID = "favorite-remove"
	e.Revision = 2
	apply()
	e.EventID = "old-add"
	e.Revision = 1
	e.Operation = "favorite_add"
	result, err := s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title)
	if err != nil || result.Status != "stale" {
		t.Fatal("stale add", result, err)
	}
	e.EventID = "watchlist-add"
	e.EntityID = "watchlist-film"
	e.Operation = "watchlist_add"
	e.Revision = 1
	apply()
	e.EventID = "rating-set"
	e.EntityID = "rating-film"
	e.Operation = "rating_set"
	score := 8
	e.Rating = &score
	apply()
	var notes, status string
	var progress, rating int
	if err = f.Pool.QueryRow(ctx, `SELECT notes,status,progress,rating FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2`, child.ProfileID, title).Scan(&notes, &status, &progress, &rating); err != nil || notes != "Keep my notes" || status != "in_progress" || progress != 12 || rating != 8 {
		t.Fatal("tracking metadata overwritten", notes, status, progress, rating, err)
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE library_entries SET rating=3 WHERE profile_id=$1 AND catalog_item_id=$2`, child.ProfileID, title); err != nil {
		t.Fatal(err)
	}
	e.EventID = "would-round-native"
	e.Revision = 2
	if _, err = s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title); err == nil || fault.Public(err).Code != "rating_precision" {
		t.Fatal("native rating precision overwritten", err)
	}
	if err = f.Pool.QueryRow(ctx, `SELECT rating FROM library_entries WHERE profile_id=$1 AND catalog_item_id=$2`, child.ProfileID, title).Scan(&rating); err != nil || rating != 3 {
		t.Fatal("native rating rounded", rating, err)
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE library_entries SET rating=8 WHERE profile_id=$1 AND catalog_item_id=$2`, child.ProfileID, title); err != nil {
		t.Fatal(err)
	}
	e.EventID = "rating-clear"
	e.Operation = "rating_remove"
	e.OccurredAt = time.Now().UTC()
	e.Rating = nil
	e.Revision = 2
	apply()
	ratings, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"rating"}, PageSize: 100})
	if err != nil || len(ratings.Items) != 1 || !ratings.Items[0].Removed || ratings.Items[0].Rating != nil {
		t.Fatalf("rating removal tombstone lost: %+v %v", ratings, err)
	}
	page, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite", "watchlist"}, PageSize: 1})
	if err != nil || !page.CompleteSnapshot || len(page.Items) != 1 || page.NextPageToken == "" {
		t.Fatal("first snapshot page", page, err)
	}
	if _, err = f.Pool.Exec(ctx, `UPDATE profile_catalog_memberships SET present=false WHERE profile_id=$1 AND kind='watchlist'`, child.ProfileID); err != nil {
		t.Fatal(err)
	}
	next, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite", "watchlist"}, PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || len(next.Items) != 1 || next.Items[0].Removed || next.NextPageToken != "" {
		t.Fatal("snapshot changed between pages", next, err)
	}
	replay, err := s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite", "watchlist"}, PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || replay.Items[0].Removed {
		t.Fatal("page replay changed", err)
	}
	if _, err = s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"rating"}, PageToken: page.NextPageToken, PageSize: 1}); err == nil {
		t.Fatal("page changed family")
	}
	e.EventID = "foreign-profile"
	e.ProfileID = "other"
	if _, err = s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title); !errors.Is(err, fault.Forbidden) {
		t.Fatal("foreign profile accepted", err)
	}
	limited := false
	for i := 0; i < 17; i++ {
		_, err = s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite", "watchlist"}, PageSize: 1})
		if errors.Is(err, fault.Limited) {
			limited = true
			break
		}
		if err != nil {
			t.Fatal("snapshot capacity", i, err)
		}
	}
	if !limited {
		t.Fatal("unbounded multi-page personal snapshots")
	}
	inactive := ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Account", ProfileID: "child", DefaultProfileID: "child", Name: "Child", AccountRevision: 2, Revision: 2, AccountActive: true, Active: false}
	if _, err = s.Provision(ctx, raw, inactive); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReadPersonalState(ctx, cred.Credential, PersonalRead{Kinds: []string{"favorite", "watchlist"}, PageSize: 1, PageToken: page.NextPageToken}); err == nil {
		t.Fatal("revoked profile read snapshot")
	}

}

func TestManagedRatingValidationPreservesEveryExactStar(t *testing.T) {
	for _, score := range []int{2, 4, 6, 8, 10} {
		e := PersonalEvent{Version: 1, InstanceID: "instance", AccountID: "account", ProfileID: "profile", EntityID: "rating", EventID: "event", Revision: 1, Operation: "rating_set", OccurredAt: time.Now(), Rating: &score}
		in, err := ValidatePersonalEvent(e)
		if err != nil || in.Rating == nil || *in.Rating != score {
			t.Fatal("exact rating changed", score, err)
		}
	}
	for _, score := range []int{-1, 0, 1, 3, 5, 7, 9, 11} {
		e := PersonalEvent{Version: 1, InstanceID: "instance", AccountID: "account", ProfileID: "profile", EntityID: "rating", EventID: "event", Revision: 1, Operation: "rating_set", OccurredAt: time.Now(), Rating: &score}
		if _, err := ValidatePersonalEvent(e); err == nil {
			t.Fatal("unrepresentable rating accepted", score)
		}
	}
}

func TestDelayedSourceCannotUndoNewerNativeMembershipRemoval(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	box, _ := sealed.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	s := Store{Pool: f.Pool, Box: box}
	instance, raw, err := s.Register(ctx, f.Owner, InstanceInput{ID: testkit.Key(t, f), URL: "https://example.invalid", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Provision(ctx, raw, ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "account", AccountName: "Account", ProfileID: "child", DefaultProfileID: "child", Name: "Child", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cred, err := s.IssueCredential(ctx, raw, child.ProfileID, "native-removal-test")
	if err != nil {
		t.Fatal(err)
	}
	var title int64
	if err = f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(kind,provider,provider_id,title,unit) VALUES('movie','tmdb','42','Synthetic film','minutes') RETURNING id`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	if _, err = f.Pool.Exec(ctx, `INSERT INTO profile_catalog_memberships(profile_id,catalog_item_id,kind,present,changed_at,source) VALUES($1,$2,'favorite',false,clock_timestamp(),'user:personal.favorite')`, child.ProfileID, title); err != nil {
		t.Fatal(err)
	}
	e := PersonalEvent{Version: 1, InstanceID: instance.ID, AccountID: "account", ProfileID: "child", EntityID: "favorite-film", EventID: "delayed-add", Revision: 1, Operation: "favorite_add", OccurredAt: old}
	for i := 0; i < 2; i++ {
		result, err := s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title)
		if err != nil || result.Status != "stale" {
			t.Fatalf("older source undid native removal: %+v %v", result, err)
		}
	}
	var present bool
	if err = f.Pool.QueryRow(ctx, `SELECT present FROM profile_catalog_memberships WHERE profile_id=$1 AND catalog_item_id=$2 AND kind='favorite'`, child.ProfileID, title).Scan(&present); err != nil || present {
		t.Fatal("native removal lost", present, err)
	}
	e.EventID = "newer-add"
	e.Revision = 2
	e.OccurredAt = time.Now().UTC()
	if result, err := s.ApplyMatchedPersonalEvent(ctx, cred.Credential, e, title); err != nil || result.Status != "applied" {
		t.Fatalf("newer source rejected: %+v %v", result, err)
	}
}
