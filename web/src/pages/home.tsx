import { SourceLink } from '../navigation';
import { useEffect, useState } from 'react';
import { calendarIsEpisode, calendarReleaseKey, calendarTitlePath, visibleCalendarReleases } from '../calendarPolicy';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Check, Info, Play, Search, Upload } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { usePreferences } from '../organize';
import { SiloForYouLink } from '../siloConnect';
import { Backdrop, CardProgress, entryPath, lastPlayedLabel, PosterCard, progressLabel, ProgressModal, Shelf, typeLabel, meterValue } from '../media';
import { arrangeHome, groupReleases } from '../browsePolicy';
import { episodeCode, episodeRange } from '../format';
import { ReviewBanner } from './insights';
import { ErrorNote, Failed, Loading, PageTitle, useAction } from '../ui';
import { locale, t } from '../i18n';

type Release = Schema['CalendarRelease'];

/** Home answers "what do I watch next?": the title played last, then the rest of what is in reach. */
export function Home() {
  const user = useUser(); const { prefs, loaded } = usePreferences();
  const [now, setNow] = useState(Date.now);
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 60_000); return () => window.clearInterval(timer); }, []);
  const query = useQuery({ refetchInterval: 60000, queryKey: privateKey(user.id, 'home'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/home', { signal })) });
  useEffect(() => { document.title = t('Home') + ' · Pastime'; }, []);
  if (query.isPending) return <div className="hero-empty"><Loading label={t('Loading your home')} /></div>;
  if (query.error) return <div className="hero-empty"><h1>{t('Home')}</h1><Failed error={query.error} retry={() => void query.refetch()} /></div>;
  const home = { ...query.data, upcoming: visibleCalendarReleases(query.data.upcoming, now) };
  if (home.stats.total === 0 && home.upcoming.length === 0 && !loaded) return <div className="hero-empty"><Loading label={t('Loading your home')} /></div>;
  if (home.stats.total === 0 && home.upcoming.length === 0) return prefs.onboarded ? <div className="hero-empty">
    <h1 tabIndex={-1}>{t('Your library starts here')}</h1>
    <p>{t('Search films, shows, books, games, music and podcasts, then follow your progress in one place.')}</p>
    <div className="actions"><Link className="button primary" to="/discover"><Search aria-hidden />{t('Find something to track')}</Link><Link className="button" to="/integrations?section=transfer"><Upload aria-hidden />{t('Import from another service')}</Link></div>
    <SiloForYouLink /></div> : <Welcome />;
  const arranged = arrangeHome(home);
  const rows = prefs.home_rows?.length ? prefs.home_rows : (['up_next', 'continue', 'upcoming', 'planned', 'recently_finished'] as const).map(kind => ({ kind, visible: true }));
  const shelf = (row: Schema['HomeRow']) => {
    switch (row.kind) {
      case 'up_next': return arranged.upNext.length > 0 && <Shelf key="up_next" title={t('Up next')} wide>{arranged.upNext.map(u => <UpNextCard key={u.entry.id} item={u} />)}</Shelf>;
      case 'continue': return arranged.continue.length > 0 && <Shelf key="continue" title={t('Continue')} link={{ to: '/library?status=in_progress', label: t('View all') }}>{arranged.continue.map(entry => <PosterCard key={entry.id} entry={entry} />)}</Shelf>;
      case 'upcoming': return home.upcoming.length > 0 && <Shelf key="upcoming" title={t('Coming soon')} link={{ to: '/calendar', label: t('View all') }} wide>{groupReleases(home.upcoming).map(group => <ReleaseCard key={calendarReleaseKey(group[0])} releases={group} />)}</Shelf>;
      case 'planned': return home.planned.length > 0 && <Shelf key="planned" title={t('On your list')} link={{ to: '/library?status=planned', label: t('View all') }}>{home.planned.map(entry => <PosterCard key={entry.id} entry={entry} />)}</Shelf>;
      case 'recently_finished': return home.recently_finished.length > 0 && <Shelf key="recently_finished" title={t('Recently finished')} link={{ to: '/library?status=completed&sort=activity', label: t('View all') }}>{home.recently_finished.map(entry => <PosterCard key={entry.id} entry={entry} />)}</Shelf>;
      case 'list': return row.list_id ? <ListShelf key={`list-${row.list_id}`} listID={row.list_id} /> : null;
      case 'mix': return row.id ? <MixShelf key={`mix-${row.id}`} row={row} /> : null;
    }
  };
  return <div className="home-page">
    {arranged.featured ? <Hero entry={arranged.featured} next={arranged.featuredNext} />
      : <div className="home-overview"><PageTitle title={t('Home')} actions={<Link className="button" to="/library">{t('Browse library')}</Link>} /></div>}
    <SiloForYouLink />
    <ReviewBanner />
    {rows.filter(r => r.visible).map(shelf)}
  </div>;
}

const interests: [string, string, string[]][] = [
  ['watch', t('Films and TV'), ['trakt', 'letterboxd', 'imdb']], ['anime', t('Anime and manga'), ['anilist']], ['read', t('Books and comics'), ['goodreads', 'storygraph']],
  ['play', t('Games'), []], ['listen', t('Music and podcasts'), []],
];
const sourceNames: Record<string, string> = { trakt: t('Trakt'), letterboxd: t('Letterboxd'), imdb: t('IMDb'), anilist: t('AniList'), goodreads: t('Goodreads'), storygraph: t('StoryGraph') };

function Welcome() {
  const { prefs, save } = usePreferences();
  const [step, setStep] = useState(0); const [picked, setPicked] = useState<string[]>(['watch']);
  const finish = async (to?: string) => { await save.run({ ...prefs, onboarded: true }); if (to) window.location.assign(to); };
  const sources = [...new Set(interests.filter(i => picked.includes(i[0])).flatMap(i => i[2]))];
  const steps = [t('What you follow'), t('Bring your history'), t('Record automatically')];
  return <div className="welcome">
    <h1 tabIndex={-1}>{t('Let’s set up your library')}</h1>
    <ol className="stepper" aria-label={t('Setup steps')}>{steps.map((label, i) => <li key={label} aria-current={i === step ? 'step' : undefined} className={i < step ? 'done' : ''}><span>{i + 1}</span>{label}</li>)}</ol>
    <section className="panel welcome-step">
      {step === 0 && <><h2>{t('What do you want to keep track of?')}</h2><p className="subtle">{t('This only shapes the suggestions on the next steps.')}</p>
        <div className="kind-cards">{interests.map(([v, l]) => <button key={v} type="button" role="checkbox" aria-checked={picked.includes(v)} onClick={() => setPicked(p => p.includes(v) ? p.filter(x => x !== v) : [...p, v])}><strong>{l}</strong></button>)}</div></>}
      {step === 1 && <><h2>{t('Bring your history with you')}</h2><p className="subtle">{t('Imports only add. Nothing is downgraded, and running one again adds nothing new.')}</p>
        {sources.length ? <div className="kind-cards">{sources.map(src => <Link key={src} to={`/integrations?source=${src}`} onClick={() => void save.run({ ...prefs, onboarded: true })}><strong>{sourceNames[src]}</strong><span className="subtle">{src === 'goodreads' || src === 'storygraph' ? t('Import your shelves') : t('Import your history')}</span></Link>)}</div>
          : <p className="subtle">{t('No imports for these yet. You can add titles from Discover.')}</p>}</>}
      {step === 2 && <><h2>{t('Record what you watch automatically')}</h2><p className="subtle">{t('Connect a media server and finished episodes and films are marked for you.')}</p>
        <div className="kind-cards">{[[t('Plex, Jellyfin or Emby'), t('Paste a webhook into your server.'), '/integrations?section=webhooks'], [t('Silo'), t('Create a key on the Connect Silo page.'), '/integrations?section=silo']].map(([label, d, to]) => <Link key={label} to={to} onClick={() => void save.run({ ...prefs, onboarded: true })}><strong>{label}</strong><span className="subtle">{d}</span></Link>)}</div></>}
      <ErrorNote error={save.error} />
      <div className="actions welcome-actions">
        {step > 0 && <button className="button ghost" onClick={() => setStep(step - 1)}>{t('Back')}</button>}
        <button className="text-button" disabled={save.pending} onClick={() => void finish()}>{t('Skip setup')}</button>
        {step < 2 ? <button className="button primary" onClick={() => setStep(step + 1)}>{t('Continue')}</button> : <button className="button primary" disabled={save.pending} onClick={() => void finish('/discover')}><Search aria-hidden />{t('Find something to track')}</button>}
      </div>
    </section>
  </div>;
}

function ListShelf({ listID }: { listID: string }) {
  const user = useUser();
  const list = useQuery({ queryKey: privateKey(user.id, 'list', listID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists/{listID}', { signal, params: { path: { listID } } })), retry: false });
  const items = useQuery({ queryKey: privateKey(user.id, 'list-entries', listID, 'home'), enabled: !!list.data, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { list_id: listID, limit: 20 } } })).items });
  if (!list.data || !items.data?.length) return null;
  return <Shelf title={list.data.name} link={{ to: `/lists/${listID}`, label: t('View all') }}>{items.data.map(entry => <PosterCard key={entry.id} entry={entry} />)}</Shelf>;
}

/** A mix row: library titles across the chosen media types and statuses. */
function MixShelf({ row }: { row: Schema['HomeRow'] }) {
  const user = useUser();
  const types = (row.media_types ?? []).join(','); const statuses = (row.statuses ?? []).join(',');
  const items = useQuery({ queryKey: privateKey(user.id, 'home-mix', row.id, types, statuses, row.sort, row.limit),
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { media_type: types, status: statuses || undefined, sort: row.sort, limit: row.limit ?? 20 } } })).items });
  if (!items.data?.length) return null;
  const link = `/library?${new URLSearchParams({ media_type: types, ...(statuses ? { status: statuses } : {}), ...(row.sort ? { sort: row.sort } : {}) })}`;
  return <Shelf title={row.title ?? ''} link={{ to: link, label: t('View all') }}>{items.data.map(entry => <PosterCard key={entry.id} entry={entry} />)}</Shelf>;
}

