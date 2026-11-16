import type { CSSProperties } from 'react';
import { Link, Navigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ChevronLeft, ChevronRight } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { Cover, typeLabel } from '../media';
import { Empty, Failed, Loading, PageTitle } from '../ui';
import { duration } from '../format';
import { locale, plural, t } from '../i18n';
import { change, reviewYear } from '../statsPolicy';
import './review.css';

type Review = Schema['YearReview'];

export function ReviewPage() {
  const params = useParams(); const year = Number(params.year);
  if (!params.year || !/^\d{4}$/.test(params.year) || year < 1900 || year > new Date().getUTCFullYear()) return <Navigate replace to={`/review/${reviewYear()}`} />;
  return <ReviewYear key={year} year={year} />;
}

const utc = (iso: string) => new Date(iso.length === 10 ? `${iso}T00:00:00Z` : iso);
const monthName = (month: number, style: 'short' | 'long') => new Date(Date.UTC(2000, month - 1, 1)).toLocaleDateString(locale, { month: style, timeZone: 'UTC' });
const dayName = (iso: string) => utc(iso).toLocaleDateString(locale, { weekday: 'long', month: 'long', day: 'numeric', timeZone: 'UTC' });
const shortDay = (iso: string) => utc(iso).toLocaleDateString(locale, { month: 'long', day: 'numeric', timeZone: 'UTC' });
const daySpan = (from: string, to: string) => new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', timeZone: 'UTC' }).formatRange(utc(from), utc(to));
/** Chart labels stay short: whole hours from one hour up, minutes below. */
const roughTime = (minutes: number) => minutes >= 60 || minutes === 0 ? t('{hours} h', { hours: Math.round(minutes / 60).toLocaleString(locale) }) : duration(minutes);

