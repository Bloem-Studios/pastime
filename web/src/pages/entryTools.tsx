import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import * as Dropdown from '@radix-ui/react-dropdown-menu';
import { Bell, BellOff, Check, Ellipsis, ImagePlus, Replace, Search } from 'lucide-react';
import { api, keyHeader, unwrap, type Entry, type Schema } from '../api/client';
import { privateKey, useRefresh, useUser } from '../state';
import { Cover, providerName } from '../media';
import { ErrorNote, Failed, Field, Loading, Modal, submit, useAction } from '../ui';
import { plural, t } from '../i18n';
import { episodeCode } from '../format';

/** Less frequent actions for one title: artwork, match correction and release notices. */
export function EntryMenu({ entry }: { entry: Entry }) {
  const user = useUser(); const client = useQueryClient();
  const [dialog, setDialog] = useState<'artwork' | 'remap' | null>(null);
  const muted = useQuery({ queryKey: privateKey(user.id, 'mute', entry.id), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/notifications', { signal, params: { path: { entryID: entry.id } } })).excluded });
  const mute = useAction(async (value: boolean) => { unwrap(await api.PUT('/api/v1/library/{entryID}/notifications', { params: { path: { entryID: entry.id } }, body: { excluded: value } })); client.setQueryData(privateKey(user.id, 'mute', entry.id), value); });
  return <>
    <Dropdown.Root>
      <Dropdown.Trigger className="button small" aria-label={t('More actions')}><Ellipsis aria-hidden />{t('More')}</Dropdown.Trigger>
      <Dropdown.Portal><Dropdown.Content className="dropdown-menu" sideOffset={8} align="start">
        <Dropdown.Item onSelect={() => setDialog('artwork')}><ImagePlus aria-hidden />{t('Change artwork')}</Dropdown.Item>
        {entry.catalog_id && <Dropdown.Item onSelect={() => setDialog('remap')}><Replace aria-hidden />{t('Correct the match')}</Dropdown.Item>}
        {entry.catalog_id && <Dropdown.Item disabled={mute.pending || muted.data === undefined} onSelect={() => void mute.run(!muted.data)}>{muted.data ? <><Bell aria-hidden />{t('Resume release notices')}</> : <><BellOff aria-hidden />{t('Mute release notices')}</>}</Dropdown.Item>}
      </Dropdown.Content></Dropdown.Portal>
    </Dropdown.Root>
    <ErrorNote error={mute.error} />
    {dialog === 'artwork' && <ArtworkDialog entry={entry} onClose={() => setDialog(null)} />}
    {dialog === 'remap' && <RemapDialog entry={entry} onClose={() => setDialog(null)} />}
  </>;
}

function ArtworkDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const refresh = useRefresh(); const [url, setURL] = useState('');
  const save = useAction(async (value: string, key) => { unwrap(await api.PUT('/api/v1/library/{entryID}/artwork', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: entry.version, image_url: value } })); await refresh(); onClose(); });
  const valid = url.startsWith('https://');
  return <Modal title={t('Change artwork')} description={t('Show your own cover for this title. Shared lists always use the catalog artwork.')} onClose={onClose} busy={save.pending}>
    <form onSubmit={submit(() => void save.run(url.trim()))}>
      <div className="artwork-preview"><Cover entry={{ title: entry.title, media_type: entry.media_type, image_url: valid ? url : entry.image_url }} /></div>
      <Field label={t('Image address')} hint={t('An https:// link to a JPEG, PNG or WebP image.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="url" inputMode="url" maxLength={1000} placeholder="https://" value={url} onChange={e => setURL(e.target.value)} />}</Field>
      <ErrorNote error={save.error} />
      <div className="dialog-actions"><button className="button ghost" type="button" disabled={save.pending} onClick={() => void save.run('')}>{t('Use catalog artwork')}</button><button className="button" type="button" disabled={save.pending} onClick={onClose}>{t('Cancel')}</button><button className="button primary" type="submit" disabled={!valid || save.pending}>{t('Save artwork')}</button></div>
    </form>
  </Modal>;
}

function RemapDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const refresh = useRefresh(); const [text, setText] = useState(entry.title); const [q, setQ] = useState(entry.title); const [result, setResult] = useState<Schema['RemapResult'] | null>(null);
  const [preview, setPreview] = useState<(Schema['RemapPreview'] & { catalogID: string }) | null>(null);
  // A title can move to another provider's record, such as from TMDB to TheTVDB.
  const providers = useQuery({ queryKey: ['catalog-providers'], staleTime: 3_600_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/providers', { signal })).items });
  const choices = (providers.data ?? []).filter(p => p.kinds.includes(entry.media_type)); const [source, setSource] = useState('');
  const results = useQuery({ queryKey: ['catalog-search', entry.media_type, q, source], enabled: q.length > 0, staleTime: 300_000, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/search', { signal, params: { query: { media_type: entry.media_type, q, provider: source || undefined } } })) });
  const pick = useAction(async (r: Schema['CatalogResult']) => {
    const id = r.catalog_id ?? unwrap(await api.POST('/api/v1/catalog/resolve', { body: { provider: r.provider, media_type: r.media_type, provider_id: r.provider_id } })).id;
    setPreview({ ...unwrap(await api.POST('/api/v1/library/{entryID}/remap/preview', { params: { path: { entryID: entry.id } }, body: { version: entry.version, catalog_id: id } })), catalogID: id });
  });
  const apply = useAction(async (_: void, key) => {
    if (!preview) return;
    setResult(unwrap(await api.POST('/api/v1/library/{entryID}/remap', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: preview.version, catalog_id: preview.catalogID } })));
    await refresh();
  });
  return <Modal title={t('Correct the match')} description={t('Point this title at the right catalog record. Your plays follow by season and episode.')} onClose={onClose} busy={pick.pending || apply.pending} wide>
    {result ? <><p className="notice" role="status"><Check size={16} aria-hidden /> {plural(result.plays_moved, 'Moved {n} play.', 'Moved {n} plays.')}{result.plays_retained ? ' ' + t('{count} unmatched plays were kept in history.', { count: result.plays_retained }) : ''}</p>
      <div className="dialog-actions"><button className="button primary" onClick={onClose}>{t('Done')}</button></div></> : preview ? <>
      <p>{t('{moved} plays will move; {retained} unmatched plays will stay in history.', { moved: preview.plays_moved, retained: preview.plays_retained })}</p>
      <p>{t('Your rating, notes and status stay. {moved} episode ratings will move.', { moved: preview.ratings_moved })}{preview.ratings_dropped ? ' ' + t('{count} episode ratings have no match and will be removed.', { count: preview.ratings_dropped }) : ''}</p>
      {preview.missing.length > 0 && <div className="notice" role="status"><p>{t('The new record is missing episodes you track:')}</p><ul>{preview.missing.map(m => <li key={`${m.season}-${m.episode}`}>{m.season != null && m.episode != null ? `${episodeCode(m.season, m.episode)} · ` : ''}{m.title}</li>)}</ul></div>}
      <p className="subtle">{t('Edition and episode-order choices will reset. Unmatched copy scopes will be cleared.')}</p>
      <ErrorNote error={apply.error} /><div className="dialog-actions"><button className="button" disabled={apply.pending} onClick={() => setPreview(null)}>{t('Back')}</button><button className="button primary" disabled={apply.pending} onClick={() => void apply.run()}>{t('Apply match')}</button></div>
    </> : <>
      <form className="search-form" role="search" onSubmit={submit(() => setQ(text.trim()))}><Search size={18} aria-hidden /><input aria-label={t('Search the catalog')} value={text} maxLength={100} onChange={e => setText(e.target.value)} />{choices.length > 1 && <select aria-label={t('Search with')} value={source} onChange={e => setSource(e.target.value)}><option value="">{t('Default source')}</option>{choices.map(p => <option key={p.name} value={p.name}>{providerName(p.name)}</option>)}</select>}<button className="button small" type="submit">{t('Search')}</button></form>
      {results.isPending && q ? <Loading /> : results.error ? <Failed error={results.error} /> : <ul className="match-results">{(results.data?.items ?? []).filter(r => r.catalog_id !== entry.catalog_id).slice(0, 12).map(r => <li key={r.provider + r.provider_id}>
        <Cover entry={{ title: r.title, media_type: r.media_type, image_url: r.image_url }} compact />
        <div><strong>{r.title}</strong><p className="subtle">{[r.year, providerName(r.provider), ...r.credits.slice(0, 1)].filter(Boolean).join(' · ')}</p></div>
        <button className="button small" disabled={pick.pending} onClick={() => void pick.run(r)}><Check aria-hidden />{t('Use this')}</button>
      </li>)}</ul>}
      <ErrorNote error={pick.error} />
    </>}
  </Modal>;
}
