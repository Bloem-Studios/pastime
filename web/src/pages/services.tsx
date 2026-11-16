import { Link, useSearchParams } from 'react-router-dom';
import { PasswordInput } from './formControls';
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, ChevronRight, ExternalLink, Link2, RefreshCw, Trash2 } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { ErrorNote, Failed, Field, Loading, Modal, submit, Timestamp, useAction, useConfirm, statusText } from '../ui';
import { t, tx } from '../i18n';

type Kind = Schema['ServiceKind']['kind'];
type FieldName = Schema['ServiceKind']['fields'][number];

export const serviceNames: Record<Kind, string> = {
  trakt: t('Trakt'), simkl: t('Simkl'), anilist: t('AniList'), mal: t('MyAnimeList'), kitsu: t('Kitsu'), hardcover: t('Hardcover'), steam: t('Steam'), bgg: t('BoardGameGeek'), jellyfin: t('Jellyfin'), emby: t('Emby'),
  plex: t('Plex'), audiobookshelf: t('Audiobookshelf'), lastfm: t('Last.fm'), listenbrainz: t('ListenBrainz'), koito: t('Koito'), gpodder: 'gPodder', pocketcasts: t('Pocket Casts'), stremio: t('Stremio'), psn: t('PlayStation'), xbox: t('Xbox'), storyteller: t('Storyteller'), radarr: t('Radarr'), sonarr: t('Sonarr'), mylar3: 'Mylar3',
  komga: 'Komga', kavita: 'Kavita', kapowarr: 'Kapowarr',
};

const groups: { title: string; kinds: Kind[] }[] = [
  { title: t('Watch history'), kinds: ['trakt', 'simkl', 'plex', 'jellyfin', 'emby', 'stremio'] },
  { title: t('Anime and manga'), kinds: ['anilist', 'mal', 'kitsu'] },
  { title: t('Books and music'), kinds: ['hardcover', 'audiobookshelf', 'storyteller', 'komga', 'kavita', 'lastfm', 'listenbrainz', 'koito'] },
  { title: t('Games'), kinds: ['steam', 'psn', 'xbox', 'bgg'] },
  { title: t('Podcasts'), kinds: ['pocketcasts', 'gpodder'] },
  { title: t('Wanted lists'), kinds: ['radarr', 'sonarr', 'mylar3', 'kapowarr'] },
];

const summaries: Record<Kind, string> = {
  trakt: t('Watched history and ratings'), simkl: t('Movies, shows and anime'), plex: t('Watched movies and episodes'), jellyfin: t('Played movies and episodes'), emby: t('Played movies and episodes'),
  anilist: t('Anime and manga lists'), mal: t('Anime and manga lists'), kitsu: t('Anime and manga library'), hardcover: t('Shelves, ratings and reads'), audiobookshelf: t('Listening progress'),
  steam: t('Owned games and playtime'), bgg: t('Board game collection and logged plays, with players and scores'), lastfm: t('Album scrobbles'), listenbrainz: t('Album listens'), koito: t('Listens from your Koito server'), stremio: t('Library and watched titles'), psn: t('Played games and playtime'), xbox: t('Played games'), storyteller: t('Reading positions'), gpodder: t('Subscriptions and finished episodes'), pocketcasts: t('Subscriptions and finished episodes'), radarr: t('Monitored movies as planned'), sonarr: t('Monitored series as planned'), mylar3: t('Downloaded and archived comic copies'),
  komga: t('Reading progress, read-only'), kavita: t('Reading progress, read-only'), kapowarr: t('Downloaded comic issues as copies'),
};

