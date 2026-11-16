import { integrationTask } from './formTasks';
import { PasswordInput } from './formControls';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, Download, ExternalLink, FileUp, KeyRound, RefreshCw, Trash2, Unplug, Upload } from 'lucide-react';
import { profileFetch, profileDownload, api, ApiError, csrfToken, unwrap, type Schema } from '../api/client';
import { Link, useSearchParams } from 'react-router-dom';
import { privateKey, useUnsaved, useUser } from '../state';
import { RollbackButton, SnapshotsSection } from './transfers';
import { LinkedServicesSection, serviceNames } from './services';
import { KOReaderSection } from './koreader';
import { CopyField, ErrorNote, Failed, Field, Loading, Modal, PageTitle, Secret, submit, Tabs, Timestamp, useAction, useConfirm, statusText } from '../ui';
import { locale, plural, t, tx } from '../i18n';
import { useTrackingProfile } from '../profileContext';
import { siloConnectKey, useSiloConnect } from '../siloConnect';
import { siloPage, siloProblem, type SiloProfile } from '../siloConnectPolicy';

type Kind = Schema['ConnectInput']['kind'];
const servers: { kind: Kind; name: string; steps: string[] }[] = [
  { kind: 'plex', name: 'Plex', steps: [t('Open Plex Web → Settings → Webhooks (requires Plex Pass).'), t('Choose Add Webhook and paste the URL.'), t('Plex reports a viewing once playback passes 90%.')] },
  { kind: 'jellyfin', name: 'Jellyfin', steps: [t('Install the Webhook plugin from the Jellyfin catalog.'), t('Add a Generic Destination with the URL, enable Playback Stop and User Data Saved, and send all properties.'), t('Playback that reaches the end, or marking an item played, is recorded.')] },
  { kind: 'emby', name: 'Emby', steps: [t('Open Emby → Settings → Notifications → Add notification → Webhooks.'), t('Paste the URL and select Playback stop and Mark played events.'), t('Completed playback and marking an item played are recorded.')] },
  { kind: 'generic', name: 'Kodi and other players', steps: [t('In Kodi, install a webhook add-on and paste the URL as its target.'), t('Playback that ends, or stops past 80%, is recorded.'), t('Other players can post movie or episode identifiers with completed: true; see the API reference.')] },
  { kind: 'seerr', name: 'Overseerr and Jellyseerr', steps: [t('Open Settings → Notifications → Webhook and paste the URL.'), t('Enable Request pending, approved and available notifications.'), t('Requested movies and shows are added as planned. Set the account filter to only take your own requests.')] },
];

export function IntegrationsPage() {
  const [params] = useSearchParams(); const task = integrationTask(params);
  const tasks = [['services', t('Linked services')], ['silo', t('Silo')], ['transfer', t('Import and export')], ['snapshots', t('Kept exports')], ['readers', 'KOReader'], ['advanced', t('Advanced')]];
  const advanced = ['advanced', 'navidrome', 'assistant', 'webhooks'].includes(task);
  return <div className="integrations-page"><PageTitle title={t('Integrations')} />
    <Tabs label={t('Integration tasks')} current={advanced ? 'advanced' : task} items={tasks.map(([value, label]) => ({ value, label, to: `/integrations?section=${value}` }))} />
    {task === 'services' && <LinkedServicesSection />}
    {task === 'transfer' && <ImportExportSection />}
    {task === 'snapshots' && <SnapshotsSection />}
    {task === 'readers' && <KOReaderSection />}
    {task === 'silo' && <SiloSection />}
    {advanced && <>
      {task !== 'advanced' && <Link className="back-link" to="/integrations?section=advanced"><ArrowLeft size={16} aria-hidden />{t('Advanced')}</Link>}
      {task === 'advanced' && <AdvancedDirectory />}
      {task === 'navidrome' && <NavidromeSection />}{task === 'assistant' && <AssistantSection />}
      {task === 'webhooks' && <section className="detail-section"><h2>{t('Webhooks')}</h2><p className="subtle">{t('A media server, player or request manager reports each viewing here the moment it ends. Movies and episodes are matched through TMDB identifiers; deliveries of the same viewing within three hours count once.')}</p><ServerList /></section>}
    </>}
  </div>;
}

