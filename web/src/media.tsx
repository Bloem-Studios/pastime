import { SourceLink } from './navigation';
import { artworkAllowed, artworkFailed } from './artworkPolicy';
import './pages/browse.css';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { Check, ChevronLeft, ChevronRight, Plus, RotateCcw, SkipForward } from 'lucide-react';
import { api, keyHeader, unwrap, type Entry, type Summary, type Schema } from './api/client';
import type { paths } from './api/schema.gen';
import { rating, usePreferences, useRatingsOn, type Preferences } from './organize';
import { privateKey, useRefresh, useUnsaved, useUser } from './state';
import { ErrorNote, Field, Modal, Status, statusText, submit, useAction, useConfirm } from './ui';
import { duration } from './format';
import { plural, t, locale } from './i18n';

export const mediaTypes: [Summary['media_type'], string, Summary['unit']][] = [
  ['movie', t('Movie'), 'minutes'], ['series', t('Series'), 'episodes'], ['anime', t('Anime'), 'episodes'], ['book', t('Book'), 'pages'],
  ['manga', t('Manga'), 'chapters'], ['comic', t('Comic'), 'items'], ['game', t('Game'), 'minutes'], ['boardgame', t('Board game'), 'items'], ['podcast', t('Podcast'), 'episodes'],
  ['music', t('Music'), 'tracks'], ['audiobook', t('Audiobook'), 'minutes'], ['video', t('Video'), 'minutes'],
];
export const statuses: [Summary['status'], string][] = [['planned',t('Planned')],['in_progress',t('In progress')],['completed',t('Completed')],['paused',t('Paused')],['dropped',t('Dropped')],['rated',t('Rated only')]];
export const units: Summary['unit'][] = ['minutes','episodes','pages','chapters','tracks','items'];
/** The unit's name, singular when count is exactly one. */
export function unitLabel(unit: string, count?: number) {
  if (count === 1) return ({ minutes: t('minute'), episodes: t('episode'), pages: t('page'), chapters: t('chapter'), tracks: t('track'), items: t('item') } as Record<string, string>)[unit] ?? unit;
  return ({ minutes: t('minutes'), episodes: t('episodes'), pages: t('pages'), chapters: t('chapters'), tracks: t('tracks'), items: t('items') } as Record<string, string>)[unit] ?? unit;
}
export const typeLabel = (value: string) => mediaTypes.find(item => item[0] === value)?.[1] ?? value;

/** A missing or not applicable value: shown grey, never as a zero. */
export const noValue = '–';
/** A day in words, with the year only when it is not this year: "Sep 13", "Dec 30, 2025". */
export function shortDate(iso: string) {
  const date = new Date(iso.length === 10 ? iso + 'T00:00:00' : iso);
  return date.toLocaleDateString(locale, date.getFullYear() === new Date().getFullYear() ? { month: 'short', day: 'numeric' } : { month: 'short', day: 'numeric', year: 'numeric' });
}
/** When a finished title was finished, in the verb of its medium: "Watched Sep 13". Empty without a date. */
export function finishedLabel(entry: Pick<Summary, 'finished_at' | 'media_type'>) {
  if (!entry.finished_at) return '';
  const date = shortDate(entry.finished_at);
  switch (entry.media_type) {
    case 'movie': case 'series': case 'anime': case 'video': return t('Watched {date}', { date });
    case 'book': case 'manga': case 'comic': return t('Read {date}', { date });
    default: return t('Finished {date}', { date });
  }
}
function amount(value: number, unit: string) { return unit === 'minutes' ? duration(value) : t('{value} {unit}', { value: value.toLocaleString(locale), unit: unitLabel(unit) }); }
/** Where a title stands: "9 of 19 episodes", "Watched Sep 13", or a grey dash when nothing is known. */
export function progressLabel(entry: Summary) {
  if (entry.status === 'completed') return finishedLabel(entry) || (entry.total ? amountOf(entry) : noValue);
  if (entry.status === 'rated') return noValue;
  if (entry.total === null) return entry.progress > 0 ? amount(entry.progress, entry.unit) : noValue;
  return amountOf(entry);
}
/** How far a title has got, without dates: "9 of 19 episodes", "61 episodes", or a grey dash. */
export function progressAmount(entry: Summary) { return entry.total ? amountOf(entry) : entry.progress > 0 ? amount(entry.progress, entry.unit) : noValue; }
function amountOf(entry: Summary) {
  const total = entry.total ?? 0;
  return entry.unit === 'minutes'
    ? t('{value} of {total}', { value: duration(entry.progress), total: duration(total) })
    : t('{value} of {total} {unit}', { value: entry.progress.toLocaleString(locale), total: total.toLocaleString(locale), unit: unitLabel(entry.unit) });
}
/** The share done, only when it can be drawn honestly: a known total and a title still being followed. */
export function meterValue(entry: Summary) { return entry.total && entry.status !== 'completed' && entry.status !== 'rated' && entry.status !== 'planned' ? percent(entry) : undefined; }

