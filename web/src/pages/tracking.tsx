/**
 * Shared pieces of the two pages about one title: the item page (/library/:id, your record of it)
 * and the title page (/title/:id, the title itself). Both answer "where am I with this title, and
 * what do I do next?" with the same header and progress panel.
 */
import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useInfiniteQuery, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Minus, Plus, RotateCcw, CircleCheck } from 'lucide-react';
import { api, keyHeader, unwrap, type Entry, type Schema } from '../api/client';
import { privateKey, useRefresh, useUnsaved, useUser } from '../state';
import { Cover, statuses, unitLabel } from '../media';
import { rating, usePreferences, useRatingsOn } from '../organize';
import { ErrorNote, submit, useAction, useConfirm } from '../ui';
import { CheckInButton } from '../checkins';
import { duration, episodeCode } from '../format';
import { locale, plural, t } from '../i18n';
import './item.css';

export type Item = Schema['CatalogItem'];

/** Kinds whose catalog record has children (seasons and episodes, issues, tracks). */
export const hierarchical = new Set(['series', 'podcast', 'comic', 'music']);
/** Today in the person's time zone, as YYYY-MM-DD. */
export const today = () => new Date().toLocaleDateString('en-CA');
export const isAired = (e: Pick<Item, 'release_date'>) => !e.release_date || e.release_date <= today();

/** A calendar date in words: "Sep 13" this year, "Sep 13, 2025" otherwise (or always, with `year`). */
export function dayText(value: string, year = false) {
  const d = value.length === 10 ? new Date(value + 'T00:00:00') : new Date(value);
  const withYear = year || d.getFullYear() !== new Date().getFullYear();
  return d.toLocaleDateString(locale, withYear ? { month: 'short', day: 'numeric', year: 'numeric' } : { month: 'short', day: 'numeric' });
}

/** A catalog value such as "Returning Series" in sentence case. */
export const sentence = (value: string) => value ? value.charAt(0).toUpperCase() + value.slice(1).toLowerCase() : value;

const watching = new Set(['movie', 'series', 'anime', 'video']);
const reading = new Set(['book', 'manga', 'comic']);
/** "Watched", "Read" or "Finished", by what kind of title it is. */
export const finishedVerb = (type: string) => watching.has(type) ? 'watched' : reading.has(type) ? 'read' : 'finished';
export function markLabel(type: string) { const v = finishedVerb(type); return v === 'watched' ? t('Mark watched') : v === 'read' ? t('Mark read') : t('Mark finished'); }

const byNumber = (a: Item, b: Item) => (a.season_number ?? 0) - (b.season_number ?? 0) || (a.episode_number ?? 0) - (b.episode_number ?? 0);
/** Regular seasons in order, then specials (season 0). */
export const seasonOrder = (a: Item, b: Item) => ((a.season_number ?? 0) === 0 ? 1 : 0) - ((b.season_number ?? 0) === 0 ? 1 : 0) || (a.season_number ?? 0) - (b.season_number ?? 0);