function fieldLabel(kind: Kind, field: FieldName): { label: string; hint?: string; optional?: boolean; secret?: boolean } {
  switch (field) {
    case 'url': return kind === 'gpodder' ? { label: t('Server address'), hint: t('Optional. Leave empty for gpodder.net.'), optional: true } : { label: t('Server address'), hint: t('For example http://192.0.2.10:8096') };
    case 'username': return kind === 'pocketcasts' || kind === 'stremio' ? { label: t('Email') } : kind === 'jellyfin' || kind === 'emby' ? { label: t('User name'), hint: t('Optional. The first administrator is used otherwise.'), optional: true } : { label: t('Username') };
    case 'user_id': return { label: t('Account ID'), hint: t('Optional. Leave empty for the server owner.'), optional: true };
    case 'steam_id': return { label: t('SteamID64'), hint: t('The 17-digit number in your profile address. Your game details must be public.') };
    case 'token':
      if (kind === 'steam') return { label: t('Steam Web API key'), hint: t('Optional when the server has one.'), optional: true, secret: true };
      if (kind === 'koito') return { label: t('API key'), hint: t('Optional. Needed when the server requires sign-in.'), optional: true, secret: true };
      if (kind === 'listenbrainz') return { label: t('User token'), hint: t('Optional. Needed only for private listens.'), optional: true, secret: true };
      if (kind === 'plex') return { label: t('Plex token'), secret: true };
      if (kind === 'psn') return { label: t('NPSSO token'), hint: t('Sign in at playstation.com, then open ca.account.sony.com/api/v1/ssocookie and copy the npsso value.'), secret: true };
      if (kind === 'xbox') return { label: t('OpenXBL API key'), hint: t('Sign in at xbl.io with your Microsoft account and create a personal API key.'), secret: true };
      if (kind === 'gpodder' || kind === 'pocketcasts' || kind === 'stremio') return { label: t('Password'), hint: t('Stored encrypted on this server and used only to sync.'), secret: true };
      if (kind === 'komga') return { label: t('API key'), hint: t('From Komga → Account settings → API keys. Pastime only reads your progress.'), secret: true };
      if (kind === 'kavita') return { label: t('Auth key'), hint: t('From Kavita → Settings → Account → API key. Pastime only reads your progress.'), secret: true };
      if (kind === 'kapowarr') return { label: t('API key'), hint: t('From Kapowarr → Settings → General.'), secret: true };
      if (kind === 'hardcover') return { label: t('API token'), hint: t('From Hardcover → Settings → API access.'), secret: true };
      return { label: t('API key'), secret: true };
  }
}

