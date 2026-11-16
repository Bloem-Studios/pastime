// Command pastime-bloem-register is the protected root operator entry point for
// native Bloem registration. Administrator authority comes from the service's own
// session checks, never from an email address or a direct row write. The new
// provisioning credential is written only to a new mode-0600 file; it never
// appears on standard output or standard error.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Bloem-Studios/pastime/internal/config"
	"github.com/Bloem-Studios/pastime/internal/connections/bloem"
	"github.com/Bloem-Studios/pastime/internal/database"
	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const usage = `usage:
  pastime-bloem-register -list-admin-sessions
  pastime-bloem-register AUTHORITY -instance ID -scope SCOPE -source https://bloem.example -output /absolute/new-file.json [-destination https://pastime.example] [-identity-issuer https://sso.example/app/]
  pastime-bloem-register AUTHORITY -set-identity-issuer https://sso.example/app/ -instance ID
  pastime-bloem-register AUTHORITY -show-identity -instance ID
  pastime-bloem-register AUTHORITY -declare-login-issuer https://sso.example/other/ -subject-mode user_uuid|hashed_user_id -instance ID
  pastime-bloem-register AUTHORITY -remove-login-issuer https://sso.example/other/ -instance ID
  pastime-bloem-register AUTHORITY -alias-login-subject -issuer https://sso.example/other/ -existing-subject OLD -new-subject NEW
  pastime-bloem-register AUTHORITY -revoke-claim -instance ID -source-account ID

AUTHORITY is -administrator USERNAME or -administrator-session SESSION_ID.

Run as root with the Pastime runtime environment loaded (DATABASE_URL and
PUBLIC_ORIGIN). -administrator signs in as that owner or administrator with the
password read from standard input (a pipe, never a terminal) and revokes the
temporary session afterwards. -administrator-session uses a current session
chosen from -list-admin-sessions. -destination defaults to PUBLIC_ORIGIN.
The output file receives {"url","provision_credential"} for the Silo plugin's
BLOEM_PASTIME_CONFIG_FILE and is never overwritten.

-set-identity-issuer sets the issuer whose verified identities the Silo instance
may send. -declare-login-issuer declares that this service's sign-in issuer
(OIDC_ISSUER) names the same people with subjects of the given mode; it allows
managed accounts to be claimed by signing in. -alias-login-subject adds a second
subject to an existing sign-in before the provider changes its subject mode.
-revoke-claim undoes a sign-in claim of one managed account: the claimer loses
every sign-in, password, session and integration of that account, which keeps its
tracked history and becomes claimable again. Correct the Silo link first.
-show-identity prints counts only, never subjects.`

// failure is an operator-facing message. It never carries credential material.
type failure string

func (f failure) Error() string { return string(f) }
func fail(format string, args ...any) error {
	return failure(fmt.Sprintf(format, args...))
}

// Modes. Registration is the default when no other mode flag is given.
const (
	modeRegister  = "register"
	modeList      = "list-admin-sessions"
	modeSetIssuer = "set-identity-issuer"
	modeShow      = "show-identity"
	modeDeclare   = "declare-login-issuer"
	modeRemove    = "remove-login-issuer"
	modeAlias     = "alias-login-subject"
	modeRevoke    = "revoke-claim"
)

// modeFlags lists the required and optional flags of each mode, besides the mode
// flag itself and the authority flags.
var modeFlags = map[string]struct{ required, optional []string }{
	modeRegister:  {[]string{"instance", "scope", "source", "output"}, []string{"destination", "identity-issuer"}},
	modeSetIssuer: {[]string{"instance"}, nil},
	modeShow:      {[]string{"instance"}, nil},
	modeDeclare:   {[]string{"instance", "subject-mode"}, nil},
	modeRemove:    {[]string{"instance"}, nil},
	modeAlias:     {[]string{"issuer", "existing-subject", "new-subject"}, nil},
	modeRevoke:    {[]string{"instance", "source-account"}, nil},
	modeList:      {},
}

type options struct {
	mode            string
	target          string // the URL argument of -set-identity-issuer, -declare-login-issuer or -remove-login-issuer
	session         string
	administrator   string
	instance        string
	scope           string
	source          string
	destination     string
	output          string
	identityIssuer  string
	subjectMode     string
	issuer          string
	existingSubject string
	newSubject      string
	sourceAccount   string
}

