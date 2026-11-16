// Package testdb opens only the explicitly named loopback development test database.
package testdb

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PASTIME_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("PASTIME_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("integration database is required")
		}
		t.Skip("set PASTIME_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	u, err := url.Parse(dsn)
	if err != nil || u == nil || u.Path != "/pastime_test" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		t.Fatal("refusing a database other than loopback pastime_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := database.Migrate(ctx, dsn); err != nil {
		t.Fatal("test database migration failed")
	}
	pool, err := database.Open(ctx, dsn, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var name string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil || name != "pastime_test" {
		t.Fatal("test database identity mismatch")
	}
	return pool
}
