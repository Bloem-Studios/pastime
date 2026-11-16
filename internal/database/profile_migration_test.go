package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/Bloem-Studios/pastime/migrations"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestProfilesMigrateExistingDataWithoutChangeFeedNoise(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	name := fmt.Sprintf("profile_migration_%d", time.Now().UnixNano())
	schema := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(os.Getenv("PASTIME_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 36); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO users(username,display_name,role,password_hash) VALUES('removed_user','Removed','member',''); DELETE FROM users WHERE username='removed_user'"); err != nil {
		t.Fatal(err)
	}
	var account, entry int64
	if err = db.QueryRowContext(ctx, "INSERT INTO users(username,display_name,role,public_profile,password_hash) VALUES('existing_owner','Existing Owner','owner',true,'') RETURNING id").Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "INSERT INTO library_entries(user_id,title,media_type,unit,status,notes) VALUES($1,'Existing title','book','pages','planned','Private note') RETURNING id", account).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO jobs(kind,dedupe_key,payload,replay_policy) VALUES('statistics_warm','warm-old',jsonb_build_object('user_id',$1::bigint,'day','2026-10-09'),'idempotent')`, account); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM change_log").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var profileID, accountID, actualEntry int64
	var public bool
	var note string
	var after int
	if err = db.QueryRowContext(ctx, "SELECT e.profile_id,p.account_id,e.id,p.public_profile,e.notes FROM library_entries e JOIN tracking_profiles p ON p.id=e.profile_id WHERE e.id=$1", entry).Scan(&profileID, &accountID, &actualEntry, &public, &note); err != nil {
		t.Fatal(err)
	}
	if accountID != account || actualEntry != entry || !public || note != "Private note" {
		t.Fatal("migration changed existing library/privacy")
	}
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM change_log").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("migration added %d artificial change events", after-before)
	}
	var jobProfile int64
	if err = db.QueryRowContext(ctx, "SELECT (payload->>'profile_id')::bigint FROM jobs WHERE dedupe_key='warm-old'").Scan(&jobProfile); err != nil {
		t.Fatal(err)
	}
	if jobProfile != profileID {
		t.Fatal("queued job lost profile ownership")
	}
	var extra int64
	if err = db.QueryRowContext(ctx, "INSERT INTO tracking_profiles(account_id,name) VALUES($1,'Child') RETURNING id", account).Scan(&extra); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO library_entries(profile_id,title,media_type,unit,status) VALUES($1,'Existing title','book','pages','planned')", extra); err != nil {
		t.Fatal(err)
	}
	// Composite owner constraints must prohibit assigning another account's profile.
	var other int64
	if err = db.QueryRowContext(ctx, "INSERT INTO users(username,display_name,role,password_hash) VALUES('unrelated_user','Unrelated','member','') RETURNING id").Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO library_entries(user_id,profile_id,title,media_type,unit,status) VALUES($1,$2,'Forbidden','book','pages','planned')", other, profileID); err == nil {
		t.Fatal("cross-account profile insert accepted")
	}
}