func parse(args []string) (options, error) {
	var o options
	var list, show, alias, revoke bool
	var setIssuer, declare, remove string
	flags := flag.NewFlagSet("pastime-bloem-register", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&list, modeList, false, "list current administrator sessions without credentials")
	flags.StringVar(&setIssuer, modeSetIssuer, "", "set the instance identity issuer")
	flags.BoolVar(&show, modeShow, false, "print identity counts for the instance")
	flags.StringVar(&declare, modeDeclare, "", "declare this service's sign-in issuer equivalent to the instance identity issuer")
	flags.StringVar(&remove, modeRemove, "", "remove a declared sign-in issuer")
	flags.BoolVar(&alias, modeAlias, false, "add a second subject to an existing sign-in")
	flags.BoolVar(&revoke, modeRevoke, false, "revoke the sign-in claim of a managed account")
	flags.StringVar(&o.session, "administrator-session", "", "current administrator session ID")
	flags.StringVar(&o.administrator, "administrator", "", "administrator username; password on standard input")
	flags.StringVar(&o.instance, "instance", "", "Silo managed instance ID")
	flags.StringVar(&o.scope, "scope", "", "Silo enrollment scope")
	flags.StringVar(&o.source, "source", "", "Silo HTTPS origin")
	flags.StringVar(&o.destination, "destination", "", "Pastime HTTPS origin written for the plugin")
	flags.StringVar(&o.output, "output", "", "new absolute path for the plugin configuration")
	flags.StringVar(&o.identityIssuer, "identity-issuer", "", "issuer of the identities the Silo instance sends")
	flags.StringVar(&o.subjectMode, "subject-mode", "", "user_uuid or hashed_user_id")
	flags.StringVar(&o.issuer, "issuer", "", "sign-in issuer of the subject to alias")
	flags.StringVar(&o.existingSubject, "existing-subject", "", "subject that signs in today")
	flags.StringVar(&o.newSubject, "new-subject", "", "subject to add for the same account")
	flags.StringVar(&o.sourceAccount, "source-account", "", "Silo source account ID of the managed account")
	if err := flags.Parse(args); err != nil {
		return o, fail("%v\n%s", err, usage)
	}
	if flags.NArg() != 0 {
		return o, fail("unexpected argument %q\n%s", flags.Arg(0), usage)
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	o.mode = modeRegister
	chosen := 0
	for _, m := range []struct {
		name string
		on   bool
	}{{modeList, list}, {modeSetIssuer, set[modeSetIssuer]}, {modeShow, show}, {modeDeclare, set[modeDeclare]}, {modeRemove, set[modeRemove]}, {modeAlias, alias}, {modeRevoke, revoke}} {
		if m.on {
			chosen++
			o.mode = m.name
		}
	}
	if chosen > 1 {
		return o, fail("choose at most one of -list-admin-sessions, -set-identity-issuer, -show-identity, -declare-login-issuer, -remove-login-issuer, -alias-login-subject or -revoke-claim\n%s", usage)
	}
	o.target = map[string]string{modeSetIssuer: setIssuer, modeDeclare: declare, modeRemove: remove}[o.mode]
	accepted := modeFlags[o.mode]
	for name := range set {
		if name == o.mode || o.mode != modeList && (name == "administrator" || name == "administrator-session") || slices.Contains(accepted.required, name) || slices.Contains(accepted.optional, name) {
			continue
		}
		if o.mode == modeList {
			return o, fail("-list-admin-sessions takes no other flags\n%s", usage)
		}
		return o, fail("-%s cannot be used with this mode\n%s", name, usage)
	}
	if o.mode == modeList {
		return o, nil
	}
	if (o.session == "") == (o.administrator == "") {
		return o, fail("choose exactly one of -administrator or -administrator-session\n%s", usage)
	}
	values := map[string]string{"instance": o.instance, "scope": o.scope, "source": o.source, "output": o.output, "subject-mode": o.subjectMode,
		"issuer": o.issuer, "existing-subject": o.existingSubject, "new-subject": o.newSubject, "source-account": o.sourceAccount}
	var missing []string
	if o.mode != modeRegister && o.mode != modeShow && o.mode != modeAlias && o.mode != modeRevoke && o.target == "" {
		missing = append(missing, "-"+o.mode+" value")
	}
	for _, name := range accepted.required {
		if values[name] == "" {
			missing = append(missing, "-"+name)
		}
	}
	if len(missing) > 0 {
		return o, fail("missing %s\n%s", strings.Join(missing, ", "), usage)
	}
	if o.mode == modeRegister && (!filepath.IsAbs(o.output) || filepath.Clean(o.output) != o.output) {
		return o, fail("-output must be a clean absolute path")
	}
	if o.mode == modeDeclare && o.subjectMode != bloem.SubjectUserUUID && o.subjectMode != bloem.SubjectHashedUserID {
		return o, fail("-subject-mode must be %s or %s", bloem.SubjectUserUUID, bloem.SubjectHashedUserID)
	}
	return o, nil
}

// httpsOrigin accepts only a bare HTTPS origin, unlike the service's loopback
// development allowance: the plugin rejects plain HTTP outside development.
func httpsOrigin(flagName, raw string) (string, error) {
	origin, err := config.NormalizeOrigin(raw)
	if err != nil || !strings.HasPrefix(origin, "https://") {
		return "", fail("%s must be an HTTPS origin without a path, query or credentials", flagName)
	}
	return origin, nil
}

// listSessions shows the sessions the service would currently accept for an
// administrator. Token digests are never selected. Registration rechecks
// authority itself, so this list is only a guide for choosing a session.
func listSessions(ctx context.Context, pool *pgxpool.Pool, out io.Writer) error {
	rows, err := pool.Query(ctx, `SELECT s.id::text,u.username,u.role,s.created_at,s.expires_at,u.last_login_at
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND s.auth_epoch=u.auth_epoch AND u.active AND u.role IN ('owner','admin')
		ORDER BY s.created_at DESC,s.id LIMIT 100`)
	if err != nil {
		return explain(err)
	}
	defer rows.Close()
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tADMINISTRATOR\tROLE\tCREATED (UTC)\tEXPIRES (UTC)\tLAST SIGN-IN (UTC)")
	count := 0
	for rows.Next() {
		var id, username, role string
		var created, expires time.Time
		var last *time.Time
		if err = rows.Scan(&id, &username, &role, &created, &expires, &last); err != nil {
			return explain(err)
		}
		lastText := "-"
		if last != nil {
			lastText = last.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, username, role, created.UTC().Format(time.RFC3339), expires.UTC().Format(time.RFC3339), lastText)
		count++
	}
	if err = rows.Err(); err != nil {
		return explain(err)
	}
	if err = w.Flush(); err != nil {
		return err
	}
	if count == 0 {
		_, err = fmt.Fprintln(out, "No current administrator sessions. Use -administrator USERNAME with the password on standard input.")
	}
	return err
}

// sessionActor turns an operator-selected session ID into a session actor. The
// stored digest is only a lookup key: RegisterAndPublish re-authenticates it with
// the service's revocation, expiry, account-state, sign-out-everywhere and role
// checks while holding the same locks as an HTTP request.
func sessionActor(ctx context.Context, pool *pgxpool.Pool, id string) (identity.Actor, error) {
	if !identity.ValidUUID(id) {
		return identity.Actor{}, fail("-administrator-session must be a session ID from -list-admin-sessions")
	}
	a := identity.Actor{Kind: "session"}
	err := pool.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE id=$1`, id).Scan(&a.Hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, fail("administrator session %s does not exist; choose one from -list-admin-sessions", id)
	}
	if err != nil {
		return a, explain(err)
	}
	return a, nil
}

// signIn creates a dedicated session through the service's normal password
// sign-in. The caller revokes it when registration ends.
func signIn(ctx context.Context, accounts *identity.Store, username string, stdin io.Reader) (identity.Actor, func() error, error) {
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return identity.Actor{}, nil, fail("pipe the administrator password on standard input; a terminal would echo it")
		}
	}
	data, err := io.ReadAll(io.LimitReader(stdin, 1025))
	if err != nil || len(data) > 1024 {
		return identity.Actor{}, nil, fail("read a bounded administrator password from standard input")
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if password == "" {
		return identity.Actor{}, nil, fail("no administrator password on standard input")
	}
	if err = accounts.Limit(ctx, "operator-bloem-register:"+strings.ToLower(strings.TrimSpace(username)), 5, time.Minute); err != nil {
		if errors.Is(err, fault.Limited) {
			return identity.Actor{}, nil, fail("too many sign-in attempts for %s; wait a minute and retry", username)
		}
		return identity.Actor{}, nil, explain(err)
	}
	_, raw, err := accounts.Login(ctx, username, password)
	if errors.Is(err, fault.Unauthenticated) {
		return identity.Actor{}, nil, fail("administrator sign-in failed: check the username and password; the account must be active and have a password")
	}
	if err != nil {
		return identity.Actor{}, nil, explain(err)
	}
	a, err := accounts.Authenticate(ctx, raw, "session")
	if err != nil {
		return identity.Actor{}, nil, explain(err)
	}
	revoke := func() error {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return accounts.Logout(c, a)
	}
	return a, revoke, nil
}

func writeConfig(file *os.File, destination, credential string) error {
	if err := json.NewEncoder(file).Encode(struct {
		URL        string `json:"url"`
		Credential string `json:"provision_credential"`
	}{destination, credential}); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(file.Name()))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// register writes the credential durably before the registration commits, so no
// committed instance can exist without its only provisioning credential.
func register(ctx context.Context, pool *pgxpool.Pool, actor identity.Actor, in bloem.InstanceInput, destination, output string, audit map[string]any) (bloem.Instance, error) {
	if !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return bloem.Instance{}, fail("-output must be a clean absolute path")
	}
	origin, err := httpsOrigin("-destination", destination)
	if err != nil {
		return bloem.Instance{}, err
	}
	if _, err = httpsOrigin("-source", in.URL); err != nil {
		return bloem.Instance{}, err
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return bloem.Instance{}, fail("output file %s already exists; registration never overwrites a configuration", output)
	}
	if err != nil {
		return bloem.Instance{}, fail("cannot create output file: %v", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(output)
		}
	}()
	store := bloem.Store{Pool: pool}
	var credential string
	instance, err := store.RegisterAndPublish(ctx, actor, in, audit, func(_ bloem.Instance, raw string) error {
		credential = raw
		if err := writeConfig(file, origin, raw); err != nil {
			return fail("cannot write output file: %v", err)
		}
		return nil
	})
	if err == nil {
		keep = true
		return instance, nil
	}
	if credential == "" {
		return bloem.Instance{}, explain(err)
	}
	// The credential reached publish, so the error came from writing it or from
	// the commit. A failed commit may still have been applied, so ask the service
	// which outcome happened before deciding whether to keep the file.
	check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	current, checkErr := store.AuthenticateProvision(check, credential)
	switch {
	case checkErr == nil && current.ID == in.ID:
		keep = true
		return current, nil
	case errors.Is(checkErr, fault.Unauthenticated):
		return bloem.Instance{}, explain(err)
	default:
		keep = true
		return bloem.Instance{}, fail("registration outcome is unknown and %s was kept; check for audit event bloem.instance.register on instance %s before reusing or deleting it", output, in.ID)
	}
}

// explain converts service errors to operator messages without database values.
func explain(err error) error {
	var f failure
	if errors.As(err, &f) {
		return err
	}
	var public *fault.Error
	var pg *pgconn.PgError
	switch {
	case errors.Is(err, fault.Unauthenticated):
		return fail("administrator authority is not current: the session is revoked or expired, the account is inactive, or it was signed out everywhere")
	case errors.Is(err, fault.Forbidden):
		return fail("the selected account is not an active Pastime owner or administrator")
	case errors.Is(err, fault.Conflict):
		return fail("this Silo instance is already registered; nothing was changed and the existing provisioning credential stays valid")
	case errors.Is(err, fault.NotFound):
		return fail("the Silo instance is not registered in Pastime")
	case errors.Is(err, fault.Unavailable):
		return fail("the Pastime database is unavailable")
	case errors.Is(err, context.DeadlineExceeded):
		return fail("timed out waiting for the Pastime database")
	case errors.As(err, &public) && public.Status >= 400 && public.Status < 500:
		return fail("%s", public.Message)
	case errors.As(err, &pg):
		return fail("database operation failed (SQLSTATE %s)", pg.Code)
	default:
		return fail("the operation failed; check database connectivity and the runtime environment")
	}
}

// authority returns the administrator actor for a mode and a cleanup that revokes
// the temporary sign-in session, if one was created.
func authority(ctx context.Context, pool *pgxpool.Pool, o options, stdin io.Reader, stderr io.Writer) (identity.Actor, map[string]any, func(), error) {
	audit := map[string]any{"entry_point": "pastime-bloem-register"}
	if o.session != "" {
		actor, err := sessionActor(ctx, pool, o.session)
		audit["authority"] = "existing_session"
		return actor, audit, func() {}, err
	}
	actor, revoke, err := signIn(ctx, identity.New(pool), o.administrator, stdin)
	if err != nil {
		return actor, nil, func() {}, err
	}
	audit["authority"] = "operator_sign_in"
	return actor, audit, func() {
		if revoke() != nil {
			fmt.Fprintf(stderr, "warning: could not revoke temporary session %s; revoke it from the administrator's sessions\n", actor.CredentialID)
		}
	}, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) error {
	o, err := parse(args)
	if err != nil {
		return err
	}
	c, err := config.Load(getenv)
	if err != nil {
		return fail("runtime environment: %v", err)
	}
	pool, err := database.Open(ctx, c.DatabaseURL, 2)
	if err != nil {
		return fail("cannot open the Pastime database; check DATABASE_URL")
	}
	defer pool.Close()
	if err = database.CheckSchema(ctx, pool); err != nil {
		return fail("%v; run this command from the same release as the service", err)
	}
	if o.mode == modeList {
		return listSessions(ctx, pool, stdout)
	}
	// Checks that need no authority fail before signing in.
	switch o.mode {
	case modeRegister:
		if o.destination == "" {
			o.destination = c.PublicOrigin
		}
		if _, err = httpsOrigin("-destination (or PUBLIC_ORIGIN)", o.destination); err != nil {
			return err
		}
		if _, err = httpsOrigin("-source", o.source); err != nil {
			return err
		}
		// Fail before signing in; register still relies on O_EXCL, not this check.
		if _, err = os.Lstat(o.output); err == nil {
			return fail("output file %s already exists; registration never overwrites a configuration", o.output)
		}
	case modeDeclare:
		// Claims compare the verified token's issuer, which is always the configured one.
		if c.OIDCIssuer == "" || o.target != c.OIDCIssuer {
			return fail("-declare-login-issuer must equal this service's configured OIDC_ISSUER")
		}
	}
	actor, audit, done, err := authority(ctx, pool, o, stdin, stderr)
	if err != nil {
		return err
	}
	defer done()
	store := bloem.Store{Pool: pool}
	switch o.mode {
	case modeSetIssuer:
		previous, changed, err := store.SetIdentityIssuer(ctx, actor, o.instance, o.target, audit)
		switch {
		case err != nil:
			return explain(err)
		case !changed:
			_, err = fmt.Fprintf(stdout, "The identity issuer of Silo instance %s is unchanged (%s).\n", o.instance, previous)
		case previous == "":
			_, err = fmt.Fprintf(stdout, "Set the identity issuer of Silo instance %s to %s.\n", o.instance, o.target)
		default:
			_, err = fmt.Fprintf(stdout, "Changed the identity issuer of Silo instance %s from %s to %s.\n", o.instance, previous, o.target)
		}
		return err
	case modeShow:
		summary, err := store.IdentitySummary(ctx, actor, o.instance)
		if err != nil {
			return explain(err)
		}
		return printSummary(stdout, summary)
	case modeDeclare:
		changed, err := store.DeclareLoginIssuer(ctx, actor, o.instance, o.target, o.subjectMode, audit)
		switch {
		case errors.Is(err, fault.Conflict):
			return fail("the sign-in issuer changed concurrently; nothing was changed, check -show-identity")
		case err != nil:
			return explain(err)
		case !changed:
			_, err = fmt.Fprintf(stdout, "Sign-in issuer %s is already declared for Silo instance %s with subject mode %s.\n", o.target, o.instance, o.subjectMode)
		default:
			_, err = fmt.Fprintf(stdout, "Declared sign-in issuer %s (subject mode %s) for Silo instance %s. Managed accounts can now be claimed by signing in.\n", o.target, o.subjectMode, o.instance)
		}
		return err
	case modeRemove:
		err := store.RemoveLoginIssuer(ctx, actor, o.instance, o.target, audit)
		if errors.Is(err, fault.NotFound) {
			return fail("sign-in issuer %s is not declared for Silo instance %s, or the instance is not registered; nothing was changed", o.target, o.instance)
		}
		if err != nil {
			return explain(err)
		}
		_, err = fmt.Fprintf(stdout, "Removed sign-in issuer %s from Silo instance %s. New claims through it stop; existing claims and sign-ins stay.\n", o.target, o.instance)
		return err
	case modeAlias:
		u, err := identity.New(pool).AliasOIDCSubject(ctx, actor, o.issuer, o.existingSubject, o.newSubject, audit)
		if errors.Is(err, fault.NotFound) {
			return fail("no sign-in exists for that issuer and existing subject; nothing was changed")
		}
		if err != nil {
			return explain(err)
		}
		_, err = fmt.Fprintf(stdout, "Added a second sign-in subject under %s to account %s; the existing subject keeps working.\n", o.issuer, u.Username)
		return err
	case modeRevoke:
		r, err := store.RevokeClaim(ctx, actor, o.instance, o.sourceAccount, audit)
		if errors.Is(err, fault.NotFound) {
			return fail("managed account %s is not registered for Silo instance %s; nothing was changed", o.sourceAccount, o.instance)
		}
		if err != nil {
			return explain(err)
		}
		_, err = fmt.Fprintf(stdout, "Revoked the claim of managed account %s on Silo instance %s. Account %s keeps its tracked history and is pending again; removed %d sign-in(s), password %t, authenticator %t, public profile %t, %d integration(s); its sessions and tokens are revoked. The next account revision attaches the identity the Silo host then holds.\n",
			o.sourceAccount, o.instance, r.Username, r.SignIns, r.Password, r.Authenticator, r.PublicProfile, r.Integrations)
		return err
	}
	in := bloem.InstanceInput{ID: o.instance, URL: o.source, Scope: o.scope, IdentityIssuer: o.identityIssuer}
	instance, err := register(ctx, pool, actor, in, o.destination, o.output, audit)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Registered Silo instance %s (scope %s, source %s). Provisioning credential written to %s.\n", instance.ID, instance.Scope, instance.URL, o.output)
	return err
}

// printSummary writes counts and issuer URLs only; subjects never reach the output.
func printSummary(out io.Writer, s bloem.IdentitySummary) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	state := "active"
	if !s.Active {
		state = "inactive"
	}
	fmt.Fprintf(w, "Silo instance\t%s (%s)\n", s.InstanceID, state)
	if s.Issuer == "" {
		fmt.Fprintln(w, "Identity issuer\tnot set")
	} else {
		fmt.Fprintf(w, "Identity issuer\t%s (%d Pastime sign-ins)\n", s.Issuer, s.IssuerSignIns)
	}
	if len(s.LoginIssuers) == 0 {
		fmt.Fprintln(w, "Declared sign-in issuers\tnone")
	}
	for _, l := range s.LoginIssuers {
		fmt.Fprintf(w, "Declared sign-in issuer\t%s (subject mode %s, %d Pastime sign-ins, %d of that shape, declared %s)\n", l.Issuer, l.SubjectMode, l.SignIns, l.SignInsMatchingMode, l.CreatedAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(w, "Managed accounts\t%d (%d inactive)\n", s.Accounts, s.Inactive)
	fmt.Fprintf(w, "With a verified identity\t%d\n", s.WithIdentity)
	fmt.Fprintf(w, "Claimable by signing in\t%d (%d of them already sign in to a Pastime account, which takes precedence)\n", s.Claimable, s.AlreadySignsIn)
	fmt.Fprintf(w, "Claimed by signing in\t%d\n", s.Claimed)
	fmt.Fprintf(w, "Linked to an existing member\t%d\n", s.LinkedAtSetup)
	fmt.Fprintf(w, "Claimed or linked, identity since cleared in Silo\t%d (review each; -revoke-claim undoes a mistaken claim)\n", s.ClearedAfterLink)
	return w.Flush()
}

func main() {
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "pastime-bloem-register must run as root")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pastime-bloem-register:", err)
		os.Exit(1)
	}
}