/** Records a play of the next episode, then refreshes what depends on it. */
function useMarkWatched(entryID: string, episode: Schema['Release'] | undefined, onDone?: () => void) {
  const refresh = useRefresh();
  return useAction(async (_: void, key) => {
    if (!episode) return;
    const current = unwrap(await api.GET('/api/v1/library/{entryID}', { params: { path: { entryID } } }));
    unwrap(await api.POST('/api/v1/library/{entryID}/plays', { params: { path: { entryID }, header: keyHeader(key) }, body: { version: current.version, items: [{ catalog_id: episode.catalog_id }] } }));
    onDone?.(); await refresh();
  });
}

/** The title played last: its next step, where it stands, and when it was last watched. */
function Hero({ entry, next }: { entry: Schema['EntrySummary']; next?: Schema['UpNext'] }) {
  const [editing, setEditing] = useState(false);
  const mark = useMarkWatched(entry.id, next?.episode);
  const art = entry.backdrop_url || entry.image_url;
  const value = meterValue(entry);
  const code = next ? episodeCode(next.episode.season_number, next.episode.episode_number) : '';
  return <section className="home-hero" aria-labelledby="hero-title">
    {art && <div className={`home-hero-art ${entry.backdrop_url ? '' : 'poster'}`} aria-hidden="true"><img src={art} alt="" decoding="async" fetchPriority="high" referrerPolicy="no-referrer" /></div>}
    <div className="home-hero-body">
      <h1 id="hero-title" tabIndex={-1}>{entry.localized_title ?? entry.title}</h1>
      <p className="home-hero-next">{next ? [code, next.episode.title].filter(Boolean).join(' · ') : value !== undefined ? typeLabel(entry.media_type) : progressLabel(entry)}</p>
      <div className="home-hero-progress">
        {value !== undefined && <span className="card-track" aria-hidden="true"><span style={{ width: `${value}%` }} /></span>}
        <span>{[value !== undefined ? progressLabel(entry) : typeLabel(entry.media_type), entry.last_played_at ? lastPlayedLabel(entry.media_type, entry.last_played_at) : ''].filter(Boolean).join(' · ')}</span>
      </div>
      <div className="actions">
        {next
          ? <button className="button primary" disabled={mark.pending} onClick={() => void mark.run()}><Check aria-hidden />{mark.pending ? t('Saving…') : t('Mark {seasonNumber} watched', { seasonNumber: code })}</button>
          : <button className="button primary" onClick={() => setEditing(true)}><Play aria-hidden />{t('Update progress')}</button>}
        <SourceLink className="button" to={entryPath(entry)}><Info aria-hidden />{t('Details')}</SourceLink>
      </div>
      <ErrorNote error={mark.error} />
    </div>
    {editing && <ProgressModal entry={entry} onClose={() => setEditing(false)} />}
  </section>;
}

