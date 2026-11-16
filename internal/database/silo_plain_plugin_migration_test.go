package database_test

import (
	"database/sql"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/testdb"
	"github.com/Bloem-Studios/pastime/migrations"
	"github.com/pressly/goose/v3"
)

// Schema 57 is additive. Its Down keeps every binding, key and receipt, and its Up
// applies again cleanly, including over a schema where version 57 was un-recorded for
// a binary-only rollback.
func TestSiloPlainPluginMigration(t *testing.T) {
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
	if _, err = provider.DownTo(ctx, 57); err != nil {
		t.Fatal("isolate schema 57", err)
	}
	var user, profile int64
	if err = pool.QueryRow(ctx, `INSERT INTO users(username,display_name,password_hash) VALUES('connect_member','Member','') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM tracking_profiles WHERE account_id=$1 AND is_default`, user).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO bloem_instances(id,url,enrollment_scope,provision_digest) VALUES('instance','https://silo.example.test','pilot',decode(repeat('ab',32),'hex'))`, nil},
		{`INSERT INTO bloem_accounts(instance_id,source_account_id,account_id,default_source_profile_id,source_revision,source_fingerprint,active,link_status,owns_account)
			VALUES('instance','pastime-account-'||$1::bigint,$1::bigint,'pastime-profile-'||$2::bigint,1,decode(repeat('cd',32),'hex'),true,'linked',false)`, []any{user, profile}},
		{`INSERT INTO bloem_profiles(instance_id,source_account_id,source_profile_id,account_id,profile_id,owns_profile,origin) VALUES('instance','pastime-account-'||$1::bigint,'pastime-profile-'||$2::bigint,$1::bigint,$2::bigint,false,'self_service')`, []any{user, profile}},
		{`INSERT INTO bloem_event_receipts(instance_id,profile_id,event_id,payload_digest,result) VALUES('instance',$1,'pevt_repeat',decode(repeat('ef',32),'hex'),'no_change')`, []any{profile}},
	} {
		if _, err = pool.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.DownTo(ctx, 56); err != nil {
		t.Fatal("down", err)
	}
	var bindings, receipts int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bloem_profiles WHERE profile_id=$1),(SELECT count(*) FROM bloem_event_receipts WHERE profile_id=$1 AND result='no_change')`, profile).Scan(&bindings, &receipts); err != nil || bindings != 1 || receipts != 1 {
		t.Fatal("down lost a binding or receipt", bindings, receipts, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bloem_event_receipts(instance_id,profile_id,event_id,payload_digest,result) VALUES('instance',$1,'pevt_new',decode(repeat('ef',32),'hex'),'no_change')`, profile); err == nil {
		t.Fatal("schema 56 accepted a new no_change receipt")
	}
	if _, err = provider.UpTo(ctx, 57); err != nil {
		t.Fatal("up again", err)
	}
	var origin string
	if err = pool.QueryRow(ctx, `SELECT origin FROM bloem_profiles WHERE profile_id=$1`, profile).Scan(&origin); err != nil || origin != "provisioned" {
		t.Fatal("re-added origin", origin, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE bloem_profiles SET origin='self_service' WHERE profile_id=$1`, profile); err != nil {
		t.Fatal(err)
	}
	// A binary-only rollback un-records version 57 and leaves its objects; the next
	// upgrade applies it again over them without changing any row.
	if _, err = pool.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=57`); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal("reapply over an un-recorded version", err)
	}
	if err = pool.QueryRow(ctx, `SELECT origin FROM bloem_profiles WHERE profile_id=$1`, profile).Scan(&origin); err != nil || origin != "self_service" {
		t.Fatal("reapplying changed a binding", origin, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO bloem_event_receipts(instance_id,profile_id,event_id,payload_digest,result) VALUES('instance',$1,'pevt_new',decode(repeat('ef',32),'hex'),'no_change')`, profile); err != nil {
		t.Fatal("schema 57 refused a no_change receipt", err)
	}
}
