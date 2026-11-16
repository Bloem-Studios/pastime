import { Fragment, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Ban, ChevronDown, CircleCheck, CircleX, RefreshCw, RotateCcw, TriangleAlert } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { typeLabel } from '../media';
import { privateKey, useUser } from '../state';
import { Empty, ErrorNote, Failed, Loading, PageTitle, Pager, Status, statusText, useAction, useConfirm } from '../ui';
import { clock, duration } from '../format';
import { locale, plural, t, tx } from '../i18n';
import { overviewQueue, queueProblem, useAdminOverview } from '../queueStatus';
import type { QueueHealth } from '../queuePolicy';
import { Dash, Joined, Strip, When, number, relative, sentence } from './adminParts';

type Overview = Schema['AdminOverview'];
type Provider = Schema['ProviderHealth'];

function bytes(n: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0; let v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}
const wait = (seconds: number) => duration(seconds / 60);

function useChecks() {
  const user = useUser();
  return useQuery({ queryKey: privateKey(user.id, 'admin', 'checks'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/checks', { signal })).items, staleTime: 60_000 });
}

// ---------------------------------------------------------------- dashboard

export function Dashboard() {
  const user = useUser(); const client = useQueryClient();
  const overview = useAdminOverview(); const checks = useChecks();
  const refresh = () => void client.invalidateQueries({ queryKey: privateKey(user.id, 'admin') });
  const pending = overview.isPending || checks.isPending;
  return <>
    <PageTitle title={t('Dashboard')} actions={overview.data && <span className="adm-fresh">
      <time dateTime={overview.data.generated_at}>{t('Updated {time}', { time: clock(overview.data.generated_at) })}</time>
      <button className="icon-button" aria-label={t('Refresh now')} disabled={overview.isFetching} onClick={refresh}><RefreshCw aria-hidden /></button>
    </span>} />
    {pending ? <Loading label={t('Loading instance health')} /> : overview.error ? <Failed error={overview.error} retry={() => void overview.refetch()} /> : <DashboardBody data={overview.data} checks={checks.data} checksError={checks.error} />}
  </>;
}

function DashboardBody({ data, checks, checksError }: { data: Overview; checks?: Schema['SetupCheck'][]; checksError: unknown }) {
  const queue = overviewQueue(data);
  const { counts, jobs } = data;
  const stuck = queue.problem === 'no_worker' || queue.problem === 'overdue';
  return <>
    {checks ? <Attention checks={checks} queue={queue} /> : <section className="panel adm-attention"><Failed error={checksError} /></section>}
    <Strip label={t('Instance totals')} items={[
      { label: t('Accounts'), value: number(counts.active_users), sub: counts.users > counts.active_users ? t('{n} disabled', { n: number(counts.users - counts.active_users) }) : undefined },
      { label: t('Library entries'), value: number(counts.entries) },
      { label: t('Plays recorded'), value: number(counts.plays) },
      { label: t('Catalog titles'), value: number(counts.catalog_titles) },
      { label: t('Jobs waiting'), value: number(jobs.queued), to: '/admin/jobs?status=queued', bad: stuck,
        sub: queue.problem === 'no_worker' ? t('no worker') : jobs.queued && jobs.oldest_queued_seconds != null ? t('oldest {wait}', { wait: wait(jobs.oldest_queued_seconds) }) : undefined },
      { label: t('Failed today'), value: number(jobs.failed_last_day), to: '/admin/jobs?status=failed', bad: jobs.blocked > 0,
        sub: jobs.blocked ? plural(jobs.blocked, '{n} blocked', '{n} blocked') : undefined },
    ]} />
    <div className="adm-columns">
      <div className="adm-stack">
        <section className="panel" aria-labelledby="throughput-title">
          <div className="panel-head"><h2 id="throughput-title">{t('Job throughput')}</h2><span className="subtle">{t('Last 24 hours, per hour')}</span></div>
          <Throughput hours={jobs.throughput} />
        </section>
        <ProvidersPanel providers={data.providers} />
      </div>
      <div className="adm-stack">
        <Processes nodes={data.nodes} />
        <section className="panel" aria-labelledby="database-title">
          <div className="panel-head"><h2 id="database-title">{t('Database')}</h2></div>
          <dl className="adm-facts">
            <div><dt>{t('PostgreSQL')}</dt><dd>{data.database.version.split(' ')[0]}</dd></div>
            <div><dt>{t('Size')}</dt><dd>{bytes(data.database.size_bytes)}</dd></div>
            <div><dt>{t('Connections')}</dt><dd>{t('{used} of {max}', { used: number(data.database.connections), max: number(data.database.max_connections) })}</dd>
              <dd className="adm-meter"><span className="meter" role="img" aria-label={t('{value}% of connections in use', { value: Math.round((data.database.connections / data.database.max_connections) * 100) })}><span style={{ width: `${Math.min(100, (data.database.connections / data.database.max_connections) * 100)}%` }} /></span></dd></div>
            <div><dt>{t('Schema version')}</dt><dd>{data.database.migration}</dd></div>
            <div><dt>{t('Active imports')}</dt><dd>{number(counts.active_imports)}</dd></div>
          </dl>
        </section>
      </div>
    </div>
  </>;
}

type AttentionItem = { id: string; status: 'error' | 'warning'; title: string; detail: string; link?: { to: string; label: string } };
const checkLinks: Record<string, { to: string; label: string }> = {
  worker: { to: '/admin/jobs?status=queued', label: t('View jobs') },
  tmdb: { to: '/admin/providers', label: t('View providers') },
};

/** Failing setup checks and queue trouble, first on the Dashboard; passing checks folded away. */
function Attention({ checks, queue }: { checks: Schema['SetupCheck'][]; queue: QueueHealth }) {
  const items: AttentionItem[] = checks.filter(c => c.status !== 'ok').map(c => ({
    id: c.id, status: c.status as AttentionItem['status'], title: c.title, link: checkLinks[c.id],
    detail: c.id === 'worker' && queue.problem === 'no_worker' && queue.oldest != null ? `${c.detail} ${t('The oldest job has been due for {wait}.', { wait: wait(queue.oldest) })}` : c.detail,
  }));
  if (queue.problem === 'overdue' || queue.problem === 'attention') items.push({ id: 'queue', status: 'error', title: queue.problem === 'overdue' ? t('Job queue') : t('Failed jobs'), detail: queueProblem(queue) ?? '', link: { to: queue.to, label: t('View jobs') } });
  items.sort((a, b) => Number(a.status === 'warning') - Number(b.status === 'warning'));
  const passing = checks.filter(c => c.status === 'ok');
  return <section className="panel adm-attention" aria-labelledby="attention-title">
    <div className="panel-head"><h2 id="attention-title">{t('Needs attention')}</h2>{items.length > 0 && <span className="subtle">{plural(items.length, '{n} item', '{n} items')}</span>}</div>
    {items.length ? <ul className="adm-checks">{items.map(item => <li key={item.id} className={item.status}>
      {item.status === 'error' ? <CircleX aria-label={t('Problem')} /> : <TriangleAlert aria-label={t('Warning')} />}
      <div><strong>{item.title}</strong><p>{item.detail}</p></div>
      {item.link && <Link className="text-link" to={item.link.to}>{item.link.label}</Link>}
    </li>)}</ul> : <p className="adm-calm">{t('Nothing needs attention.')}</p>}
    {passing.length > 0 && <details className="adm-more">
      <summary>{plural(passing.length, '{n} check passes', '{n} checks pass')}<ChevronDown aria-hidden /></summary>
      <ul className="adm-checks">{passing.map(c => <li key={c.id} className="ok"><CircleCheck aria-label={t('Passes')} /><div><strong>{c.title}</strong><p>{c.detail}</p></div></li>)}</ul>
    </details>}
  </section>;
}

/** Stacked hourly columns on a drawn zero line: succeeded below, failed or blocked above. */
function Throughput({ hours }: { hours: Schema['JobHour'][] }) {
  const total = hours.reduce((n, h) => n + h.succeeded, 0); const failed = hours.reduce((n, h) => n + h.failed, 0);
  const peak = Math.max(1, ...hours.map(h => h.succeeded + h.failed));
  const w = 720, h = 120, top = 6, gap = 4, col = (w - gap * (hours.length - 1)) / Math.max(1, hours.length);
  const y = (v: number) => (v / peak) * (h - top);
  return <>
    <div className="adm-chart">
      <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" role="img" aria-label={t('{total} jobs succeeded and {failed} failed in the last 24 hours.', { total: number(total), failed: number(failed) })}>
        {total + failed > 0 && [0.5, 1].map(f => <line key={f} className="grid-line" x1={0} x2={w} y1={h - (h - top) * f} y2={h - (h - top) * f} />)}
        {hours.map((hour, i) => {
          const x = i * (col + gap); const ok = y(hour.succeeded); const bad = y(hour.failed);
          return <g key={hour.hour}><title>{t('{hour}: {succeeded} succeeded, {failed} failed', { hour: new Date(hour.hour).toLocaleTimeString(locale, { hour: 'numeric' }), succeeded: hour.succeeded, failed: hour.failed })}</title>
            <rect x={x} y={0} width={col} height={h} fill="transparent" />
            {hour.succeeded > 0 && <rect x={x} y={h - ok} width={col} height={ok} rx={2} fill="var(--chart-2)" />}
            {hour.failed > 0 && <rect x={x} y={h - ok - bad - (ok ? 2 : 0)} width={col} height={bad} rx={2} fill="var(--danger)" />}
          </g>;
        })}
        <line className="baseline" x1={0} x2={w} y1={h - 0.5} y2={h - 0.5} />
      </svg>
      {total + failed === 0 && <p className="adm-chart-empty">{t('No jobs finished in the last 24 hours.')}</p>}
    </div>
    <div className="adm-axis" aria-hidden="true"><span>{t('24 h ago')}</span><span>{t('12 h ago')}</span><span>{t('Now')}</span></div>
    {total + failed > 0 && <div className="legend adm-legend"><span><i style={{ background: 'var(--chart-2)' }} />{t('Succeeded')} {number(total)}</span><span><i style={{ background: 'var(--danger)' }} />{t('Failed or blocked')} {number(failed)}</span></div>}
  </>;
}

function Processes({ nodes }: { nodes: Schema['RuntimeNode'][] }) {
  const servers = nodes.filter(n => n.role === 'server').length; const workers = nodes.length - servers;
  // Workers first; past four rows the rest fold away.
  const ordered = [...nodes].sort((a, b) => Number(a.role === 'server') - Number(b.role === 'server'));
  const shown = ordered.slice(0, 4); const more = ordered.slice(4);
  return <section className="panel" aria-labelledby="nodes-title">
    <div className="panel-head"><h2 id="nodes-title">{t('Processes')}</h2><span className="subtle">{plural(servers, '{n} server', '{n} servers')} · {workers ? plural(workers, '{n} worker', '{n} workers') : t('no worker')}</span></div>
    {nodes.length ? <>
      <ul className="adm-rows">{shown.map(node => <ProcessRow key={node.id} node={node} />)}</ul>
      {more.length > 0 && <details className="adm-more">
        <summary>{plural(more.length, '{n} more process', '{n} more processes')}<ChevronDown aria-hidden /></summary>
        <ul className="adm-rows">{more.map(node => <ProcessRow key={node.id} node={node} />)}</ul>
      </details>}
    </> : <p className="subtle">{t('No process has reported in the last 90 seconds.')}</p>}
  </section>;
}

function ProcessRow({ node: n }: { node: Schema['RuntimeNode'] }) {
  return <li>
    <div className="adm-line"><strong>{n.role === 'server' ? t('API server') : t('Worker')}</strong><span className="subtle mono">{n.version}</span></div>
    <p>{[n.hostname, t('up {time}', { time: duration((Date.now() - new Date(n.started_at).getTime()) / 60000) }), t('{mb} MB heap', { mb: n.stats.heap_mb.toFixed(0) }), t('{used} of {max} connections', { used: n.stats.pool.acquired, max: n.stats.pool.max }), n.stats.concurrency ? plural(n.stats.concurrency, '{n} slot', '{n} slots') : ''].filter(Boolean).join(' · ')}</p>
  </li>;
}

// ---------------------------------------------------------------- providers

const providerNames: Record<string, string> = { tmdb: 'TMDB', openlibrary: t('Open Library'), anilist: t('AniList'), musicbrainz: t('MusicBrainz'), itunes: t('Apple Podcasts'), igdb: 'IGDB', comicvine: t('Comic Vine'), bgg: t('BoardGameGeek'), tvdb: 'TheTVDB', mangabaka: 'MangaBaka', youtube: 'YouTube', rss: 'RSS', gcd: t('Grand Comics Database'), audnexus: 'Audnexus' };
const providerName = (name: string) => providerNames[name] ?? name;
const kindList = (kinds: string[]) => kinds.map(typeLabel).join(', ');

type Ranked = { p: Provider; failing: boolean; rate: number; uncovered: string[]; rank: number };
/**
 * Providers needing action first: failing ones, then unconfigured ones, those leaving the most
 * media types with no provider at all leading; then the rest by name.
 */
function rankProviders(providers: Provider[]): Ranked[] {
  const covered = new Set(providers.filter(p => p.configured).flatMap(p => p.kinds));
  return providers.map(p => {
    const rate = p.requests ? p.failures / p.requests : 0;
    const failing = p.configured && (p.health === 'temporarily_unavailable' || rate >= 0.2);
    return { p, rate, failing, uncovered: p.configured ? [] : p.kinds.filter(k => !covered.has(k)), rank: failing ? 0 : p.configured ? 2 : 1 };
  }).sort((a, b) => a.rank - b.rank || b.uncovered.length - a.uncovered.length || providerName(a.p.name).localeCompare(providerName(b.p.name), locale));
}

/** "Set TMDB_API_KEY" with each setting name as code. */
function Setup({ p }: { p: Provider }) {
  const names = p.credential.split(',').map(s => s.trim()).filter(Boolean);
  return <>{tx('Not configured: set {settings}', { settings: names.map((name, i) => <Fragment key={name}>{i > 0 && (i === names.length - 1 ? t(' and ') : ', ')}<code>{name}</code></Fragment>) })}</>;
}
const failingText = (r: Ranked) => r.p.health === 'temporarily_unavailable' ? statusText('temporarily_unavailable') : t('{rate}% of {requests} requests failed', { rate: (r.rate * 100).toFixed(1), requests: number(r.p.requests) });

function ProvidersPanel({ providers }: { providers: Provider[] }) {
  const ranked = rankProviders(providers);
  const action = ranked.filter(r => r.failing || r.uncovered.length);
  const rest = ranked.filter(r => !action.includes(r));
  const enabled = rest.filter(r => r.p.configured).length; const off = rest.length - enabled;
  const more = action.length > 0;
  const fold = [
    enabled ? (more ? t('{n} more enabled, none failing', { n: enabled }) : t('{n} enabled, none failing', { n: enabled })) : '',
    off ? (more ? t('{n} more not configured', { n: off }) : t('{n} not configured', { n: off })) : '',
  ].filter(Boolean).join(' · ');
  return <section className="panel" aria-labelledby="providers-title">
    <div className="panel-head"><h2 id="providers-title">{t('Metadata providers')}</h2><Link className="text-link adm-panel-link" to="/admin/providers">{t('All providers')}</Link></div>
    {action.length > 0 && <ul className="adm-rows">{action.map(r => <li key={r.p.name}>
      <div className="adm-line"><strong>{providerName(r.p.name)}</strong>{r.failing && <span className="subtle">{kindList(r.p.kinds)}</span>}</div>
      <p className={r.failing ? 'bad' : ''}>{r.failing ? failingText(r) : <Setup p={r.p} />}</p>
      {!r.failing && <p>{t('No other provider for {types}.', { types: kindList(r.uncovered) })}</p>}
    </li>)}</ul>}
    {fold && <p className={more ? 'adm-fold' : 'adm-calm'}>{fold}.</p>}
  </section>;
}

export function ProvidersPage() {
  const overview = useAdminOverview();
  const providers = overview.data?.providers ?? [];
  return <>
    <PageTitle title={t('Providers')} description={overview.data ? t('{configured} of {total} enabled. Figures count requests since each process started.', { configured: providers.filter(p => p.configured).length, total: providers.length }) : undefined} />
    {overview.isPending ? <Loading /> : overview.error ? <Failed error={overview.error} retry={() => void overview.refetch()} /> : <table className="data-table adm-table">
      <caption className="sr-only">{t('Metadata providers, those needing action first')}</caption>
      <thead><tr><th scope="col">{t('Provider')}</th><th scope="col" className="adm-desk">{t('Types')}</th><th scope="col" className="adm-desk">{t('Status')}</th><th scope="col" className="num adm-desk">{t('Requests')}</th><th scope="col" className="num adm-desk">{t('Failures')}</th><th scope="col" className="num adm-desk">{t('Latency')}</th><th scope="col" className="num adm-desk">{t('Last request')}</th></tr></thead>
      <tbody>{rankProviders(providers).map(r => <ProviderRow key={r.p.name} r={r} />)}</tbody>
    </table>}
  </>;
}

function ProviderRow({ r }: { r: Ranked }) {
  const { p } = r;
  const about = [p.browse?.supported ? t('Search and browse') : '', p.defaults?.length ? t('Default for {types}', { types: kindList(p.defaults) }) : ''].filter(Boolean).join(' · ');
  const tvdbNote = p.name === 'tvdb' && (!p.configured || r.failing) ? t('Subscriber keys also need TVDB_PIN; licensed project keys may work without one.') : '';
  const status = r.failing ? <Status value={p.health === 'temporarily_unavailable' ? 'temporarily_unavailable' : 'degraded'} /> : p.health === 'available' ? <Status value="available" /> : <span className="status">{t('Enabled')}</span>;
  const latency = p.mean_latency_ms != null ? t('{ms} ms', { ms: number(p.mean_latency_ms) }) : null;
  const phone = p.configured ? [status, kindList(p.kinds), plural(p.requests, '{n} request', '{n} requests'), p.requests ? plural(p.failures, '{n} failure', '{n} failures') : '', latency ? t('{ms} mean', { ms: latency }) : '', p.last_at ? t('last {time}', { time: relative(p.last_at) }) : ''] : [kindList(p.kinds)];
  return <tr>
    <th scope="row">
      <span className="adm-name">{providerName(p.name)}</span>
      {about && <span className="adm-sub">{about}</span>}
      <span className="adm-phone"><Joined parts={phone} />{!p.configured && <span className="adm-phone-line"><Setup p={p} /></span>}{tvdbNote && <span className="adm-phone-line">{tvdbNote}</span>}</span>
    </th>
    <td className="adm-desk">{kindList(p.kinds)}</td>
    {p.configured ? <>
      <td className="adm-desk">{status}</td>
      <td className="num adm-desk">{number(p.requests)}</td>
      <td className="num adm-desk">{number(p.failures)}{(p.failures > 0 || p.rate_limited > 0) && <span className="adm-sub">{[p.failures ? `${(r.rate * 100).toFixed(1)}%` : '', p.rate_limited ? t('{n} rate limited', { n: number(p.rate_limited) }) : ''].filter(Boolean).join(' · ')}</span>}</td>
      <td className="num adm-desk">{latency ?? <Dash />}</td>
      <td className="num adm-desk">{p.last_at ? <time dateTime={p.last_at} title={new Date(p.last_at).toLocaleString(locale)}>{relative(p.last_at)}</time> : <Dash />}</td>
    </> : <td className="adm-desk adm-setup" colSpan={5}><Setup p={p} />{tvdbNote && <span className="adm-sub">{tvdbNote}</span>}</td>}
  </tr>;
}

// ---------------------------------------------------------------- jobs

const jobStatuses = ['queued', 'running', 'failed', 'blocked', 'succeeded'] as const;
type JobStatus = typeof jobStatuses[number];
type Job = Schema['AdminJob'];

const jobKindNames: Record<string, string> = {
  catalog_refresh: t('Catalog refresh'), calendar_title: t('Calendar lookup'), critic_refresh: t('Critic score refresh'), statistics_warm: t('Statistics warm-up'),
  scheduled_export: t('Scheduled export'), release_notices: t('Release notices'), library_import: t('Library import'), import: t('Import'),
  playback_push: t('Playback push'), listens: t('Listen sync'), list_sync: t('List sync'), scrobble: t('Scrobble'), runtime_probe: t('Runtime probe'),
};
const jobKindLabel = (kind: string) => jobKindNames[kind] ?? sentence(kind);

/** "Overdue 3 h 13 min", "Now", "In 5 min"; null for jobs that are not waiting. */
function dueText(job: Job): string | null {
  if (job.status !== 'queued') return null;
  const seconds = (Date.now() - new Date(job.run_after).getTime()) / 1000;
  if (seconds >= 60) return t('Overdue {wait}', { wait: wait(seconds) });
  if (seconds > -60) return t('Now');
  return t('In {wait}', { wait: wait(-seconds) });
}

/** Consecutive jobs that read the same (kind, state, attempts, error, minute created, minute due) share one row. */
function groupJobs(jobs: Job[]): Job[][] {
  const groups: Job[][] = [];
  const key = (j: Job) => [j.kind, j.status, j.attempts, j.max_attempts, j.last_error ?? '', j.created_at.slice(0, 16), j.status === 'queued' ? j.run_after.slice(0, 16) : ''].join('|');
  for (const job of jobs) {
    const last = groups[groups.length - 1];
    if (last && key(last[0]) === key(job)) last.push(job); else groups.push([job]);
  }
  return groups;
}

// Literal plural() calls, so the string extractor finds every form.
const jobCounts: Record<Exclude<JobStatus, 'succeeded'>, (n: number) => string> = {
  queued: n => plural(n, '{n} queued job', '{n} queued jobs'), running: n => plural(n, '{n} running job', '{n} running jobs'),
  failed: n => plural(n, '{n} failed job', '{n} failed jobs'), blocked: n => plural(n, '{n} blocked job', '{n} blocked jobs'),
};

const emptyJobs: Record<JobStatus | 'all', [string, string]> = {
  all: [t('No jobs.'), t('Metadata refreshes, scrobbles and imports appear here when they are queued.')],
  queued: [t('No queued jobs.'), t('Nothing is waiting to run.')],
  running: [t('No running jobs.'), t('Nothing is running right now.')],
  failed: [t('No failed jobs.'), t('Jobs that fail after their last attempt appear here, with Retry.')],
  blocked: [t('No blocked jobs.'), t('Jobs stopped because a replay could repeat an outside effect appear here.')],
  succeeded: [t('No jobs succeeded in the last 7 days.'), t('Finished jobs are listed here for a week.')],
};

export function JobsPage() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const [params, setParams] = useSearchParams();
  const status = (jobStatuses as readonly string[]).includes(params.get('status') ?? '') ? params.get('status') as JobStatus : undefined;
  const kind = params.get('kind') || undefined; const cursor = params.get('cursor') || undefined;
  const kinds = useQuery({ queryKey: privateKey(user.id, 'admin', 'job-kinds'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/job-kinds', { signal })).items, refetchInterval: 15_000 });
  const jobs = useQuery({ queryKey: privateKey(user.id, 'admin', 'jobs', status, kind, cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/jobs', { signal, params: { query: { status, kind, cursor, limit: 50 } } })), refetchInterval: 15_000 });
  const overview = useAdminOverview(); const queue = overview.data && overviewQueue(overview.data);
  const refresh = () => client.invalidateQueries({ queryKey: privateKey(user.id, 'admin') });
  const retry = useAction(async (jobID: string) => { unwrap(await api.POST('/api/admin/v1/jobs/{jobID}/retry', { params: { path: { jobID } } })); await refresh(); });
  const cancel = useAction(async (jobID: string) => { unwrap(await api.POST('/api/admin/v1/jobs/{jobID}/cancel', { params: { path: { jobID } } })); await refresh(); });
  function change(key: string, value?: string) { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); if (key !== 'cursor') next.delete('cursor'); setParams(next); }
  const inScope = (kinds.data ?? []).filter(k => !kind || k.kind === kind);
  const count = (s: JobStatus) => inScope.reduce((n, k) => n + k[s], 0);
  const unfinished = count('queued') + count('running') + count('failed') + count('blocked');
  const chips = jobStatuses.filter(s => count(s) > 0 || s === status);
  const stuck = queue && (queue.problem === 'no_worker' || queue.problem === 'overdue') ? queue : null;
  const total = !kinds.data ? null : status === 'succeeded' ? t('{n} succeeded in the last 7 days', { n: number(count('succeeded')) })
    : status ? jobCounts[status](count(status)) : `${plural(unfinished, '{n} unfinished job', '{n} unfinished jobs')} · ${t('{n} succeeded in the last 7 days', { n: number(count('succeeded')) })}`;
  const actions = (j: Job) => <>
    {(j.status === 'failed' || j.status === 'blocked') && <button className="button small ghost" disabled={retry.pending} onClick={async () => { if (j.status !== 'blocked' || await confirm({ title: t('Retry a blocked job?'), description: t('It was stopped because replaying it could repeat an outside effect, such as a scrobble. Retry only if you know it did not complete.'), label: t('Retry job') })) void retry.run(j.id); }}><RotateCcw aria-hidden />{t('Retry')}</button>}
    {(j.status === 'queued' || j.status === 'blocked') && <button className="button small ghost" disabled={cancel.pending} onClick={async () => { if (await confirm({ title: t('Cancel this job?'), description: t('It is marked failed and will not run. You can retry it later.'), label: t('Cancel job'), danger: true })) void cancel.run(j.id); }}><Ban aria-hidden />{t('Cancel')}</button>}
  </>;
  return <>
    <PageTitle title={t('Jobs')} />
    {stuck && <div className="adm-notice" role="status"><CircleX aria-hidden /><p>{queueProblem(stuck)} {stuck.problem === 'no_worker' && tx('Start one with {command}.', { command: <code>pastime worker</code> })}</p></div>}
    {kinds.data && kinds.data.length > 1 && <section className="adm-section" aria-labelledby="kinds-title">
      <div className="adm-section-head"><h2 id="kinds-title">{t('By kind')}</h2><span>{t('Unfinished, plus succeeded in the last 7 days')}</span></div>
      <table className="data-table adm-table">
        <thead><tr><th scope="col">{t('Kind')}</th>{jobStatuses.map(s => <th key={s} scope="col" className="num adm-desk">{statusText(s)}</th>)}</tr></thead>
        <tbody>{kinds.data.map(k => <tr key={k.kind}>
          <th scope="row"><button className="adm-kind" onClick={() => change('kind', k.kind === kind ? undefined : k.kind)} aria-pressed={k.kind === kind}>{jobKindLabel(k.kind)}</button>
            <span className="adm-phone">{jobStatuses.filter(s => k[s]).map(s => `${statusText(s)} ${number(k[s])}`).join(' · ') || t('None')}</span></th>
          {jobStatuses.map(s => <td key={s} className={`num adm-desk ${k[s] ? '' : 'subtle'}`}>{number(k[s])}</td>)}
        </tr>)}</tbody>
      </table>
    </section>}
    <div className="chip-filter adm-chips" role="group" aria-label={t('Job status')}>
      <button aria-pressed={!status} onClick={() => change('status')}>{t('All')}</button>
      {chips.map(s => <button key={s} aria-pressed={status === s} onClick={() => change('status', s)}>{statusText(s)}<span className="adm-count">{number(count(s))}</span></button>)}
      {kind && <button onClick={() => change('kind')} aria-label={t('Remove kind filter {kind}', { kind: jobKindLabel(kind) })}>{jobKindLabel(kind)} ×</button>}
    </div>
    <ErrorNote error={retry.error || cancel.error} />
    {jobs.isPending ? <Loading label={t('Loading jobs')} /> : jobs.error ? <Failed error={jobs.error} retry={() => void jobs.refetch()} /> : jobs.data.items.length ? <section className="adm-section" aria-label={t('Jobs')}>
      {total && <div className="adm-section-head"><h2>{total}</h2></div>}
      <table className="data-table adm-table">
        <caption className="sr-only">{t('Jobs, newest first')}</caption>
        <thead><tr><th scope="col">{t('Kind')}</th><th scope="col" className="adm-desk">{t('Status')}</th><th scope="col" className="num adm-desk">{t('Attempts')}</th><th scope="col" className="adm-desk">{t('Last error')}</th><th scope="col" className="adm-desk">{t('Created')}</th><th scope="col" className="adm-desk">{t('Due')}</th><th scope="col"><span className="sr-only">{t('Actions')}</span></th></tr></thead>
        <tbody>{groupJobs(jobs.data.items).map(group => <JobRows key={group[0].id} group={group} actions={actions} />)}</tbody>
      </table>
      <Pager current={cursor} next={jobs.data.next_cursor} change={value => change('cursor', value)} />
    </section> : <Empty title={emptyJobs[status ?? 'all'][0]} action={kind || status ? <button className="button" onClick={() => setParams({})}>{t('Show all jobs')}</button> : undefined}>{emptyJobs[status ?? 'all'][1]}</Empty>}
  </>;
}

function JobRows({ group, actions }: { group: Job[]; actions: (job: Job) => ReactNode }) {
  const [open, setOpen] = useState(false);
  const j = group[0]; const due = dueText(j); const many = group.length > 1;
  const attempts = t('{n} of {max}', { n: j.attempts, max: j.max_attempts });
  return <>
    <tr className={many ? 'adm-group' : ''}>
      <th scope="row">
        <span className="adm-name">{jobKindLabel(j.kind)}</span>
        {many ? <span className="adm-sub">{plural(group.length, '{n} job', '{n} jobs')}</span> : <span className="adm-sub mono">{j.id.slice(0, 8)}</span>}
        <span className="adm-phone"><Joined parts={[<Status key="s" value={j.status} />, t('{attempts} attempts', { attempts }), <span key="c">{t('created')} <When value={j.created_at} /></span>, due]} />{j.last_error && <span className="adm-phone-error mono">{j.last_error}</span>}</span>
      </th>
      <td className="adm-desk"><Status value={j.status} /></td>
      <td className="num adm-desk">{attempts}</td>
      <td className="adm-desk mono">{j.last_error ?? <Dash />}</td>
      <td className="adm-desk"><When value={j.created_at} /></td>
      <td className="adm-desk">{due ?? <Dash />}</td>
      <td className="adm-action"><div className="actions">{many
        ? <button className="button small ghost" aria-expanded={open} aria-label={open ? plural(group.length, 'Hide {n} job', 'Hide {n} jobs') : plural(group.length, 'Show {n} job', 'Show {n} jobs')} onClick={() => setOpen(!open)}>{open ? t('Hide') : t('Show')}<ChevronDown aria-hidden className={open ? 'flip' : ''} /></button>
        : actions(j)}</div></td>
    </tr>
    {many && open && group.map(job => <tr key={job.id} className="adm-member">
      <th scope="row"><span className="mono">{job.id.slice(0, 8)}</span></th>
      <td className="adm-desk" colSpan={5} />
      <td className="adm-action"><div className="actions">{actions(job)}</div></td>
    </tr>)}
  </>;
}

// ---------------------------------------------------------------- catalog

export function CatalogPage() {
  const user = useUser(); const client = useQueryClient();
  const stats = useQuery({ queryKey: privateKey(user.id, 'admin', 'catalog'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/catalog', { signal })) });
  const overview = useAdminOverview(); const queue = overview.data && overviewQueue(overview.data);
  const [scheduled, setScheduled] = useState<number | null>(null);
  const refresh = useAction(async (_: void) => { setScheduled(unwrap(await api.POST('/api/admin/v1/catalog/refresh-stale')).scheduled); await client.invalidateQueries({ queryKey: privateKey(user.id, 'admin') }); });
  const kinds = stats.data?.kinds ?? [];
  const stale = kinds.reduce((n, k) => n + k.stale, 0);
  return <div>
    <PageTitle title={t('Catalog')} description={t('A tracked title is due for refresh after 7 days; due titles refresh automatically every 30 minutes.')} />
    {queue?.problem === 'no_worker' && <div className="adm-notice" role="status"><CircleX aria-hidden /><p>{t('No worker is running, so due titles are not refreshed.')} <Link className="adm-inline-link" to="/admin/jobs?status=queued">{t('View waiting jobs')}</Link></p></div>}
    <ErrorNote error={refresh.error} />
    {scheduled !== null && <p className="notice" role="status">{scheduled ? t('{scheduled} titles scheduled for refresh. Follow them under Jobs.', { scheduled: number(scheduled) }) : t('Nothing is due right now.')}</p>}
    {stats.isPending ? <Loading /> : stats.error ? <Failed error={stats.error} retry={() => void stats.refetch()} /> : <>
      <Strip label={t('Catalog totals')} items={[
        { label: t('Records'), value: number(stats.data.records), sub: t('estimate') },
        { label: t('Titles'), value: number(kinds.reduce((n, k) => n + k.titles, 0)) },
        { label: t('Tracked'), value: number(kinds.reduce((n, k) => n + k.tracked, 0)), sub: t('by at least one account') },
        { label: t('Due for refresh'), value: number(stale), sub: stale
          ? <button className="text-button adm-strip-action" disabled={refresh.pending} onClick={() => void refresh.run()}>{refresh.pending ? t('Scheduling…') : t('Refresh now')}</button>
          : t('none older than 7 days') },
      ]} />
      <section className="adm-section" aria-labelledby="catalog-kinds-title">
        <div className="adm-section-head"><h2 id="catalog-kinds-title">{t('By type and provider')}</h2></div>
        <table className="data-table adm-table">
          <thead><tr><th scope="col">{t('Type')}</th><th scope="col" className="adm-desk">{t('Provider')}</th><th scope="col" className="num adm-desk">{t('Titles')}</th><th scope="col" className="num adm-desk">{t('Tracked')}</th><th scope="col" className="num adm-desk">{t('Due')}</th></tr></thead>
          <tbody>{kinds.map(k => <tr key={k.kind + k.provider}>
            <th scope="row"><span className="adm-name">{typeLabel(k.kind)}</span><span className="adm-phone"><Joined parts={[providerName(k.provider), plural(k.titles, '{n} title', '{n} titles'), t('{n} tracked', { n: number(k.tracked) }), t('{n} due', { n: number(k.stale) })]} /></span></th>
            <td className="adm-desk">{providerName(k.provider)}</td><td className="num adm-desk">{number(k.titles)}</td><td className="num adm-desk">{number(k.tracked)}</td><td className="num adm-desk">{number(k.stale)}</td>
          </tr>)}</tbody>
        </table>
      </section>
    </>}
  </div>;
}

// ---------------------------------------------------------------- imports

const importStatuses = ['queued', 'running', 'succeeded', 'partial_success', 'failed', 'cancelled'] as const;
const emptyImports: Record<string, string> = {
  queued: t('No queued imports.'), running: t('No running imports.'), succeeded: t('No finished imports.'),
  partial_success: t('No imports need repair.'), failed: t('No failed imports.'), cancelled: t('No cancelled imports.'),
};

export function ImportsPage() {
  const user = useUser(); const [params, setParams] = useSearchParams();
  const status = (params.get('status') || undefined) as Schema['AdminImportRun']['status'] | undefined; const cursor = params.get('cursor') || undefined;
  const runs = useQuery({ queryKey: privateKey(user.id, 'admin', 'imports', status, cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/imports', { signal, params: { query: { status, cursor, limit: 50 } } })), refetchInterval: 15_000 });
  function change(key: string, value?: string) { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); if (key !== 'cursor') next.delete('cursor'); setParams(next); }
  const none = !status && !cursor && runs.data?.items.length === 0;
  return <>
    <PageTitle title={t('Imports')} description={t('Import runs across every account. Only counts are shown; titles and files stay private.')} />
    {!none && runs.data && <div className="chip-filter adm-chips" role="group" aria-label={t('Import status')}><button aria-pressed={!status} onClick={() => change('status')}>{t('All')}</button>{importStatuses.map(s => <button key={s} aria-pressed={status === s} onClick={() => change('status', s)}>{statusText(s)}</button>)}</div>}
    {runs.isPending ? <Loading /> : runs.error ? <Failed error={runs.error} retry={() => void runs.refetch()} /> : runs.data.items.length ? <section className="adm-section" aria-label={t('Import runs')}><table className="data-table adm-table">
      <thead><tr><th scope="col">{t('Account')}</th><th scope="col" className="adm-desk">{t('Source')}</th><th scope="col" className="adm-desk">{t('Status')}</th><th scope="col" className="adm-desk">{t('Progress')}</th><th scope="col" className="num adm-desk">{t('Imported')}</th><th scope="col" className="num adm-desk">{t('Plays')}</th><th scope="col" className="num adm-desk">{t('Unmatched')}</th><th scope="col" className="adm-desk">{t('Started')}</th></tr></thead>
      <tbody>{runs.data.items.map(r => <tr key={r.id}>
        <th scope="row"><span className="adm-name">@{r.username}</span>
          <span className="adm-phone"><Joined parts={[sentence(r.source), <Status key="s" value={r.status} />, r.total ? t('{processed} of {total}', { processed: number(r.processed), total: number(r.total) }) : null, t('{n} imported', { n: number(r.imported) }), plural(r.plays_added, '{n} play', '{n} plays'), t('{n} unmatched', { n: number(r.unmatched) }), <When key="w" value={r.created_at} />]} /></span></th>
        <td className="adm-desk">{sentence(r.source)}</td><td className="adm-desk"><Status value={r.status} /></td>
        <td className="adm-desk adm-progress">{r.total ? <><span className="meter" role="img" aria-label={t('{value}% processed', { value: Math.round((r.processed / r.total) * 100) })}><span style={{ width: `${(r.processed / r.total) * 100}%` }} /></span><span className="subtle">{t('{processed} of {total}', { processed: number(r.processed), total: number(r.total) })}</span></> : <Dash />}</td>
        <td className="num adm-desk">{number(r.imported)}</td><td className="num adm-desk">{number(r.plays_added)}</td><td className="num adm-desk">{number(r.unmatched)}</td><td className="adm-desk"><When value={r.created_at} /></td>
      </tr>)}</tbody>
    </table><Pager current={cursor} next={runs.data.next_cursor} change={value => change('cursor', value)} /></section>
      : status ? <Empty title={emptyImports[status] ?? t('No imports match.')} action={<button className="button" onClick={() => change('status')}>{t('Show all imports')}</button>}>{t('Import runs with this status appear here.')}</Empty>
      : <Empty title={t('No imports yet')}>{t('When someone imports from Trakt, Letterboxd, Goodreads or another service, the run appears here.')}</Empty>}
  </>;
}