export function LinkedServicesSection() {
  const user = useUser(); const [params] = useSearchParams(); const selected = params.get('service');
  const kinds = useQuery({ queryKey: privateKey(user.id, 'service-kinds'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/service-kinds', { signal })).items, staleTime: 60_000 });
  const links = useQuery({ queryKey: privateKey(user.id, 'services'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/services', { signal })).items,
    refetchInterval: query => query.state.data?.some(l => l.last_status === 'queued' || l.last_status === 'running' || l.push_status === 'queued' || l.push_status === 'running') ? 3000 : false });
  const [linking, setLinking] = useState<Schema['ServiceKind'] | null>(null);
  if (kinds.isPending || links.isPending) return <section className="detail-section"><h2>{t('Linked services')}</h2><Loading label={t('Loading services')} /></section>;
  if (kinds.error || links.error) return <section className="detail-section"><h2>{t('Linked services')}</h2><Failed error={kinds.error || links.error} retry={() => { void kinds.refetch(); void links.refetch(); }} /></section>;
  const byKind = new Map(kinds.data.map(k => [k.kind, k]));
  const noKey = kinds.data.every(k => !k.available);
  const order = groups.flatMap(g => g.kinds);
  const linked = [...links.data].sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind));
  const unavailable = order.map(k => byKind.get(k)).filter((k): k is Schema['ServiceKind'] => !!k && !k.available && !links.data.some(l => l.kind === k.kind));
  const href = (kind: Kind) => `/integrations?section=services&service=${kind}`;
  return <section className="detail-section services-section"><h2>{t('Linked services')}</h2>
    <p className="subtle">{t('A linked account syncs on a schedule. Each sync only adds or moves things forward and never repeats a play.')}</p>
    {noKey && <p className="notice">{t('Linked services are not set up on this server. Ask your administrator to enable them.')}</p>}
    {selected && byKind.has(selected as Kind) ? <>
      <Link className="back-link" to="/integrations?section=services"><ArrowLeft size={16} aria-hidden />{t('All linked services')}</Link>
      <ServiceTile key={selected} kind={byKind.get(selected as Kind)!} link={links.data.find(l => l.kind === selected)} onLink={() => setLinking(byKind.get(selected as Kind)!)} />
    </> : <>
      {linked.length > 0 && <div className="service-block"><h3>{t('Connected')}</h3>
        <ul className="directory-list">{linked.map(link => { const running = link.last_status === 'queued' || link.last_status === 'running'; const tone = running ? 'running' : link.last_status === 'failed' ? 'failed' : link.health === 'healthy' ? 'active' : link.health.replaceAll('_', '-'); return <li key={link.id}><Link to={href(link.kind)}>
          <span><strong>{serviceNames[link.kind]}</strong><span className="subtle">{link.last_run_at ? tx('Last sync {date}', { date: <Timestamp value={link.last_run_at} /> }) : t('First sync pending')}{link.next_run_at && <> · {tx('next {date}', { date: <Timestamp value={link.next_run_at} /> })}</>}</span></span>
          <span className={`status status-${tone}`}>{running ? t('Syncing') : link.last_status === 'failed' ? t('Last sync failed') : statusText(link.health)}</span><ChevronRight aria-hidden /></Link></li>; })}</ul></div>}
      {!noKey && <div className="service-block"><h3>{t('Ready to connect')}</h3>
        {groups.map(g => { const ready = g.kinds.map(k => byKind.get(k)).filter((k): k is Schema['ServiceKind'] => !!k && k.available && !links.data.some(l => l.kind === k.kind)); return ready.length ? <div key={g.title} className="service-group-list"><h4>{g.title}</h4>
          <ul className="integration-directory">{ready.map(k => <li key={k.kind}><Link to={href(k.kind)}><strong>{serviceNames[k.kind]}</strong><small>{summaries[k.kind]}</small></Link></li>)}</ul></div> : null; })}</div>}
      {unavailable.length > 0 && <div className="service-block unavailable"><h3>{t('Not set up on this server')}</h3>
        <p className="subtle">{t('An administrator adds the keys these services need.')}</p>
        <ul className="unavailable-list">{unavailable.map(k => <li key={k.kind}><Link to={href(k.kind)}>{serviceNames[k.kind]}</Link></li>)}</ul></div>}
    </>}
    {linking && (linking.auth ? <AuthorizeDialog kind={linking} onClose={() => setLinking(null)} /> : <LinkDialog kind={linking} onClose={() => setLinking(null)} />)}
  </section>;
}

function ServiceTile({ kind, link, onLink }: { kind: Schema['ServiceKind']; link?: Schema['ServiceLink']; onLink: () => void }) {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const reload = () => Promise.all([client.invalidateQueries({ queryKey: privateKey(user.id, 'services') }), client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') })]);
  const sync = useAction(async (_: void) => { unwrap(await api.POST('/api/v1/me/services/{serviceID}/sync', { params: { path: { serviceID: link!.id } } })); await reload(); });
  const schedule = useAction(async (value: Schema['ServiceLink']['schedule']) => { unwrap(await api.PUT('/api/v1/me/services/{serviceID}/schedule', { params: { path: { serviceID: link!.id } }, body: { schedule: value } })); await reload(); });
  const unlink = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/me/services/{serviceID}', { params: { path: { serviceID: link!.id } } })); await reload(); });
  const name = serviceNames[kind.kind];
  const running = link?.last_status === 'queued' || link?.last_status === 'running';
  return <article className={`service-tile${link ? ' linked' : ''}`}>
    <header><h3>{name}</h3><p className="subtle">{link ? link.description : summaries[kind.kind]}</p></header>
    {link ? <>
      <dl className="service-facts">
        <div><dt>{t('Status')}</dt><dd><span className={`status status-${running ? 'running' : link.last_status === 'failed' ? 'failed' : 'active'}`}>{running ? t('Syncing') : link.last_status === 'failed' ? t('Last sync failed') : t('Linked')}</span></dd></div>
        <div><dt>{t('Connection health')}</dt><dd>{statusText(link.health)}</dd></div>
        {link.push_status && <div><dt>{t('Playback delivery')}</dt><dd>{statusText(link.push_status)}</dd></div>}
        <div><dt>{t('Last sync')}</dt><dd>{link.last_run_at ? <Timestamp value={link.last_run_at} /> : <span className="subtle">—</span>}</dd></div>
        <div><dt>{t('Next sync')}</dt><dd>{link.next_run_at ? <Timestamp value={link.next_run_at} /> : <span className="subtle">—</span>}</dd></div>
      </dl>
      <div className="service-actions">
        <label className="sr-only" htmlFor={`schedule-${link.id}`}>{t('Sync schedule for')} {name}</label>
        <select id={`schedule-${link.id}`} value={link.schedule} disabled={schedule.pending} onChange={e => void schedule.run(e.target.value as Schema['ServiceLink']['schedule'])}>
          <option value="two_hourly">{t('Every two hours')}</option><option value="hourly">{t('Hourly')}</option><option value="daily">{t('Daily')}</option><option value="weekly">{t('Weekly')}</option><option value="manual">{t('Manual only')}</option></select>
        <button className="button" disabled={sync.pending || running} onClick={() => void sync.run()}><RefreshCw aria-hidden className={running ? 'spin' : undefined} />{t('Sync now')}</button>
        <button className="button danger-outline" disabled={unlink.pending} onClick={async () => { if (await confirm({ title: t('Unlink {name}?', { name: name }), description: t('Saved credentials are deleted. What it imported stays; roll back its imports to remove that.'), label: t('Unlink'), danger: true })) void unlink.run(); }}><Trash2 aria-hidden />{t('Unlink')}</button>
      </div>
      <ErrorNote error={sync.error || schedule.error || unlink.error} />
    </> : <div className="service-actions">
      <button className="button primary" disabled={!kind.available} onClick={onLink}><Link2 aria-hidden />{kind.auth ? t('Sign in with {name}', { name: name }) : t('Link')}</button>
      {!kind.available && <span className="subtle">{t('Not set up on this server')}</span>}
    </div>}
  </article>;
}

function LinkDialog({ kind, onClose }: { kind: Schema['ServiceKind']; onClose: () => void }) {
  const user = useUser(); const client = useQueryClient();
  const [values, setValues] = useState<Partial<Record<FieldName, string>>>({}); const [schedule, setSchedule] = useState<Schema['ServiceLink']['schedule']>(kind.kind === 'mylar3' || kind.kind === 'kapowarr' ? 'two_hourly' : 'daily'); const [push, setPush] = useState(false);
  const canPush = kind.kind === 'jellyfin' || kind.kind === 'emby';
  const name = serviceNames[kind.kind];
  const save = useAction(async (_: void) => {
    unwrap(await api.POST('/api/v1/me/services', { body: { kind: kind.kind, schedule, config: { ...Object.fromEntries(Object.entries(values).map(([k, v]) => [k, (v ?? '').trim()])), ...(canPush ? { push } : {}) } } }));
    await Promise.all([client.invalidateQueries({ queryKey: privateKey(user.id, 'services') }), client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') })]);
    onClose();
  });
  return <Modal title={t('Link {name}', { name: name })} description={t('The first sync starts right away.')} onClose={onClose} busy={save.pending} dirty={Object.values(values).some(Boolean) || push || schedule !== (kind.kind === 'mylar3' || kind.kind === 'kapowarr' ? 'two_hourly' : 'daily')}>
    <form onSubmit={submit(() => void save.run())}><fieldset disabled={save.pending}>
      {kind.fields.map(f => { const meta = fieldLabel(kind.kind, f); return <Field key={f} label={meta.label} hint={meta.hint}>{(id, hint) =>
        meta.secret ? <PasswordInput id={id} aria-describedby={hint} required={!meta.optional} maxLength={1000} autoComplete="off" spellCheck={false} value={values[f] ?? ''} onChange={e => setValues(v => ({ ...v, [f]: e.target.value }))} /> : <input id={id} aria-describedby={hint} required={!meta.optional} maxLength={1000} type={f === 'url' ? 'url' : 'text'} autoComplete="off" spellCheck={false} value={values[f] ?? ''} onChange={e => setValues(v => ({ ...v, [f]: e.target.value }))} />}</Field>; })}
      {canPush && <label className="check-row"><input type="checkbox" checked={push} onChange={e => setPush(e.target.checked)} /><span>{t('Also mark what I watch here as played on {name}', { name })}<span className="subtle"> · {t('starting from now')}</span></span></label>}
      <Field label={t('Sync')}>{id => <select id={id} value={schedule} onChange={e => setSchedule(e.target.value as typeof schedule)}><option value="two_hourly">{t('Every two hours')}</option><option value="hourly">{t('Hourly')}</option><option value="daily">{t('Daily')}</option><option value="weekly">{t('Weekly')}</option><option value="manual">{t('Manual only')}</option></select>}</Field>
    </fieldset><ErrorNote error={save.error} />
    <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{save.pending ? t('Linking…') : t('Link {name}', { name: name })}</button></div></form>
  </Modal>;
}

function AuthorizeDialog({ kind, onClose }: { kind: Schema['ServiceKind']; onClose: () => void }) {
  const user = useUser(); const client = useQueryClient();
  const name = serviceNames[kind.kind];
  const [auth, setAuth] = useState<Schema['ServiceAuthorization'] | null>(null);
  const needsAddress = kind.fields.includes('url'); const [address, setAddress] = useState(''); const [started, setStarted] = useState(!needsAddress);
  const begin = useAction(async (_: void) => setAuth(unwrap(await api.POST('/api/v1/me/service-authorizations', { body: { kind: kind.kind as Schema['ServiceAuthorizationInput']['kind'], ...(needsAddress ? { url: address.trim() } : {}) } }))));
  useEffect(() => { if (started) void begin.run(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [started]);
  useEffect(() => {
    if (!auth || auth.status !== 'pending') return;
    const timer = window.setTimeout(async () => {
      try {
        const next = unwrap(await api.POST('/api/v1/me/service-authorizations/{authorizationID}/poll', { params: { path: { authorizationID: auth.id } } }));
        setAuth(next);
        if (next.status === 'linked') { await Promise.all([client.invalidateQueries({ queryKey: privateKey(user.id, 'services') }), client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') })]); onClose(); }
      } catch { setAuth(a => a && { ...a }); }
    }, Math.max(auth.interval, 5) * 1000);
    return () => window.clearTimeout(timer);
  }, [auth, client, onClose, user.id]);
  return <Modal title={t('Sign in with {name}', { name: name })} description={t('Approve access on {name}; this window finishes on its own.', { name: name })} onClose={onClose}>
    {!started ? <form onSubmit={submit(() => setStarted(true))}>
        <Field label={t('Server address')} hint={t('The address you open Storyteller at, such as https://books.example.com.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="url" required maxLength={500} value={address} onChange={e => setAddress(e.target.value)} autoFocus />}</Field>
        <div className="dialog-actions"><button className="button primary" type="submit">{t('Continue')}</button></div></form>
      : !auth ? (begin.error ? <><ErrorNote error={begin.error} />{needsAddress && <div className="dialog-actions"><button className="button" onClick={() => { begin.reset(); setStarted(false); }}>{t('Change address')}</button></div>}</> : <Loading label={t('Starting sign-in')} />)
      : auth.status === 'expired' ? <><p>{t('The code expired.')}</p><div className="dialog-actions"><button className="button primary" onClick={() => { setAuth(null); void begin.run(); }}>{t('Get a new code')}</button></div></>
      : <div className="device-code">
        <ol className="setup-steps"><li>{t('Open')} <a className="text-link" href={auth.verification_url} target="_blank" rel="noreferrer">{auth.verification_url.replace(/^https?:\/\//, '')}<ExternalLink aria-hidden className="inline-icon" /></a></li><li>{t('Enter this code and approve access.')}</li></ol>
        <output className="device-code-value" aria-label={t('Sign-in code')}>{auth.user_code}</output>
        <p className="subtle" role="status">{t('Waiting for approval…')}</p>
      </div>}
  </Modal>;
}