function childrenQuery(id: string) {
  return { queryKey: ['catalog-children', id], staleTime: 300_000,
    queryFn: async ({ signal }: { signal: AbortSignal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}/children', { signal, params: { path: { catalogID: id } } })).items };
}

export type EpisodeIndex = { seasons: Item[]; flat: Item[]; bySeason: Map<string, Item[]>; episodes: Item[]; byID: Map<string, Item>; pending: boolean; error: unknown; empty: boolean; retry: () => void };

/** Every child of a title: for a show, its seasons and all their episodes, loaded together. */
export function useEpisodes(root: Pick<Item, 'id' | 'kind'> | null): EpisodeIndex | null {
  const enabled = !!root && hierarchical.has(root.kind);
  const top = useQuery({ ...childrenQuery(root?.id ?? ''), enabled });
  const seasons = useMemo(() => (top.data ?? []).filter(c => c.kind === 'season').sort(seasonOrder), [top.data]);
  const inner = useQueries({ queries: seasons.map(s => childrenQuery(s.id)) });
  const stamp = inner.map(q => q.dataUpdatedAt).join();
  const index = useMemo(() => {
    const bySeason = new Map<string, Item[]>();
    seasons.forEach((s, i) => bySeason.set(s.id, [...(inner[i]?.data ?? [])].sort(byNumber)));
    const flat = (top.data ?? []).filter(c => c.kind !== 'season');
    const episodes = [...flat, ...[...bySeason.values()].flat()];
    return { bySeason, flat, episodes, byID: new Map(episodes.map(e => [e.id, e])) };
  }, [top.data, seasons, stamp]);
  if (!enabled) return null;
  const failed = inner.find(q => q.error);
  return { seasons, ...index, pending: top.isPending || inner.some(q => q.isPending), error: top.error ?? failed?.error ?? null, empty: top.isSuccess && top.data.length === 0,
    retry: () => { void top.refetch(); inner.forEach(q => { if (q.error) void q.refetch(); }); } };
}

/** What is done in the current cycle: played catalog records (to their play id) and skipped ones. */
export function useMarks(entry: Entry | null) {
  const user = useUser(); const enabled = !!entry?.catalog_id;
  const plays = useQuery({ queryKey: privateKey(user.id, 'plays', entry?.id, entry?.cycle), enabled,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/plays', { signal, params: { path: { entryID: entry!.id } } })).items });
  const skipped = useQuery({ queryKey: privateKey(user.id, 'plays', 'skipped', entry?.id, entry?.cycle), enabled,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/skipped', { signal, params: { path: { entryID: entry!.id } } })).items });
  const marks = useMemo(() => {
    const m = new Map<string, string>();
    for (const p of plays.data ?? []) m.set(p.catalog_id, p.id);
    for (const id of skipped.data ?? []) if (!m.has(id)) m.set(id, 'skipped');
    return m;
  }, [plays.data, skipped.data]);
  return { marks };
}

/**
 * Where a show stands: episodes watched of those aired, what is announced, and the next one. The
 * server keeps the next episode, following the chosen episode order and skips.
 */
function showProgress(entry: Entry, index: EpisodeIndex | null) {
  const regular = (index?.episodes ?? []).filter(e => e.kind === 'episode' && (e.season_number ?? 1) > 0);
  if (!regular.length) return null;
  const aired = regular.filter(isAired).length;
  return { watched: entry.progress, aired: Math.max(aired, entry.progress), announced: regular.length - aired, next: entry.next_item };
}

/** "9 aired, 7 announced" for a show's regular episodes; empty when none are listed. */
export function episodeCount(index: EpisodeIndex | null) {
  const regular = (index?.episodes ?? []).filter(e => e.kind === 'episode' && (e.season_number ?? 1) > 0);
  const aired = regular.filter(isAired).length;
  if (!regular.length) return '';
  return regular.length > aired ? t('{aired} aired, {more} announced', { aired: aired.toLocaleString(locale), more: (regular.length - aired).toLocaleString(locale) }) : aired.toLocaleString(locale);
}

/** The entry as it is now; every write fences on its version. */
export async function latestEntry(entryID: string) { return unwrap(await api.GET('/api/v1/library/{entryID}', { params: { path: { entryID } } })); }

/** The title's header: cover, name and the facts that identify it. */
export function TitleHead({ title, original, cover, backdrop, facts, links, actions }: { title: string; original?: string | null; cover: { title: string; media_type: string; image_url?: string }; backdrop?: string; facts: (string | number | null | undefined | false)[]; links?: ReactNode; actions?: ReactNode }) {
  useEffect(() => { document.title = `${title} · Pastime`; }, [title]);
  const line = facts.filter(Boolean).join(' · ');
  return <header className={`title-head${backdrop ? ' has-art' : ''}`}>
    {backdrop && <div className="title-head-art" aria-hidden="true"><img src={backdrop} alt="" decoding="async" fetchPriority="high" referrerPolicy="no-referrer" /></div>}
    <Cover entry={cover} />
    <div className="title-head-body">
      <h1 tabIndex={-1}>{title}</h1>
      {original && original !== title && <p className="title-head-original" lang="">{original}</p>}
      {line && <p className="title-head-facts">{line}</p>}
      {links && <div className="title-head-links">{links}</div>}
    </div>
    {actions && <div className="title-head-actions">{actions}</div>}
  </header>;
}

/**
 * The progress panel: how far along you are, the next thing to watch or read with its one
 * primary action, and the status, rating and progress controls.
 */
export function TrackingPanel({ entry, item, index, extra }: { entry: Entry; item: Item | null; index: EpisodeIndex | null; extra?: ReactNode }) {
  const refresh = useRefresh(); const confirm = useConfirm(); const ratingsOn = useRatingsOn();
  const show = item?.kind === 'series' ? showProgress(entry, index) : null;
  const play = useAction(async (catalogID: string, key) => {
    const current = await latestEntry(entry.id);
    unwrap(await api.POST('/api/v1/library/{entryID}/plays', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: current.version, items: [{ catalog_id: catalogID }] } }));
    await refresh();
  });
  const restart = useAction(async (_: void, key) => {
    const current = await latestEntry(entry.id);
    unwrap(await api.POST('/api/v1/library/{entryID}/restart', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: current.version } }));
    await refresh();
  });
  const verb = finishedVerb(entry.media_type);
  const completed = entry.status === 'completed';
  const single = !hierarchical.has(entry.media_type) && entry.media_type !== 'anime';
  const playable = !!entry.catalog_id && (entry.media_type === 'movie' || entry.media_type === 'video');
  const busy = play.pending || restart.pending;
  const again = async () => {
    if (playable) { void play.run(entry.catalog_id!); return; }
    if (await confirm({ title: t('Start again?'), description: t('Progress goes back to zero for a new cycle. Your earlier plays and completion stay.'), label: t('Start again') })) void restart.run();
  };
  const againLabel = playable ? t('Watched it again') : verb === 'read' ? t('Read again') : verb === 'watched' ? t('Watch again') : t('Start again');

  let figure: ReactNode; let meter: number | null = null; let note: ReactNode = null; let next: ReactNode = null; let action: ReactNode = null;
  if (show) {
    figure = <p className="track-figure"><strong className="num">{show.watched.toLocaleString(locale)}</strong> {t('of {total} episodes', { total: show.aired.toLocaleString(locale) })}</p>;
    meter = show.aired ? Math.min(100, Math.round((show.watched / show.aired) * 100)) : 0;
    const finished = completed && entry.finished_at ? t('Finished {date}', { date: dayText(entry.finished_at, true) }) : '';
    note = [finished, show.announced ? plural(show.announced, '{n} more episode announced', '{n} more episodes announced') : ''].filter(Boolean).join(' · ') || null;
    if (show.next) {
      const code = episodeCode(show.next.season_number, show.next.episode_number);
      const out = isAired(show.next);
      next = <div className="track-next">
        <div className="track-next-text"><p className="track-next-label">{t('Next')}</p>
          <p className="track-next-title"><span className="num">{code}</span> {show.next.title}</p>
          <p className="track-next-meta">{[show.next.release_date && (out ? t('Aired {date}', { date: dayText(show.next.release_date, true) }) : t('Airs {date}', { date: dayText(show.next.release_date, true) })), show.next.runtime_minutes ? duration(show.next.runtime_minutes) : ''].filter(Boolean).join(' · ')}</p></div>
        {out && <button className="button primary" disabled={busy} onClick={() => void play.run(show.next!.catalog_id)}><CircleCheck aria-hidden />{play.pending ? t('Saving…') : t('Mark {code} watched', { code })}</button>}
      </div>;
    } else if (completed) next = <div className="track-next"><p className="track-next-title">{t('You have watched every episode.')}</p><button className="button" disabled={busy} onClick={() => void again()}><RotateCcw aria-hidden />{againLabel}</button></div>;
  } else if (completed && single) {
    const when = entry.finished_at ?? entry.last_played_at;
    figure = <p className="track-figure track-figure-text">{when ? (verb === 'watched' ? t('Watched {date}', { date: dayText(when, true) }) : verb === 'read' ? t('Read {date}', { date: dayText(when, true) }) : t('Finished {date}', { date: dayText(when, true) })) : verb === 'watched' ? t('Watched') : verb === 'read' ? t('Read') : t('Finished')}</p>;
    action = <button className="button" disabled={busy} onClick={() => void again()}><RotateCcw aria-hidden />{play.pending || restart.pending ? t('Saving…') : againLabel}</button>;
  } else {
    const known = entry.total != null;
    figure = entry.progress > 0 || known && entry.unit !== 'minutes'
      ? <p className="track-figure"><strong className="num">{entry.progress.toLocaleString(locale)}</strong> {known ? t('of {total} {unit}', { total: entry.total!.toLocaleString(locale), unit: unitLabel(entry.unit) }) : unitLabel(entry.unit)}</p>
      : <p className="track-figure track-figure-text">{verb === 'watched' ? t('Not watched yet') : verb === 'read' ? t('Not read yet') : t('Not started yet')}</p>;
    if (known && (entry.progress > 0 || entry.unit !== 'minutes')) meter = entry.total ? Math.min(100, Math.round((entry.progress / entry.total) * 100)) : 0;
    if (completed && entry.finished_at) note = t('Finished {date}', { date: dayText(entry.finished_at, true) });
    if (single && entry.catalog_id && !completed) action = <div className="track-actions">
      <button className="button primary" disabled={busy} onClick={() => void play.run(entry.catalog_id!)}><CircleCheck aria-hidden />{play.pending ? t('Saving…') : markLabel(entry.media_type)}</button>
      <CheckInButton catalogID={entry.catalog_id} />
    </div>;
  }
  const cycles = entry.completed_cycles >= 1 && !(completed && entry.completed_cycles === 1) ? plural(entry.completed_cycles, 'Completed {n} time', 'Completed {n} times') : null;
  // A show's progress follows its episodes; a number typed here would be overwritten by the next play.
  const stepper = !(show || (entry.catalog_id && hierarchical.has(entry.media_type))) && !(completed && single) && !(playable && entry.progress === 0);
  return <section className="track" aria-label={t('Your progress')}>
    <div className="track-summary">
      <div className="track-headline">{figure}{action}</div>
      {meter !== null && <div className="track-meter" role="img" aria-label={t('{value}% done', { value: meter })}><span style={{ width: `${meter}%` }} /></div>}
      {(note || cycles) && <p className="track-note">{[note, cycles].filter(Boolean).join(' · ')}</p>}
    </div>
    {next}
    <ErrorNote error={play.error || restart.error} />
    <div className="track-controls">
      <StatusSelect entry={entry} />
      {ratingsOn && <RatingSelect entry={entry} />}
      {stepper && <ProgressStepper entry={entry} primary={!next && !action} />}
      {extra}
    </div>
  </section>;
}

/** Status, saved as soon as it is chosen. */
function StatusSelect({ entry }: { entry: Entry }) {
  const refresh = useRefresh();
  const save = useAction(async (status: Entry['status'], key) => {
    const current = await latestEntry(entry.id);
    unwrap(await api.POST('/api/v1/library/{entryID}/progress', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: current.version, progress: current.progress, status } }));
    await refresh();
  });
  return <div className="track-field">
    <label htmlFor={`status-${entry.id}`}>{t('Status')}</label>
    <select id={`status-${entry.id}`} value={entry.status} disabled={save.pending} onChange={e => void save.run(e.target.value as Entry['status'])}>{statuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>
    <ErrorNote error={save.error} />
  </div>;
}

/** Your rating of the title on your scale, saved as soon as it is chosen. */
export function RatingSelect({ entry }: { entry: Entry }) {
  const refresh = useRefresh(); const { prefs } = usePreferences();
  const save = useAction(async (value: number | null, key) => {
    const c = await latestEntry(entry.id);
    unwrap(await api.PUT('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { title: c.title, media_type: c.media_type, year: c.year, unit: c.unit, total: c.total, status: c.status, progress: c.progress, rating: value, notes: c.notes, version: c.version } }));
    await refresh();
  });
  return <div className="track-field">
    <label htmlFor={`rating-${entry.id}`}>{t('Your rating')}</label>
    <select id={`rating-${entry.id}`} value={entry.rating ?? ''} disabled={save.pending} onChange={e => void save.run(e.target.value === '' ? null : Number(e.target.value))}>
      <option value="">{t('Not rated')}</option>{Array.from({ length: 11 }, (_, n) => 10 - n).map(n => <option key={n} value={n}>{rating(n, prefs.rating_scale)}</option>)}
    </select>
    <ErrorNote error={save.error} />
  </div>;
}

const stepSize = (unit: string) => unit === 'pages' ? 10 : unit === 'minutes' ? 15 : 1;

/** "− 120 of 412 pages +" with Save: progress you count yourself (pages, minutes, or a show kept by hand). */
function ProgressStepper({ entry, primary }: { entry: Entry; primary: boolean }) {
  const refresh = useRefresh(); const unsaved = useUnsaved();
  const [value, setValue] = useState(String(entry.progress)); const [dirty, setDirty] = useState(false);
  useEffect(() => { if (!dirty) setValue(String(entry.progress)); }, [entry.progress, dirty]);
  const max = entry.total ?? 100_000_000; const step = stepSize(entry.unit);
  const number = Math.max(0, Math.min(max, Math.round(Number(value) || 0)));
  const change = (next: number) => { setValue(String(Math.max(0, Math.min(max, next)))); setDirty(true); unsaved.markDirty(); save.reset(); };
  const save = useAction(async (progress: number, key) => {
    const current = await latestEntry(entry.id);
    const status = current.total != null && progress >= current.total ? 'completed' : progress > 0 && (current.status === 'planned' || current.status === 'completed') ? 'in_progress' : current.status;
    unwrap(await api.POST('/api/v1/library/{entryID}/progress', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: current.version, progress, status } }));
    setDirty(false); unsaved.markSaved(); await refresh();
  });
  const id = `progress-${entry.id}`;
  return <form className="track-field track-stepper" onSubmit={submit(() => void save.run(number))}>
    <label htmlFor={id}>{t('Progress')}</label>
    <div className="count-stepper">
      <button type="button" className="count-step" aria-label={t('Decrease progress')} disabled={save.pending || number <= 0} onClick={() => change(number - step)}><Minus aria-hidden /></button>
      <input id={id} className="num" type="number" inputMode="numeric" min={0} max={max} value={value} disabled={save.pending} onChange={e => { setValue(e.target.value); setDirty(true); unsaved.markDirty(); save.reset(); }} />
      <span className="count-of">{entry.total != null ? t('of {total} {unit}', { total: entry.total.toLocaleString(locale), unit: unitLabel(entry.unit) }) : unitLabel(entry.unit)}</span>
      <button type="button" className="count-step" aria-label={t('Increase progress')} disabled={save.pending || number >= max} onClick={() => change(number + step)}><Plus aria-hidden /></button>
      <button type="submit" className={`button small${primary ? ' primary' : ''}`} disabled={save.pending || !dirty}>{save.pending ? t('Saving…') : t('Save')}</button>
    </div>
    {save.saved && !dirty && <p role="status" className="track-saved">{t('Progress saved.')}</p>}
    <ErrorNote error={save.error} />
  </form>;
}

/** Your ratings of a show's episodes, every page of them, by catalog record. */
export function useEpisodeRatings(entryID: string | null) {
  const user = useUser();
  const query = useInfiniteQuery({ queryKey: privateKey(user.id, 'episode-ratings', entryID), enabled: !!entryID, initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/episode-ratings', { signal, params: { path: { entryID: entryID! }, query: { cursor: pageParam } } })),
    getNextPageParam: page => page.next_cursor ?? undefined });
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = query;
  useEffect(() => { if (hasNextPage && !isFetchingNextPage) void fetchNextPage(); }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  return useMemo(() => new Map((query.data?.pages ?? []).flatMap(p => p.items).map(r => [r.catalog_id, r])), [query.data]);
}

/** One episode's rating, saved as soon as it is chosen. */
export function EpisodeRatingSelect({ entryID, episode, value }: { entryID: string; episode: Item; value?: Schema['EpisodeRating'] }) {
  const refresh = useRefresh(); const client = useQueryClient(); const user = useUser(); const { prefs } = usePreferences();
  const save = useAction(async (next: string) => {
    unwrap(await api.PUT('/api/v1/library/{entryID}/episode-ratings/{catalogID}', { params: { path: { entryID, catalogID: episode.id } }, body: { rating: next === '' ? null : Number(next), version: value?.version ?? 0 } }));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'episode-ratings', entryID) }); await refresh();
  });
  const code = episodeCode(episode.season_number, episode.episode_number);
  return <span className="ep-rating">
    <select aria-label={t('Your rating for {episode}', { episode: [code, episode.title].filter(Boolean).join(' ') })} data-rated={value?.rating != null} disabled={save.pending} value={value?.rating ?? ''} onChange={e => void save.run(e.target.value)}>
      <option value="">{t('Rate')}</option>{Array.from({ length: 11 }, (_, n) => 10 - n).map(n => <option key={n} value={n}>{rating(n, prefs.rating_scale)}</option>)}
    </select>
    <ErrorNote error={save.error} />
  </span>;
}