/** Progress in the person's chosen form: counts or percent. */
export function progressText(entry: Summary, prefs: Preferences) {
  return prefs.progress_display === 'percent' && entry.total && entry.status !== 'completed' ? t('{entry}% complete', { entry: percent(entry) }) : progressLabel(entry);
}
export type CardField = 'type' | 'year' | 'status' | 'progress' | 'rating';
/** The line under a poster: the person's choice for this media type, else what matters for its status. */
export function cardMeta(entry: Summary, prefs: Preferences) {
  const fields = prefs.card_fields?.[entry.media_type];
  if (!fields?.length) return statusMeta(entry, prefs);
  return fields.map(field => {
    switch (field) {
      case 'type': return typeLabel(entry.media_type);
      case 'year': return entry.year ? String(entry.year) : '';
      case 'status': return statusText(entry.status);
      case 'progress': return progressText(entry, prefs) === noValue ? '' : progressText(entry, prefs);
      case 'rating': return entry.rating != null && prefs.rating_scale !== 'off' ? rating(entry.rating, prefs.rating_scale) : '';
    }
  }).filter(Boolean).join(' · ');
}
/** What is left of a title with a known total: "10 episodes left", "1 h 5 min left". */
export function remaining(entry: Summary) {
  const left = Math.max(0, (entry.total ?? 0) - entry.progress);
  return entry.unit === 'minutes' ? t('{time} left', { time: duration(left) }) : t('{value} {unit} left', { value: left.toLocaleString(locale), unit: unitLabel(entry.unit, left) });
}
function statusMeta(entry: Summary, prefs: Preferences) {
  const progress = progressText(entry, prefs);
  switch (entry.status) {
    case 'in_progress': return entry.total ? remaining(entry) : progress === noValue ? statusText(entry.status) : progress;
    case 'completed': return [finishedLabel(entry) || statusText(entry.status), entry.rating != null && prefs.rating_scale !== 'off' ? rating(entry.rating, prefs.rating_scale) : ''].filter(Boolean).join(' · ');
    case 'planned': return [typeLabel(entry.media_type), entry.year].filter(Boolean).join(' · ');
    case 'rated': return entry.rating != null && prefs.rating_scale !== 'off' ? t('Rated {rating}', { rating: rating(entry.rating, prefs.rating_scale) }) : statusText(entry.status);
    default: return [statusText(entry.status), progress === noValue ? '' : progress].filter(Boolean).join(' · ');
  }
}

export function Cover({ entry, compact = false }: { entry: { title: string; media_type: string; image_url?: string }; compact?: boolean }) {
  const [failedURL, setFailedURL] = useState<string>();
  if (entry.image_url && failedURL !== entry.image_url && artworkAllowed(entry.image_url)) return <div className={`cover has-image ${compact ? 'compact' : ''}`} aria-hidden="true"><img src={entry.image_url} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => { artworkFailed(entry.image_url!); setFailedURL(entry.image_url); }} /></div>;
  return <div className={`cover cover-${entry.media_type} ${compact ? 'compact' : ''}`} aria-hidden="true"><span className="cover-category">{typeLabel(entry.media_type)}</span><strong>{entry.title}</strong><span className="cover-rule" /></div>;
}
export const entryPath = (entry: Pick<Summary, 'id' | 'catalog_id'>) => entry.catalog_id ? `/title/${entry.catalog_id}` : `/library/${entry.id}`;
export const percent = (entry: Pick<Summary, 'progress' | 'total'>) => entry.total ? Math.min(100, Math.round((entry.progress / entry.total) * 100)) : 0;

