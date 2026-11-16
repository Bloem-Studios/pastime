package identity_test

import (
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
	"time"
)

func TestServiceAccessSnapshotRejectsReplayAndForeignIssuer(t *testing.T) {
	f := testkit.New(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	const issuer = "https://sso.example.test/pastime/"
	apply := func(provider string, at, until time.Time, body string) error {
		_, err := f.Pool.Exec(t.Context(), `SELECT pastime_apply_service_access($1,$2,$3,$4::jsonb)`, provider, at, until, body)
		return err
	}
	if err := apply(issuer, now, now.Add(time.Minute), `[]`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		issuer    string
		at, until time.Time
		body      string
	}{
		{issuer, now, now.Add(time.Minute), `[]`},
		{"https://foreign.example.test/pastime/", now.Add(time.Millisecond), now.Add(time.Minute), `[]`},
		{issuer, now.Add(time.Millisecond), now.Add(121 * time.Second), `[]`},
		{issuer, now.Add(time.Millisecond), now.Add(time.Minute), `[{"subject":"invalid","expires_at":"2026-01-01T00:00:00Z"}]`},
	} {
		if err := apply(tc.issuer, tc.at, tc.until, tc.body); err == nil {
			t.Fatal("invalid snapshot accepted", tc.issuer)
		}
	}
	var saved string
	if err := f.Pool.QueryRow(t.Context(), `SELECT issuer FROM service_access_policy WHERE id=1`).Scan(&saved); err != nil || saved != issuer {
		t.Fatal("invalid snapshot changed authority", saved, err)
	}
}
