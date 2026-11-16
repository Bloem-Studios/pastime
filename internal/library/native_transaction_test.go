package library_test

import (
	"context"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/library"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

func TestNativePlayEffectSharesCallerTransaction(t *testing.T) {
	f := testkit.New(t)
	ctx := context.Background()
	lib := library.Store{Pool: f.Pool}
	var catalogID int64
	if err := f.Pool.QueryRow(ctx, `INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total) VALUES(0,'movie','synthetic','native-transaction','Native film','minutes',90) RETURNING id`).Scan(&catalogID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	applied, err := lib.ScrobbleTx(ctx, tx, f.Owner, "bloem", catalogID, catalogID, time.Now(), "consumption-one")
	if err != nil || !applied {
		t.Fatal("transactional play", applied, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1", f.Owner.TrackingID()).Scan(&count); err != nil || count != 0 {
		t.Fatal("effect escaped receipt rollback", count, err)
	}
	for _, id := range []string{"consumption-one", "consumption-one", "consumption-two"} {
		tx, err = f.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = lib.ScrobbleTx(ctx, tx, f.Owner, "bloem", catalogID, catalogID, time.Now(), id)
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.Pool.QueryRow(ctx, "SELECT count(*) FROM plays WHERE profile_id=$1", f.Owner.TrackingID()).Scan(&count); err != nil || count != 2 {
		t.Fatal("stable consumption identity dedup", count, err)
	}
}
