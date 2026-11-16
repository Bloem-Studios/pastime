import { PasswordInput } from './formControls';
import { AuthenticatorSection, DisplaySection, FieldsSection, HomeScreenSection, NotificationsSection, PanelActions, SettingsSection, SharingSection, SignInSection, TagsSection } from './settings';
import { useEffect, useState } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useAuth, useRefresh, useUnsaved, useUser } from '../state';
import { Empty, ErrorNote, Failed, Field, Modal, PageTitle, Secret, submit, Tabs, useAction, useConfirm } from '../ui';
import { clock, relative } from '../format';
import { deviceOf } from '../userAgent';
import { locale, plural, t } from '../i18n';
import { isProfileLogin } from '../profileLoginPolicy';
import { useTrackingProfile } from '../profileContext';
import { ProfileLogins } from './profileLogins';

const legacyTabs: Record<string, string> = { preferences: 'display', appearance: 'display' };

export function AccountPage() {
  const user = useUser(); const [params] = useSearchParams(); const { session } = useAuth();
  // A profile login acts only for its own profile: no household account details,
  // other profiles' sign-ins, authenticator recovery or single sign-on.
  const household = !isProfileLogin(session);
  const requested = params.get('tab') || 'profile'; const tab = legacyTabs[requested] ?? requested;
  const tabs: [string, string][] = [['profile', t('Profile')], ...(household ? [['profiles', t('Profiles')] as [string, string]] : []), ['display', t('Display')], ['home', t('Home screen')], ['notifications', t('Notifications')], ['tags', t('Tags')], ['fields', t('Collection fields')], ['security', t('Security')], ['tokens', t('API tokens')]];
  if (!tabs.some(([value]) => value === tab)) return <Navigate to="/account" replace />;
  return <div className="settings-page">
    <PageTitle title={t('Account & security')} description={household ? t('Signed in as @{username}', { username: user.username }) : t('Signed in to the {profile} profile as {login}', { profile: user.display_name, login: user.username })} />
    <Tabs label={t('Account sections')} current={tab} items={tabs.map(([value, label]) => ({ value, label, to: `/account?tab=${value}` }))} />
    {tab === 'profile' ? <>{household && <Profile />}<SharingSection /></>
      : tab === 'profiles' ? <ProfileLogins />
      : tab === 'display' ? <DisplaySection />
      : tab === 'home' ? <HomeScreenSection />
      : tab === 'notifications' ? <NotificationsSection />
      : tab === 'tags' ? <TagsSection />
      : tab === 'fields' ? <FieldsSection />
      : tab === 'security' ? <><PasswordSection household={household} />{household && <><AuthenticatorSection /><SignInSection /></>}<Sessions /></>
      : <Tokens />}
  </div>;
}

