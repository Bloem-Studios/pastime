import { useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronLeft, ChevronRight, Trash2, X } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { usePreferences } from '../organize';
import { Cover, typeLabel } from '../media';
import { Empty, ErrorNote, Failed, Loading, LoadMore, PageTitle, useAction, useConfirm } from '../ui';
import { calendarMonthDays, calendarMonthRange, shiftCalendarMonth } from '../calendarPolicy';
import { clock, clockRange, duration, episodeCode, episodeRange, localDay } from '../format';
import { locale, plural, t } from '../i18n';
import './history.css';

type Play = Schema['HistoryItem'];

/** One line of the history: a single play, a viewing session of one title, or a bulk "mark played". */
type Row = { key: string; kind: 'play' | 'session' | 'marked'; plays: Play[] };

/**
 * Plays of one day, newest first. Consecutive plays of the same title form one row: a session
 * when they were watched, a bulk mark when the server marked them (three or more titles stamped in
 * one minute). Marks never mix with real plays.
 */
export function historyRows(plays: Play[]): Row[] {
  const rows: Row[] = [];
  for (const p of plays) {
    const last = rows[rows.length - 1];
    const marked = !!p.marked;
    if (last && last.plays[0].entry_id === p.entry_id && (last.kind === 'marked') === marked) {
      last.plays.push(p);
      if (!marked) last.kind = 'session';
    } else {
      rows.push({ key: p.play_id, kind: marked ? 'marked' : 'play', plays: [p] });
    }
  }
  return rows;
}

