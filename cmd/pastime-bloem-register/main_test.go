package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/Bloem-Studios/pastime/internal/testkit"
)

const destination = "https://tracker.example.test"

type pluginConfig struct {
	URL        string `json:"url"`
	Credential string `json:"provision_credential"`
}

func readConfig(t *testing.T, path string) (pluginConfig, []byte) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		t.Fatal("configuration must be a protected regular file", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg pluginConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil || cfg.URL == "" || !strings.HasPrefix(cfg.Credential, "ptbp_") {
		t.Fatal("invalid plugin configuration")
	}
	return cfg, raw
}

func instances(t *testing.T, f *testkit.Fixture) int {
	t.Helper()
	var n int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_instances`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func env(f *testkit.Fixture) func(string) string {
	values := map[string]string{"DATABASE_URL": f.Pool.Config().ConnString(), "PUBLIC_ORIGIN": destination}
	return func(key string) string { return values[key] }
}

func TestRegistrationUsesCurrentAdministratorAndProtectedOutput(t *testing.T) {
	f := testkit.New(t)
	output := filepath.Join(t.TempDir(), "plugin.json")
	in := bloem.InstanceInput{ID: testkit.Key(t, f), Scope: testkit.Key(t, f), URL: "https://source.example.test"}
	actor, err := sessionActor(t.Context(), f.Pool, f.Owner.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = register(t.Context(), f.Pool, actor, in, destination, output, map[string]any{"authority": "existing_session"}); err != nil {
		t.Fatal(err)
	}
	cfg, raw := readConfig(t, output)
	if cfg.URL != destination {
		t.Fatal("invalid backend destination")
	}
	instance, err := (bloem.Store{Pool: f.Pool}).AuthenticateProvision(t.Context(), cfg.Credential)
	if err != nil || instance.ID != in.ID || instance.Scope != in.Scope {
		t.Fatal("provisioning authority does not match registration")
	}
	var audits int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='bloem.instance.register' AND actor_id=$1 AND target_id=$2 AND metadata->>'authority'='existing_session'`, f.Owner.User.ID, in.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("registration did not retain administrator audit attribution", err)
	}
	if _, err := register(t.Context(), f.Pool, actor, in, cfg.URL, output, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal("must not replace an existing configuration", err)
	}
	after, _ := os.ReadFile(output)
	if string(raw) != string(after) {
		t.Fatal("existing configuration changed")
	}
	// A repeated registration never mints a second credential for the instance.
	again := filepath.Join(t.TempDir(), "again.json")
	if _, err := register(t.Context(), f.Pool, actor, in, destination, again, nil); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatal("duplicate registration accepted", err)
	}
	if _, err := os.Lstat(again); !os.IsNotExist(err) {
		t.Fatal("rejected duplicate published a configuration")
	}
	if _, err := (bloem.Store{Pool: f.Pool}).AuthenticateProvision(t.Context(), cfg.Credential); err != nil {
		t.Fatal("duplicate registration disturbed the existing credential", err)
	}
}