/** The share done as a short figure: "9 / 19", or "47%" for time and when the person prefers percent. */
export function progressFigure(entry: Summary, prefs: Pick<Preferences, 'progress_display'>) {
  if (!entry.total) return '';
  return entry.unit === 'minutes' || prefs.progress_display === 'percent' ? `${percent(entry)}%` : `${entry.progress.toLocaleString(locale)} / ${entry.total.toLocaleString(locale)}`;
}
/** A thin track under artwork with its figure at the right. Drawn only when the share is known. */
export function CardProgress({ entry, reserve = false }: { entry: Summary; reserve?: boolean }) {
  const { prefs } = usePreferences();
  const value = meterValue(entry);
  // In a grid every card keeps the slot, so titles line up across a row whether or not a title has a share.
  if (value === undefined) return reserve ? <span className="card-progress" aria-hidden="true" /> : null;
  return <span className="card-progress" role="img" aria-label={progressLabel(entry)}><span className="card-track"><span style={{ width: `${value}%` }} /></span><span className="card-figure">{progressFigure(entry, prefs)}</span></span>;
}

/** Poster card: 2:3 artwork, a progress track while a title is followed, one line of title and one of detail. */
export function PosterCard({ entry, meta, badge, action, progress = true }: { entry: Summary; meta?: string; badge?: string; action?: boolean; progress?: boolean }) {
  const [editing, setEditing] = useState(false);
  const { prefs } = usePreferences();
  const to = entryPath(entry);
  const title = useDisplayTitle(entry);
  // Only a title being followed has a next step; a finished film has nothing to add.
  const actionable = action ?? entry.status === 'in_progress';
  const episodes = entry.unit === 'episodes';
  return <article className="poster-card">
    <SourceLink to={to} className="art" tabIndex={-1} aria-hidden="true"><Cover entry={entry} />{badge && <span className="art-badge">{badge}</span>}</SourceLink>
    {progress && <CardProgress entry={entry} reserve />}
    <div className="card-text"><SourceLink to={to} className="card-title media-title" lang={title !== entry.title ? '' : undefined}>{title}</SourceLink><span className="card-meta">{meta ?? cardMeta(entry, prefs)}</span></div>
    {actionable && <button className="button ghost small card-action" onClick={() => setEditing(true)} aria-label={episodes ? t('Record the next episode of {title}', { title: entry.title }) : t('Update progress for {title}', { title: entry.title })}>{episodes ? <SkipForward aria-hidden /> : <Plus aria-hidden />}{episodes ? t('Next episode') : t('Update progress')}</button>}
    {editing && <ProgressModal entry={entry} step={episodes ? 1 : 0} onClose={() => setEditing(false)} />}
  </article>;
}
/** The title in the person's preferred language: original where known, else the catalog title. */
export function useDisplayTitle(entry: Pick<Summary, 'title' | 'original_title'> & { localized_title?: string | null }) {
  const { prefs } = usePreferences();
  if (entry.localized_title) return entry.localized_title;
  return prefs.title_language === 'original' && entry.original_title ? entry.original_title : entry.title;
}
/** Kept for pages that list library entries in a grid. */
export const MediaCard = ({ entry }: { entry: Summary }) => <PosterCard entry={entry} />;

export type FacetQuery = NonNullable<paths['/api/v1/library/facets']['get']['parameters']['query']>;
/**
 * How many titles each media type and status would show under the other filters, counted by the
 * server. The last counts stay on screen while new ones load, so the chips do not jump.
 */
