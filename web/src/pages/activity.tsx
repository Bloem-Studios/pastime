import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { useInfiniteQuery } from '@tanstack/react-query';
import { ChevronDown } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { Cover } from '../media';
import { Empty, Failed, Loading, LoadMore, PageTitle, statusText } from '../ui';
import { clock, clockRange, localDay } from '../format';
import { locale, plural, t } from '../i18n';
import { activityRows, previousEvents, type ActivityRow } from '../statsPolicy';
import './activity.css';

type Event = Schema['ActivityEvent'];
type Row = ActivityRow<Event>;

// Product names stay as they are; the two generic sources read as words.
const sourceNames: Record<string, string> = {
  trakt: 'Trakt', letterboxd: 'Letterboxd', imdb: 'IMDb', goodreads: 'Goodreads', storygraph: 'StoryGraph', anilist: 'AniList', native: t('a Pastime export'), yamtrack: 'Yamtrack',
  grouvee: 'Grouvee', tvtime: 'TV Time', csv: t('a spreadsheet'), clz: 'CLZ', wetrakr: 'WeTrakr', playnite: 'Playnite', simkl: 'Simkl', mal: 'MyAnimeList', kitsu: 'Kitsu', hardcover: 'Hardcover',
  steam: 'Steam', bgg: 'BoardGameGeek', jellyfin: 'Jellyfin', emby: 'Emby', plex: 'Plex', audiobookshelf: 'Audiobookshelf', lastfm: 'Last.fm', listenbrainz: 'ListenBrainz', koito: 'Koito',
  gpodder: 'gPodder', pocketcasts: 'Pocket Casts', stremio: 'Stremio', psn: 'PlayStation', xbox: 'Xbox', storyteller: 'Storyteller', radarr: 'Radarr', sonarr: 'Sonarr', mylar3: 'Mylar3',
  komga: 'Komga', kavita: 'Kavita', kapowarr: 'Kapowarr',
};

