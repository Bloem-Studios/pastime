import { libraryResources } from '../api/invalidation';
import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Archive, Check, Download, EyeOff, Search, Trash2 } from 'lucide-react';
import { profileDownload, api, unwrap, type Schema } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { Cover, typeLabel } from '../media';
import { Empty, ErrorNote, Failed, Field, Loading, Modal, PageTitle, Pager, submit, Tabs, Timestamp, useAction, useConfirm } from '../ui';
import { plural, t } from '../i18n';

type Unmatched = Schema['Unmatched'];
const searchType = (kind: string): Schema['EntrySummary']['media_type'] => (['movie', 'series', 'anime', 'manga', 'book', 'audiobook', 'comic', 'game', 'boardgame', 'music', 'podcast', 'video'].includes(kind) ? kind : kind === 'episode' || kind === 'show' ? 'series' : 'movie') as Schema['EntrySummary']['media_type'];

export function UnmatchedPage() {
  const user = useUser(); const [params, setParams] = useSearchParams(); const refresh = useRefresh([...libraryResources, 'unmatched', 'imports']);
  const run = params.get('run') || undefined; const cursor = params.get('cursor') || undefined; const status = (params.get('status') || 'pending') as Unmatched['status'];
  const query = useQuery({ queryKey: privateKey(user.id, 'unmatched', run, status, cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/import-unmatched', { signal, params: { query: { run_id: run, status, cursor, limit: 30 } } })) });
  const [matching, setMatching] = useState<Unmatched | null>(null);
  const ignore = useAction(async (id: string) => { unwrap(await api.POST('/api/v1/import-unmatched/{unmatchedID}/ignore', { params: { path: { unmatchedID: id } } })); await refresh(); });
  const states: [Unmatched['status'], string][] = [['pending', t('To match')], ['resolved', t('Matched')], ['ignored', t('Ignored')]];
  const filter = (s: Unmatched['status']) => `/integrations/unmatched?${new URLSearchParams({ ...(run ? { run } : {}), status: s })}`;
  return <>
    <PageTitle title={t('Titles to match')} actions={<Link className="button" to="/integrations?section=transfer"><ArrowLeft aria-hidden />{t('Import and export')}</Link>} />
    <Tabs label={t('Status')} current={status} items={states.map(([value, label]) => ({ value, label, to: filter(value) }))} />
    {status === 'pending' && !!query.data?.items.length && <p className="subtle unmatched-note">{t('Pick the right title and it is applied exactly as the import would have, with its history.')}</p>}
    <ErrorNote error={ignore.error} />
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : query.data.items.length ? <>
      <ul className="record-list">{query.data.items.map(u => <li key={u.id} className="record-row">
        <div><h3>{u.label}</h3><p>{[u.source, typeLabel(searchType(u.kind)), u.plays ? `${plural(u.plays, '{n} play', '{n} plays')}` : ''].filter(Boolean).join(' · ')}</p></div>
        {u.status === 'pending' ? <div className="actions"><button className="button small primary" onClick={() => setMatching(u)}><Search aria-hidden />{t('Find a match')}</button>
          <button className="button small ghost" disabled={ignore.pending} onClick={() => void ignore.run(u.id)}><EyeOff aria-hidden />{t('Ignore')}</button></div>
          : u.catalog_id ? <Link className="button small ghost" to={`/title/${u.catalog_id}`}>{t('Open title')}</Link> : <span className="status">{t('Ignored')}</span>}
      </li>)}</ul>
      <Pager current={cursor} next={query.data.next_cursor} change={value => setParams({ ...(run ? { run } : {}), status, ...(value ? { cursor: value } : {}) })} /></>
      : <Empty title={status === 'pending' ? t('Everything is matched.') : status === 'resolved' ? t('No matched titles yet.') : t('No ignored titles.')}>{status === 'pending' ? t('New imports list anything they cannot match here.') : t('Titles appear here once you match or ignore them.')}</Empty>}
    {matching && <MatchDialog item={matching} onClose={() => setMatching(null)} />}
  </>;
}

function MatchDialog({ item, onClose }: { item: Unmatched; onClose: () => void }) {
  const refresh = useRefresh([...libraryResources, 'unmatched', 'imports']); const type = searchType(item.kind);
  const [text, setText] = useState(item.label.replace(/\s*\(\d{4}\)\s*$/, '')); const [q, setQ] = useState(text);
  const results = useQuery({ queryKey: ['catalog-search', type, q], enabled: q.length > 0, staleTime: 300_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/search', { signal, params: { query: { media_type: type, q } } })) });
  const pick = useAction(async (r: Schema['CatalogResult']) => {
    const id = r.catalog_id ?? unwrap(await api.POST('/api/v1/catalog/resolve', { body: { provider: r.provider, media_type: r.media_type, provider_id: r.provider_id } })).id;
    unwrap(await api.POST('/api/v1/import-unmatched/{unmatchedID}/resolve', { params: { path: { unmatchedID: item.id } }, body: { catalog_id: id } }));
    await refresh(); onClose();
  });
  return <Modal title={t('Find a match')} description={t('Imported as “{label}”. Choose the right title.', { label: item.label })} onClose={onClose} busy={pick.pending} wide>
    <form className="search-form" role="search" onSubmit={submit(() => setQ(text.trim()))}><Search size={18} aria-hidden /><input aria-label={t('Search the catalog')} value={text} maxLength={100} onChange={e => setText(e.target.value)} /><button className="button small" type="submit">{t('Search')}</button></form>
    {results.isPending && q ? <Loading /> : results.error ? <Failed error={results.error} /> : <ul className="match-results">{(results.data?.items ?? []).slice(0, 12).map(r => <li key={r.provider + r.provider_id}>
      <Cover entry={{ title: r.title, media_type: r.media_type, image_url: r.image_url }} compact />
      <div><strong>{r.title}</strong><p className="subtle">{[r.year, ...r.credits.slice(0, 2)].filter(Boolean).join(' · ')}</p></div>
      <button className="button small" disabled={pick.pending} onClick={() => void pick.run(r)}><Check aria-hidden />{t('This one')}</button>
    </li>)}</ul>}
    <ErrorNote error={pick.error} />
  </Modal>;
}

const formatLabels: Record<string, string> = { jsonl: t('Full export (JSON lines)'), csv: t('Spreadsheet (CSV)'), letterboxd: t('Letterboxd CSV') };
function bytes(n: number) { return n > 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`; }

export function SnapshotsSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const files = useQuery({ queryKey: privateKey(user.id, 'export-files'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/export-files', { signal })).items });
  const schedule = useQuery({ queryKey: privateKey(user.id, 'export-schedule'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/export-schedule', { signal })).schedule });
  const [draft, setDraft] = useState<{ frequency: 'daily' | 'weekly' | 'monthly'; format: 'jsonl' | 'csv' | 'letterboxd'; keep: number } | null>(null);
  const current = draft ?? (schedule.data ? { frequency: schedule.data.frequency, format: schedule.data.format, keep: schedule.data.keep } : { frequency: 'weekly' as const, format: 'jsonl' as const, keep: 5 });
  const reload = () => client.invalidateQueries({ queryKey: privateKey(user.id) });
  const save = useAction(async (_: void) => { unwrap(await api.PUT('/api/v1/me/export-schedule', { body: current })); setDraft(null); await reload(); });
  const stop = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/me/export-schedule')); setDraft(null); await reload(); });
  const now = useAction(async (format: 'jsonl' | 'csv' | 'letterboxd') => { unwrap(await api.POST('/api/v1/export-files', { body: { format } })); await reload(); });
  const remove = useAction(async (id: string) => { unwrap(await api.DELETE('/api/v1/export-files/{fileID}', { params: { path: { fileID: id } } })); await reload(); });
  return <section className="detail-section" aria-labelledby="snapshots-title">
    <div className="section-heading"><h2 id="snapshots-title">{t('Kept exports')}</h2><button className="button" disabled={now.pending} onClick={() => void now.run(current.format)}><Archive aria-hidden />{now.pending ? t('Saving…') : t('Keep a snapshot now')}</button></div>
    <p className="subtle">{t('Snapshots are stored on this server so you always have a recent copy, even if you forget to download one.')}</p>
    <form className="panel schedule-form" onSubmit={submit(() => void save.run())}>
      <div className="form-grid three">
        <Field label={t('Every')}>{id => <select id={id} value={current.frequency} onChange={e => setDraft({ ...current, frequency: e.target.value as typeof current.frequency })}><option value="daily">{t('Day')}</option><option value="weekly">{t('Week')}</option><option value="monthly">{t('Month')}</option></select>}</Field>
        <Field label={t('Format')}>{id => <select id={id} value={current.format} onChange={e => setDraft({ ...current, format: e.target.value as typeof current.format })}>{Object.entries(formatLabels).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>
        <Field label={t('Keep')}>{id => <select id={id} value={current.keep} onChange={e => setDraft({ ...current, keep: Number(e.target.value) })}>{[1, 3, 5, 10, 20].map(n => <option key={n} value={n}>{plural(n, '{n} snapshot', '{n} snapshots')}</option>)}</select>}</Field>
      </div>
      <div className="actions"><button className="button primary" type="submit" disabled={save.pending}>{schedule.data ? t('Update schedule') : t('Start keeping exports')}</button>
        {schedule.data && <><span className="subtle">{t('Next one')} <Timestamp value={schedule.data.next_run_at} /></span><button className="text-button" type="button" disabled={stop.pending} onClick={() => void stop.run()}>{t('Stop')}</button></>}</div>
    </form>
    <ErrorNote error={save.error || stop.error || now.error || remove.error} />
    {files.data?.length ? <ul className="record-list">{files.data.map(f => <li key={f.id} className="record-row">
      <div><h3>{formatLabels[f.format]}</h3><p>{f.trigger === 'schedule' ? t('Scheduled') : t('Kept by you')} · <Timestamp value={f.created_at} /> · {plural(f.entries, '{n} title', '{n} titles')} · {bytes(f.size_bytes)}</p></div>
      <div className="actions"><a className="button small" href={profileDownload(`/api/v1/export-files/${f.id}`)} download><Download aria-hidden />{t('Download')}</a>
        <button className="icon-button" aria-label={t('Delete snapshot')} onClick={async () => { if (await confirm({ title: t('Delete this snapshot?'), description: t('The file is removed from the server.'), label: t('Delete'), danger: true })) void remove.run(f.id); }}><Trash2 aria-hidden /></button></div>
    </li>)}</ul> : files.isPending ? null : <Empty compact title={t('No snapshots yet')}>{schedule.data ? t('The first one is kept on schedule.') : t('Start a schedule above, or keep one now.')}</Empty>}
  </section>;
}

export function RollbackButton({ run }: { run: Schema['ImportRun'] }) {
  const refresh = useRefresh([...libraryResources, 'unmatched', 'imports']); const confirm = useConfirm(); const client = useQueryClient(); const user = useUser();
  const [result, setResult] = useState<Schema['RollbackResult'] | null>(null);
  const action = useAction(async (_: void) => {
    setResult(unwrap(await api.POST('/api/v1/imports/{importID}/rollback', { params: { path: { importID: run.id } } })));
    await refresh(); await client.invalidateQueries({ queryKey: privateKey(user.id, 'imports') });
  });
  if (run.status === 'queued' || run.status === 'running' || run.status === 'rolled_back') return null;
  return <>{result ? <span className="save-confirmation">{t('Removed {titles} titles and {plays} plays.', { titles: result.entries_removed, plays: result.plays_removed })}</span>
    : <button className="text-button" disabled={action.pending} onClick={async () => { if (await confirm({ title: t('Roll back this import?'), description: t('The plays it added are removed, and so are titles it created that you have not touched since. Status or rating changes it made to titles you already had are kept.'), label: t('Roll back'), danger: true })) void action.run(); }}>{t('Roll back')}</button>}
    <ErrorNote error={action.error} /></>;
}
