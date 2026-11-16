import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarCheck, CalendarPlus, Check, ChevronLeft, ChevronRight, Copy, Play } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { usePreferences } from '../organize';
import { Cover, typeLabel } from '../media';
import { Empty, ErrorNote, Failed, Loading, Modal, PageTitle, useAction, useConfirm } from '../ui';
import { clock, episodeCode } from '../format';
import { locale, plural, t } from '../i18n';
import { calendarEntryPath, calendarIsEpisode, calendarMonthDays, calendarMonthRange, calendarReleaseKey, shiftCalendarMonth, visibleCalendarReleases } from '../calendarPolicy';
import { BloemHandoffSettings } from './bloemHandoff';
import './calendar.css';

type Release = Schema['CalendarRelease'];
type Filter = 'all' | 'following' | 'favourites' | 'watchlist';

const releaseTypes: Record<string, string> = { theatrical: t('In cinemas'), digital: t('Digital release'), physical: t('Disc release') };
/** What a title-level release is: its type, or for a movie its regional release kind. */
const releaseLabel = (r: Release) => r.release_type ? releaseTypes[r.release_type] : typeLabel(r.media_type);
const episodeName = (r: Release) => r.title && r.title !== r.entry_title ? r.title : '';
const releaseDetail = (r: Release) => calendarIsEpisode(r) ? [episodeCode(r.season_number, r.episode_number), episodeName(r)].filter(Boolean).join(' · ') : releaseLabel(r);
const shortDetail = (r: Release) => calendarIsEpisode(r) ? episodeCode(r.season_number, r.episode_number) || episodeName(r) : releaseLabel(r);

