import { useEffect, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Link2, Plus, Search, ShieldCheck, TriangleAlert } from 'lucide-react';
import { api, unwrap, type Schema, type User } from '../api/client';
import { privateKey, useRefresh, useUnsaved, useUser } from '../state';
import { Empty, ErrorNote, Failed, Field, Loading, Modal, PageTitle, Pager, Secret, Status, statusText, submit, useAction, useConfirm } from '../ui';
import { plural, t } from '../i18n';
import { useAdminOverview } from '../queueStatus';
import { Joined, When, number, roleName, sentence } from './adminParts';

const accountStatus = (user: User) => !user.active ? 'disabled' : !user.password_ready ? 'awaiting_setup' : 'active';
const accountStatusText = (user: User) => accountStatus(user) === 'awaiting_setup' ? t('Awaiting setup') : statusText(accountStatus(user));
const AccountStatus = ({ user }: { user: User }) => <span className={`status status-${accountStatus(user).replaceAll('_', '-')}`}>{accountStatusText(user)}</span>;
const roleHints: Record<User['role'], string> = {
  member: t('Tracks their own media.'),
  admin: t('Manages member accounts and their access.'),
  owner: t('Manages every account, role and administration setting.'),
};
function RoleOptions() { const user = useUser(); return <><option value="member">{t('Member')}</option>{user.role === 'owner' && <><option value="admin">{t('Administrator')}</option><option value="owner">{t('Owner')}</option></>}</>; }