export function useLibraryFacets(filters: FacetQuery = {}) {
  const user = useUser();
  return useQuery({ queryKey: privateKey(user.id, 'library', 'facets', filters), staleTime: 60_000, placeholderData: keepPreviousData,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/facets', { signal, params: { query: filters } })) });
}

/** When a title was last played, in the verb of its medium: "Last watched Sep 13". */
export function lastPlayedLabel(type: string, iso: string) {
  const date = shortDate(iso);
  return ['book', 'manga', 'comic'].includes(type) ? t('Last read {date}', { date }) : ['movie', 'series', 'anime', 'video'].includes(type) ? t('Last watched {date}', { date }) : t('Last played {date}', { date });
}

/** Wide card: 16:9 artwork, falling back to a blurred poster when the catalog has no wide art. */
export function Backdrop({ title, backdrop, poster, children }: { title: string; backdrop?: string; poster?: string; children?: ReactNode }) {
  const [failedURL, setFailedURL] = useState<string>();
  return <>{backdrop && failedURL !== backdrop && artworkAllowed(backdrop)
    ? <img src={backdrop} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => { artworkFailed(backdrop); setFailedURL(backdrop); }} />
    : <span className="backdrop-fallback">{poster && <img src={poster} alt="" loading="lazy" decoding="async" referrerPolicy="no-referrer" />}<strong>{title}</strong></span>}{children}</>;
}

/** A horizontally scrolling row. Pointer devices get edge buttons; touch scrolls by swiping. */
export function Shelf({ title, count, link, wide = false, children }: { title: string; count?: number; link?: { to: string; label: string }; wide?: boolean; children: ReactNode }) {
  const scroller = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ start: true, end: true });
  const measure = () => { const el = scroller.current; if (el) setEdges({ start: el.scrollLeft < 8, end: el.scrollLeft + el.clientWidth >= el.scrollWidth - 8 }); };
  useEffect(() => {
    measure();
    const el = scroller.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    // Cards change width with the window and covers load late; re-measure so the arrows
    // appear exactly when the row holds more than fits.
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    for (const child of Array.from(el.children)) observer.observe(child);
    return () => observer.disconnect();
  }, [children]);
  const move = (direction: number) => scroller.current?.scrollBy({ left: direction * scroller.current.clientWidth * 0.85, behavior: 'smooth' });
  const overflows = !(edges.start && edges.end);
  return <section className="shelf" aria-label={title}>
    <div className="shelf-head"><h2>{title}{count !== undefined && <span className="count">{count.toLocaleString(locale)}</span>}</h2>
      <div className="shelf-tools">{link && <Link className="text-link" to={link.to}>{link.label}<ChevronRight size={16} aria-hidden /></Link>}
        {overflows && <div className="shelf-nav">
          <button type="button" className="shelf-arrow" aria-label={t('Scroll {title} back', { title: title })} disabled={edges.start} onClick={() => move(-1)}><ChevronLeft aria-hidden /></button>
          <button type="button" className="shelf-arrow" aria-label={t('Scroll {title} forward', { title: title })} disabled={edges.end} onClick={() => move(1)}><ChevronRight aria-hidden /></button>
        </div>}</div></div>
    <div className={`shelf-scroll ${wide ? 'wide' : ''}`} ref={scroller} onScroll={measure}>{children}</div>
  </section>;
}
export function EntryEditor({ entry, onClose }: { entry?: Entry; onClose: () => void }) {
  const ratingsOn = useRatingsOn();
  const navigate = useNavigate(); const refresh = useRefresh(); const unsaved = useUnsaved();
  const [dirty, setDirty] = useState(false);
  const [draft, setDraft] = useState<Schema['EntryInput']>(() => entry ? { title: entry.title, media_type: entry.media_type, year: entry.year, unit: entry.unit, total: entry.total, status: entry.status, progress: entry.progress, rating: entry.rating, notes: entry.notes, version: entry.version } : { title: '', media_type: 'movie', unit: 'minutes', year: null, total: null, status: 'planned', progress: 0, rating: null, notes: '' });
  const action = useAction(async (value: Schema['EntryInput'], key) => {
    const result = entry ? unwrap(await api.PUT('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: value })) : unwrap(await api.POST('/api/v1/library', { params: { header: keyHeader(key) }, body: value }));
    unsaved.markSaved(); setDirty(false); await refresh(); onClose(); if (!entry) navigate(`/library/${result.id}`);
  });
  function set<K extends keyof Schema['EntryInput']>(key: K, value: Schema['EntryInput'][K]) { setDraft(previous => ({ ...previous, [key]: value })); setDirty(true); unsaved.markDirty(); action.reset(); }
  return <Modal title={entry ? t('Edit media') : t('Add media')} description={t('Add your own metadata. This item and its notes stay private.')} onClose={onClose} dirty={dirty} busy={action.pending} wide><form onSubmit={submit(() => void action.run(draft))}><fieldset disabled={action.pending}><Field label={t('Title')}>{id => <input id={id} required maxLength={200} value={draft.title} onChange={e => set('title', e.target.value)} />}</Field><div className="form-grid"><Field label={t('Media type')}>{id => <select id={id} value={draft.media_type} onChange={e => { const value = e.target.value as Summary['media_type']; setDraft(previous => ({ ...previous, media_type: value, unit: mediaTypes.find(item => item[0] === value)![2] })); setDirty(true); unsaved.markDirty(); action.reset(); }}>{mediaTypes.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>}</Field><Field label={t('Status')}>{id => <select id={id} value={draft.status} onChange={e => set('status', e.target.value as Summary['status'])}>{statuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>}</Field></div><details className="form-advanced" style={{ marginBlock: '1rem', borderTop: '1px solid var(--line)', paddingTop: '.75rem' }}><summary style={{ cursor: 'pointer', minHeight: 44, paddingBlock: '.65rem', fontWeight: 600 }}>{t('Advanced details')}</summary><div className="form-grid"><Field label={t('Release year')} hint={t('Optional')}>{(id, hint) => <input id={id} aria-describedby={hint} type="number" min={1} max={9999} value={draft.year ?? ''} onChange={e => set('year', e.target.value === '' ? null : Number(e.target.value))} />}</Field><Field label={t('Progress unit')}>{id => <select id={id} value={draft.unit} onChange={e => set('unit', e.target.value as Summary['unit'])}>{units.map(unit => <option key={unit} value={unit}>{unitLabel(unit)}</option>)}</select>}</Field><Field label={t('Total')} hint={t('Leave empty if unknown.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="number" min={1} max={100000000} value={draft.total ?? ''} onChange={e => set('total', e.target.value === '' ? null : Number(e.target.value))} />}</Field><Field label={t('Current progress')}>{id => <input id={id} required type="number" min={0} max={draft.total ?? 100000000} value={draft.progress} onChange={e => set('progress', Number(e.target.value))} />}</Field>{ratingsOn && <Field label={t('Rating')} hint={t('Optional, from 0 to 10.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="number" min={0} max={10} value={draft.rating ?? ''} onChange={e => set('rating', e.target.value === '' ? null : Number(e.target.value))} />}</Field>}</div><Field label={t('Private notes')}>{id => <textarea id={id} rows={4} maxLength={10000} value={draft.notes} onChange={e => set('notes', e.target.value)} />}</Field></details></fieldset><ErrorNote error={action.error} /><div className="dialog-actions"><button className="button primary" disabled={action.pending} type="submit">{action.pending ? t('Saving…') : entry ? t('Save changes') : t('Add media')}</button></div></form></Modal>;
}
/** The progress dialog. `step` starts the form that much ahead, as for "Next episode". */
export function ProgressModal({ entry, step = 0, onClose }: { entry: Summary; step?: number; onClose: () => void }) {
  const [dirty, setDirty] = useState(false); const [busy, setBusy] = useState(false);
  return <Modal title={step ? t('Record the next episode') : t('Update progress')} description={entry.localized_title ?? entry.title} onClose={onClose} dirty={dirty} busy={busy}><ProgressForm entry={entry} step={step} onSaved={onClose} onDirty={() => setDirty(true)} onBusy={setBusy} /></Modal>;
}
export function ProgressForm({ entry, step = 0, onSaved, onDirty, onBusy }: { entry: Summary; step?: number; onSaved?: () => void; onDirty?: () => void; onBusy?: (value: boolean) => void }) {
  const refresh = useRefresh(); const unsaved = useUnsaved(); const confirm = useConfirm();
  const [base, setBase] = useState(entry); const [progress, setProgress] = useState(String(Math.min(entry.progress + step, entry.total ?? 100000000))); const [status, setStatus] = useState(entry.status);
  const action = useAction(async (value: Schema['ProgressInput'], key) => {
    unwrap(await api.POST('/api/v1/library/{entryID}/progress', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: value }));
    unsaved.markSaved(); await refresh(); const current = unwrap(await api.GET('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id } } })); setBase(current); setProgress(String(current.progress)); setStatus(current.status); onSaved?.();
  });
  const repeat = useAction(async (_: void, key) => {
    unwrap(await api.POST('/api/v1/library/{entryID}/restart', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: base.version } }));
    unsaved.markSaved(); await refresh(); const current = unwrap(await api.GET('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id } } })); setBase(current); setProgress(String(current.progress)); setStatus(current.status); onSaved?.();
  });
  const pending = action.pending || repeat.pending;
  useEffect(() => { onBusy?.(pending); }, [pending, onBusy]);
  return <form onSubmit={submit(() => void action.run({ version: base.version, progress: Number(progress), status }))}><fieldset disabled={pending}><div className="form-grid"><Field label={t('Progress ({unit})', { unit: unitLabel(base.unit) })}>{id => <input id={id} type="number" required min={0} max={base.total ?? 100000000} value={progress} onChange={e => { setProgress(e.target.value); unsaved.markDirty(); onDirty?.(); action.reset(); }} />}</Field><Field label={t('Status')}>{id => <select id={id} value={status} onChange={e => { setStatus(e.target.value as Summary['status']); unsaved.markDirty(); onDirty?.(); action.reset(); }}>{statuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>}</Field></div></fieldset><ErrorNote error={action.error || repeat.error} />{entry.version !== base.version && <p className="notice">{t('This item changed.')} <button type="button" className="text-button" onClick={async () => { if (await confirm({ title: t('Reload current progress?'), description: t('This replaces any unsaved progress in this form.'), label: t('Reload current') })) { setBase(entry); setProgress(String(entry.progress)); setStatus(entry.status); unsaved.markSaved(); action.reset(); } }}>{t('Reload current progress')}</button></p>}<div className="quick-steps" role="group" aria-label={t('Add progress')}>{steps(base.unit).map(n => <button key={n} type="button" className="button small ghost" disabled={pending} onClick={() => { const next = Math.min(Number(progress || 0) + n, base.total ?? 100000000); setProgress(String(next)); unsaved.markDirty(); onDirty?.(); action.reset(); }}>+{n}{base.unit === 'minutes' ? ` ${t('min')}` : ''}</button>)}</div>
  <div className="actions"><button className="button primary" disabled={pending} type="submit">{pending ? t('Saving…') : t('Update progress')}</button>{base.status !== 'completed' && <button className="button" disabled={pending} type="button" onClick={() => void action.run({ version: base.version, progress: Number(progress), status: 'completed' })}><Check aria-hidden />{t('Mark completed')}</button>}{base.status === 'completed' && <button className="button" disabled={pending} type="button" onClick={async () => { if (await confirm({ title: t('Start another cycle?'), description: t('Current progress resets to zero. Your previous completion and activity remain.'), label: t('Start again') })) void repeat.run(); }}><RotateCcw aria-hidden />{t('Start again')}</button>}</div>{action.saved && <p role="status" className="save-confirmation">{t('Progress saved.')}</p>}<p className="field-hint">{plural(base.completed_cycles, '{n} completed cycle. Repeats are recorded separately.', '{n} completed cycles. Repeats are recorded separately.')}</p></form>;
}
const steps = (unit: string) => unit === 'minutes' ? [15, 30, 60] : unit === 'pages' ? [10, 25, 50] : [1];
export function EntryStatus({ entry }: { entry: Summary }) { return <Status value={entry.status} />; }

/** A metadata provider's display name. */
export const providerNames: Record<string, string> = { tmdb: 'TMDB', tvdb: 'TheTVDB', anilist: 'AniList', mangabaka: 'MangaBaka', comicvine: 'Comic Vine', gcd: t('Grand Comics Database'), openlibrary: 'Open Library', igdb: 'IGDB', bgg: 'BoardGameGeek', musicbrainz: 'MusicBrainz', itunes: 'Apple Podcasts', rss: 'RSS', youtube: 'YouTube' };
export const providerName = (name: string) => providerNames[name] ?? name;