const dayName = (day: string) => {
  const date = new Date(`${day}T00:00:00`);
  return date.toLocaleDateString(locale, date.getFullYear() === new Date().getFullYear() ? { weekday: 'long', month: 'long', day: 'numeric' } : { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
};

function amount(n: number, unit: Event['unit']) {
  switch (unit) {
    case 'episodes': return plural(n, '{n} episode', '{n} episodes');
    case 'pages': return plural(n, '{n} page', '{n} pages');
    case 'chapters': return plural(n, '{n} chapter', '{n} chapters');
    case 'tracks': return plural(n, '{n} track', '{n} tracks');
    case 'items': return plural(n, '{n} item', '{n} items');
    case 'minutes': return plural(n, '{n} minute', '{n} minutes');
  }
}

/** What changed, in a few words. `before` is the title's previous event, when it is loaded. */
function describe(e: Event, before?: Event): string {
  // Watched minutes of a film are not progress anyone reads; counts of episodes and pages are.
  const progress = e.progress > 0 && e.unit !== 'minutes' ? amount(e.progress, e.unit) : '';
  switch (e.kind) {
    case 'added': return [t('Added to library'), e.status !== 'planned' ? statusText(e.status) : ''].filter(Boolean).join(' · ');
    case 'progress':
      if (before && before.status !== e.status) return [`${statusText(before.status)} → ${statusText(e.status)}`, progress].filter(Boolean).join(' · ');
      return [statusText(e.status), progress].filter(Boolean).join(' · ');
    case 'updated': return t('Edited details');
    case 'restarted': return t('Started again');
    case 'unplayed': return [t('Removed a play'), progress].filter(Boolean).join(' · ');
    default: return statusText(e.status);
  }
}

export function ActivityPage() {
  const user = useUser();
  const query = useInfiniteQuery({ queryKey: privateKey(user.id, 'activity', 'log'), initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => unwrap(await api.GET('/api/v1/activity', { signal, params: { query: { cursor: pageParam, limit: 100 } } })),
    getNextPageParam: (last: Schema['ActivityPage']) => last.next_cursor ?? undefined });
  const events = useMemo(() => query.data?.pages.flatMap(p => p.items) ?? [], [query.data]);
  const before = useMemo(() => previousEvents(events), [events]);
  const days = useMemo(() => {
    const byDay = new Map<string, Event[]>();
    for (const e of events) { if (e.kind === 'played') continue; const d = localDay(e.created_at); byDay.set(d, [...(byDay.get(d) ?? []), e]); }
    return [...byDay].map(([day, list]) => ({ day, count: list.length, rows: activityRows(list) }));
  }, [events]);
  const shown = days.reduce((n, d) => n + d.rows.length, 0);
  // Plays are left out, so a page can come back nearly empty: read on a little before showing it.
  const pages = query.data?.pages.length ?? 0;
  useEffect(() => { if (query.hasNextPage && !query.isFetchingNextPage && !query.isFetchNextPageError && shown < 20 && pages < 5) void query.fetchNextPage(); }, [query, shown, pages]);
  return <div className="activity-page">
    <PageTitle title={t('Activity')} description={t('What you added and changed in your library, by day. Plays are in History.')} />
    {query.isPending ? <Loading label={t('Loading activity')} />
      : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
      : days.length === 0 ? (query.hasNextPage || query.isFetchingNextPage ? <Loading label={t('Loading activity')} /> : <Empty title={t('No changes yet')} action={<Link className="button" to="/history">{t('Open History')}</Link>}>{t('Titles you add, and the status and progress you set, appear here by day.')}</Empty>)
      : <><ol className="activity-days">{days.map(d => <li key={d.day} className="activity-day">
          <div className="activity-day-head"><h2>{dayName(d.day)}</h2><span className="num">{plural(d.count, '{n} change', '{n} changes')}</span></div>
          <ul className="activity-rows">{d.rows.map(r => <ActivityLine key={r.key} row={r} before={before} />)}</ul>
        </li>)}</ol>
        <LoadMore available={!!query.hasNextPage} pending={query.isFetchingNextPage} onLoad={() => void query.fetchNextPage()} /></>}
  </div>;
}

function Poster({ e }: { e: Event }) {
  return <Cover entry={{ title: e.title, media_type: e.media_type, image_url: e.image_url }} compact />;
}

function ActivityLine({ row, before }: { row: Row; before: Map<string, Event> }) {
  const [open, setOpen] = useState(false);
  const newest = row.events[0], oldest = row.events[row.events.length - 1];
  const name = (e: Event) => e.localized_title ?? e.title;
  if (row.kind === 'event') {
    const e = newest;
    return <li className="activity-row">
      <Link to={`/library/${e.entry_id}`} className="activity-cover" tabIndex={-1} aria-hidden="true"><Poster e={e} /></Link>
      <p className="activity-line"><Link to={`/library/${e.entry_id}`} className="activity-title">{name(e)}</Link> <span className="activity-change">{describe(e, before.get(e.id))}</span></p>
      <time className="activity-time num" dateTime={e.created_at}>{clock(e.created_at)}</time>
      <span />
    </li>;
  }
  // A burst or an import: one row, the titles behind a disclosure.
  const titles: Event[] = []; const seen = new Set<string>();
  for (const e of [...row.events].reverse()) if (!seen.has(e.entry_id)) { seen.add(e.entry_id); titles.push(e); }
  const added = titles.filter(e => row.events.some(x => x.entry_id === e.entry_id && x.kind === 'added')).length;
  const source = row.source ? sourceNames[row.source] ?? row.source : '';
  const label = row.kind === 'import'
    ? added ? plural(added, 'Imported {n} title from {source}', 'Imported {n} titles from {source}', { source }) : plural(titles.length, 'Updated {n} title from {source}', 'Updated {n} titles from {source}', { source })
    : plural(titles.length, 'Added {n} title', 'Added {n} titles');
  const names = titles.slice(0, 2).map(name).join(', ');
  const preview = titles.length > 2 ? t('{titles} and {count} more', { titles: names, count: (titles.length - 2).toLocaleString(locale) }) : names;
  return <li className="activity-row bulk">
    <span className="activity-stack" aria-hidden="true">{titles.slice(0, 3).map(e => <Poster key={e.id} e={e} />)}</span>
    <p className="activity-line"><span className="activity-title">{label}</span> <span className="activity-change">{preview}</span></p>
    <time className="activity-time num" dateTime={newest.created_at}>{clockRange(oldest.created_at, newest.created_at)}</time>
    <button className="icon-button" aria-expanded={open} aria-label={open ? t('Hide the titles') : t('Show the titles')} onClick={() => setOpen(!open)}><ChevronDown aria-hidden className={open ? 'flip' : ''} /></button>
    {open && <ul className="activity-sub">{titles.map(e => <li key={e.id}>
      <Link to={`/library/${e.entry_id}`} className="activity-cover" tabIndex={-1} aria-hidden="true"><Poster e={e} /></Link>
      <p className="activity-line"><Link to={`/library/${e.entry_id}`} className="activity-title">{name(e)}</Link> <span className="activity-change">{e.kind === 'added' ? statusText(e.status) : describe(e)}</span></p>
    </li>)}</ul>}
  </li>;
}
