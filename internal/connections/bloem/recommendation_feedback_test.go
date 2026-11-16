package bloem

import (
	"testing"
	"time"
)

func TestRecommendationFeedbackSurvivesRefreshAndOrderedUndo(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	in := RecommendationsInput{Version: 1, InstanceID: page.InstanceID, AccountID: page.AccountID, ProfileID: page.ProfileID, GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Items: []AIRecommendation{{ContentID: "movie", Kind: "movie", Title: "AI movie"}}}
	if err := s.AcceptRecommendations(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	choice := RecommendationChoice{InstanceID: page.InstanceID, ContentID: "movie", Dismissed: true}
	if err := s.SetRecommendationChoice(t.Context(), member, choice); err != nil {
		t.Fatal(err)
	}
	rows, err := s.AIRecommendations(t.Context(), member)
	if err != nil || len(rows) != 0 {
		t.Fatalf("dismissed feed: %v %v", rows, err)
	}
	pending, err := s.PendingRecommendationFeedback(t.Context(), raw)
	if err != nil || len(pending) != 1 || !pending[0].Dismissed {
		t.Fatalf("pending: %v %v", pending, err)
	}
	old := pending[0]
	in.GeneratedAt = in.GeneratedAt.Add(time.Millisecond)
	if err = s.AcceptRecommendations(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	rows, err = s.AIRecommendations(t.Context(), member)
	if err != nil || len(rows) != 0 {
		t.Fatal("refresh erased choice", rows, err)
	}
	choice.Dismissed = false
	if err = s.SetRecommendationChoice(t.Context(), member, choice); err != nil {
		t.Fatal(err)
	}
	if err = s.AcknowledgeRecommendationFeedback(t.Context(), raw, old); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingRecommendationFeedback(t.Context(), raw)
	if err != nil || len(pending) != 1 || pending[0].Dismissed || pending[0].Revision <= old.Revision {
		t.Fatal("stale ack lost undo", pending, err)
	}
	rows, err = s.AIRecommendations(t.Context(), member)
	if err != nil || len(rows) != 1 {
		t.Fatal("undo did not restore", rows, err)
	}
	other := f.User(t, "feedback_other", "member")
	choice.Dismissed = true
	if err = s.SetRecommendationChoice(t.Context(), other, choice); err == nil {
		t.Fatal("foreign profile changed choice")
	}
	if err = s.AcknowledgeRecommendationFeedback(t.Context(), f.OwnerRaw, pending[0]); err == nil {
		t.Fatal("viewer acknowledged machine work")
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_instances SET active=false WHERE id=$1`, page.InstanceID); err != nil {
		t.Fatal(err)
	}
	if err = s.SetRecommendationChoice(t.Context(), member, choice); err == nil {
		t.Fatal("revoked instance changed choice")
	}
}

func TestRecommendationFeedbackQueueDoesNotStarvePastOneBatch(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	if _, err := f.Pool.Exec(t.Context(), `INSERT INTO bloem_recommendation_feedback(instance_id,profile_id,content_id,title,dismissed,revision) SELECT $1,$2,'choice-'||n,'Synthetic choice',true,1 FROM generate_series(1,105) n`, page.InstanceID, member.TrackingID()); err != nil {
		t.Fatal(err)
	}
	first, err := s.PendingRecommendationFeedback(t.Context(), raw)
	if err != nil || len(first) != 100 {
		t.Fatal(first, err)
	}
	seen := map[string]bool{}
	for _, item := range first {
		seen[item.ContentID] = true
	}
	next, err := s.PendingRecommendationFeedback(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range next {
		seen[item.ContentID] = true
	}
	if len(seen) != 105 {
		t.Fatal("failed first batch starved later choices", len(seen))
	}
}