func TestRegistrationRejectsUnavailableAuthorityWithoutPublishing(t *testing.T) {
	for _, variant := range []string{"revoked", "expired", "member", "inactive", "signed_out_everywhere", "unknown_session", "malformed_session", "http_destination", "http_source", "relative_output", "symlink_output"} {
		t.Run(variant, func(t *testing.T) {
			f := testkit.New(t)
			session := f.Owner.CredentialID
			dest := destination
			source := "https://source.example.test"
			dir := t.TempDir()
			output := filepath.Join(dir, "plugin.json")
			switch variant {
			case "revoked":
				_, _ = f.Pool.Exec(t.Context(), `UPDATE sessions SET revoked_at=now() WHERE id=$1`, session)
			case "expired":
				_, _ = f.Pool.Exec(t.Context(), `UPDATE sessions SET expires_at=now()-interval '1 second' WHERE id=$1`, session)
			case "member":
				session = f.User(t, "member", "member").CredentialID
			case "inactive":
				_, _ = f.Pool.Exec(t.Context(), `UPDATE users SET active=false WHERE id=$1`, f.Owner.User.ID)
			case "signed_out_everywhere":
				_, _ = f.Pool.Exec(t.Context(), `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, f.Owner.User.ID)
			case "unknown_session":
				session = "00000000-0000-4000-8000-000000000000"
			case "malformed_session":
				session = "not-a-session"
			case "http_destination":
				dest = "http://tracker.example.test"
			case "http_source":
				source = "http://source.example.test"
			case "relative_output":
				output = "plugin.json"
			case "symlink_output":
				if err := os.Symlink(filepath.Join(dir, "elsewhere.json"), output); err != nil {
					t.Fatal(err)
				}
			}
			in := bloem.InstanceInput{ID: testkit.Key(t, f), Scope: testkit.Key(t, f), URL: source}
			actor, err := sessionActor(t.Context(), f.Pool, session)
			if err == nil {
				_, err = register(t.Context(), f.Pool, actor, in, dest, output, nil)
			}
			if err == nil {
				t.Fatal("unauthorized registration accepted")
			}
			if variant == "symlink_output" {
				if _, statErr := os.Lstat(filepath.Join(dir, "elsewhere.json")); !os.IsNotExist(statErr) {
					t.Fatal("registration followed a symlink")
				}
			} else if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatal("denied registration published a configuration")
			}
			if instances(t, f) != 0 {
				t.Fatal("denied registration published an instance")
			}
		})
	}
}

func TestPublishFailureRollsRegistrationBack(t *testing.T) {
	f := testkit.New(t)
	output := filepath.Join(t.TempDir(), "missing-directory", "plugin.json")
	in := bloem.InstanceInput{ID: testkit.Key(t, f), Scope: "pilot", URL: "https://source.example.test"}
	if _, err := register(t.Context(), f.Pool, f.Owner, in, destination, output, nil); err == nil {
		t.Fatal("unwritable output accepted")
	}
	if instances(t, f) != 0 {
		t.Fatal("registration committed without a durable credential")
	}
	failed := false
	_, err := (bloem.Store{Pool: f.Pool}).RegisterAndPublish(t.Context(), f.Owner, in, nil, func(bloem.Instance, string) error {
		failed = true
		return context.Canceled
	})
	if err == nil || !failed || instances(t, f) != 0 {
		t.Fatal("publish failure did not roll back registration", err)
	}
	var audits int
	if err = f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='bloem.instance.register'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("rolled back registration left an audit event", err)
	}
}

func TestListAdministratorSessionsWithoutCredentials(t *testing.T) {
	f := testkit.New(t)
	member := f.User(t, "member", "member")
	second, secondRaw := f.Login(t, "fixture_owner")
	revoked, revokedRaw := f.Login(t, "fixture_owner")
	if err := f.Accounts.RevokeSession(t.Context(), f.Owner, revoked.CredentialID); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"-list-admin-sessions"}, env(f), strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{f.Owner.CredentialID, second.CredentialID, "fixture_owner", "owner"} {
		if !strings.Contains(out, want) {
			t.Fatalf("listing lacks %q", want)
		}
	}
	for _, secret := range []string{f.OwnerRaw, secondRaw, revokedRaw, hex.EncodeToString(identity.Digest(f.OwnerRaw)), revoked.CredentialID, member.CredentialID} {
		if strings.Contains(out, secret) || strings.Contains(stderr.String(), secret) {
			t.Fatal("listing exposed a credential or a session the service would reject")
		}
	}
}

func TestOperatorSignInRegistersAndRevokesTemporarySession(t *testing.T) {
	f := testkit.New(t)
	output := filepath.Join(t.TempDir(), "plugin.json")
	instance := testkit.Key(t, f)
	activeSessions := func() int {
		var n int
		if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL`, f.Owner.User.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := activeSessions()
	var stdout, stderr bytes.Buffer
	args := []string{"-administrator", "fixture_owner", "-instance", instance, "-scope", "pilot", "-source", "https://lib.example.test", "-output", output}
	if err := run(t.Context(), args, env(f), strings.NewReader(testkit.Password+"\n"), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	cfg, _ := readConfig(t, output)
	if cfg.URL != destination {
		t.Fatal("destination did not default to PUBLIC_ORIGIN")
	}
	for _, stream := range []string{stdout.String(), stderr.String()} {
		if strings.Contains(stream, cfg.Credential) || strings.Contains(stream, testkit.Password) {
			t.Fatal("secret written to an output stream")
		}
	}
	if !strings.Contains(stdout.String(), instance) || !strings.Contains(stdout.String(), output) {
		t.Fatal("success message lacks the instance and configuration path")
	}
	if got := activeSessions(); got != before {
		t.Fatal("temporary operator session survived registration", before, got)
	}
	var audits int
	if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='bloem.instance.register' AND actor_id=$1 AND target_id=$2 AND metadata->>'authority'='operator_sign_in' AND metadata->>'entry_point'='pastime-bloem-register'`, f.Owner.User.ID, instance).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("operator registration audit missing", err)
	}
}

func TestOperatorSignInRefusals(t *testing.T) {
	for _, variant := range []string{"wrong_password", "member", "empty_password", "existing_output"} {
		t.Run(variant, func(t *testing.T) {
			f := testkit.New(t)
			f.User(t, "member", "member")
			output := filepath.Join(t.TempDir(), "plugin.json")
			user, password, want := "fixture_owner", testkit.Password, ""
			switch variant {
			case "wrong_password":
				password, want = "not-the-password", "sign-in failed"
			case "member":
				user, want = "member", "not an active Pastime owner or administrator"
			case "empty_password":
				password, want = "", "no administrator password"
			case "existing_output":
				if err := os.WriteFile(output, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "already exists"
			}
			var stdout, stderr bytes.Buffer
			args := []string{"-administrator", user, "-instance", testkit.Key(t, f), "-scope", "pilot", "-source", "https://lib.example.test", "-output", output}
			err := run(t.Context(), args, env(f), strings.NewReader(password), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("got %v, want %q", err, want)
			}
			if strings.Contains(err.Error(), password) && password != "" || strings.Contains(stdout.String()+stderr.String(), testkit.Password) {
				t.Fatal("password echoed")
			}
			if instances(t, f) != 0 {
				t.Fatal("refused registration published an instance")
			}
			if variant == "existing_output" {
				if data, _ := os.ReadFile(output); string(data) != "keep" {
					t.Fatal("existing output changed")
				}
			} else if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
				t.Fatal("refused registration published a configuration")
			}
			var open int
			if err := f.Pool.QueryRow(t.Context(), `SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.username=$1 AND s.revoked_at IS NULL`, user).Scan(&open); err != nil {
				t.Fatal(err)
			}
			if limit := map[string]int{"member": 1, "fixture_owner": 1}[user]; open != limit {
				t.Fatal("temporary session survived a refused registration", open)
			}
		})
	}
}

func TestArgumentValidation(t *testing.T) {
	base := []string{"-instance", "i", "-scope", "s", "-source", "https://a.example", "-output", "/tmp/x.json"}
	for name, args := range map[string][]string{
		"no authority":             base,
		"both":                     append([]string{"-administrator", "a", "-administrator-session", "00000000-0000-4000-8000-000000000000"}, base...),
		"missing":                  {"-administrator", "a"},
		"list and more":            {"-list-admin-sessions", "-instance", "i"},
		"unclean output":           {"-administrator", "a", "-instance", "i", "-scope", "s", "-source", "https://a.example", "-output", "/tmp/../tmp/x.json"},
		"positional":               append(append([]string{"-administrator", "a"}, base...), "extra"),
		"issuer no instance":       {"-administrator", "a", "-set-identity-issuer", bloemIssuer},
		"issuer with scope":        {"-administrator", "a", "-set-identity-issuer", bloemIssuer, "-instance", "i", "-scope", "s"},
		"issuer no authority":      {"-set-identity-issuer", bloemIssuer, "-instance", "i"},
		"issuer and list":          {"-list-admin-sessions", "-set-identity-issuer", bloemIssuer},
		"two modes":                {"-administrator", "a", "-instance", "i", "-show-identity", "-set-identity-issuer", bloemIssuer},
		"declare without mode":     {"-administrator", "a", "-instance", "i", "-declare-login-issuer", loginIssuer},
		"bad subject mode":         {"-administrator", "a", "-instance", "i", "-declare-login-issuer", loginIssuer, "-subject-mode", "email"},
		"mode without declare":     {"-administrator", "a", "-instance", "i", "-show-identity", "-subject-mode", "user_uuid"},
		"remove with mode":         {"-administrator", "a", "-instance", "i", "-remove-login-issuer", loginIssuer, "-subject-mode", "user_uuid"},
		"alias without new":        {"-administrator", "a", "-alias-login-subject", "-issuer", loginIssuer, "-existing-subject", "x"},
		"alias with instance":      {"-administrator", "a", "-alias-login-subject", "-issuer", loginIssuer, "-existing-subject", "x", "-new-subject", "y", "-instance", "i"},
		"alias flags without mode": {"-administrator", "a", "-instance", "i", "-show-identity", "-issuer", loginIssuer},
		"register issuer flag":     {"-administrator", "a", "-instance", "i", "-set-identity-issuer", bloemIssuer, "-identity-issuer", bloemIssuer},
		"show with output":         {"-administrator", "a", "-instance", "i", "-show-identity", "-output", "/tmp/x.json"},
		"revoke without source":    {"-administrator", "a", "-revoke-claim", "-instance", "i"},
		"revoke without instance":  {"-administrator", "a", "-revoke-claim", "-source-account", "s"},
		"revoke with issuer":       {"-administrator", "a", "-revoke-claim", "-instance", "i", "-source-account", "s", "-issuer", loginIssuer},
		"source without revoke":    {"-administrator", "a", "-show-identity", "-instance", "i", "-source-account", "s"},
		"revoke and show":          {"-administrator", "a", "-revoke-claim", "-show-identity", "-instance", "i", "-source-account", "s"},
	} {
		if _, err := parse(args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, args := range map[string][]string{
		"register with issuer": append([]string{"-administrator", "a", "-identity-issuer", bloemIssuer}, base...),
		"set issuer":           {"-administrator-session", "00000000-0000-4000-8000-000000000000", "-set-identity-issuer", bloemIssuer, "-instance", "i"},
		"show":                 {"-administrator", "a", "-show-identity", "-instance", "i"},
		"declare":              {"-administrator", "a", "-declare-login-issuer", loginIssuer, "-subject-mode", "hashed_user_id", "-instance", "i"},
		"remove":               {"-administrator", "a", "-remove-login-issuer", loginIssuer, "-instance", "i"},
		"alias":                {"-administrator", "a", "-alias-login-subject", "-issuer", loginIssuer, "-existing-subject", "x", "-new-subject", "y"},
		"revoke":               {"-administrator", "a", "-revoke-claim", "-instance", "i", "-source-account", "s"},
	} {
		if _, err := parse(args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

const (
	bloemIssuer = "https://sso.example.test/application/o/bloem/"
	loginIssuer = "https://sso.example.test/application/o/pastime/"
)

func TestRegistrationRecordsIdentityIssuer(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	instance := testkit.Key(t, f)
	base := []string{"-administrator", "fixture_owner", "-instance", instance, "-scope", "pilot", "-source", "https://lib.example.test"}
	var stdout, stderr bytes.Buffer
	bad := append(append([]string{}, base...), "-output", filepath.Join(t.TempDir(), "bad.json"), "-identity-issuer", "http://sso.example.test/")
	if err := run(ctx, bad, env(f), strings.NewReader(testkit.Password), &stdout, &stderr); err == nil || instances(t, f) != 0 {
		t.Fatal("plain HTTP identity issuer accepted", err)
	}
	good := append(append([]string{}, base...), "-output", filepath.Join(t.TempDir(), "plugin.json"), "-identity-issuer", bloemIssuer)
	if err := run(ctx, good, env(f), strings.NewReader(testkit.Password), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var issuer string
	if err := f.Pool.QueryRow(ctx, `SELECT identity_issuer FROM bloem_instances WHERE id=$1`, instance).Scan(&issuer); err != nil || issuer != bloemIssuer {
		t.Fatal("registration identity issuer", issuer, err)
	}
}

func TestIdentityModesUseOperatorAuthority(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := bloem.Store{Pool: f.Pool}
	instance, raw, err := s.Register(ctx, f.Owner, bloem.InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot"})
	if err != nil {
		t.Fatal(err)
	}
	f.User(t, "member", "member")
	values := map[string]string{"DATABASE_URL": f.Pool.Config().ConnString(), "PUBLIC_ORIGIN": destination, "OIDC_ISSUER": loginIssuer}
	getenv := func(key string) string { return values[key] }
	sessions := func() int {
		var n int
		if err := f.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE revoked_at IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	open := sessions()
	command := func(args ...string) (string, error) {
		t.Helper()
		// Every call signs in afresh; reset the operator sign-in rate limit between them.
		if _, err := f.Pool.Exec(ctx, `DELETE FROM rate_buckets`); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := run(ctx, append([]string{"-administrator", "fixture_owner"}, args...), getenv, strings.NewReader(testkit.Password), &stdout, &stderr)
		if strings.Contains(stdout.String()+stderr.String(), testkit.Password) {
			t.Fatal("password echoed")
		}
		if got := sessions(); got != open {
			t.Fatal("temporary operator session survived", got, open)
		}
		return stdout.String(), err
	}
	audited := func(action string) map[string]any {
		t.Helper()
		var data []byte
		if err := f.Pool.QueryRow(ctx, `SELECT metadata FROM audit_events WHERE action=$1 AND actor_id=$2 ORDER BY id DESC LIMIT 1`, action, f.Owner.User.ID).Scan(&data); err != nil {
			t.Fatal(action, err)
		}
		meta := map[string]any{}
		_ = json.Unmarshal(data, &meta)
		if meta["entry_point"] != "pastime-bloem-register" || meta["authority"] != "operator_sign_in" {
			t.Fatal(action, "audit attribution", meta)
		}
		return meta
	}
	var stdout, stderr bytes.Buffer
	if err = run(ctx, []string{"-administrator", "member", "-set-identity-issuer", bloemIssuer, "-instance", instance.ID}, getenv, strings.NewReader(testkit.Password), &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "not an active Pastime owner or administrator") {
		t.Fatal("member set the identity issuer", err)
	}
	// The sign-in issuer must be the configured one; that is checked before signing in.
	if _, err = command("-declare-login-issuer", "https://sso.example.test/application/o/other/", "-subject-mode", "user_uuid", "-instance", instance.ID); err == nil || !strings.Contains(err.Error(), "OIDC_ISSUER") {
		t.Fatal("unconfigured sign-in issuer declared", err)
	}
	if _, err = command("-declare-login-issuer", loginIssuer, "-subject-mode", "user_uuid", "-instance", instance.ID); err == nil || !strings.Contains(err.Error(), "identity issuer") {
		t.Fatal("declared without an instance identity issuer", err)
	}
	out, err := command("-set-identity-issuer", bloemIssuer, "-instance", instance.ID)
	if err != nil || !strings.Contains(out, bloemIssuer) {
		t.Fatal(out, err)
	}
	if meta := audited("bloem.instance.identity_issuer"); meta["old_issuer"] != "" || meta["new_issuer"] != bloemIssuer {
		t.Fatal(meta)
	}
	if out, err = command("-set-identity-issuer", bloemIssuer, "-instance", instance.ID); err != nil || !strings.Contains(out, "unchanged") {
		t.Fatal(out, err)
	}
	if out, err = command("-declare-login-issuer", loginIssuer, "-subject-mode", "user_uuid", "-instance", instance.ID); err != nil || !strings.Contains(out, loginIssuer) {
		t.Fatal(out, err)
	}
	if meta := audited("bloem.instance.login_issuer.declare"); meta["login_issuer"] != loginIssuer || meta["subject_mode"] != "user_uuid" {
		t.Fatal(meta)
	}
	if _, err = command("-set-identity-issuer", "https://sso.example.test/application/o/other/", "-instance", instance.ID); err == nil || !strings.Contains(err.Error(), "cannot change") {
		t.Fatal("issuer changed under a declaration", err)
	}
	const subject = "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	in := bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "summary", AccountName: "Summary", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: bloemIssuer, IdentitySubject: subject}
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	hashed := strings.Repeat("cd", 32)
	if _, err = f.Pool.Exec(ctx, `INSERT INTO oidc_identities(issuer,subject,user_id) VALUES($1,$2,$3)`, loginIssuer, hashed, f.Owner.User.ID); err != nil {
		t.Fatal(err)
	}
	if out, err = command("-show-identity", "-instance", instance.ID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{bloemIssuer, loginIssuer, "user_uuid"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, subject) || strings.Contains(out, hashed) {
		t.Fatal("summary printed a subject")
	}
	const aliased = "4c3d2e1f-0a9b-4c8d-8e7f-6a5b4c3d2e1f"
	if out, err = command("-alias-login-subject", "-issuer", loginIssuer, "-existing-subject", hashed, "-new-subject", aliased); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, hashed) || strings.Contains(out, aliased) || !strings.Contains(out, "fixture_owner") {
		t.Fatal("alias output", out)
	}
	audited("oidc.subject_alias")
	var owner int64
	if err = f.Pool.QueryRow(ctx, `SELECT user_id FROM oidc_identities WHERE issuer=$1 AND subject=$2`, loginIssuer, aliased).Scan(&owner); err != nil || owner != f.Owner.User.ID {
		t.Fatal("alias row", err)
	}
	if _, err = command("-alias-login-subject", "-issuer", loginIssuer, "-existing-subject", "missing", "-new-subject", "other"); err == nil || !strings.Contains(err.Error(), "no sign-in") {
		t.Fatal("alias of a missing sign-in", err)
	}
	if out, err = command("-remove-login-issuer", loginIssuer, "-instance", instance.ID); err != nil || !strings.Contains(out, "Removed") {
		t.Fatal(out, err)
	}
	audited("bloem.instance.login_issuer.remove")
	if _, err = command("-remove-login-issuer", loginIssuer, "-instance", instance.ID); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatal("removed an undeclared issuer", err)
	}
	// An existing administrator session works for read-only inspection too.
	stdout.Reset()
	if err = run(ctx, []string{"-administrator-session", f.Owner.CredentialID, "-show-identity", "-instance", instance.ID}, getenv, strings.NewReader(""), &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), bloemIssuer) {
		t.Fatal(stdout.String(), err)
	}
}

func TestRevokeClaimUsesOperatorAuthority(t *testing.T) {
	f := testkit.New(t)
	ctx := t.Context()
	s := bloem.Store{Pool: f.Pool}
	instance, raw, err := s.Register(ctx, f.Owner, bloem.InstanceInput{ID: testkit.Key(t, f), URL: "https://lib.example.test", Scope: "pilot", IdentityIssuer: bloemIssuer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeclareLoginIssuer(ctx, f.Owner, instance.ID, loginIssuer, bloem.SubjectUserUUID, nil); err != nil {
		t.Fatal(err)
	}
	const subject = "0b6f2a2e-5c1d-4f8e-9a3b-7d2c1e0f4a51"
	in := bloem.ProvisionInput{Version: 1, InstanceID: instance.ID, AccountID: "claimed", AccountName: "Claimed", ProfileID: "main", DefaultProfileID: "main", Name: "Main", AccountRevision: 1, Revision: 1, AccountActive: true, Active: true, IdentityIssuer: bloemIssuer, IdentitySubject: subject}
	if _, err = s.Provision(ctx, raw, in); err != nil {
		t.Fatal(err)
	}
	accounts := identity.New(f.Pool)
	accounts.ManagedClaim = s.ClaimForLogin
	claimer, session, err := accounts.OIDCSignIn(ctx, identity.OIDCClaims{Issuer: loginIssuer, Subject: subject}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	getenv := env(f)
	command := func(args ...string) (string, error) {
		t.Helper()
		if _, err := f.Pool.Exec(ctx, `DELETE FROM rate_buckets`); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		err := run(ctx, append([]string{"-administrator", "fixture_owner"}, args...), getenv, strings.NewReader(testkit.Password), &stdout, &stderr)
		if out := stdout.String() + stderr.String(); strings.Contains(out, subject) || strings.Contains(out, bloemIssuer) || strings.Contains(out, testkit.Password) {
			t.Fatal("revoke output carries identity details or the password", out)
		}
		return stdout.String(), err
	}
	if _, err = command("-revoke-claim", "-instance", instance.ID, "-source-account", "missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatal("revoked a missing account", err)
	}
	out, err := command("-revoke-claim", "-instance", instance.ID, "-source-account", "claimed")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{claimer.Username, "1 sign-in", "pending"} {
		if !strings.Contains(out, want) {
			t.Fatalf("revoke output lacks %q:\n%s", want, out)
		}
	}
	if _, err = accounts.Authenticate(ctx, session, "session"); err == nil {
		t.Fatal("claimer session survived")
	}
	var data []byte
	if err = f.Pool.QueryRow(ctx, `SELECT metadata FROM audit_events WHERE action='bloem.account.unclaim' AND actor_id=$1`, f.Owner.User.ID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{}
	_ = json.Unmarshal(data, &meta)
	if meta["entry_point"] != "pastime-bloem-register" || meta["authority"] != "operator_sign_in" || meta["source_account_id"] != "claimed" {
		t.Fatal("revoke audit attribution", meta)
	}
	if _, err = command("-revoke-claim", "-instance", instance.ID, "-source-account", "claimed"); err == nil || !strings.Contains(err.Error(), "not claimed") {
		t.Fatal("revoked twice", err)
	}
}
