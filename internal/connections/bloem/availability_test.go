package bloem

import (
	"errors"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"testing"
	"time"
)

func TestAvailabilityIsPrivateLeasedAndRejectsLateRequests(t *testing.T) {
	f, s, raw, _, member, page := calendarFixture(t)
	var item int64
	if err := f.Pool.QueryRow(t.Context(), `INSERT INTO catalog_items(provider,provider_id,kind,title,unit) VALUES('tmdb','123','movie','Synthetic film','minutes') RETURNING id`).Scan(&item); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Availability(t.Context(), member, []int64{item})
	if err != nil || len(rows) != 1 || rows[0].Status != "checking" {
		t.Fatalf("queue: %v %v", rows, err)
	}
	pending, err := s.PendingAvailability(t.Context(), raw)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %v %v", pending, err)
	}
	in := AvailabilityInput{Version: 1, InstanceID: page.InstanceID, AccountID: page.AccountID, ProfileID: page.ProfileID, CheckedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Results: []AvailabilityResult{{RequestID: pending[0].RequestID, Available: true, ContentID: "movie"}}}
	if err = s.AcceptAvailability(t.Context(), raw, in); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Availability(t.Context(), member, []int64{item})
	if err != nil || len(rows) != 1 || rows[0].Status != "available" || rows[0].Handoff == nil {
		t.Fatalf("available: %v %v", rows, err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_availability SET expires_at=clock_timestamp()+interval '50 seconds'`); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Availability(t.Context(), member, []int64{item})
	if err != nil || rows[0].Status != "available" {
		t.Fatalf("refresh discarded valid lease: %v %v", rows, err)
	}
	pending, err = s.PendingAvailability(t.Context(), raw)
	if err != nil || len(pending) != 1 || pending[0].RequestID == in.Results[0].RequestID {
		t.Fatalf("refresh not queued: %v %v", pending, err)
	}
	other := f.User(t, "availability_other", "member")
	otherRows, err := s.Availability(t.Context(), other, []int64{item})
	if err != nil || len(otherRows) != 0 {
		t.Fatalf("profile leak: %v %v", otherRows, err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_availability SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Availability(t.Context(), member, []int64{item})
	if err != nil || rows[0].Status != "checking" || rows[0].Handoff != nil {
		t.Fatalf("expired: %v %v", rows, err)
	}
	if err = s.AcceptAvailability(t.Context(), raw, in); !errors.Is(err, fault.Conflict) {
		t.Fatalf("late response accepted: %v", err)
	}
	if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_profiles SET excluded=true WHERE profile_id=$1`, member.TrackingID()); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Availability(t.Context(), member, []int64{item})
	if err != nil || len(rows) != 0 {
		t.Fatalf("revoked profile leaked: %v %v", rows, err)
	}
}