/** Advanced: the integrations that hand an app a token or a URL, each with what it does. */
function AdvancedDirectory() {
  const user = useUser();
  const connections = useQuery({ queryKey: privateKey(user.id, 'connections'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/connections', { signal })).items });
  const connected = connections.data?.length ?? 0;
  const items: [string, string, string, string?][] = [
    ['/integrations?section=webhooks', t('Webhooks'), t('Plex, Jellyfin, Emby, Kodi and request managers report plays as they happen.'), connections.data ? (connected ? plural(connected, '{n} connected', '{n} connected') : t('Not connected')) : undefined],
    ['/integrations?section=navidrome', 'Navidrome', t('Scrobble what you play in Navidrome through its ListenBrainz support.')],
    ['/integrations?section=assistant', t('Assistants'), t('Let an AI assistant read and update your library over MCP.')],
    ['/account?tab=tokens', t('API tokens'), t('Personal tokens for scripts and apps, with the permissions you choose.')],
  ];
  return <section className="detail-section"><h2>{t('Advanced integrations')}</h2>
    <p className="subtle">{t('Tokens and webhook URLs grant access to your library. Share them only with apps you trust.')}</p>
    <ul className="directory-list">{items.map(([to, label, summary, state]) => <li key={to}><Link to={to}><span><strong>{label}</strong><span className="subtle">{summary}</span></span>{state && <span className="directory-state">{state}</span>}<ChevronRight aria-hidden /></Link></li>)}</ul>
  </section>;
}

// Each Pastime profile connects to one Silo profile with its own key. Silo's
// Pastime plugin sends plays, live playback, favourites, watchlist and ratings.
function SiloSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const { active } = useTrackingProfile();
  const query = useSiloConnect();
  const [secret, setSecret] = useState<{ value: string; profile: string } | null>(null);
  const reload = () => client.invalidateQueries({ queryKey: siloConnectKey(user.id) });
  const create = useAction(async (profile: SiloProfile) => {
    const out = unwrap(await api.POST('/api/v1/integrations/silo/profiles/{profileID}/credential', { params: { path: { profileID: profile.profile_id } }, body: {} }));
    setSecret({ value: out.credential, profile: out.profile_name || profile.name });
    await reload();
  });
  const disconnect = useAction(async (profile: SiloProfile) => {
    unwrap(await api.DELETE('/api/v1/integrations/silo/profiles/{profileID}/credential', { params: { path: { profileID: profile.profile_id } } }));
    await reload();
  });
  async function createKey(profile: SiloProfile) {
    disconnect.reset();
    if (profile.connected && !await confirm({ title: t('Replace the Silo key for {name}?', { name: profile.name }), description: t('The current key stops working once Silo uses the new one, or after 24 hours.'), label: t('Create new key') })) return;
    void create.run(profile);
  }
  async function disconnectProfile(profile: SiloProfile) {
    create.reset();
    if (await confirm({ title: t('Disconnect {name} from Silo?', { name: profile.name }), description: t('The key stops working at once, so Silo can no longer send plays to this profile. What is already recorded stays in Pastime. Remove Pastime from Watch Providers in Silo too.'), label: t('Disconnect'), danger: true })) void disconnect.run(profile);
  }
  const state = query.data;
  const watchProviders = siloPage(state?.silo_url, '/settings/watch-providers');
  return <section className="detail-section"><h2>{t('Connect Silo')}</h2>
    <p className="subtle">{t('Silo sends what you watch to Pastime: finished plays, live playback, favourites, watchlist and ratings. Each Pastime profile connects to one Silo profile with its own key.')}</p>
    {query.isPending ? <Loading label={t('Loading Silo connection')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
      : !state ? <p className="notice">{t('This Pastime server cannot connect to Silo.')}</p>
      : !state.available ? <p className="notice">{t('Silo is not set up on this Pastime server yet. Ask your Pastime administrator.')}</p>
      : <>
        <ol className="setup-steps">
          <li>{t('Create a key for the Pastime profile you want to connect.')}</li>
          <li>{t('In Silo, switch to the profile you want to connect, open {page} and choose Pastime.', { page: t('Settings → Watch Providers') })}</li>
          <li>{t('Paste the key and connect. Silo then sends what you watch to this Pastime profile.')}</li>
        </ol>
        {watchProviders && <p><a className="text-link" href={watchProviders} rel="noreferrer">{t('Open Watch Providers in Silo')}<ExternalLink size={14} aria-hidden /></a></p>}
        {!state.service_access && <p className="notice" role="status">{t('Your Pastime account does not have access to tracking, so it cannot create Silo keys. Ask your Pastime administrator.')}</p>}
        <SiloProblemNote error={create.error || disconnect.error} />
        <div className="record-list">{state.profiles.map(profile => <article className="record-row" key={profile.profile_id}>
          <div>
            <h3>{profile.name}{profile.profile_id === active.id && <span className="subtle"> · {t('This profile')}</span>}</h3>
            <p>{profile.connected ? <span className="status status-in-progress">{t('Connected')}</span> : t('Not connected')}</p>
            {profile.connected && profile.expires_at && <p>{t('Stays valid while Silo keeps syncing. Unused, it expires on {date}.', { date: new Date(profile.expires_at).toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' }) })}</p>}
          </div>
          <div className="actions">
            <button className={profile.connected ? 'button' : 'button primary'} disabled={!state.service_access || create.pending || disconnect.pending} onClick={() => void createKey(profile)}><KeyRound aria-hidden />{profile.connected ? t('Create new key') : t('Create Silo key')}</button>
            {profile.connected && <button className="button danger-outline" disabled={create.pending || disconnect.pending} onClick={() => void disconnectProfile(profile)}><Unplug aria-hidden />{t('Disconnect from Silo')}</button>}
          </div>
        </article>)}</div>
        <p className="subtle">{t('A profile has one Silo key. A new key replaces the previous one: the old key stops working once Silo uses the new key, or after 24 hours.')}</p>
      </>}
    {secret && <Secret title={t('Silo key for {name}', { name: secret.profile })} value={secret.value} description={t('In Silo, open Settings → Watch Providers on the profile you want to connect, choose Pastime and paste this key. It is shown only once.')} onClose={() => setSecret(null)} />}
  </section>;
}

/** Explains a refused key request in terms of what the person can do next. */
function SiloProblemNote({ error }: { error: unknown }) {
  if (!(error instanceof ApiError)) return <ErrorNote error={error} />;
  const messages: Record<string, string> = {
    service_access: t('Your Pastime account does not have access to tracking, so it cannot create Silo keys. Ask your Pastime administrator.'),
    not_registered: t('Silo is not set up on this Pastime server yet. Ask your Pastime administrator.'),
    excluded: t('This profile is excluded from Silo. Ask your Pastime administrator.'),
    not_found: t('This profile is no longer available. Reload the page and try again.'),
    ambiguous: t('More than one Silo server is registered here. Ask your Pastime administrator to remove the old one.'),
    rate_limited: t('Too many key requests. Wait a minute and try again.'),
  };
  const problem = siloProblem(error.status, error.code);
  return <ErrorNote error={problem === 'other' ? error : new ApiError(error.status, error.code, messages[problem])} />;
}

function AssistantSection() {
  const [creating, setCreating] = useState(false); const [secret, setSecret] = useState<string | null>(null);
  return <section className="detail-section"><h2>{t('Assistants')}</h2>
    <p className="subtle">{t('Let an AI assistant that speaks the Model Context Protocol search the catalog, read your library and history, add titles and record what you watched.')}</p>
    <ol className="setup-steps"><li>{t('Create a token below. It can read and update your library and nothing else; revoke it any time.')}</li><li>{t('In your assistant, add a remote MCP server at this address:')}<CopyField label={t('MCP server address')} value={`${window.location.origin}/mcp`} /></li><li>{tx('Send the token as a Bearer {header} header.', { header: <code>Authorization</code> })}</li></ol>
    <button className="button" onClick={() => setCreating(true)}><KeyRound aria-hidden />{t('Create assistant token')}</button>
    {creating && <SyncToken defaultName="Assistant" onClose={() => setCreating(false)} onSecret={setSecret} />}
    {secret && <Secret title={t('Assistant token')} value={secret} description={t("Paste this into your assistant's MCP server settings. It is shown only once.")} onClose={() => setSecret(null)} />}
  </section>;
}

function SyncToken({ onClose, onSecret, defaultName }: { onClose: () => void; onSecret: (value: string) => void; defaultName: string }) {
  const [password, setPassword] = useState(''); const [name, setName] = useState(defaultName);
  const action = useAction(async (_: void) => { const out = unwrap(await api.POST('/api/v1/me/tokens', { body: { name, scopes: ['library:read', 'library:write'], expires_in_days: 365, password } })); setPassword(''); onClose(); onSecret(out.secret); });
  return <Modal title={t('Create sync token')} description={t('Confirm your password. The token expires in one year.')} onClose={onClose} busy={action.pending} dirty={!!password || name !== defaultName}><form onSubmit={submit(() => void action.run())}><fieldset disabled={action.pending}>
    <Field label={t('Token name')}>{id => <input id={id} required maxLength={80} value={name} onChange={e => setName(e.target.value)} />}</Field>
    <Field label={t('Current password')}>{id => <PasswordInput id={id} type="password" required autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} />}</Field></fieldset>
    <ErrorNote error={action.error} /><div className="dialog-actions"><button className="button primary" type="submit" disabled={action.pending}>{action.pending ? t('Creating…') : t('Create token')}</button></div></form></Modal>;
}

function ServerList() {
  const user = useUser(); const [params] = useSearchParams(); const selected = params.get('server');
  const query = useQuery({ queryKey: privateKey(user.id, 'connections'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/connections', { signal })).items });
  if (query.isPending) return <Loading label={t('Loading connections')} />;
  if (query.error) return <Failed error={query.error} retry={() => void query.refetch()} />;
  const server = servers.find(s => s.kind === selected);
  return server ? <><Link className="back-link" to="/integrations?section=webhooks"><ArrowLeft size={16} aria-hidden />{t('All webhooks')}</Link><ServerCard key={server.kind} server={server} connection={query.data.find(c => c.kind === server.kind)} /></> : <>
    <ul className="directory-list">{servers.map(s => { const connection = query.data.find(c => c.kind === s.kind); return <li key={s.kind}><Link to={`/integrations?section=webhooks&server=${s.kind}`}>
      <span><strong>{s.name}</strong><span className="subtle">{connection ? <>{plural(connection.events_recorded, '{n} viewing recorded', '{n} viewings recorded')}{connection.last_event_at && <> · {tx('last event {date}', { date: <Timestamp value={connection.last_event_at} /> })}</>}</> : t('Not connected')}</span></span>
      {connection && <span className="status status-active">{t('Connected')}</span>}<ChevronRight aria-hidden /></Link></li>; })}</ul>
    <p className="subtle directory-note">{tx('Plex, Jellyfin and Emby can also be linked under {link}, which reads their history on a schedule.', { link: <Link className="text-link" to="/integrations?section=services">{t('Linked services')}</Link> })}</p></>;
}

function ServerCard({ server, connection }: { server: typeof servers[number]; connection?: Schema['Connection'] }) {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm(); const unsaved = useUnsaved();
  const [url, setURL] = useState<string | null>(null); const [username, setUsername] = useState(connection?.settings.username ?? '');
  const reload = () => client.invalidateQueries({ queryKey: privateKey(user.id, 'connections') });
  const connect = useAction(async (_: void) => { const out = unwrap(await api.POST('/api/v1/connections', { body: { kind: server.kind, settings: { username: username.trim() } } })); setURL(out.webhook_url); unsaved.markSaved(); await reload(); });
  const save = useAction(async (_: void) => { unwrap(await api.PATCH('/api/v1/connections/{connectionID}', { params: { path: { connectionID: connection!.id } }, body: { settings: { username: username.trim() } } })); unsaved.markSaved(); await reload(); });
  const remove = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/connections/{connectionID}', { params: { path: { connectionID: connection!.id } } })); setUsername(''); unsaved.markSaved(); await reload(); });
  return <article className="server-card"><header><h3>{server.name}</h3>{connection ? <span className="status status-active">{t('Connected')}</span> : <span className="status">{t('Not connected')}</span>}</header>
    {connection && <p className="subtle">{plural(connection.events_recorded, '{n} viewing recorded', '{n} viewings recorded')}{connection.last_event_at && <> · {tx('last event {date}', { date: <Timestamp value={connection.last_event_at} /> })}</>}{connection.last_outcome && <><br />{connection.last_outcome}</>}</p>}
    <ol className="setup-steps">{server.steps.map(step => <li key={step}>{step}</li>)}</ol>
    <form onSubmit={submit(() => void (connection ? save.run() : connect.run()))}><Field label={server.kind === 'seerr' ? t('Requester filter') : t('{name} account filter', { name: server.name })} hint={server.kind === 'seerr' ? t('Optional. Only add requests made by this user.') : t('Optional. Only record events from this media server user.')}>{(id, hint) => <input id={id} aria-describedby={hint} maxLength={100} value={username} onChange={e => { setUsername(e.target.value); unsaved.markDirty(); save.reset(); }} />}</Field>
      <div className="actions">{connection ? <><button className="button" type="submit" disabled={save.pending}>{t('Save filter')}</button><button className="button" type="button" disabled={connect.pending} onClick={async () => { if (await confirm({ title: t('Replace the webhook URL?'), description: t('The current URL stops working until you paste the new one into {name}.', { name: server.name }), label: t('Replace URL') })) void connect.run(); }}><RefreshCw aria-hidden />{t('New URL')}</button><button className="button danger-outline" type="button" disabled={remove.pending} onClick={async () => { if (await confirm({ title: t('Disconnect {name}?', { name: server.name }), description: t('Its webhook URL stops working. Recorded history stays.'), label: t('Disconnect'), danger: true })) void remove.run(); }}><Trash2 aria-hidden />{t('Disconnect')}</button></>
        : <button className="button primary" type="submit" disabled={connect.pending}>{connect.pending ? t('Connecting…') : t('Get webhook URL')}</button>}</div></form>
    <ErrorNote error={connect.error || save.error || remove.error} />
    {url && <Secret title={t('{name} webhook URL', { name: server.name })} value={url} description={t('Paste this into your media server. It is shown only once and works like a password.')} onClose={() => setURL(null)} />}
  </article>;
}

const importSources: { value: Schema['ImportRun']['source']; label: string; hint: string; accept?: string; username?: boolean }[] = [
  { value: 'trakt', label: t('Trakt'), hint: t('The .zip from Trakt → Settings → Data → Export.'), accept: '.zip' },
  { value: 'letterboxd', label: t('Letterboxd'), hint: t('The .zip from Letterboxd → Settings → Data → Export, or diary.csv.'), accept: '.zip,.csv' },
  { value: 'imdb', label: t('IMDb'), hint: t('Your ratings or watchlist CSV from IMDb.'), accept: '.csv' },
  { value: 'goodreads', label: t('Goodreads'), hint: t('The library export CSV from Goodreads → My Books → Import and export.'), accept: '.csv' },
  { value: 'storygraph', label: t('StoryGraph'), hint: t('The CSV from StoryGraph → Manage account → Export.'), accept: '.csv' },
  { value: 'anilist', label: t('AniList'), hint: t('Your public AniList anime and manga lists.'), username: true },
  { value: 'yamtrack', label: t('Floppy or Yamtrack'), hint: t('The CSV from Floppy or Yamtrack → Settings → Export.'), accept: '.csv' },
  { value: 'tvtime', label: t('TV Time'), hint: t('Your TV Time data request .zip, or tracking-prod-records.csv.'), accept: '.zip,.csv' },
  { value: 'grouvee', label: t('Grouvee'), hint: t('The JSON or .zip from Grouvee → Settings → Export.'), accept: '.json,.zip' },
  { value: 'playnite', label: t('Playnite'), hint: t('A library CSV from a Playnite export extension, with Name, Completion Status and playtime columns.'), accept: '.csv' },
  { value: 'wetrakr', label: 'WeTrakr', hint: t('Upload a WeTrakr CSV or ZIP containing history, ratings, notes and lists. Missing files never remove existing data.'), accept: '.csv,.zip' },
  { value: 'clz', label: t('CLZ collection'), hint: t('The CSV from a CLZ app (Movies, Books, Comics, Games or Music) → Export to text/CSV. Each title gets an owned copy.'), accept: '.csv,.txt' },
  { value: 'csv', label: t('Any spreadsheet (CSV)'), hint: t('Columns such as title, type, year, status, rating, date, imdb_id, tmdb_id or isbn are recognized.'), accept: '.csv' },
  { value: 'native', label: t('This app'), hint: t('A .jsonl export from this or another server.'), accept: '.jsonl,.json,.gz' },
];

export function ImportExportSection() {
  const user = useUser(); const client = useQueryClient();
  const [params] = useSearchParams();
  const [source, setSource] = useState<Schema['ImportRun']['source']>(() => (importSources.find(s => s.value === params.get('source'))?.value ?? 'trakt')); const [file, setFile] = useState<File | null>(null); const [username, setUsername] = useState('');
  const runs = useQuery({ queryKey: privateKey(user.id, 'imports'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/imports', { signal })).items,
    refetchInterval: query => query.state.data?.some(r => r.status === 'queued' || r.status === 'running') ? 2000 : false });
  const option = importSources.find(s => s.value === source)!;
  const start = useAction(async (_: void) => {
    let response: Response;
    if (option.username) {
      response = await profileFetch('/api/v1/imports', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken() }, body: JSON.stringify({ source, username }) });
    } else {
      const body = new FormData(); body.set('source', source); if (file) body.set('file', file);
      response = await profileFetch('/api/v1/imports', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrfToken() }, body });
    }
    if (!response.ok) { const problem = await response.json().catch(() => ({})); throw new ApiError(response.status, problem.code ?? 'request_failed', problem.message ?? 'The import could not be started.'); }
    setFile(null); await client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') });
  });
  const cancel = useAction(async (id: string) => { unwrap(await api.POST('/api/v1/imports/{importID}/cancel', { params: { path: { importID: id } } })); await client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') }); });
  return <section className="detail-section"><h2>{t('Import and export')}</h2>
    <p className="subtle">{t('Imports only add or move things forward: nothing you already track is downgraded, and running the same import again adds nothing. Titles are matched by their identifiers or exact title and year; anything that can’t be matched confidently is listed rather than guessed.')}</p>
    {source === 'native' && <p className="subtle">{t('Native restore requires an empty library, lists, tags and collection fields. Use service imports to merge existing libraries.')}</p>}
    <form className="import-form" onSubmit={submit(() => void start.run())}>
      <Field label={t('Source')}>{id => <select id={id} value={source} onChange={e => { setSource(e.target.value as typeof source); setFile(null); }}>{importSources.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}</select>}</Field>
      {option.username ? <Field label={t('AniList username')} hint={option.hint}>{(id, hint) => <input id={id} aria-describedby={hint} required maxLength={60} value={username} onChange={e => setUsername(e.target.value)} />}</Field>
        : <Field label={t('Export file')} hint={option.hint}>{(id, hint) => <div className="file-picker"><input key={source} id={id} aria-describedby={hint} type="file" required accept={option.accept} onChange={e => setFile(e.target.files?.[0] ?? null)} /><span className="button" aria-hidden><FileUp aria-hidden />{t('Choose file')}</span><span className={file ? 'file-name' : 'file-name subtle'}>{file?.name ?? t('No file chosen')}</span></div>}</Field>}
      <div className="actions"><button className="button primary" type="submit" disabled={start.pending}><Upload aria-hidden />{start.pending ? t('Uploading…') : t('Start import')}</button></div>
      <ErrorNote error={start.error || cancel.error} />
    </form>
    {runs.data && runs.data.length > 0 && <ul className="import-runs">{runs.data.slice(0, 10).map(r => <li key={r.id}>
      <div className="import-run-head"><strong>{importSources.find(s => s.value === r.source)?.label ?? serviceNames[r.source as keyof typeof serviceNames] ?? r.source}</strong><span className={`status status-${r.status}`}>{statusText(r.status)}</span><Timestamp value={r.created_at} />
        {(r.status === 'queued' || r.status === 'running') && !r.cancel_requested && <button className="text-button" onClick={() => void cancel.run(r.id)}>{t('Cancel')}</button>}</div>
      {r.total > 0 && <progress max={r.total} value={r.processed} aria-label={t('Import progress')} />}
      <p className="subtle">{t('{processed} of {total} titles · {imported} added or updated · {plays} plays · {unchanged} unchanged · {unmatched} unmatched', { processed: r.processed, total: r.total, imported: r.imported, plays: r.plays_added, unchanged: r.unchanged, unmatched: r.unmatched })}{r.failed ? t(' · {failed} failed', { failed: r.failed }) : ''}</p>
      {r.error && <p className="error-text">{r.error}</p>}
      <div className="actions">{(r.unmatched + r.failed) > 0 && r.status !== 'rolled_back' && <Link className="text-link" to={`/integrations/unmatched?run=${r.id}`}>{t('Match')} {plural(r.unmatched + r.failed, '{n} title', '{n} titles')}</Link>}<RollbackButton run={r} /></div>
    </li>)}</ul>}
    <p><Link className="text-link" to="/integrations/unmatched">{t('Titles waiting to be matched')}<ChevronRight size={16} aria-hidden /></Link></p>
    <h3 className="export-heading">{t('Export')}</h3>
    <div className="actions"><a className="button" href={profileDownload("/api/v1/export/library.jsonl")} download><Download aria-hidden />{t('Full export (re-importable)')}</a><a className="button" href={profileDownload("/api/v1/export/library.csv")} download><Download aria-hidden />{t('Spreadsheet (CSV)')}</a><a className="button" href={profileDownload("/api/v1/export/letterboxd.csv")} download><Download aria-hidden />{t('Letterboxd CSV')}</a></div>
  </section>;
}

function NavidromeSection() {
 const [creating,setCreating]=useState(false);const [secret,setSecret]=useState<string|null>(null);
 return <section className="detail-section"><h2>Navidrome</h2><p>{t('Enable ListenBrainz scrobbling in Navidrome, set its base URL below, then connect using a sync token in your Navidrome profile.')}</p><div className="copy-stack"><CopyField label={t('Navidrome setting')} value="ND_LISTENBRAINZ_ENABLED=true" /><CopyField label={t('Navidrome setting')} value={`ND_LISTENBRAINZ_BASEURL=${window.location.origin}/apis/listenbrainz/1/`} /></div><button className="button" onClick={()=>setCreating(true)}>{t('Create sync token')}</button>{creating&&<SyncToken defaultName="Navidrome" onClose={()=>setCreating(false)} onSecret={setSecret}/ >}{secret&&<Secret title={t('Sync token')} value={secret} description={t('Use this token in your Navidrome ListenBrainz settings.')} onClose={()=>setSecret(null)}/>}</section>;
}
