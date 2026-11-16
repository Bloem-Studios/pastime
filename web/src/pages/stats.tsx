import { useEffect, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { CalendarRange } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { rating, usePreferences, useRatingsOn } from '../organize';
import { typeLabel } from '../media';
import { Empty, Failed, Loading, PageTitle, statusText } from '../ui';
import { duration } from '../format';
import { locale, plural, t } from '../i18n';
import { axisLabels, change, grainFor, lastDays, minuteTicks, previousRange, reviewYear, timeBins, type Bin, type Grain } from '../statsPolicy';
import './stats.css';

type Insights = Schema['Insights'];
type RangeKey = '30d' | '90d' | '1y' | 'all';
const ranges: { key: RangeKey; label: string; days: number; against: string }[] = [
  { key: '30d', label: t('30 days'), days: 30, against: t('vs the previous 30 days') },
  { key: '90d', label: t('90 days'), days: 90, against: t('vs the previous 90 days') },
  { key: '1y', label: t('12 months'), days: 365, against: t('vs the 12 months before') },
  // The API covers at most ten years; that is all time for anyone tracking here.
  { key: 'all', label: t('All time'), days: 3650, against: '' },
];

const utcDate = (date: string) => new Date(`${date}T00:00:00Z`);
const fmtDay = (date: string) => utcDate(date).toLocaleDateString(locale, { month: 'short', day: 'numeric', timeZone: 'UTC' });
const fmtMonth = (date: string, year = false) => utcDate(date).toLocaleDateString(locale, year ? { month: 'short', year: 'numeric', timeZone: 'UTC' } : { month: 'short', timeZone: 'UTC' });
const fmtSpan = (from: string, to: string) => new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' }).formatRange(utcDate(from), utcDate(to));
const count = (n: number) => n.toLocaleString(locale);
const titles = (n: number) => plural(n, '{n} title', '{n} titles');

export function StatsPage() {
  const user = useUser(); const [params, setParams] = useSearchParams();
  const range = ranges.find(r => r.key === params.get('range')) ?? ranges[2];
  const span = lastDays(range.days); const prev = previousRange(span);
  const query = useQuery({ queryKey: privateKey(user.id, 'stats', span.from, span.toExclusive), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats', { signal, params: { query: { from: span.from, to: span.toExclusive } } })) });
  const compare = range.key !== 'all';
  const previous = useQuery({ queryKey: privateKey(user.id, 'stats', prev.from, prev.toExclusive), enabled: compare, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats', { signal, params: { query: { from: prev.from, to: prev.toExclusive } } })) });
  const s = query.data;
  const summary = s && s.plays + s.sessions > 0 ? t('{time} over {days}, {finished}.', { time: duration(s.minutes), days: plural(s.days.length, '{n} day', '{n} days'), finished: plural(s.completed, '{n} title finished', '{n} titles finished') }) : undefined;
  return <div className="stats-page">
    <PageTitle title={t('Statistics')} description={summary} actions={<Link className="button" to={`/review/${reviewYear()}`}><CalendarRange aria-hidden />{t('Year in Review')}</Link>} />
    <div className="range-tabs" role="group" aria-label={t('Time range')}>{ranges.map(r => <button key={r.key} aria-pressed={range.key === r.key} onClick={() => setParams({ range: r.key }, { replace: true })}>{r.label}</button>)}</div>
    {query.isPending ? <Loading label={t('Calculating statistics')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
      : <StatsBody stats={query.data} previous={compare ? previous.data : undefined} compare={compare} against={range.against} from={range.key === 'all' ? query.data.days[0]?.date ?? span.to : span.from} to={span.to} />}
  </div>;
}

function StatsBody({ stats, previous, compare, against, from, to }: { stats: Insights; previous?: Insights; compare: boolean; against: string; from: string; to: string }) {
  const { prefs } = usePreferences(); const ratingsOn = useRatingsOn();
  if (stats.plays === 0 && stats.sessions === 0 && stats.by_type.length === 0) return <Empty action={<Link className="button primary" to="/discover">{t('Find something to track')}</Link>} title={t('Nothing to count yet')}>{t('Statistics fill in as you record plays and finish titles.')}</Empty>;
  // A phone has room for about a month of daily bars; longer ranges go by week there.
  const narrow = useNarrow();
  const wide = grainFor(from, to); const grain = narrow && wide === 'day' && timeBins([], from, to, 'day').length > 35 ? 'week' : wide;
  const bins = timeBins(stats.days, from, to, grain, prefs.week_start);
  const chartTitle = { day: t('Time per day'), week: t('Time per week'), month: t('Time per month') }[grain];
  const played = stats.by_type.filter(b => b.minutes > 0 || b.plays > 0 || b.sessions > 0);
  const owned = stats.by_type.filter(b => b.entries > 0);
  const name = (e: { title: string; localized_title: string | null }) => e.localized_title ?? e.title;
  return <>
    <dl className="stat-strip">
      <div className="lead"><dt>{t('Time spent')}</dt><dd className="stat-value num">{duration(stats.minutes)}</dd>
        <Delta compare={compare} now={stats.minutes} before={previous?.minutes} against={against} format={duration} />
        {stats.sessions > 0 && <dd className="stat-foot">{plural(stats.sessions, 'Includes {n} reading or game session.', 'Includes {n} reading and game sessions.')}</dd>}
        {stats.unknown_runtime > 0 && <dd className="stat-foot">{plural(stats.unknown_runtime, '{n} play has no known runtime.', '{n} plays have no known runtime.')}</dd>}</div>
      <div><dt>{t('Plays')}</dt><dd className="stat-value num">{count(stats.plays)}</dd><Delta compare={compare} now={stats.plays} before={previous?.plays} against={against} format={count} /></div>
      <div><dt>{t('Titles finished')}</dt><dd className="stat-value num">{count(stats.completed)}</dd><Delta compare={compare} now={stats.completed} before={previous?.completed} against={against} format={count} /></div>
      <div><dt>{t('Active days')}</dt><dd className="stat-value num">{count(stats.days.length)}</dd><Delta compare={compare} now={stats.days.length} before={previous?.days.length} against={against} format={count} />
        {stats.streaks.current.days > 0 && <dd className="stat-foot">{plural(stats.streaks.current.days, 'Current streak: {n} day', 'Current streak: {n} days')}</dd>}
        {stats.streaks.longest.days > 1 && <dd className="stat-foot">{plural(stats.streaks.longest.days, 'Longest streak: {n} day', 'Longest streak: {n} days')}</dd>}</div>
    </dl>
    <Card title={chartTitle} note={`${fmtSpan(from, to)} · ${t('UTC')}`} wide><TimeChart bins={bins} grain={grain} weekStart={prefs.week_start} /></Card>
    <div className="stat-cards">
      <Card title={t('Time by media type')}>{played.length ? <TypeTable rows={played} /> : <p className="stat-none">{t('Nothing played in this period.')}</p>}</Card>
      <Card title={t('Your library')} note={t('All time')}>{owned.length ? <>
        <table className="stat-table"><caption className="sr-only">{t('Titles in your library by media type')}</caption>
          <thead><tr><th scope="col">{t('Type')}</th><th scope="col" className="num">{t('Titles')}</th><th scope="col" className="num">{t('Completed')}</th></tr></thead>
          <tbody>{owned.map(b => <tr key={b.media_type}><th scope="row">{typeLabel(b.media_type)}</th><td className="num">{count(b.entries)}</td><td className="num">{count(b.completed)}</td></tr>)}</tbody>
          {owned.length > 1 && <tfoot><tr><th scope="row">{t('All')}</th><td className="num">{count(owned.reduce((n, b) => n + b.entries, 0))}</td><td className="num">{count(owned.reduce((n, b) => n + b.completed, 0))}</td></tr></tfoot>}
        </table>
        {stats.statuses.length > 0 && <p className="stat-card-foot">{stats.statuses.map(s => `${statusText(s.name)} ${count(s.count)}`).join(' · ')}</p>}</> : <p className="stat-none">{t('Your library is empty.')}</p>}</Card>
      {stats.genres.length > 0 && <RankCard title={t('Top genres')} rows={stats.genres.map(g => ({ name: g.name, value: g.count, text: titles(g.count) }))} />}
      {stats.people.length > 0 && <RankCard title={t('People you keep coming back to')} rows={stats.people.map(g => ({ name: g.name, value: g.count, text: titles(g.count) }))} />}
      {stats.studios.length > 0 && <RankCard title={t('Studios and networks')} rows={stats.studios.map(g => ({ name: g.name, value: g.count, text: titles(g.count) }))} />}
      {stats.most_played.length > 0 && <RankCard title={t('Most played')} rows={stats.most_played.map(e => ({ key: e.id, name: name(e), to: `/library/${e.id}`, value: e.plays, text: plural(e.plays, '{n} play', '{n} plays') }))} />}
      {ratingsOn && stats.top_rated.length > 0 && <RankCard title={t('Your top rated')} note={t('All time')} bars={false} rows={stats.top_rated.map(e => ({ key: e.id, name: name(e), to: `/library/${e.id}`, value: e.rating ?? 0, text: e.rating != null ? rating(e.rating, prefs.rating_scale) : '–' }))} />}
      <CreatorsCard creators={stats.creators} />
    </div>
    <YearsCard />
  </>;
}

function useNarrow() {
  const query = '(max-width: 767px)';
  const [narrow, setNarrow] = useState(() => window.matchMedia(query).matches);
  useEffect(() => { const m = window.matchMedia(query); const on = () => setNarrow(m.matches); m.addEventListener('change', on); return () => m.removeEventListener('change', on); }, []);
  return narrow;
}

function Card({ title, note, wide, children }: { title: string; note?: ReactNode; wide?: boolean; children: ReactNode }) {
  return <section className={`stat-card${wide ? ' wide' : ''}`}><header className="stat-card-head"><h2>{title}</h2>{note && <span>{note}</span>}</header>{children}</section>;
}

type RankRow = { key?: string; name: string; to?: string; value: number; text: string };
/** A ranked card: eight rows, and "Show all" in its header when there are more. */
function RankCard({ title, note, rows, bars = true }: { title: string; note?: string; rows: RankRow[]; bars?: boolean }) {
  const [all, setAll] = useState(false); const cap = 8;
  const more = rows.length > cap && <button className="text-button rank-more" aria-expanded={all} onClick={() => setAll(!all)}>{all ? t('Show fewer') : t('Show all {count}', { count: rows.length })}</button>;
  return <Card title={title} note={more || note}><RankList label={title} rows={all ? rows : rows.slice(0, cap)} bars={bars} /></Card>;
}

function Delta({ compare, now, before, against, format }: { compare: boolean; now: number; before?: number; against: string; format: (n: number) => string }) {
  if (!compare) return null;
  // Keep the line while the previous period loads, so the strip does not jump.
  if (before === undefined) return <dd className="stat-delta" aria-hidden="true">&nbsp;</dd>;
  const c = change(now, before);
  // Growth from a tiny base reads better as the change itself: "+35 (from 2)", not "+1,750%".
  const text = c.kind === 'same' ? t('No change') : c.kind === 'new' ? t('+{value} (new)', { value: format(now) })
    : c.kind === 'percent' && c.value > 500 ? t('+{value} (from {before})', { value: format(now - before), before: format(before) })
    : c.kind === 'percent' ? `${c.value > 0 ? '+' : '−'}${Math.abs(c.value).toLocaleString(locale)}%` : '–';
  return <dd className="stat-delta"><span className="num">{text}</span> {against}</dd>;
}

function TypeTable({ rows }: { rows: Schema['TypeStats'][] }) {
  const max = Math.max(1, ...rows.map(r => r.minutes)); const sessions = rows.some(r => r.sessions > 0);
  return <table className="stat-table"><caption className="sr-only">{t('Time by media type')}</caption>
    <thead><tr><th scope="col">{t('Type')}</th><th scope="col" className="num">{t('Time')}</th><th scope="col" className="num">{t('Plays')}</th>{sessions && <th scope="col" className="num">{t('Sessions')}</th>}</tr></thead>
    <tbody>{rows.map(r => <tr key={r.media_type}><th scope="row">{typeLabel(r.media_type)}<span className="rank-bar" aria-hidden="true"><span style={{ width: `${(r.minutes / max) * 100}%` }} /></span></th>
      <td className="num">{r.minutes ? duration(r.minutes) : '–'}</td><td className="num">{count(r.plays)}</td>{sessions && <td className="num">{count(r.sessions)}</td>}</tr>)}</tbody>
    {rows.length > 1 && <tfoot><tr><th scope="row">{t('All')}</th><td className="num">{duration(rows.reduce((n, r) => n + r.minutes, 0))}</td><td className="num">{count(rows.reduce((n, r) => n + r.plays, 0))}</td>{sessions && <td className="num">{count(rows.reduce((n, r) => n + r.sessions, 0))}</td>}</tr></tfoot>}</table>;
}

/** A ranked list: name and value on one line, a thin grey bar under it. */
function RankList({ rows, label, bars = true }: { rows: RankRow[]; label: string; bars?: boolean }) {
  const max = Math.max(1, ...rows.map(r => r.value));
  return <ol className={`rank-rows${bars ? '' : ' plain'}`} aria-label={label}>{rows.map((r, i) => <li key={r.key ?? r.name}>
      <span className="rank-index num" aria-hidden="true">{i + 1}</span>
      <span className="rank-name">{r.to ? <Link to={r.to}>{r.name}</Link> : r.name}</span>
      <span className="rank-value num">{r.text}</span>
      {bars && <span className="rank-bar" aria-hidden="true"><span style={{ width: `${(r.value / max) * 100}%` }} /></span>}
    </li>)}</ol>;
}

// Credits of film, TV, anime and video are already in "People you keep coming back to".
const creatorTypes = ['book', 'audiobook', 'comic', 'manga', 'game', 'boardgame', 'music', 'podcast'];
export function creatorGroups(creators: Schema['Creator'][]) {
  const groups = new Map<string, Schema['Creator'][]>();
  for (const c of creators) if (creatorTypes.includes(c.media_type)) groups.set(c.media_type, [...(groups.get(c.media_type) ?? []), c]);
  return [...groups];
}

export function CreatorsCard({ creators }: { creators: Schema['Creator'][] }) {
  const groups = creatorGroups(creators);
  if (groups.length === 0) return null;
  return <Card title={t('Top creators')}>{groups.map(([type, list]) => <div key={type} className="creator-group"><h3>{typeLabel(type)}</h3>
    <RankList label={t('Top creators: {type}', { type: typeLabel(type) })} rows={list.map(c => ({ name: c.name, value: c.count, text: titles(c.count) }))} /></div>)}</Card>;
}

/** Time per bin as columns on a time axis, with round y ticks. Empty bins stay empty. */
function TimeChart({ bins, grain, weekStart }: { bins: Bin[]; grain: Grain; weekStart: 'monday' | 'sunday' }) {
  const peak = Math.max(0, ...bins.map(b => b.minutes));
  const ticks = minuteTicks(peak); const top = ticks[ticks.length - 1];
  const labels = axisLabels(bins, grain, weekStart);
  const binName = (b: Bin) => grain === 'day' ? utcDate(b.start).toLocaleDateString(locale, { weekday: 'short', month: 'short', day: 'numeric', timeZone: 'UTC' }) : grain === 'week' ? t('Week of {date}', { date: fmtDay(b.start) }) : fmtMonth(b.start, true);
  const busiest = bins.reduce<Bin | null>((a, b) => b.minutes > (a?.minutes ?? 0) ? b : a, null);
  const yearOnly = grain === 'month' && labels.every(l => l.date.slice(5, 7) === '01');
  const labelText = (l: { date: string; unit: 'day' | 'month' }, i: number) => l.unit === 'day' ? fmtDay(l.date) : yearOnly ? l.date.slice(0, 4) : fmtMonth(l.date, l.date.slice(5, 7) === '01' || i === 0);
  const summary = busiest ? t('{grain}; the most was {when} with {time}.', { grain: { day: t('Time per day'), week: t('Time per week'), month: t('Time per month') }[grain], when: binName(busiest), time: duration(busiest.minutes) }) : t('No timed plays in this period.');
  return <figure className="time-chart">
    <div className="time-chart-y" aria-hidden="true">{ticks.map(v => <span key={v} style={{ bottom: `${(v / top) * 100}%` }}>{v === 0 ? '0' : duration(v)}</span>)}</div>
    <div className="time-chart-plot" role="img" aria-label={summary}>
      {ticks.map(v => <span key={v} className={`time-chart-rule${v === 0 ? ' base' : ''}`} style={{ bottom: `${(v / top) * 100}%` }} />)}
      <div className="time-chart-bars">{bins.map(b => <span key={b.start} className="time-chart-bin" title={`${binName(b)}: ${b.minutes ? duration(b.minutes) : t('nothing recorded')}${b.plays ? ` · ${plural(b.plays, '{n} play', '{n} plays')}` : ''}`}>
        {b.minutes > 0 && <span className={b === busiest ? 'peak' : ''} style={{ height: `${(b.minutes / top) * 100}%` }} />}</span>)}</div>
      {!busiest && <p className="time-chart-empty">{t('No timed plays in this period.')}</p>}
    </div>
    <div className="time-chart-x" aria-hidden="true">{labels.map((l, i) => { const left = ((l.index + 0.5) / bins.length) * 100; return <span key={l.index} className={left < 4 ? 'start' : left > 96 ? 'end' : ''} style={{ left: `${left}%` }}>{labelText(l, i)}</span>; })}</div>
  </figure>;
}

// Every year with activity, with the change in time against the year before.
function YearsCard() {
  const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'stats-years'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats/years', { signal })) });
  const years = [...(query.data?.items ?? [])].reverse();
  if (years.length === 0) return null;
  const max = Math.max(1, ...years.map(y => y.minutes));
  const delta = (y: Schema['YearStats']) => {
    const prev = years.find(x => x.year === y.year - 1);
    const c = change(y.minutes, prev ? prev.minutes : years.some(x => x.year < y.year) ? 0 : undefined);
    return c.kind === 'none' ? '–' : c.kind === 'same' ? '0%' : c.kind === 'new' ? t('new') : `${c.value > 0 ? '+' : '−'}${Math.abs(c.value).toLocaleString(locale)}%`;
  };
  return <Card title={t('Year by year')} note={t('UTC')} wide>
    <table className="stat-table year-table"><caption className="sr-only">{t('Year by year')}</caption>
      <thead><tr><th scope="col">{t('Year')}</th><th scope="col" className="num">{t('Time spent')}</th><th scope="col"><span className="sr-only">{t('Share of the busiest year')}</span></th><th scope="col" className="num">{t('vs previous year')}</th><th scope="col" className="num">{t('Plays')}</th><th scope="col" className="num">{t('Active days')}</th><th scope="col" className="num">{t('Finished')}</th></tr></thead>
      <tbody>{years.map(y => <tr key={y.year}>
        <th scope="row"><Link to={`/review/${y.year}`}>{y.year}</Link></th><td className="num">{duration(y.minutes)}</td>
        <td className="year-bar"><span className="rank-bar" aria-hidden="true"><span style={{ width: `${(y.minutes / max) * 100}%` }} /></span></td>
        <td className="num subtle">{delta(y)}</td><td className="num">{count(y.plays)}</td><td className="num">{count(y.active_days)}</td><td className="num">{count(y.completed)}</td></tr>)}</tbody></table>
    <ol className="year-list">{years.map(y => <li key={y.year}>
      <Link to={`/review/${y.year}`} className="year-list-name">{y.year}</Link><span className="num">{duration(y.minutes)}</span>
      <p>{[plural(y.plays, '{n} play', '{n} plays'), plural(y.active_days, '{n} active day', '{n} active days'), plural(y.completed, '{n} finished', '{n} finished'), delta(y) === '–' ? '' : t('{change} vs {year}', { change: delta(y), year: y.year - 1 })].filter(Boolean).join(' · ')}</p>
    </li>)}</ol>
  </Card>;
}
