package identity_test

import (
	"context"
	"errors"
	"github.com/Bloem-Studios/pastime/internal/discover"
	"github.com/Bloem-Studios/pastime/internal/exports"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/kosync"
	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/notify"
	"github.com/Bloem-Studios/pastime/internal/testkit"
	"testing"
)

func TestRevokedActorCannotChangeServiceSettings(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	prefs, err := lib.Preferences(ctx, f.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Pool.Exec(ctx, "UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1", f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	for name, write := range map[string]func() error{
		"preferences": func() error { _, err := lib.SavePreferences(ctx, f.Owner, prefs); return err },
		"notification settings": func() error {
			_, err := (notify.Store{Pool: f.Pool}).SaveSettings(ctx, f.Owner, notify.Settings{})
			return err
		},
		"reader credentials": func() error {
			return (kosync.Store{Pool: f.Pool}).SetPassword(ctx, f.Owner, "synthetic-reader-password")
		},
		"export schedule": func() error {
			_, err := (exports.Store{Pool: f.Pool}).SaveSchedule(ctx, f.Owner, exports.Schedule{Frequency: "daily", Format: "jsonl", Keep: 5})
			return err
		},
		"discovery dismissal": func() error {
			return (discover.Store{Pool: f.Pool}).Dismiss(ctx, f.Owner, "manual", "book", "synthetic", false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(); !errors.Is(err, fault.Unauthenticated) {
				t.Fatal("stale credential accepted", err)
			}
		})
	}
}