const localDate = (date: string) => new Date(`${date}T00:00:00`);
const todayKey = () => new Date().toLocaleDateString('en-CA');
const dayName = (date: string) => {
  const d = localDate(date);
  return d.toLocaleDateString(locale, d.getFullYear() === new Date().getFullYear() ? { weekday: 'long', month: 'long', day: 'numeric' } : { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' });
};
const monthName = (month: string) => localDate(`${month}-01`).toLocaleDateString(locale, { month: 'long', year: 'numeric' });
/** "Today", "Tomorrow", "In 3 days". */
function relativeDay(date: string) {
  const days = Math.round((localDate(date).getTime() - localDate(todayKey()).getTime()) / 864e5);
  const text = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(days, 'day');
  return text.charAt(0).toLocaleUpperCase(locale) + text.slice(1);
}
function byDate(releases: Release[]) {
  const groups = new Map<string, Release[]>();
  for (const r of releases) groups.set(r.date, [...(groups.get(r.date) ?? []), r]);
  return groups;
}

export function CalendarPage() {
  const user = useUser(); const { prefs } = usePreferences();
  const [view, setView] = useState<'agenda' | 'month'>('agenda');
  const thisMonth = todayKey().slice(0, 7);
  const [month, setMonth] = useState(thisMonth);
  const [selectedDay, setSelectedDay] = useState<string | null>(null);
  const [filter, setFilter] = useState<Filter>('all');
  const [subscribing, setSubscribing] = useState(false);
  // Releases shared by a connected server expire on the minute; the list follows the clock.
  const [now, setNow] = useState(Date.now);
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => window.clearInterval(timer); }, []);
  const agendaTo = new Date(now + 90 * 864e5).toISOString().slice(0, 10);
  const range = view === 'month' ? calendarMonthRange(month)! : { to: agendaTo };
  const query = useQuery({ refetchInterval: 60000, queryKey: privateKey(user.id, 'calendar', filter, range), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/calendar', { signal, params: { query: { filter, ...range } } })).items });
  const feed = useQuery({ queryKey: privateKey(user.id, 'calendar-feed'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/calendar-feed', { signal })) });
  const releases = visibleCalendarReleases(query.data ?? [], now);
  const description = !query.data ? undefined : view === 'month'
    ? plural(releases.length, '{n} release in {month}.', '{n} releases in {month}.', { month: monthName(month) })
    : plural(releases.length, '{n} release in the next 90 days.', '{n} releases in the next 90 days.');
  const dayReleases = selectedDay ? releases.filter(r => r.date === selectedDay) : [];
  const body = (content: () => ReactNode) => query.isPending ? <Loading label={t('Loading releases')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : content();
  return <div className="calendar-page">
    <PageTitle title={t('Calendar')} description={description} actions={<button className="button" onClick={() => setSubscribing(true)}>{feed.data?.enabled ? <CalendarCheck aria-hidden /> : <CalendarPlus aria-hidden />}{feed.data?.enabled ? t('Subscribed') : t('Subscribe')}</button>} />
    <div className="calendar-tools">
      <div className="calendar-view" role="group" aria-label={t('Calendar view')}>
        <button aria-pressed={view === 'agenda'} onClick={() => setView('agenda')}>{t('Agenda')}</button>
        <button aria-pressed={view === 'month'} onClick={() => setView('month')}>{t('Month')}</button>
      </div>
      <label className="select-pill calendar-filter"><span className="sr-only">{t('Show releases from')}</span><select name="filter" value={filter} onChange={e => setFilter(e.target.value as Filter)}>
        <option value="all">{t('All releases')}</option><option value="following">{t('Following')}</option><option value="favourites">{t('Favourites')}</option><option value="watchlist">{t('Watchlist')}</option>
      </select></label>
    </div>
    {view === 'agenda'
      ? <div className="calendar-layout">
          <div className="calendar-main">{body(() => releases.length ? <ReleaseDays releases={releases} /> : <Empty title={t('Nothing scheduled')}>{t('Releases appear here for titles you plan to watch or are watching. Shows load their upcoming episodes in the background after you add them.')}</Empty>)}</div>
          {releases.length > 0 && <aside className="calendar-rail" aria-label={t('Release calendar')}>
            <MiniMonth releases={releases} weekStart={prefs.week_start} first={thisMonth} last={agendaTo.slice(0, 7)} />
            <ComingUp releases={releases} />
          </aside>}
        </div>
      : <section className="month-view" aria-labelledby="month-view-title">
          <div className="month-view-head">
            <h2 id="month-view-title">{monthName(month)}</h2>
            <div className="month-view-nav">
              {month !== thisMonth && <button className="text-button" onClick={() => setMonth(thisMonth)}>{t('This month')}</button>}
              <button className="icon-button" aria-label={t('Previous month')} disabled={shiftCalendarMonth(month, -1) === month} onClick={() => setMonth(shiftCalendarMonth(month, -1))}><ChevronLeft aria-hidden /></button>
              <button className="icon-button" aria-label={t('Next month')} disabled={shiftCalendarMonth(month, 1) === month} onClick={() => setMonth(shiftCalendarMonth(month, 1))}><ChevronRight aria-hidden /></button>
            </div>
          </div>
          {body(() => <MonthGrid month={month} releases={releases} weekStart={prefs.week_start} onDay={setSelectedDay} />)}
        </section>}
    {user.role !== 'member' && <BloemHandoffSettings />}
    {selectedDay && view === 'month' && <Modal title={dayName(selectedDay)} description={plural(dayReleases.length, '{n} release', '{n} releases')} onClose={() => setSelectedDay(null)}><ReleaseRows releases={dayReleases} /></Modal>}
    {subscribing && <SubscriptionDialog onClose={() => setSubscribing(false)} />}
  </div>;
}

/** The agenda: releases by day, the way History lists plays. */
function ReleaseDays({ releases }: { releases: Release[] }) {
  const today = todayKey();
  return <ol className="release-days">{[...byDate(releases)].map(([date, items]) => <li key={date} id={`releases-${date}`} className={`release-day${date === today ? ' is-today' : ''}`}>
    <div className="release-day-head"><h2><time dateTime={date}>{dayName(date)}</time></h2><span>{relativeDay(date)}</span></div>
    <ReleaseRows releases={items} />
  </li>)}</ol>;
}

function ReleaseRows({ releases }: { releases: Release[] }) {
  return <ul className="release-rows">{releases.map(r => {
    const path = calendarEntryPath(r);
    const cover = <Cover entry={{ title: r.entry_title, media_type: r.media_type, image_url: r.image_url }} compact />;
    const tags = [r.watched ? t('Watched') : '', r.playable ? t('In Silo') : ''].filter(Boolean);
    return <li key={calendarReleaseKey(r)} className="release-row">
      {path ? <Link to={path} className="release-cover" tabIndex={-1} aria-hidden="true">{cover}</Link> : <span className="release-cover" aria-hidden="true">{cover}</span>}
      <div className="release-main">
        {path ? <Link to={path} className="release-title">{r.entry_title}</Link> : <span className="release-title">{r.entry_title}</span>}
        <p className="release-detail"><span>{releaseDetail(r)}</span>{tags.map(tag => <span key={tag} className="release-tag">{tag}</span>)}</p>
        {r.handoff && <a className="release-play" href={r.handoff.url} rel="noreferrer"><Play aria-hidden />{r.handoff.label}</a>}
      </div>
      <span className="release-time num">{r.air_at ? <time dateTime={r.air_at}>{clock(r.air_at)}</time> : null}</span>
    </li>;
  })}</ul>;
}

/** The rail's month: days with releases are marked and jump to their place in the agenda. */
function MiniMonth({ releases, weekStart, first, last }: { releases: Release[]; weekStart: 'monday' | 'sunday'; first: string; last: string }) {
  const [month, setMonth] = useState(first);
  const cells = calendarMonthDays(month, weekStart);
  const groups = useMemo(() => byDate(releases), [releases]);
  const today = todayKey();
  const names = cells.slice(0, 7).map(d => localDate(d).toLocaleDateString(locale, { weekday: 'narrow' }));
  return <section className="mini-month">
    <div className="mini-month-head">
      <h2>{monthName(month)}</h2>
      <div>
        <button className="icon-button" aria-label={t('Previous month')} disabled={month <= first} onClick={() => setMonth(shiftCalendarMonth(month, -1))}><ChevronLeft aria-hidden /></button>
        <button className="icon-button" aria-label={t('Next month')} disabled={month >= last} onClick={() => setMonth(shiftCalendarMonth(month, 1))}><ChevronRight aria-hidden /></button>
      </div>
    </div>
    <div className="mini-month-grid" aria-label={t('Release days in {month}', { month: monthName(month) })}>
      {names.map((n, i) => <span key={i} className="mini-weekday" aria-hidden="true">{n}</span>)}
      {cells.map(d => {
        if (d.slice(0, 7) !== month) return <span key={d} className="mini-day outside" aria-hidden="true" />;
        const items = groups.get(d) ?? [];
        const cls = `mini-day${items.length ? ' has-release' : ''}${d === today ? ' today' : ''}`;
        return items.length
          ? <button key={d} className={cls} aria-label={`${dayName(d)}: ${items.map(r => r.entry_title).join(', ')}`} title={items.map(r => `${r.entry_title} ${shortDetail(r)}`).join('\n')} onClick={() => document.getElementById(`releases-${d}`)?.scrollIntoView({ block: 'start' })}>{Number(d.slice(8))}</button>
          : <span key={d} className={cls}>{Number(d.slice(8))}</span>;
      })}
    </div>
  </section>;
}

/** Which titles have something coming, and how much. */
function ComingUp({ releases }: { releases: Release[] }) {
  const titles = new Map<string, { title: string; path: string | null; episodes: number; other: number; next: string }>();
  for (const r of releases) {
    const key = r.entry_id && r.entry_id !== '0' ? r.entry_id : r.entry_title;
    const row = titles.get(key) ?? { title: r.entry_title, path: calendarEntryPath(r), episodes: 0, other: 0, next: r.date };
    if (calendarIsEpisode(r)) row.episodes++; else row.other++;
    titles.set(key, row);
  }
  const rows = [...titles.values()].sort((a, b) => a.next.localeCompare(b.next)).slice(0, 8);
  return <section className="coming-up"><h2>{t('Coming up')}</h2><ul>{rows.map(row => <li key={row.title}>
    {row.path ? <Link to={row.path}>{row.title}</Link> : <span>{row.title}</span>}
    <span className="num">{row.episodes && !row.other ? plural(row.episodes, '{n} episode', '{n} episodes') : plural(row.episodes + row.other, '{n} release', '{n} releases')}</span>
  </li>)}</ul></section>;
}

/** The month: every release named in its day, up to three, then a count. */
function MonthGrid({ month, releases, weekStart, onDay }: { month: string; releases: Release[]; weekStart: 'monday' | 'sunday'; onDay: (date: string) => void }) {
  const days = calendarMonthDays(month, weekStart);
  const groups = byDate(releases); const today = todayKey();
  return <div className="month-grid-frame">
    <div className="month-weekdays" aria-hidden="true">{days.slice(0, 7).map(d => <span key={d}>{localDate(d).toLocaleDateString(locale, { weekday: 'short' })}</span>)}</div>
    <ol className="month-grid">{days.map(date => {
      const items = groups.get(date) ?? []; const outside = !date.startsWith(month);
      const inner = <>
        <time className="month-date" dateTime={date} aria-current={date === today ? 'date' : undefined}>{Number(date.slice(-2))}</time>
        {items.slice(0, 3).map(r => <span key={calendarReleaseKey(r)} className="month-release"><span className="month-release-title">{r.entry_title}</span><span className="month-release-detail">{shortDetail(r)}</span></span>)}
        {items.length > 3 && <span className="month-more">{t('{count} more', { count: items.length - 3 })}</span>}
        {items.length > 0 && <span className="month-dots" aria-hidden="true">{items.slice(0, 3).map(r => <span key={calendarReleaseKey(r)} />)}</span>}
      </>;
      return <li key={date} className={`month-cell${outside ? ' outside' : ''}${date === today ? ' is-today' : ''}`}>
        {items.length
          ? <button className="month-day" onClick={() => onDay(date)} aria-label={`${dayName(date)}: ${items.map(r => `${r.entry_title} ${shortDetail(r)}`).join(', ')}`}>{inner}</button>
          : <div className="month-day">{inner}</div>}
      </li>;
    })}</ol>
  </div>;
}

/** Turn the calendar subscription on, replace its link, or turn it off. The link is shown once. */
function SubscriptionDialog({ onClose }: { onClose: () => void }) {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const [url, setUrl] = useState<string | null>(null); const [copied, setCopied] = useState(false);
  const status = useQuery({ queryKey: privateKey(user.id, 'calendar-feed'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/calendar-feed', { signal })) });
  const rotate = useAction(async (_: void) => { setUrl(unwrap(await api.POST('/api/v1/me/calendar-feed')).url); await client.invalidateQueries({ queryKey: privateKey(user.id, 'calendar-feed') }); });
  const revoke = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/me/calendar-feed')); await client.invalidateQueries({ queryKey: privateKey(user.id, 'calendar-feed') }); });
  const enabled = !!status.data?.enabled;
  return <Modal title={t('Calendar subscription')} description={t('Add your releases to Apple Calendar, Google Calendar or Outlook. Anyone with the link can see your release dates, so keep it private.')} onClose={onClose} busy={rotate.pending || revoke.pending}>
    {status.isPending ? <Loading /> : status.error ? <Failed error={status.error} retry={() => void status.refetch()} /> : url ? <>
      <label className="subscription-label" htmlFor="feed-url">{t('Subscription link')}</label>
      <textarea id="feed-url" className="secret-value" readOnly rows={3} value={url} onFocus={e => e.currentTarget.select()} />
      <p className="subscription-note">{t('This link is shown only once.')}</p>
      <div className="dialog-actions"><button className="button" onClick={onClose}>{t('Done')}</button><button className="button primary" onClick={async () => { try { await navigator.clipboard.writeText(url); setCopied(true); } catch { setCopied(false); } }}>{copied ? <Check aria-hidden /> : <Copy aria-hidden />}{copied ? t('Copied') : t('Copy link')}</button></div>
    </> : <>
      <p className="subscription-note">{enabled ? t('Your subscription is on. Calendar apps using its link keep updating.') : t('You have no subscription link yet.')}</p>
      <ErrorNote error={rotate.error || revoke.error} />
      <div className="dialog-actions">
        {enabled && <button className="button danger-outline" disabled={revoke.pending} onClick={async () => { if (await confirm({ title: t('Turn off calendar subscription?'), description: t('Calendar apps using the current link stop updating.'), label: t('Turn off'), danger: true })) void revoke.run(); }}>{t('Turn off')}</button>}
        <button className="button primary" disabled={rotate.pending} onClick={async () => { if (!enabled || await confirm({ title: t('Create a new subscription link?'), description: t('The previous link stops working.'), label: t('Create new link') })) void rotate.run(); }}>{enabled ? t('Create new link') : t('Create subscription link')}</button>
      </div>
    </>}
  </Modal>;
}