/** The HTTPS setup check, when it fails: setup links travel in the clear until the server is behind HTTPS. */
function useHttpsWarning() {
  const user = useUser();
  const checks = useQuery({ queryKey: privateKey(user.id, 'admin', 'checks'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/checks', { signal })).items, staleTime: 60_000 });
  const origin = checks.data?.find(c => c.id === 'origin');
  return origin && origin.status !== 'ok' ? origin.detail : null;
}

function InviteDialog({ createAccount = false, onClose, onSecret }: { createAccount?: boolean; onClose: () => void; onSecret: (value: string) => void }) {
  const [username, setUsername] = useState(''); const [display, setDisplay] = useState(''); const [email, setEmail] = useState(''); const [role, setRole] = useState<User['role']>('member'); const [hours, setHours] = useState(72); const [dirty, setDirty] = useState(false); const unsaved = useUnsaved(); const refresh = useRefresh(['admin', 'admin-users', 'admin-user', 'audit', 'invitations']); const confirm = useConfirm();
  const https = useHttpsWarning();
  const action = useAction(async (body: Schema['CreateUserInput'] | Schema['InviteInput']) => {
    const out = createAccount ? unwrap(await api.POST('/api/admin/v1/users', { body: body as Schema['CreateUserInput'] })) : unwrap(await api.POST('/api/admin/v1/invitations', { body: body as Schema['InviteInput'] }));
    unsaved.markSaved(); setDirty(false); await refresh(); onClose(); onSecret(out.claim_url);
  });
  async function save() { if (role === 'owner' && !await confirm({ title: t('Grant owner access?'), description: t('This person will be able to manage all account roles and administration. Personal media remains private.'), label: t('Grant owner access') })) return; void action.run(createAccount ? { username: username.trim(), display_name: display.trim(), email: email.trim(), role } : { role, recipient_email: email.trim(), expires_in_hours: hours }); }
  return <Modal title={createAccount ? t('Create account') : t('Create invite link')} description={createAccount ? t('Creates the account and a one-use setup link, shown once. Nothing is emailed; send the link yourself.') : t('A one-use setup link, shown once. Nothing is emailed; send the link yourself.')} onClose={onClose} dirty={dirty} busy={action.pending}>
    {https && <p className="adm-dialog-note" role="note"><TriangleAlert aria-hidden /><span>{https}</span></p>}
    <form onSubmit={submit(() => void save())} onChange={() => { setDirty(true); unsaved.markDirty(); }}><fieldset disabled={action.pending}>
      {createAccount && <><Field label={t('Username')}>{id => <input id={id} required minLength={3} maxLength={48} autoCapitalize="none" spellCheck={false} value={username} onChange={e => setUsername(e.target.value)} />}</Field><Field label={t('Display name')} hint={t('Optional')}>{(id, hint) => <input id={id} aria-describedby={hint} maxLength={80} value={display} onChange={e => setDisplay(e.target.value)} />}</Field></>}
      <Field label={t('Role')} hint={roleHints[role]}>{(id, hint) => <select id={id} aria-describedby={hint} value={role} onChange={e => setRole(e.target.value as User['role'])}><RoleOptions /></select>}</Field>
      {!createAccount && <Field label={t('Link expires after')}>{id => <select id={id} value={hours} onChange={e => setHours(Number(e.target.value))}><option value={1}>{t('1 hour')}</option><option value={24}>{t('1 day')}</option><option value={72}>{t('3 days')}</option><option value={168}>{t('7 days')}</option></select>}</Field>}
      <Field label={t('Email (optional)')} hint={t('A note for your records. Nothing is sent to it.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="email" maxLength={254} value={email} onChange={e => setEmail(e.target.value)} />}</Field>
    </fieldset><ErrorNote error={action.error} /><div className="dialog-actions"><button className="button" type="button" disabled={action.pending} onClick={async () => { if (!dirty || await confirm({ title: t('Discard changes?'), description: t('Your unsaved changes will be lost.'), label: t('Discard changes'), danger: true })) onClose(); }}>{t('Cancel')}</button><button className="button primary" disabled={action.pending} type="submit">{action.pending ? t('Creating…') : createAccount ? t('Create account') : t('Create link')}</button></div></form>
  </Modal>;
}

// ---------------------------------------------------------------- users

export function UsersPage() {
  const self = useUser(); const [params, setParams] = useSearchParams(); const q = params.get('q') || ''; const [search, setSearch] = useState(q); useEffect(() => setSearch(q), [q]); const [dialog, setDialog] = useState<'invite' | 'create' | null>(null); const [secret, setSecret] = useState('');
  const filters = { q: q || undefined, role: (params.get('role') || undefined) as User['role'] | undefined, status: (params.get('status') || undefined) as 'active' | 'disabled' | undefined, cursor: params.get('cursor') || undefined, limit: 30 };
  const query = useQuery({ queryKey: privateKey(self.id, 'admin-users', filters), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/users', { signal, params: { query: filters } })) });
  const counts = useAdminOverview().data?.counts;
  function change(key: string, value: string) { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); if (key !== 'cursor') next.delete('cursor'); setParams(next); }
  const filtering = !!(filters.q || filters.role || filters.status);
  const disabled = counts ? counts.users - counts.active_users : 0;
  return <div>
    <PageTitle title={t('Users')} description={counts ? [plural(counts.users, '{n} account', '{n} accounts'), disabled ? t('{n} disabled', { n: number(disabled) }) : ''].filter(Boolean).join(' · ') : undefined}
      actions={<><button className="button" onClick={() => setDialog('create')}><Plus aria-hidden />{t('Create account')}</button><button className="button primary" onClick={() => setDialog('invite')}><Link2 aria-hidden />{t('Create invite link')}</button></>} />
    {(filtering || (counts?.users ?? 0) >= 10) && <form className="adm-toolbar" role="search" onSubmit={submit(() => change('q', search.trim()))}>
      <label className="adm-search"><Search aria-hidden /><span className="sr-only">{t('Search usernames')}</span><input type="search" placeholder={t('Search usernames')} maxLength={48} value={search} onChange={e => setSearch(e.target.value)} /></label>
      <select aria-label={t('Role')} value={filters.role ?? ''} onChange={e => change('role', e.target.value)}><option value="">{t('All roles')}</option><option value="member">{t('Member')}</option><option value="admin">{t('Administrator')}</option><option value="owner">{t('Owner')}</option></select>
      <select aria-label={t('Status')} value={filters.status ?? ''} onChange={e => change('status', e.target.value)}><option value="">{t('All accounts')}</option><option value="active">{t('Active or awaiting setup')}</option><option value="disabled">{t('Disabled')}</option></select>
      {filtering && <button type="button" className="text-button" onClick={() => setParams({})}>{t('Clear filters')}</button>}
    </form>}
    {query.isPending ? <Loading label={t('Loading accounts')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : query.data.items.length ? <>
      <table className="data-table adm-table">
        <caption className="sr-only">{t('Accounts')}</caption>
        <thead><tr><th scope="col">{t('Account')}</th><th scope="col" className="adm-desk">{t('Role')}</th><th scope="col" className="adm-desk">{t('Status')}</th><th scope="col" className="adm-desk">{t('Last sign-in')}</th></tr></thead>
        <tbody>{query.data.items.map(user => <tr key={user.id}>
          <th scope="row"><Link className="adm-person" to={`/admin/users/${user.id}`}>
            <span className="avatar" aria-hidden>{Array.from(user.display_name)[0]?.toUpperCase()}</span>
            <span className="adm-person-text">
              <span className="adm-name">{user.display_name}</span>
              {user.username !== user.display_name && <span className="adm-sub">@{user.username}</span>}
              <span className="adm-phone"><Joined parts={[roleName(user.role), <AccountStatus key="s" user={user} />, user.last_login_at ? <span key="l">{t('signed in')} <When value={user.last_login_at} /></span> : t('never signed in')]} /></span>
            </span>
          </Link></th>
          <td className="adm-desk">{roleName(user.role)}</td>
          <td className="adm-desk"><AccountStatus user={user} /></td>
          <td className="adm-desk"><When value={user.last_login_at} empty={t('Never')} /></td>
        </tr>)}</tbody>
      </table>
      <Pager current={filters.cursor} next={query.data.next_cursor} change={value => change('cursor', value)} />
    </> : <Empty title={t('No accounts match')} action={<button className="button" onClick={() => setParams({})}>{t('Clear filters')}</button>}>{t('Try another username prefix or remove a filter.')}</Empty>}
    {dialog && <InviteDialog createAccount={dialog === 'create'} onClose={() => setDialog(null)} onSecret={setSecret} />}
    {secret && <Secret title={t('Copy the setup link')} value={secret} description={t('Nothing was emailed. Anyone holding this link can use it once before it expires.')} onClose={() => setSecret('')} />}
  </div>;
}

export function UserDetail() {
  const { userID = '' } = useParams(); const self = useUser();
  const query = useQuery({ queryKey: privateKey(self.id, 'admin-user', userID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/users/{userID}', { signal, params: { path: { userID } } })) });
  if (query.isPending) return <Loading label={t('Loading account')} />; if (query.error) return <Failed error={query.error} retry={() => void query.refetch()} />;
  return <UserContent key={userID} user={query.data} />;
}

function UserContent({ user }: { user: User }) {
  const self = useUser(); const client = useQueryClient(); const [base, setBase] = useState(user); const [display, setDisplay] = useState(user.display_name); const [email, setEmail] = useState(user.email); const [role, setRole] = useState(user.role); const [dirty, setDirty] = useState(false); const [secret, setSecret] = useState(''); const refresh = useRefresh(['admin', 'admin-users', 'admin-user', 'audit', 'invitations']); const confirm = useConfirm(); const unsaved = useUnsaved();
  const canManage = self.role === 'owner' || user.role === 'member';
  // The last active owner cannot lose the role or be locked out, and nobody locks themself out.
  const ownersFilter = { role: 'owner' as const, status: 'active' as const, limit: 2 };
  const owners = useQuery({ queryKey: privateKey(self.id, 'admin-users', ownersFilter), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/users', { signal, params: { query: ownersFilter } })), enabled: user.role === 'owner' && user.active });
  const isSelf = user.id === self.id;
  const lastOwner = user.role === 'owner' && user.active && owners.data?.items.length === 1 && !owners.data.next_cursor;
  const locked = isSelf ? { role: t('You cannot change your own role.'), access: t('This is your account: another owner has to revoke its access or disable it.') }
    : lastOwner ? { role: t('The only active owner keeps the owner role. Make another account an owner first.'), access: t('The only active owner cannot be locked out. Make another account an owner first.') } : null;
  useEffect(() => { if (!dirty && user.version !== base.version) { setBase(user); setDisplay(user.display_name); setEmail(user.email); setRole(user.role); } }, [user, base.version, dirty]);
  const save = useAction(async (body: Schema['UserPatch']) => { const updated = unwrap(await api.PATCH('/api/admin/v1/users/{userID}', { params: { path: { userID: user.id } }, body })); setBase(updated); setDirty(false); unsaved.markSaved(); await refresh(); if (user.id === self.id) await client.invalidateQueries({ queryKey: ['session'] }); });
  const reset = useAction(async (_: void) => { const out = unwrap(await api.POST('/api/admin/v1/users/{userID}/reset-link', { params: { path: { userID: user.id } } })); await refresh(); setSecret(out.claim_url); });
  const revoke = useAction(async (_: void) => { unwrap(await api.POST('/api/admin/v1/users/{userID}/revoke-access', { params: { path: { userID: user.id } } })); await refresh(); if (user.id === self.id) await client.invalidateQueries({ queryKey: ['session'] }); });
  const toggle = useAction(async (value: { version: number; active: boolean }) => { unwrap(await api.PATCH('/api/admin/v1/users/{userID}', { params: { path: { userID: user.id } }, body: value })); await refresh(); if (user.id === self.id) await client.invalidateQueries({ queryKey: ['session'] }); });
  const pending = save.pending || reset.pending || revoke.pending || toggle.pending;
  async function submitChanges() { if (role !== base.role && !await confirm({ title: t('Change account role?'), description: t('Existing sessions, tokens and pending access links will be revoked. The user must sign in again.'), label: t('Change role') })) return; void save.run({ version: base.version, display_name: display, email, role }); }
  return <>
    <Link className="back-link" to="/admin/users"><ArrowLeft aria-hidden />{t('Back to users')}</Link>
    <PageTitle title={user.display_name} />
    <dl className="adm-factsrow">
      <div><dt>{t('Username')}</dt><dd>@{user.username}</dd></div>
      <div><dt>{t('Role')}</dt><dd>{roleName(user.role)}</dd></div>
      <div><dt>{t('Status')}</dt><dd><AccountStatus user={user} /></dd></div>
      <div><dt>{t('Created')}</dt><dd><When value={user.created_at} /></dd></div>
      <div><dt>{t('Last sign-in')}</dt><dd><When value={user.last_login_at} empty={t('Never')} /></dd></div>
    </dl>
    <div className="privacy-note"><ShieldCheck aria-hidden /><span>{t('Only account metadata is available here. Private media, lists, notes and personal history are not accessible.')}</span></div>
    <section className="detail-section"><h2>{t('Account')}</h2>{!canManage && <p className="notice">{t('Only an owner can change this account.')}</p>}
      <form className="bounded-form" onSubmit={submit(() => void submitChanges())} onChange={() => { setDirty(true); unsaved.markDirty(); save.reset(); }}><fieldset disabled={!canManage || pending}>
        <Field label={t('Display name')}>{id => <input id={id} required maxLength={80} value={display} onChange={e => setDisplay(e.target.value)} />}</Field>
        <Field label={t('Email (optional)')} hint={t('A note for your records; it is not verified and nothing is sent to it.')}>{(id, hint) => <input id={id} type="email" aria-describedby={hint} maxLength={254} value={email} onChange={e => setEmail(e.target.value)} />}</Field>
        <Field label={t('Role')} hint={locked && canManage ? locked.role : roleHints[role]}>{(id, hint) => <select id={id} aria-describedby={hint} value={role} disabled={!!locked} onChange={e => setRole(e.target.value as User['role'])}>{self.role === 'owner' ? <RoleOptions /> : <option value={role}>{roleName(role)}</option>}</select>}</Field>
      </fieldset>
      {user.version !== base.version && <p className="notice">{t('This account changed.')} <button type="button" className="text-button" onClick={async () => { if (await confirm({ title: t('Reload account?'), description: t('Your unsaved account changes will be replaced.'), label: t('Reload account') })) { setDirty(false); unsaved.markSaved(); } }}>{t('Reload account')}</button></p>}
      <ErrorNote error={save.error} /><div className="actions"><button className="button primary" type="submit" disabled={!canManage || pending || !dirty}>{save.pending ? t('Saving…') : t('Save changes')}</button>{save.saved && <span role="status" className="save-confirmation">{t('Changes saved.')}</span>}</div></form>
    </section>
    <section className="detail-section"><h2>{t('Account access')}</h2>
      {locked && canManage && <p className="adm-locked">{locked.access}</p>}
      <ErrorNote error={reset.error || revoke.error || toggle.error} />
      <div className="actions">
        <button className="button" disabled={!canManage || !user.active || pending} onClick={async () => { if (await confirm({ title: t('Create a password-reset link?'), description: t('The new link expires in one hour and replaces previous reset links. Current access remains until the link is used.'), label: t('Create reset link') })) void reset.run(); }}>{t('Create reset link')}</button>
        <button className="button" disabled={!canManage || !!locked || pending} onClick={async () => { if (await confirm({ title: t('Revoke all access?'), description: t('End all sessions and revoke personal tokens and pending access links. An active account can still sign in with its password.'), label: t('Revoke access'), danger: true })) void revoke.run(); }}>{t('Revoke all access')}</button>
        <button className={user.active ? 'button danger-outline' : 'button'} disabled={!canManage || (user.active && !!locked) || pending} onClick={async () => { if (await confirm({ title: user.active ? t('Disable this account?') : t('Reactivate this account?'), description: user.active ? t('Current access is revoked and new sign-ins are blocked.') : t('The account can sign in again. Previously revoked sessions and tokens stay revoked.'), label: user.active ? t('Disable account') : t('Reactivate'), danger: user.active })) void toggle.run({ version: user.version, active: !user.active }); }}>{user.active ? t('Disable account') : t('Reactivate')}</button>
      </div>
    </section>
    {secret && <Secret title={t('Copy the reset link')} value={secret} description={t('Deliver privately. The link can set a new password once and expires in one hour.')} onClose={() => setSecret('')} />}
  </>;
}

// ---------------------------------------------------------------- invitations

export function InvitationsPage() {
  const user = useUser(); const [creating, setCreating] = useState(false); const [secret, setSecret] = useState(''); const refresh = useRefresh(['admin', 'admin-users', 'admin-user', 'audit', 'invitations']); const confirm = useConfirm();
  const query = useQuery({ queryKey: privateKey(user.id, 'invitations'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/invitations', { signal })) });
  const revoke = useAction(async (id: string) => { unwrap(await api.DELETE('/api/admin/v1/invitations/{invitationID}', { params: { path: { invitationID: id } } })); await refresh(); });
  const pendingCount = query.data?.items.filter(i => i.status === 'pending').length ?? 0;
  return <div>
    <PageTitle title={t('Invitations')} description={query.data?.items.length ? plural(pendingCount, '{n} link waiting to be used', '{n} links waiting to be used') : undefined}
      actions={<button className="button primary" onClick={() => setCreating(true)}><Link2 aria-hidden />{t('Create invite link')}</button>} />
    <ErrorNote error={revoke.error} />
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : query.data.items.length ? <table className="data-table adm-table">
      <caption className="sr-only">{t('Invite links, newest first')}</caption>
      <thead><tr><th scope="col">{t('Link')}</th><th scope="col" className="adm-desk">{t('Role')}</th><th scope="col" className="adm-desk">{t('Status')}</th><th scope="col" className="adm-desk">{t('Expires')}</th><th scope="col"><span className="sr-only">{t('Actions')}</span></th></tr></thead>
      <tbody>{query.data.items.map(invite => <tr key={invite.id}>
        <th scope="row">
          <span className="adm-name">{invite.username ? `@${invite.username}` : invite.recipient_email || t('Invite link')}</span>
          <span className="adm-sub">{invite.kind === 'join' ? t('New account') : t('Password setup or reset')}</span>
          <span className="adm-phone"><Joined parts={[roleName(invite.role), <Status key="s" value={invite.status} />, <span key="e">{t('expires')} <When value={invite.expires_at} /></span>]} /></span>
        </th>
        <td className="adm-desk">{roleName(invite.role)}</td>
        <td className="adm-desk"><Status value={invite.status} /></td>
        <td className="adm-desk"><When value={invite.expires_at} /></td>
        <td className="adm-action">{invite.status === 'pending' && (user.role === 'owner' || invite.role === 'member') && <button className="button small ghost" disabled={revoke.pending} onClick={async () => { if (await confirm({ title: t('Revoke this invitation?'), description: t('This link will stop working immediately.'), label: t('Revoke invitation'), danger: true })) void revoke.run(invite.id); }}>{t('Revoke')}</button>}</td>
      </tr>)}</tbody>
    </table> : <Empty title={t('No invitations yet')}>{t('Invite links you create are listed here until they are used or expire.')}</Empty>}
    {creating && <InviteDialog onClose={() => setCreating(false)} onSecret={setSecret} />}
    {secret && <Secret title={t('Copy the invite link')} value={secret} description={t('Nothing was emailed. Send this one-use link privately.')} onClose={() => setSecret('')} />}
  </div>;
}

// ---------------------------------------------------------------- audit

const auditActions: Record<string, string> = {
  'owner.bootstrapped': t('Owner created at setup'), 'user.created': t('Account created'), 'user.registered': t('Account registered'), 'user.updated': t('Account changed'),
  'user.access_revoked': t('Access revoked'), 'invitation.created': t('Invite link created'), 'invitation.revoked': t('Invite link revoked'), 'invitation.claimed': t('Invite link used'),
  'password.changed': t('Password changed'), 'password.recovered': t('Password recovered'), 'session.oidc': t('Signed in with single sign-on'), 'session.revoked': t('Session ended'),
  'token.created': t('Token created'), 'token.revoked': t('Token revoked'), 'totp.enabled': t('Two-step sign-in turned on'), 'totp.disabled': t('Two-step sign-in turned off'),
  'oidc.unlinked': t('Single sign-on unlinked'), 'oidc.subject_alias': t('Single sign-on identity linked'), 'calendar_feed.rotate': t('Calendar feed address changed'),
  'connection.create': t('Connection added'), 'catalog.refresh_stale': t('Catalog refresh started'), 'job.retry': t('Job retried'), 'job.cancel': t('Job cancelled'),
  'silo.credential.create': t('Silo credential created'), 'silo.credential.revoke': t('Silo credential revoked'),
};
const targetKinds: Record<string, string> = {
  user: t('Account'), invitation: t('Invite link'), token: t('Token'), session: t('Session'), job: t('Job'), catalog: t('Catalog'),
  connection: t('Connection'), tracking_profile: t('Profile'), bloem_instance: t('Bloem instance'),
};

export function AuditPage() {
  const user = useUser(); const [params, setParams] = useSearchParams(); const cursor = params.get('cursor') || undefined;
  const query = useQuery({ queryKey: privateKey(user.id, 'audit', cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/audit', { signal, params: { query: { cursor, limit: 30 } } })) });
  const actor = (name: string) => name === 'bootstrap' ? t('Setup') : `@${name}`;
  const target = (event: Schema['AuditEvent']) => {
    if (event.target_kind === 'user' && event.target_label) return <>@{event.target_label}</>;
    if (!event.target_kind) return null;
    const kind = targetKinds[event.target_kind] ?? sentence(event.target_kind);
    return event.target_id ? <>{kind} <span className="mono subtle">{event.target_id.slice(0, 8)}</span></> : <>{kind}</>;
  };
  return <div>
    <PageTitle title={t('Audit log')} description={t('Account and access changes, without private media or secret values.')} />
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : query.data.items.length ? <>
      <table className="data-table adm-table adm-audit">
        <caption className="sr-only">{t('Audit events, newest first')}</caption>
        <thead><tr><th scope="col" className="adm-desk">{t('When')}</th><th scope="col" className="adm-desk">{t('Actor')}</th><th scope="col">{t('Action')}</th><th scope="col" className="adm-desk">{t('Target')}</th></tr></thead>
        <tbody>{query.data.items.map(event => <tr key={event.id}>
          <td className="adm-desk adm-time"><When value={event.created_at} /></td>
          <td className="adm-desk">{actor(event.actor)}</td>
          <th scope="row"><span className="adm-name">{auditActions[event.action] ?? sentence(event.action)}</span>
            <span className="adm-phone"><Joined parts={[<When key="w" value={event.created_at} />, actor(event.actor), target(event)]} /></span></th>
          <td className="adm-desk">{target(event) ?? <span className="subtle">–</span>}</td>
        </tr>)}</tbody>
      </table>
      <Pager current={cursor} next={query.data.next_cursor} change={value => setParams(value ? { cursor: value } : {})} />
    </> : <Empty title={t('No events on this page')}>{t('Privileged account changes will appear here.')}</Empty>}
  </div>;
}
