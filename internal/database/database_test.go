package database_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/Bloem-Studios/pastime/migrations"
)

func TestMigrateIdempotentAndSchemaReady(t *testing.T) {
	pool := testdb.Open(t)
	if err := database.CheckSchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background(), os.Getenv("PASTIME_TEST_DATABASE_URL")); err != nil {
		t.Fatal(err)
	}
	if err := database.CheckSchema(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	// Migration numbers must run 1..N without gaps so ExpectedVersion names the whole set.
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		if want := fmt.Sprintf("%05d_", i+1); !strings.HasPrefix(name, want) {
			t.Fatalf("migration %s is out of sequence; expected prefix %s", name, want)
		}
	}
	if database.ExpectedVersion() != int64(len(names)) {
		t.Fatal("expected schema version does not match the migration set")
	}
}
