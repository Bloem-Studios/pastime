package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Isolated uses a unique schema inside the already-validated loopback test database.
// It never truncates shared fixture tables or touches the development database.
func Isolated(t testing.TB) *pgxpool.Pool {
	t.Helper()
	admin := Open(t)
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "test_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{name}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error("could not remove task-owned test schema:", err)
		}
	})
	u, err := url.Parse(os.Getenv("PASTIME_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	if err = database.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err = database.Open(ctx, u.String(), 8)
	if err != nil {
		t.Fatal(err)
	}
	var current string
	if err = pool.QueryRow(ctx, "SELECT current_schema()").Scan(&current); err != nil || current != name {
		t.Fatal("isolated schema identity mismatch")
	}
	return pool
}
