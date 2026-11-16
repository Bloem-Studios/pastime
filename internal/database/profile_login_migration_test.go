package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// profileLoginFixture creates a synthetic account with one extra profile and
// returns the account ID and that profile's ID.
func profileLoginFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string) (int64, int64) {
	t.Helper()
	var account, profile int64
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash) VALUES($1,'Fixture Owner','') RETURNING id`, username).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO tracking_profiles(account_id,name) VALUES($1,'Sam') RETURNING id`, account).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	return account, profile
}

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func TestLoginNameCannotMatchUsername(t *testing.T) {
	pool := testdb.Isolated(t)
	ctx := t.Context()
	account, profile := profileLoginFixture(t, ctx, pool, "fixture_owner")
	_, err := pool.Exec(ctx, `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,'fixture_owner','synthetic-hash')`, account, profile)
	if pgCode(err) != "23505" {
		t.Fatalf("login named after an account username: got %v, want 23505", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,'sam','synthetic-hash')`, account, profile); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash) VALUES('sam','Another Sam','')`)
	if pgCode(err) != "23505" {
		t.Fatalf("username taken by a profile login: got %v, want 23505", err)
	}
}

func TestRemovingLoginRemovesItsSessions(t *testing.T) {
	pool := testdb.Isolated(t)
	ctx := t.Context()
	account, profile := profileLoginFixture(t, ctx, pool, "fixture_owner")
	var login int64
	if err := pool.QueryRow(ctx, `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,'sam','synthetic-hash') RETURNING id`, account, profile).Scan(&login); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,auth_epoch,expires_at,profile_login_id) VALUES($1,sha256('synthetic-session'::bytea),1,now()+interval '1 day',$2)`, account, login); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM profile_logins WHERE id=$1`, login); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, account).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("sessions after removing the login: %d, want 0", left)
	}
}

func TestLoginFollowsProfileAccount(t *testing.T) {
	pool := testdb.Isolated(t)
	ctx := t.Context()
	_, profile := profileLoginFixture(t, ctx, pool, "fixture_owner")
	other, _ := profileLoginFixture(t, ctx, pool, "other_owner")
	_, err := pool.Exec(ctx, `INSERT INTO profile_logins(account_id,profile_id,login_name,password_hash) VALUES($1,$2,'sam','synthetic-hash')`, other, profile)
	if pgCode(err) != "23503" {
		t.Fatalf("login for another account's profile: got %v, want 23503", err)
	}
}