function ReviewYear({ year }: { year: number }) {
  const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'review', year), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/review/{year}', { signal, params: { path: { year } } })) });
  const years = useQuery({ queryKey: privateKey(user.id, 'stats-years'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/stats/years', { signal })) });
  const known = (years.data?.items ?? []).map(y => y.year);
  const before = known.filter(y => y < year).pop(); const after = known.find(y => y > year);
  const review = query.data; const s = review?.stats;
  const lastDay = new Date(Date.UTC(year, 11, 31)).toLocaleDateString(locale, { month: 'long', day: 'numeric', timeZone: 'UTC' });
  const facts = s && s.plays + s.sessions > 0 ? { time: duration(s.minutes), days: plural(s.days.length, '{n} day', '{n} days'), finished: plural(s.completed, '{n} title finished', '{n} titles finished'), date: lastDay } : undefined;
  const description = !facts ? undefined : review?.complete ? t('{time} over {days}, {finished}.', facts) : t('So far: {time} over {days}, {finished}. The year fills in until {date}.', facts);
  return <div className="review-page"><PageTitle title={t('Your {year}', { year })} description={description}
    actions={(before || after) && <nav className="review-nav" aria-label={t('Other years')}>
      {before && <Link className="button" to={`/review/${before}`}><ChevronLeft aria-hidden />{before}</Link>}
      {after && <Link className="button" to={`/review/${after}`}>{after}<ChevronRight aria-hidden /></Link>}</nav>} />
    {query.isPending ? <Loading label={t('Putting your year together')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : <ReviewBody review={query.data} />}</div>;
}

function ReviewBody({ review }: { review: Review }) {
  const s = review.stats;
  if (s.plays === 0 && s.sessions === 0) return <Empty title={t('Nothing recorded in {year}', { year: review.year })} action={<Link className="button" to="/stats">{t('Statistics')}</Link>}>{t('Plays, sessions and finished titles from that year appear here.')}</Empty>;
  const busiest = review.months.reduce((a, b) => b.minutes > a.minutes ? b : a);
  const types = review.types.filter(x => x.minutes > 0 || x.plays > 0 || x.sessions > 0).sort((a, b) => b.minutes - a.minutes || b.plays - a.plays);
  const total = Math.max(1, types.reduce((n, x) => n + x.minutes, 0));
  return <>
    <section className="review-card" aria-labelledby="review-months">
      <header className="review-card-head"><h2 id="review-months">{t('Month by month')}</h2>{busiest.minutes > 0 && <span>{t('Busiest: {month}, {time}', { month: monthName(busiest.month, 'long'), time: duration(busiest.minutes) })}</span>}</header>
      <MonthBars review={review} busiest={busiest.month} />
    </section>
    <section className="review-card" aria-labelledby="review-moments">
      <header className="review-card-head"><h2 id="review-moments">{t('Moments')}</h2></header>
      <Moments review={review} />
    </section>
    <section className="review-card" aria-labelledby="review-where">
      <header className="review-card-head"><h2 id="review-where">{t('Where your time went')}</h2><span>{t('Your biggest titles by time, then plays')}</span></header>
      <div className="review-types">{types.map(x => {
        const share = Math.round((x.minutes / total) * 100);
        return <section key={x.media_type} className="review-type" aria-label={typeLabel(x.media_type)}>
          <div className="review-type-head"><h3>{typeLabel(x.media_type)}</h3><span className="num">{[x.minutes ? duration(x.minutes) : '', x.minutes ? t('{share}% of your time', { share }) : plural(x.plays + x.sessions, '{n} play', '{n} plays')].filter(Boolean).join(' · ')}</span></div>
          <span className="review-share" aria-hidden="true"><span style={{ width: `${(x.minutes / total) * 100}%` }} /></span>
          <ol className="review-titles">{x.titles.map(e => <li key={e.id}>
            <Link to={`/library/${e.id}`} className="review-cover" tabIndex={-1} aria-hidden="true"><Cover entry={{ title: e.title, media_type: e.media_type, image_url: e.image_url }} compact /></Link>
            <div><Link to={`/library/${e.id}`} className="review-title">{e.localized_title ?? e.title}</Link>
              <p>{[e.minutes ? duration(e.minutes) : '', e.plays ? plural(e.plays, '{n} play', '{n} plays') : ''].filter(Boolean).join(' · ')}</p></div>
          </li>)}</ol>
        </section>;
      })}</div>
    </section>
  </>;
}

/** Twelve months as bars with their time written on them; the busiest one in full colour. */
function MonthBars({ review, busiest }: { review: Review; busiest: number }) {
  const peak = Math.max(1, ...review.months.map(m => m.minutes));
  const now = new Date(); const future = (m: number) => review.year === now.getUTCFullYear() && m > now.getUTCMonth() + 1;
  return <ol className="review-months">{review.months.map(m => {
    const label = `${monthName(m.month, 'long')}: ${future(m.month) ? t('still to come') : m.minutes ? duration(m.minutes) : t('nothing recorded')}`;
    return <li key={m.month} className={`${m.month === busiest && m.minutes > 0 ? 'busiest' : ''}${future(m.month) ? ' future' : ''}${m.minutes ? '' : ' empty'}`} title={label}>
      <span className="sr-only">{label}</span>
      <span className="month-track" aria-hidden="true" style={{ '--v': m.minutes / peak } as CSSProperties}>
        {m.minutes > 0 && <span className="month-fill" />}
        <span className="month-value num">{future(m.month) ? '–' : roughTime(m.minutes)}</span>
      </span>
      <span className="month-name" aria-hidden="true">{monthName(m.month, 'short')}</span>
    </li>;
  })}</ol>;
}

function Moments({ review }: { review: Review }) {
  const s = review.stats; const p = review.previous ?? undefined; const streak = s.streaks.longest;
  const genre = s.genres[0];
  const first = review.first_play;
  const against = p ? change(s.minutes, p.minutes) : undefined;
  return <dl className="review-moments">
    {first && <div className="moment wide"><dt>{t('First play of the year')}</dt><dd className="moment-title">
      <Link to={`/library/${first.entry_id}`} className="review-cover" tabIndex={-1} aria-hidden="true"><Cover entry={{ title: first.entry_title, media_type: first.media_type, image_url: first.image_url }} compact /></Link>
      <span><Link to={`/library/${first.entry_id}`} className="review-title">{first.entry_title}</Link><span className="moment-sub">{dayName(first.played_at)}</span></span></dd></div>}
    {review.busiest_day && review.busiest_day.minutes > 0 && <div className="moment"><dt>{t('Busiest day')}</dt><dd>{shortDay(review.busiest_day.date)}<span className="moment-sub">{duration(review.busiest_day.minutes)}</span></dd></div>}
    {streak.days > 1 && streak.from && streak.to && <div className="moment"><dt>{t('Longest streak')}</dt><dd>{plural(streak.days, '{n} day', '{n} days')}<span className="moment-sub">{daySpan(streak.from, streak.to)}</span></dd></div>}
    <div className="moment"><dt>{t('New to you')}</dt><dd>{plural(review.new_titles, '{n} title', '{n} titles')}{review.returning > 0 && <span className="moment-sub">{plural(review.returning, 'and {n} you came back to', 'and {n} you came back to')}</span>}</dd></div>
    {genre && <div className="moment"><dt>{t('Top genre')}</dt><dd>{genre.name}<span className="moment-sub">{plural(genre.count, '{n} title', '{n} titles')}</span></dd></div>}
    {against && against.kind !== 'none' && <div className="moment"><dt>{t('Against {year}', { year: review.year - 1 })}</dt><dd className="num">{against.kind === 'same' ? t('Same time') : against.kind === 'new' ? t('New this year') : t('{change}% time', { change: `${against.value > 0 ? '+' : '−'}${Math.abs(against.value).toLocaleString(locale)}` })}<span className="moment-sub">{t('{time} in {year}', { time: duration(p!.minutes), year: review.year - 1 })}</span></dd></div>}
  </dl>;
}
