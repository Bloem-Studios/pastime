package database_test

import (
	"database/sql"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/Bloem-Studios/pastime/migrations"
	"github.com/pressly/goose/v3"
)

// TestBloemIdentityClaimMigrationDown checks that schema 43 has a working Down
// that keeps managed accounts, their link status and sign-ins, and can be reapplied,
// binding linked accounts to the identity they carry.
func TestBloemIdentityClaimMigrationDown(t *testing.T) {
	pool := testdb.Isolated(t)
	ctx := t.Context()
	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	// This test targets migration 43 even when newer migrations exist.
	if _, err = provider.DownTo(ctx, 43); err != nil {
		t.Fatal("isolate schema 43", err)
	}
	const subject = "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	var user int64
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash) VALUES('claimed_member','Claimed','') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO bloem_instances(id,url,enrollment_scope,identity_issuer,provision_digest) VALUES('instance','https://lib.example.test','pilot','https://sso.example.test/a/',decode(repeat('ab',32),'hex'))`, nil},
		{`INSERT INTO bloem_login_issuers(instance_id,login_issuer,subject_mode) VALUES('instance','https://sso.example.test/b/','user_uuid')`, nil},
		{`INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,identity_issuer,identity_subject,owns_account,claimed_at)
			VALUES('instance','source',$1,'main',1,decode(repeat('cd',32),'hex'),true,'linked','https://sso.example.test/a/',$2,true,clock_timestamp())`, []any{user, subject}},
		{`INSERT INTO oidc_identities(issuer,subject,user_id) VALUES('https://sso.example.test/b/',$2,$1)`, []any{user, subject}},
	} {
		if _, err = pool.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.DownTo(ctx, 42); err != nil {
		t.Fatal("down", err)
	}
	var status string
	var columns, tables, identities int
	if err = pool.QueryRow(ctx, `SELECT link_status FROM bloem_accounts WHERE source_account_id='source'`).Scan(&status); err != nil || status != "linked" {
		t.Fatal("down lost the managed account", status, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='bloem_accounts' AND column_name IN ('claimed_at','bound_issuer','bound_subject')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("down kept claim columns", columns, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='bloem_login_issuers'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("down kept declarations", tables, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM oidc_identities WHERE user_id=$1`, user).Scan(&identities); err != nil || identities != 1 {
		t.Fatal("down removed a sign-in", identities, err)
	}
	// A pending account stays unbound when schema 43 returns.
	if _, err = pool.Exec(ctx, `INSERT INTO users(username,display_name,password_hash) VALUES('pending_member','Pending','')`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,identity_issuer,identity_subject,owns_account)
		SELECT 'instance','pending',id,'main',1,decode(repeat('ef',32),'hex'),true,'pending','https://sso.example.test/a/','4c3d2e1f-0a9b-4c8d-8e7f-6a5b4c3d2e1f',true FROM users WHERE username='pending_member'`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 43); err != nil {
		t.Fatal("up again", err)
	}
	// A linked account is bound to the identity it carries when schema 43 arrives.
	var bound string
	if err = pool.QueryRow(ctx, `SELECT bound_issuer||' '||bound_subject FROM bloem_accounts WHERE source_account_id='source'`).Scan(&bound); err != nil || bound != "https://sso.example.test/a/ "+subject {
		t.Fatal("up did not bind the linked account", bound, err)
	}
	if err = pool.QueryRow(ctx, `SELECT bound_issuer||bound_subject FROM bloem_accounts WHERE source_account_id='pending'`).Scan(&bound); err != nil || bound != "" {
		t.Fatal("up bound a pending account", bound, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_accounts SET bound_subject='' WHERE source_account_id='source'`); err == nil {
		t.Fatal("half-bound identity accepted")
	}
	// Restore the application schema before checking application readiness.
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("restore current schema", err)
	}
	if err = database.CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
}
