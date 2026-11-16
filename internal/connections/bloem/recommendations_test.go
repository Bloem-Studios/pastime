package bloem

import (
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"testing"
	"time"
)

func TestAIRecommendationsArePersonalLeasedAndOrdered(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	in := RecommendationsInput{Version: 1, InstanceID: page.InstanceID, AccountID: page.AccountID, ProfileID: page.ProfileID, GeneratedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Items: []AIRecommendation{{ContentID: "movie", Kind: "movie", Title: "Personal AI title", Reason: "Taste match"}}}
	if err := s.AcceptRecommendations(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	got, err := s.AIRecommendations(t.Context(), member)
	if err != nil || len(got) != 1 || got[0].Reason != "Taste match" {
		t.Fatalf("personal feed: %v %v", got, err)
	}
	other := f.User(t, "recommendations_other", "member")
	got, err = s.AIRecommendations(t.Context(), other)
	if err != nil || len(got) != 0 {
		t.Fatalf("profile leak: %v %v", got, err)
	}
	newer := in
	newer.GeneratedAt = in.GeneratedAt.Add(time.Millisecond)
	newer.Items = []AIRecommendation{}
	if err = s.AcceptRecommendations(t.Context(), raw, newer); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptRecommendations(t.Context(), raw, in); !errors.Is(err, fault.Conflict) {
		t.Fatalf("stale projection: %v", err)
	}
	got, err = s.AIRecommendations(t.Context(), member)
	if err != nil || len(got) != 0 {
		t.Fatalf("removed recommendations: %v %v", got, err)
	}
	in.GeneratedAt = newer.GeneratedAt.Add(time.Millisecond)
	if err = s.AcceptRecommendations(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_ai_recommendations SET expires_at=clock_timestamp()-interval '1 second' WHERE instance_id=$1`, in.InstanceID); err != nil {
		t.Fatal(err)
	}
	got, err = s.AIRecommendations(t.Context(), member)
	if err != nil || len(got) != 0 {
		t.Fatalf("expired recommendation: %v %v", got, err)
	}
	if err = s.AcceptRecommendations(t.Context(), f.OwnerRaw, in); err == nil {
		t.Fatal("UI session used machine endpoint")
	}
}
