import { PasswordInput } from './formControls';
import { useState } from 'react';
import { Link, Navigate, useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { KeyRound, Lock } from 'lucide-react';
import { useAuthMethods } from '../authMethods';
import { BrandMark } from '../brand';
import { api, unwrap } from '../api/client';
import { useAuth } from '../state';
import { Empty, ErrorNote, Failed, Field, Loading, submit, useAction, useDocumentTitle } from '../ui';
import { t } from '../i18n';

// Capture before any request and remove the bearer secret from history/address-bar state.
const capturedInvitation = window.location.pathname === '/join' ? window.location.hash.slice(1) : '';
if (window.location.pathname === '/join' && window.location.hash) window.history.replaceState(window.history.state, '', window.location.pathname + window.location.search);
const origin = () => ({ header: { Origin: window.location.origin } });
function Brand() { return <Link className="brand" to="/login"><span className="brand-mark"><BrandMark /></span><span>{'Pastime'}</span></Link>; }
export function Login() {
  const auth = useAuth(); const navigate = useNavigate(); const location = useLocation();
  const [username, setUsername] = useState(''); const [password, setPassword] = useState('');
  const action = useAction(async (value: { username: string; password: string }) => {
    const session = unwrap(await api.POST('/api/v1/login', { params: origin(), body: value }));
    setPassword(''); await auth.establish(session);
    const target = (location.state as { from?: unknown } | null)?.from;
    navigate(typeof target === 'string' && target.startsWith('/') && !target.startsWith('//') && !target.startsWith('/login') && !target.startsWith('/join') ? target : '/', { replace: true });
  });
  const methods = useAuthMethods();
  const [params] = useSearchParams();
  const returnTo = (() => { const t = (location.state as { from?: unknown } | null)?.from; return typeof t === 'string' && t.startsWith('/') && !t.startsWith('//') ? t : '/'; })();
  const ssoError = ({ sign_in_expired: t('That sign-in expired. Please try again.'), identity_unlinked: t('That account is not linked to Pastime yet. Sign in with your password, then link it under Account → Security.'), identity_linked: t('That account is already linked to someone else.'), identity_claim_conflict: t('This sign-in matches a managed Pastime account that cannot be claimed automatically. Ask your administrator for help.'), sso_unavailable: t('Single sign-on is not available right now.'), sso_denied: t('Sign-in was cancelled.'), sso_failed: t('Single sign-on failed. Please try again.'), service_access_required: t('An active service subscription is required to use Pastime.'), unauthenticated: t('This account is disabled.') } as Record<string, string>)[params.get('error') ?? ''];
  useDocumentTitle(t('Sign in'));
  if (auth.loading) return <main id="main" className="auth-loading"><Loading label={t('Checking your session')} /></main>;
  if (auth.session) return <Navigate to="/" replace />;
  return <div className="auth-page"><div className="auth-panel"><header><Brand /></header><main id="main" className="auth-form"><h1 tabIndex={-1}>{t('Sign in to Pastime')}</h1><p>{t('Every film, series, book, game and album you love, and where you left off.')}</p>{ssoError && <div className="error-note" role="alert">{ssoError}</div>}
      {methods.data?.oidc.enabled && <><a className="button full-width sso-button" href={`/auth/oidc/start?return_to=${encodeURIComponent(returnTo)}`}><KeyRound aria-hidden />{t('Continue with {name}', { name: methods.data.oidc.name })}</a><p className="subtle">{t('Your Authentik or other identity-provider password is managed by that provider.')}</p><p className="divider"><span>{t('or')}</span></p><p className="subtle">{t('Sign in below with your local Pastime username and password.')}</p></>}<form onSubmit={submit(() => void action.run({ username: username.trim(), password }))}><fieldset disabled={action.pending}><Field label={t('Username')}>{id => <input id={id} autoComplete="username" autoCapitalize="none" spellCheck={false} required maxLength={254} value={username} onChange={e => setUsername(e.target.value)} />}</Field><Field label={t('Password')}>{id => <PasswordInput id={id} type="password" autoComplete="current-password" required maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field></fieldset><ErrorNote error={action.error || auth.error} /><button className="button primary full-width" type="submit" disabled={action.pending}>{action.pending ? t('Signing in…') : t('Sign in')}</button></form>{methods.data?.recovery && <p className="auth-help"><Link className="text-link" to="/recover">{t('Forgot your local Pastime password?')}</Link></p>}<p className="auth-help">{methods.data?.registration ? <Link className="text-link" to="/register">{t('Create account')}</Link> : t('Need an account? Ask your server administrator for an invitation.')}</p><div className="privacy-note"><Lock size={14} aria-hidden /><span>{t('Your library and notes stay private.')}</span></div></main><footer><a href="/docs/licenses.txt">{t('Third-party notices')}</a></footer></div></div>;
}
export function Register() {
  const auth = useAuth(); const methods = useAuthMethods(); const navigate = useNavigate();
  const [username, setUsername] = useState(''); const [displayName, setDisplayName] = useState('');
  const [email, setEmail] = useState(''); const [password, setPassword] = useState('');
  const action = useAction(async (_: void) => {
    const session = unwrap(await api.POST('/api/v1/register', { params: origin(), body: { username: username.trim(), display_name: displayName.trim(), email: email.trim(), password } }));
    setPassword(''); await auth.establish(session); navigate('/', { replace: true });
  });
  useDocumentTitle(t('Create account'));
  if (auth.loading || methods.isPending) return <main id="main" className="auth-loading"><Loading label={t('Checking your session')} /></main>;
  if (auth.session) return <Navigate to="/" replace />;
  return <div className="auth-page"><div className="auth-panel"><header><Brand /></header><main id="main" className="auth-form">
    {methods.error ? <><h1 tabIndex={-1}>{t('Create account')}</h1><Failed error={methods.error} /></> : !methods.data?.registration ? <Empty heading="h1" title={t('Accounts are by invitation')} action={<Link className="button" to="/login">{t('Go to sign in')}</Link>}>{t('Ask your server administrator for an invitation, then open its link.')}</Empty> : <>
      <h1 tabIndex={-1}>{t('Create account')}</h1><p>{t('A private place for everything you watch, read, play and hear.')}</p>
      <form onSubmit={submit(() => void action.run())}><fieldset disabled={action.pending}>
        <Field label={t('Username')} hint={t('3–48 letters, numbers, dots, underscores or hyphens.')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="username" autoCapitalize="none" spellCheck={false} required minLength={3} maxLength={48} pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,47}" value={username} onChange={e => setUsername(e.target.value)} />}</Field>
        <Field label={t('Display name')} hint={t('Optional')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="nickname" maxLength={80} value={displayName} onChange={e => setDisplayName(e.target.value)} />}</Field>
        <Field label={t('Email')}>{id => <input id={id} type="email" autoComplete="email" autoCapitalize="none" spellCheck={false} required maxLength={254} value={email} onChange={e => setEmail(e.target.value)} />}</Field>
        <Field label={t('Password')} hint={t('Use 12–128 characters. A long, unique passphrase works well.')}>{(id, hint) => <PasswordInput id={id} aria-describedby={hint} type="password" autoComplete="new-password" required minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      </fieldset><ErrorNote error={action.error || auth.error} /><button className="button primary full-width" type="submit" disabled={action.pending}>{action.pending ? t('Saving…') : t('Create account')}</button></form>
      <p className="auth-help"><Link className="text-link" to="/login">{t('Back to sign in')}</Link></p>
      <div className="privacy-note"><Lock size={14} aria-hidden /><span>{t('Your library and notes stay private.')}</span></div>
    </>}
  </main><footer><a href="/docs/licenses.txt">{t('Third-party notices')}</a></footer></div></div>;
}

export function Join() {
  const auth = useAuth(); const [token] = useState(capturedInvitation); const [username, setUsername] = useState(''); const [displayName, setDisplayName] = useState(''); const [password, setPassword] = useState(''); const [done, setDone] = useState(false);
  const preview = useQuery({ queryKey: ['invitation-preview'], queryFn: async () => unwrap(await api.POST('/api/v1/invitations/preview', { params: origin(), body: { token } })), enabled: !!token, gcTime: 0, retry: false, refetchOnWindowFocus: false });
  const claim = useAction(async (value: { token: string; username?: string; display_name?: string; password: string }) => { unwrap(await api.POST('/api/v1/invitations/claim', { params: origin(), body: value })); setPassword(''); setDone(true); });
  const signOut = useAction(async (_: void) => { unwrap(await api.POST('/api/v1/logout')); await auth.clear(); });
  useDocumentTitle(t('Join Pastime'));
  return <div className="auth-page"><div className="auth-panel"><header><Brand /></header><main id="main" className="auth-form">{!token ? <Empty heading="h1" title={t('Open your invitation link')} action={<Link className="button" to="/login">{t('Go to sign in')}</Link>}>{t('For privacy, invitation secrets are not stored in your browser. Reopen the original link to continue.')}</Empty> : done ? <Empty heading="h1" title={t('You are ready')} action={<Link className="button primary" to="/login">{t('Sign in')}</Link>}>{t('Your password has been saved. Sign in with your account to continue.')}</Empty> : auth.session ? <><h1 tabIndex={-1}>{t('Use this invitation?')}</h1><p>{t('You are signed in as @{username}. Sign out before creating or resetting an account.', { username: auth.session.user.username })}</p><ErrorNote error={signOut.error} /><div className="auth-actions"><button className="button" disabled={signOut.pending} onClick={() => void signOut.run()}>{t('Sign out to continue')}</button></div></> : preview.isPending ? <Loading label={t('Checking invitation')} /> : preview.error ? <><h1 tabIndex={-1}>{t('This link is unavailable')}</h1><ErrorNote error={preview.error} /><p>{t('Ask your administrator for a new invitation or reset link.')}</p><div className="auth-actions"><Link className="button" to="/login">{t('Go to sign in')}</Link></div></> : <><h1 tabIndex={-1}>{preview.data!.kind === 'reset' ? t('Choose a new password.') : t('A place for your stories.')}</h1><p>{preview.data!.kind === 'reset' ? t('Set the password for @{username}. Existing sessions and tokens will be revoked.', { username: preview.data!.username }) : t('You have been invited with {role} access. Choose how you will sign in.', { role: preview.data!.role })}</p><form onSubmit={submit(() => void claim.run({ token, password, ...(preview.data!.kind === 'join' ? { username: username.trim(), display_name: displayName.trim() } : {}) }))}><fieldset disabled={claim.pending}>{preview.data!.kind === 'join' && <><Field label={t('Username')} hint={t('3–48 letters, numbers, dots, underscores or hyphens.')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="username" autoCapitalize="none" spellCheck={false} required minLength={3} maxLength={48} pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,47}" value={username} onChange={e => setUsername(e.target.value)} />}</Field><Field label={t('Display name')} hint={t('Optional')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="nickname" maxLength={80} value={displayName} onChange={e => setDisplayName(e.target.value)} />}</Field></>}<Field label={t('Password')} hint={t('Use 12–128 characters. A long, unique passphrase works well.')}>{(id, hint) => <PasswordInput id={id} aria-describedby={hint} type="password" autoComplete="new-password" required minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field></fieldset><ErrorNote error={claim.error} /><button className="button primary full-width" disabled={claim.pending} type="submit">{claim.pending ? t('Saving…') : preview.data!.kind === 'reset' ? t('Set password') : t('Create account')}</button></form></>}</main><footer><a href="/docs/licenses.txt">{t('Third-party notices')}</a></footer></div></div>;
}

export function Recover() {
  const auth = useAuth(); const [username, setUsername] = useState(''); const [code, setCode] = useState(''); const [password, setPassword] = useState(''); const [done, setDone] = useState(false);
  const action = useAction(async (_: void) => { unwrap(await api.POST('/api/v1/password-recovery', { params: origin(), body: { username: username.trim(), code: code.trim(), password } })); setPassword(''); setCode(''); setDone(true); await auth.clear(); });
  useDocumentTitle(t('Reset your local Pastime password'));
  return <div className="auth-page"><div className="auth-panel"><header><Brand /></header><main id="main" className="auth-form">
    {done ? <Empty heading="h1" title={t('Your password is set')} action={<Link className="button primary" to="/login">{t('Sign in')}</Link>}>{t('Every earlier session and token was signed out.')}</Empty> : <>
      <h1 tabIndex={-1}>{t('Reset your local Pastime password')}</h1><p>{t('Use the authenticator or recovery codes you set up in Pastime. Authentik codes and MFA belong to Authentik; recover them there.')}</p>
      <form onSubmit={submit(() => void action.run())}><fieldset disabled={action.pending}>
        <Field label={t('Username')}>{id => <input id={id} autoComplete="username" autoCapitalize="none" spellCheck={false} required maxLength={48} value={username} onChange={e => setUsername(e.target.value)} />}</Field>
        <Field label={t('Code')} hint={t('Six digits from the app, or a recovery code.')}>{(id, hint) => <input id={id} aria-describedby={hint} autoComplete="one-time-code" required maxLength={40} value={code} onChange={e => setCode(e.target.value)} />}</Field>
        <Field label={t('New password')} hint={t('12 to 128 characters.')}>{(id, hint) => <PasswordInput id={id} aria-describedby={hint} type="password" autoComplete="new-password" required minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      </fieldset><ErrorNote error={action.error} /><button className="button primary full-width" type="submit" disabled={action.pending}>{action.pending ? t('Saving…') : t('Set new password')}</button></form>
      <p className="auth-help">{t('No authenticator? Ask your administrator for a reset link.')} <Link className="text-link" to="/login">{t('Back to sign in')}</Link></p>
    </>}
  </main><footer><a href="/docs/licenses.txt">{t('Third-party notices')}</a></footer></div></div>;
}