function UpNextCard({ item }: { item: Schema['UpNext'] }) {
  const [done, setDone] = useState(false);
  const mark = useMarkWatched(item.entry.id, item.episode, () => setDone(true));
  const code = episodeCode(item.episode.season_number, item.episode.episode_number);
  const to = `/title/${item.episode.title_catalog_id}`;
  return <article className="backdrop-card">
    <Link to={to} className="art" tabIndex={-1} aria-hidden="true"><Backdrop title={item.entry.title} backdrop={item.entry.backdrop_url} poster={item.entry.image_url} /></Link>
    <CardProgress entry={item.entry} />
    <div className="card-text"><Link to={to} className="card-title media-title">{item.entry.localized_title ?? item.entry.title}</Link><span className="card-meta">{[code, item.episode.title].filter(Boolean).join(' · ')}</span></div>
    <button className="button small card-action" disabled={mark.pending || done} onClick={() => void mark.run()} aria-label={t('Mark {title} {label} watched', { title: item.entry.title, label: code })}><Check aria-hidden />{done ? t('Watched') : mark.pending ? t('Saving…') : t('Mark watched')}</button>
    <ErrorNote error={mark.error} />
  </article>;
}

const releaseTypes: Record<string, string> = { theatrical: t('In cinemas'), digital: t('Digital release'), physical: t('Disc release') };
const releaseDay = (iso: string) => new Date(iso + 'T00:00:00').toLocaleDateString(locale, { weekday: 'short', month: 'short', day: 'numeric' });

/** One show's coming releases: the first date, and the episodes as one range. */
function ReleaseCard({ releases }: { releases: Release[] }) {
  const first = releases[0], last = releases[releases.length - 1];
  const path = calendarTitlePath(first);
  const what = calendarIsEpisode(first)
    ? releases.length > 1 ? episodeRange(first, last) : [episodeCode(first.season_number, first.episode_number), first.title].filter(Boolean).join(' · ')
    : first.release_type ? releaseTypes[first.release_type] : typeLabel(first.media_type);
  const art = <Backdrop title={first.entry_title} backdrop={first.backdrop_url || first.image_url} poster={first.image_url} />;
  return <article className="backdrop-card">
    {path ? <Link to={path} className="art" tabIndex={-1} aria-hidden="true">{art}</Link> : <div className="art" aria-hidden="true">{art}</div>}
    <div className="card-text">{path ? <Link to={path} className="card-title media-title">{first.entry_title}</Link> : <span className="card-title media-title">{first.entry_title}</span>}
      <span className="card-meta"><time dateTime={first.date}>{releases.length > 1 ? t('From {date}', { date: releaseDay(first.date) }) : releaseDay(first.date)}</time> · {what}</span></div>
  </article>;
}