function Profile() {
  const user = useUser(); const [base, setBase] = useState(user); const [display, setDisplay] = useState(user.display_name); const [email, setEmail] = useState(user.email); const [dirty, setDirty] = useState(false); const unsaved = useUnsaved(); const client = useQueryClient(); const confirm = useConfirm();
  useEffect(() => { if (!dirty && user.version !== base.version) { setBase(user); setDisplay(user.display_name); setEmail(user.email); } }, [base.version, user, dirty]);
  const action = useAction(async (body: Schema['UserPatch']) => { const updated = unwrap(await api.PATCH('/api/v1/me', { body })); setBase(updated); setDirty(false); unsaved.markSaved(); await client.invalidateQueries({ queryKey: ['session'] }); });
  const discard = () => { setDisplay(base.display_name); setEmail(base.email); setDirty(false); unsaved.markSaved(); action.reset(); };
  return <SettingsSection title={t('Profile')} description={t('How you appear to other people on this server. Your username is fixed.')}>
    <form onSubmit={submit(() => void action.run({ version: base.version, display_name: display, email }))} onChange={() => { setDirty(true); unsaved.markDirty(); action.reset(); }}>
      <fieldset disabled={action.pending}>
        <Field label={t('Display name')}>{id => <input id={id} autoComplete="nickname" required maxLength={80} value={display} onChange={e => setDisplay(e.target.value)} />}</Field>
        <Field label={t('Contact email')} hint={t('Optional contact metadata. This is not a verified login or automated recovery address.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="email" autoComplete="email" maxLength={254} value={email} onChange={e => setEmail(e.target.value)} />}</Field>
      </fieldset>
      {base.version !== user.version && <p className="notice">{t('Your profile changed.')} <button className="text-button" type="button" onClick={async () => { if (await confirm({ title: t('Reload profile?'), description: t('Unsaved changes will be replaced by the latest profile.'), label: t('Reload profile') })) { setDirty(false); unsaved.markSaved(); } }}>{t('Reload profile')}</button></p>}
      <ErrorNote error={action.error} />
      <PanelActions dirty={dirty} pending={action.pending} saved={action.saved} label={t('Save changes')} onDiscard={discard} />
    </form>
  </SettingsSection>;
}

function PasswordSection({ household }: { household: boolean }) {
  const [open, setOpen] = useState(false);
  return <SettingsSection title={t('Password')} description={household ? t('Your local Pastime password. Changing it does not change your Authentik or other identity-provider password or MFA.') : t('The password you sign in to Pastime with. The person who set up your sign-in can also reset it.')}>
    <div className="setting-row"><div><strong>{household ? t('Local Pastime password') : t('Your password')}</strong><p className="subtle">{household ? t('Changing it signs out every session and revokes every token.') : t('Changing it signs you out of every browser.')}</p></div>
      <button className="button" onClick={() => setOpen(true)}>{t('Change password')}</button></div>
    {open && <PasswordDialog household={household} onClose={() => setOpen(false)} />}
  </SettingsSection>;
}

function PasswordDialog({ household, onClose }: { household: boolean; onClose: () => void }) {
  const auth = useAuth(); const navigate = useNavigate(); const unsaved = useUnsaved();
  const [currentPassword, setCurrentPassword] = useState(''); const [password, setPassword] = useState('');
  const change = useAction(async (body: Schema['PasswordInput']) => { unwrap(await api.POST('/api/v1/me/password', { body })); setCurrentPassword(''); setPassword(''); unsaved.markSaved(); await auth.clear(); navigate('/login', { replace: true }); });
  return <Modal title={t('Change your password')} description={household ? t('Every session, token and pending access link is revoked, including this browser. You sign in again with the new password.') : t('Every browser you’re signed in on is signed out, including this one. You sign in again with the new password.')} onClose={onClose} busy={change.pending} dirty={!!currentPassword || !!password}>
    <form onSubmit={submit(() => void change.run({ current_password: currentPassword, password }))} onChange={() => unsaved.markDirty()}>
      <fieldset disabled={change.pending}>
        <Field label={t('Current password')}>{id => <PasswordInput id={id} required type="password" autoComplete="current-password" maxLength={128} value={currentPassword} onChange={e => setCurrentPassword(e.target.value)} />}</Field>
        <Field label={t('New password')} hint={t('12–128 characters; use a long, unique passphrase.')}>{(id, hint) => <PasswordInput id={id} aria-describedby={hint} required type="password" autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      </fieldset>
      <ErrorNote error={change.error} />
      <div className="dialog-actions"><button className="button primary" disabled={change.pending} type="submit">{change.pending ? t('Changing…') : t('Change password')}</button></div>
    </form>
  </Modal>;
}

const signedIn = (iso: string) => `${new Date(iso).toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' })}, ${clock(iso)}`;
const shortDate = (iso: string) => new Date(iso).toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' });

/** The browser and system a session signed in with: "Firefox on Linux". Empty for older sessions. */
function deviceName(userAgent: string): string {
  const { browser, os } = deviceOf(userAgent);
  return browser && os ? t('{browser} on {os}', { browser, os }) : browser || os;
}

/**
 * Browser sessions, this browser first, then by sign-in. Each is named by the browser it signed in
 * with; a session from before that was recorded is named by its place in sign-in order.
 */
function Sessions() {
  const user = useUser(); const auth = useAuth(); const navigate = useNavigate(); const refresh = useRefresh(['sessions', 'tokens']); const confirm = useConfirm(); const unsaved = useUnsaved();
  const sessions = useQuery({ queryKey: privateKey(user.id, 'sessions'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/sessions', { signal })) });
  const revoke = useAction(async (value: Schema['SessionRecord']) => { unwrap(await api.DELETE('/api/v1/me/sessions/{credentialID}', { params: { path: { credentialID: value.id } } })); if (value.current) { unsaved.markSaved(); await auth.clear(); navigate('/login', { replace: true }); } else await refresh(); });
  const items = sessions.data?.items ?? [];
  const order = new Map([...items].sort((a, b) => a.created_at.localeCompare(b.created_at)).map((s, i) => [s.id, i + 1]));
  const shown = [...items].sort((a, b) => Number(b.current) - Number(a.current) || b.created_at.localeCompare(a.created_at));
  const times = (session: Schema['SessionRecord']) => {
    const values = { date: signedIn(session.created_at), expires: shortDate(session.expires_at) };
    if (session.current) return t('Active now · signed in {date} · expires {expires}', values);
    // Hours read well up to two days; older uses read as a date.
    if (session.last_used_at) return t('Last used {time} · signed in {date} · expires {expires}', { ...values, time: Date.now() - Date.parse(session.last_used_at) < 2 * 864e5 ? relative(session.last_used_at) : shortDate(session.last_used_at) });
    return t('Signed in {date} · expires {expires}', values);
  };
  return <SettingsSection title={t('Sessions')} description={t('Up to ten browser sessions stay signed in, each for seven days.')}>
    <ErrorNote error={revoke.error} />
    {sessions.isPending ? <p className="subtle">{t('Loading')}</p> : sessions.error ? <Failed error={sessions.error} retry={() => void sessions.refetch()} /> : <>
      <p className="panel-count">{plural(items.length, '{n} session', '{n} sessions')}</p>
      <ul className="record-list session-list">{shown.map(session => <li className="record-row" key={session.id}>
        <div><h3>{deviceName(session.user_agent) || (session.current ? t('This browser') : t('Session {n}', { n: order.get(session.id) ?? 0 }))}{session.current && session.user_agent && <span className="session-current">{t('This browser')}</span>}</h3>
          <p>{times(session)}</p></div>
        <button className="button small ghost" disabled={revoke.pending} onClick={async () => { if (await confirm({ title: session.current ? t('Sign out this browser?') : t('Revoke this session?'), description: session.current ? t('This signs you out of the current browser.') : t('The session will no longer be able to access the server.'), label: session.current ? t('Sign out') : t('Revoke session'), danger: true })) void revoke.run(session); }}>{session.current ? t('Sign out') : t('Revoke')}</button>
      </li>)}</ul></>}
  </SettingsSection>;
}

const scopeOptions: [Schema['TokenInput']['scopes'][number], string][] = [['profile:read', t('Read profile')], ['library:read', t('Read library')], ['library:write', t('Update library')], ['lists:read', t('Read lists')], ['lists:write', t('Update lists')]];
const scopeLabel = (scope: string) => scopeOptions.find(([value]) => value === scope)?.[1] ?? scope;

function TokenDialog({ onClose, onSecret }: { onClose: () => void; onSecret: (value: string) => void }) {
  const [name, setName] = useState(''); const [scopes, setScopes] = useState<Schema['TokenInput']['scopes']>(['library:read']); const [days, setDays] = useState(30); const [password, setPassword] = useState(''); const [dirty, setDirty] = useState(false); const unsaved = useUnsaved(); const refresh = useRefresh(['sessions', 'tokens']);
  const action = useAction(async (body: Schema['TokenInput']) => { const out = unwrap(await api.POST('/api/v1/me/tokens', { body })); setPassword(''); unsaved.markSaved(); setDirty(false); onClose(); onSecret(out.secret); void refresh(); });
  return <Modal title={t('Create API token')} description={t('Choose only the permissions this app needs. Tokens cannot administer accounts.')} onClose={onClose} dirty={dirty} busy={action.pending}><form onSubmit={submit(() => void action.run({ name, scopes, expires_in_days: days, password }))} onChange={() => { setDirty(true); unsaved.markDirty(); }}><fieldset disabled={action.pending}><Field label={t('Token name')}>{id => <input id={id} required maxLength={80} value={name} onChange={e => setName(e.target.value)} />}</Field><fieldset className="scope-options"><legend>{t('Permissions')}</legend>{scopeOptions.map(([scope, label]) => <label key={scope}><input type="checkbox" checked={scopes.includes(scope)} onChange={e => setScopes(previous => e.target.checked ? [...previous, scope] : previous.filter(value => value !== scope))} /><span>{label}</span></label>)}</fieldset><Field label={t('Expires after')}>{id => <select id={id} value={days} onChange={e => setDays(Number(e.target.value))}><option value={7}>{t('7 days')}</option><option value={30}>{t('30 days')}</option><option value={90}>{t('90 days')}</option><option value={365}>{t('1 year')}</option></select>}</Field><Field label={t('Confirm your password')}>{id => <PasswordInput id={id} type="password" required autoComplete="current-password" maxLength={128} value={password} onChange={e => setPassword(e.target.value)} />}</Field></fieldset><ErrorNote error={action.error} /><div className="dialog-actions"><button className="button primary" disabled={action.pending || scopes.length === 0} type="submit">{action.pending ? t('Creating…') : t('Create token')}</button></div></form></Modal>;
}

function Tokens() {
  const { profiles } = useTrackingProfile();
  // With more than one profile, say which one each token acts for.
  const profileName = (id: string) => profiles.length > 1 ? profiles.find(p => p.id === id)?.name : undefined;
  const user = useUser(); const refresh = useRefresh(['sessions', 'tokens']); const confirm = useConfirm(); const [creating, setCreating] = useState(false); const [secret, setSecret] = useState('');
  const query = useQuery({ queryKey: privateKey(user.id, 'tokens'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/tokens', { signal })) });
  const revoke = useAction(async (id: string) => { unwrap(await api.DELETE('/api/v1/me/tokens/{credentialID}', { params: { path: { credentialID: id } } })); await refresh(); });
  const create = <button className="button" onClick={() => setCreating(true)}><Plus aria-hidden />{t('Create token')}</button>;
  const items = query.data?.items ?? [];
  return <SettingsSection title={t('API tokens')} description={t('For apps you trust. At most 20 can be active; secrets are shown only when created, and revoked tokens never return.')} action={items.length ? create : undefined}>
    <ErrorNote error={revoke.error} />
    {query.isPending ? <p className="subtle">{t('Loading')}</p> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : items.length ? <ul className="record-list">{items.map(token => { const expired = new Date(token.expires_at).getTime() <= Date.now(); return <li className="record-row" key={token.id}>
      <div><h3>{token.name}</h3>{profileName(token.profile_id) && <p>{t('For {profile}', { profile: profileName(token.profile_id)! })}</p>}<p>{token.scopes.map(scopeLabel).join(', ')}</p><p>{token.revoked ? t('Revoked') : expired ? t('Expired {date}', { date: shortDate(token.expires_at) }) : t('Expires {date}', { date: shortDate(token.expires_at) })}</p></div>
      {!token.revoked && !expired && <button className="button small ghost" disabled={revoke.pending} onClick={async () => { if (await confirm({ title: t('Revoke this token?'), description: t('“{name}” will stop working immediately.', { name: token.name }), label: t('Revoke token'), danger: true })) void revoke.run(token.id); }}>{t('Revoke')}</button>}
    </li>; })}</ul> : <Empty compact title={t('No tokens yet')} action={create}>{t('Create one only when connecting a trusted app.')}</Empty>}
    {creating && <TokenDialog onClose={() => setCreating(false)} onSecret={setSecret} />}
    {secret && <Secret title={t('Copy your API token')} value={secret} description={t('Keep this secret. Grant it only to an app you trust; it will not be displayed again.')} onClose={() => setSecret('')} />}
  </SettingsSection>;
}