const thisMonth = () => new Date().toLocaleDateString('en-CA').slice(0, 7);
const monthName = (month: string) => new Date(`${month}-01T00:00:00`).toLocaleDateString(locale, { month: 'long', year: 'numeric' });
const dayName = (day: string) => {
  const date = new Date(`${day}T00:00:00`);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return date.toLocaleDateString(locale, sameYear ? { weekday: 'long', month: 'long', day: 'numeric' } : { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
};

export function HistoryPage() {
  const user = useUser(); const { prefs } = usePreferences(); const refresh = useRefresh(); const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const media = params.get('media_type') || undefined;
  const month = calendarMonthRange(params.get('month') ?? '') ? params.get('month')! : '';
  const [shown, setShown] = useState(month || thisMonth());
  const railMonth = month || shown;
  function change(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(next)) { if (v) p.set(k, v); else p.delete(k); }
    setParams(p, { replace: true });
  }
  const range = month ? { from: `${month}-01`, to: `${shiftCalendarMonth(month, 1)}-01` } : {};
  const query = useInfiniteQuery({ queryKey: privateKey(user.id, 'history', media, month), initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam, signal }) => unwrap(await api.GET('/api/v1/history', { signal, params: { query: { cursor: pageParam, limit: 100, media_type: media as Schema['EntrySummary']['media_type'] | undefined, ...range } } })),
    getNextPageParam: (last: Schema['HistoryPage']) => last.next_cursor ?? undefined });
  // The month's real viewing (bulk marks are not counted) for the calendar and the summary line.
  const monthStats = useQuery({ queryKey: privateKey(user.id, 'stats', `${railMonth}-01`, `${shiftCalendarMonth(railMonth, 1)}-01`),
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats', { signal, params: { query: { from: `${railMonth}-01`, to: `${shiftCalendarMonth(railMonth, 1)}-01` } } })) });
  // Which kinds of media this profile has ever played, so the filter offers only those.
  const tenYears = new Date(Date.now() - 3650 * 864e5).toISOString().slice(0, 10), tomorrow = new Date(Date.now() + 864e5).toISOString().slice(0, 10);
  const allTime = useQuery({ queryKey: privateKey(user.id, 'stats', tenYears, tomorrow), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats', { signal, params: { query: { from: tenYears, to: tomorrow } } })) });
  const types = (allTime.data?.by_type ?? []).filter(b => b.plays > 0).sort((a, b) => b.plays - a.plays);

  const remove = useAction(async (playIDs: string[]) => {
    for (const playID of playIDs) unwrap(await api.DELETE('/api/v1/history/{playID}', { params: { path: { playID }, header: keyHeader(crypto.randomUUID()) } }));
    await refresh();
  });
  const removePlay = async (p: Play, label: string) => {
    if (await confirm({ title: t('Delete this play?'), description: t('{title}, {when}. Progress for this title is recalculated.', { title: label, when: `${dayName(localDay(p.played_at))} ${clock(p.played_at)}` }), label: t('Delete play'), danger: true })) void remove.run([p.play_id]);
  };
  const removeMark = async (row: Row) => {
    const p = row.plays[0];
    if (await confirm({ title: t('Remove this mark?'), description: t('{count} episodes of {title} were marked watched at once on {when}. Their plays are deleted and progress is recalculated.', { count: row.plays.length, title: p.entry_title, when: dayName(localDay(p.played_at)) }), label: t('Remove mark'), danger: true })) void remove.run(row.plays.map(x => x.play_id));
  };

  const items = useMemo(() => query.data?.pages.flatMap(p => p.items) ?? [], [query.data]);
  const days = useMemo(() => {
    const byDay = new Map<string, Play[]>();
    for (const p of items) { const d = localDay(p.played_at); byDay.set(d, [...(byDay.get(d) ?? []), p]); }
    return [...byDay].map(([day, plays]) => ({ day, plays, rows: historyRows(plays) }));
  }, [items]);

  const stats = monthStats.data;
  const summary = stats ? (stats.plays === 0 ? t('Nothing watched in {month}.', { month: monthName(railMonth) })
    : t('{month}: {plays} on {days}, {time}.', { month: monthName(railMonth), plays: plural(stats.plays, '{n} play', '{n} plays'), days: plural(stats.days.filter(d => d.plays > 0).length, '{n} day', '{n} days'), time: duration(stats.minutes) })) : undefined;

  return <div className="history-page">
    <PageTitle title={t('History')} description={summary} />
    <div className="history-layout">
      <div className="history-main">
        <div className="history-tools">
          <div className="history-month" role="group" aria-label={t('Month')}>
            <button className="icon-button" aria-label={t('Previous month')} onClick={() => change({ month: shiftCalendarMonth(month || thisMonth(), month ? -1 : 0) })}><ChevronLeft aria-hidden /></button>
            <span className="history-month-name">{month ? monthName(month) : t('All time')}</span>
            <button className="icon-button" aria-label={t('Next month')} disabled={!month || month >= thisMonth()} onClick={() => change({ month: shiftCalendarMonth(month, 1) })}><ChevronRight aria-hidden /></button>
            {month && <button className="text-button" onClick={() => change({ month: '' })}><X aria-hidden />{t('All time')}</button>}
          </div>
          {types.length > 1 && <div className="chip-filter" role="group" aria-label={t('Media type')}>
            <button aria-pressed={!media} onClick={() => change({ media_type: '' })}>{t('All')}</button>
            {types.map(b => <button key={b.media_type} aria-pressed={media === b.media_type} onClick={() => change({ media_type: b.media_type })}>{typeLabel(b.media_type)} <span className="chip-count">{b.plays.toLocaleString(locale)}</span></button>)}
          </div>}
        </div>
        <ErrorNote error={remove.error} />
        {query.isPending ? <Loading label={t('Loading history')} />
          : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
          : days.length === 0 ? <Empty title={month ? t('Nothing played in {month}', { month: monthName(month) }) : media ? t('No plays of this type yet') : t('No plays yet')}
              action={month || media ? <button className="button" onClick={() => change({ month: '', media_type: '' })}>{t('Show all history')}</button> : <Link className="button primary" to="/discover">{t('Find something to watch')}</Link>}>
              {month || media ? t('Plays you record appear here by day.') : t('Plays recorded here, from Silo or from imports, appear by day with the time you watched.')}</Empty>
          : <><ol className="history-days">{days.map(d => <HistoryDay key={d.day} day={d.day} rows={d.rows} showType={!media && types.length > 1} onDelete={removePlay} onRemoveMark={removeMark} busy={remove.pending} />)}</ol>
            <LoadMore available={!!query.hasNextPage} pending={query.isFetchingNextPage} onLoad={() => void query.fetchNextPage()} /></>}
      </div>
      <aside className="history-rail" aria-label={t('Viewing calendar')}>
        <ViewingCalendar month={railMonth} weekStart={prefs.week_start} days={stats?.days ?? []}
          onMonth={m => month ? change({ month: m }) : setShown(m)} onDay={d => { change({ month: d.slice(0, 7) }); window.setTimeout(() => document.getElementById(`day-${d}`)?.scrollIntoView({ block: 'start' }), 250); }} />
        {stats && <dl className="history-totals">
          <div><dt>{t('Plays')}</dt><dd className="num">{stats.plays.toLocaleString(locale)}</dd></div>
          <div><dt>{t('Time')}</dt><dd className="num">{duration(stats.minutes)}</dd></div>
          <div><dt>{t('Days')}</dt><dd className="num">{stats.days.filter(x => x.plays > 0).length}</dd></div>
        </dl>}
        {!month && <button className="text-button" onClick={() => change({ month: railMonth })}>{t('Show only {month}', { month: monthName(railMonth) })}</button>}
      </aside>
    </div>
  </div>;
}

function HistoryDay({ day, rows, showType, onDelete, onRemoveMark, busy }: { day: string; rows: Row[]; showType: boolean; onDelete: (p: Play, label: string) => void; onRemoveMark: (row: Row) => void; busy: boolean }) {
  const real = rows.filter(r => r.kind !== 'marked').flatMap(r => r.plays);
  const marks = rows.filter(r => r.kind === 'marked').reduce((n, r) => n + r.plays.length, 0);
  const minutes = real.reduce((n, p) => n + (p.minutes ?? 0), 0);
  const total = [real.length ? plural(real.length, '{n} play', '{n} plays') : '', minutes ? duration(minutes) : '', marks ? plural(marks, '{n} marked', '{n} marked') : ''].filter(Boolean).join(' · ');
  return <li id={`day-${day}`} className="history-day">
    <div className="history-day-head"><h2>{dayName(day)}</h2><span className="num">{total}</span></div>
    <ul className="play-rows">{rows.map(r => <HistoryRow key={r.key} row={r} showType={showType} onDelete={onDelete} onRemoveMark={onRemoveMark} busy={busy} />)}</ul>
  </li>;
}

function HistoryRow({ row, showType, onDelete, onRemoveMark, busy }: { row: Row; showType: boolean; onDelete: (p: Play, label: string) => void; onRemoveMark: (row: Row) => void; busy: boolean }) {
  const [open, setOpen] = useState(false);
  const newest = row.plays[0], oldest = row.plays[row.plays.length - 1];
  const minutes = row.plays.reduce((n, p) => n + (p.minutes ?? 0), 0);
  const episode = (p: Play) => [episodeCode(p.season_number, p.episode_number), p.item_title && p.item_title !== p.entry_title ? p.item_title : ''].filter(Boolean).join(' · ');
  let detail: string; let time: string;
  if (row.kind === 'play') {
    detail = [newest.unmapped ? t('Kept from an earlier catalog match') : '', episode(newest) || (showType ? typeLabel(newest.media_type) : ''), newest.minutes ? duration(newest.minutes) : '', newest.cycle > 1 ? t('rewatch') : ''].filter(Boolean).join(' · ');
    time = clock(newest.played_at);
  } else if (row.kind === 'session') {
    detail = [episodeRange(oldest, newest), plural(row.plays.length, '{n} episode', '{n} episodes'), minutes ? duration(minutes) : ''].filter(Boolean).join(' · ');
    time = clockRange(oldest.played_at, newest.played_at);
  } else {
    detail = [t('Marked watched'), episodeRange(oldest, newest), plural(row.plays.length, '{n} episode', '{n} episodes')].filter(Boolean).join(' · ');
    time = clock(newest.played_at);
  }
  const many = row.plays.length > 1;
  return <li className={`play-row ${row.kind}`}>
    <Link to={`/library/${newest.entry_id}`} className="play-cover" tabIndex={-1} aria-hidden="true"><Cover entry={{ title: newest.entry_title, media_type: newest.media_type, image_url: newest.image_url }} compact /></Link>
    <div className="play-main"><Link to={`/library/${newest.entry_id}`} className="play-title">{newest.entry_title}</Link><p className="play-detail">{detail}</p></div>
    <time className="play-time num" dateTime={newest.played_at}>{time}</time>
    <div className="play-action">{many
      ? <button className="icon-button" aria-expanded={open} aria-label={open ? t('Hide the episodes of {title}', { title: newest.entry_title }) : t('Show the episodes of {title}', { title: newest.entry_title })} onClick={() => setOpen(!open)}><ChevronDown aria-hidden className={open ? 'flip' : ''} /></button>
      : row.kind === 'marked' ? <button className="icon-button" aria-label={t('Remove the mark on {title}', { title: newest.entry_title })} disabled={busy} onClick={() => onRemoveMark(row)}><Trash2 aria-hidden /></button>
      : <button className="icon-button" aria-label={t('Delete this play of {title}', { title: newest.entry_title })} disabled={busy} onClick={() => onDelete(newest, newest.entry_title)}><Trash2 aria-hidden /></button>}
    </div>
    {open && many && <div className="play-episodes">
      {row.kind === 'marked' && <p className="play-mark-note">{t('These were marked watched at once, so they count toward progress but not toward time or plays.')} <button className="text-button danger" disabled={busy} onClick={() => onRemoveMark(row)}>{t('Remove the whole mark')}</button></p>}
      <ol>{[...row.plays].reverse().map(p => <li key={p.play_id}>
        <span>{episode(p) || typeLabel(p.media_type)}</span>
        <span className="num subtle">{p.minutes && row.kind !== 'marked' ? duration(p.minutes) : ''}</span>
        <time className="num subtle" dateTime={p.played_at}>{clock(p.played_at)}</time>
        <button className="icon-button" aria-label={t('Delete this play of {title} {episode}', { title: p.entry_title, episode: episode(p) })} disabled={busy} onClick={() => onDelete(p, [p.entry_title, episode(p)].filter(Boolean).join(' '))}><Trash2 aria-hidden /></button>
      </li>)}</ol>
    </div>}
  </li>;
}

/** The month as a grid of days; days with real viewing are tinted by how long. */
function ViewingCalendar({ month, weekStart, days, onMonth, onDay }: { month: string; weekStart: 'monday' | 'sunday'; days: Schema['DayStats'][]; onMonth: (m: string) => void; onDay: (d: string) => void }) {
  const cells = calendarMonthDays(month, weekStart);
  const byDay = new Map(days.map(d => [d.date, d]));
  const peak = Math.max(1, ...days.map(d => d.minutes));
  const today = new Date().toLocaleDateString('en-CA');
  const names = cells.slice(0, 7).map(d => new Date(`${d}T00:00:00`).toLocaleDateString(locale, { weekday: 'narrow' }));
  return <section className="viewing-calendar">
    <div className="viewing-calendar-head">
      <h2>{monthName(month)}</h2>
      <div>
        <button className="icon-button" aria-label={t('Previous month')} onClick={() => onMonth(shiftCalendarMonth(month, -1))}><ChevronLeft aria-hidden /></button>
        <button className="icon-button" aria-label={t('Next month')} disabled={month >= thisMonth()} onClick={() => onMonth(shiftCalendarMonth(month, 1))}><ChevronRight aria-hidden /></button>
      </div>
    </div>
    <div className="viewing-grid" role="group" aria-label={t('Days watched in {month}', { month: monthName(month) })}>
      {names.map((n, i) => <span key={i} className="viewing-weekday" aria-hidden="true">{n}</span>)}
      {cells.map(d => {
        const stat = byDay.get(d); const inMonth = d.slice(0, 7) === month;
        const level = stat && stat.minutes > 0 ? Math.min(4, Math.ceil((stat.minutes / peak) * 4)) : 0;
        const label = `${dayName(d)}: ${stat?.plays ? `${plural(stat.plays, '{n} play', '{n} plays')}, ${duration(stat.minutes)}` : t('nothing watched')}`;
        return inMonth ? <button key={d} className={`viewing-day level-${level}${d === today ? ' today' : ''}`} aria-label={label} title={label} disabled={!stat?.plays} onClick={() => onDay(d)}>{Number(d.slice(8))}</button>
          : <span key={d} className="viewing-day outside" aria-hidden="true" />;
      })}
    </div>
  </section>;
}
