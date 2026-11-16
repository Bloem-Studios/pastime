import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Disc3, Pencil, Plus, Tag as TagIcon, Trash2, X } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from './api/client';
import { privateKey, useRefresh, useUser } from './state';
import { mediaTypes, statuses, typeLabel, useLibraryFacets } from './media';
import { facetChoices, facetCounts } from './browsePolicy';
import { ErrorNote, Field, Modal, submit, useAction, useConfirm } from './ui';
import { t, locale } from './i18n';

export type Preferences = Schema['Preferences'];
export type Tag = Schema['Tag'];
export type Copy = Schema['Copy'];

// ---------------------------------------------------------------- preferences

const fallback: Preferences = { home_rows: [], rating_scale: 'ten', title_language: 'localized', progress_display: 'count', library_view: 'grid', library_sort: 'added', week_start: 'monday', public_profile: false, version: 0 };

export function usePreferences() {
  const user = useUser(); const client = useQueryClient();
  const query = useQuery({ queryKey: privateKey(user.id, 'preferences'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/preferences', { signal })), staleTime: 300_000 });
  const save = useAction(async (next: Preferences) => {
    const saved = unwrap(await api.PUT('/api/v1/me/preferences', { body: next }));
    client.setQueryData(privateKey(user.id, 'preferences'), saved);
    return saved;
  });
  return { prefs: query.data ?? fallback, loaded: !!query.data, query, save };
}

/** Whether the person uses ratings; with ratings off every rating control is hidden. */
export function useRatingsOn() {
  return usePreferences().prefs.rating_scale !== 'off';
}

/** Shows a 0–10 rating on the person's chosen scale. */
export function rating(value: number | null | undefined, scale: Preferences['rating_scale']) {
  if (value == null || scale === 'off') return '';
  if (scale === 'five') return `${(value / 2).toLocaleString(locale, { maximumFractionDigits: 1 })}/5`;
  if (scale === 'hundred') return `${value * 10}%`;
  return `${value}/10`;
}

// ---------------------------------------------------------------- tags

export function useTags() {
  const user = useUser();
  return useQuery({ queryKey: privateKey(user.id, 'tags'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/tags', { signal })).items, staleTime: 60_000 });
}

export function TagChip({ tag, onRemove }: { tag: Pick<Tag, 'name' | 'color'>; onRemove?: () => void }) {
  return <span className="tag-chip" style={tag.color ? { ['--tag' as string]: tag.color } : undefined}>
    <span className="tag-dot" aria-hidden />{tag.name}
    {onRemove && <button type="button" aria-label={t('Remove tag {name}', { name: tag.name })} onClick={onRemove}><X aria-hidden /></button>}
  </span>;
}

/** Tags on one entry, with inline add and create. */
export function TagPicker({ entryID }: { entryID: string }) {
  const user = useUser(); const client = useQueryClient(); const all = useTags();
  const current = useQuery({ queryKey: privateKey(user.id, 'entry-tags', entryID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/tags', { signal, params: { path: { entryID } } })).items });
  const [text, setText] = useState('');
  const set = useAction(async (ids: string[], key) => {
    const out = unwrap(await api.PUT('/api/v1/library/{entryID}/tags', { params: { path: { entryID }, header: keyHeader(key) }, body: { tag_ids: ids } })).items;
    client.setQueryData(privateKey(user.id, 'entry-tags', entryID), out);
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'tags') });
  });
  const create = useAction(async (name: string, key) => {
    const made = unwrap(await api.POST('/api/v1/tags', { params: { header: keyHeader(key) }, body: { name } }));
    await set.run([...(current.data ?? []).map(item => item.id), made.id]);
  });
  const ids = (current.data ?? []).map(item => item.id);
  const match = text.trim().toLowerCase();
  const suggestions = (all.data ?? []).filter(item => !ids.includes(item.id) && (!match || item.name.toLowerCase().includes(match))).slice(0, 8);
  const exact = (all.data ?? []).some(item => item.name.toLowerCase() === match);
  return <div className="tag-picker">
    <div className="tag-row">{(current.data ?? []).map(item => <TagChip key={item.id} tag={item} onRemove={() => void set.run(ids.filter(id => id !== item.id))} />)}
      {!current.data?.length && <span className="subtle">{t('No tags yet')}</span>}</div>
    <form className="tag-add" onSubmit={submit(() => { if (match && !exact) { void create.run(text.trim()); setText(''); } })}>
      <label className="sr-only" htmlFor={`tag-${entryID}`}>{t('Add a tag')}</label>
      <TagIcon size={16} aria-hidden /><input id={`tag-${entryID}`} placeholder={t('Add a tag')} maxLength={50} value={text} onChange={e => setText(e.target.value)} />
      {match && !exact && <button className="button small" type="submit" disabled={create.pending}><Plus aria-hidden />{t('Create “{name}”', { name: text.trim() })}</button>}
    </form>
    {suggestions.length > 0 && <div className="tag-row suggestions" aria-label={t('Suggested tags')}>{suggestions.map(item => <button key={item.id} type="button" className="tag-chip ghost" onClick={() => { void set.run([...ids, item.id]); setText(''); }}><Plus aria-hidden />{item.name}</button>)}</div>}
    <ErrorNote error={set.error || create.error} />
  </div>;
}

// ---------------------------------------------------------------- owned copies

export const formats: [Copy['format'], string][] = [
  ['digital', t('Digital')], ['uhd_bluray', '4K Blu-ray'], ['bluray', t('Blu-ray')], ['dvd', 'DVD'], ['vhs', 'VHS'], ['hardcover', t('Hardcover')], ['paperback', t('Paperback')],
  ['ebook', t('E-book')], ['audiobook', t('Audiobook')], ['vinyl', t('Vinyl')], ['cd', 'CD'], ['cassette', t('Cassette')], ['game_physical', t('Game disc or cartridge')],
  ['game_digital', t('Digital game')], ['boxed', t('Boxed set')], ['other', t('Other')],
];
export const formatLabel = (f: string) => formats.find(x => x[0] === f)?.[1] ?? f;
const resolutions = [['', t('Not set')], ['sd', 'SD'], ['720p', '720p'], ['1080p', '1080p'], ['2160p', '4K (2160p)'], ['4320p', '8K']] as const;
const hdrs = [['', t('None')], ['hdr10', 'HDR10'], ['hdr10plus', t('HDR10+')], ['dolby_vision', t('Dolby Vision')], ['hlg', 'HLG']] as const;
export function money(cents?: number | null, currency?: string) {
  if (cents == null) return '';
  if (!currency) return (cents / 100).toLocaleString(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  try { return (cents / 100).toLocaleString(locale, { style: 'currency', currency }); } catch { return (cents / 100).toFixed(2); }
}
export function copySummary(c: Copy) {
  return [formatLabel(c.format), c.scope_title, c.edition, c.resolution && c.resolution.toUpperCase(), c.hdr && hdrs.find(h => h[0] === c.hdr)?.[1]].filter(Boolean).join(' · ');
}

export function CopiesSection({ entryID, seasons }: { entryID: string; seasons?: { id: string; title: string }[] }) {
  const user = useUser(); const refresh = useRefresh(); const confirm = useConfirm();
  const [cursor, setCursor] = useState<string | undefined>();
  useEffect(() => setCursor(undefined), [entryID]);
  const copies = useQuery({ queryKey: privateKey(user.id, 'copies', entryID, cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/copies', { signal, params: { path: { entryID }, query: { cursor, limit: 100 } } })) });
  const [editing, setEditing] = useState<Copy | 'new' | null>(null);
  const remove = useAction(async (copyID: string, key) => { unwrap(await api.DELETE('/api/v1/library/{entryID}/copies/{copyID}', { params: { path: { entryID, copyID }, header: keyHeader(key) } })); await refresh(); });
  return <section className="detail-section" aria-labelledby={`copies-${entryID}`}>
    <div className="section-heading"><h2 id={`copies-${entryID}`}><Disc3 size={18} aria-hidden />{t('Your copies')}</h2><button className="button small" onClick={() => setEditing('new')}><Plus aria-hidden />{t('Add a copy')}</button></div>
    {copies.data?.items.length ? <ul className="copy-list">{copies.data.items.map(c => <li key={c.id}>
      <div><strong>{copySummary(c)}</strong><p className="subtle">{[c.acquired_on && t('Acquired {value}', { value: new Date(c.acquired_on + 'T00:00:00').toLocaleDateString(locale) }), money(c.price_cents, c.currency), c.store, c.location].filter(Boolean).join(' · ') || 'No purchase details'}</p></div>
      <div className="actions"><button className="icon-button" aria-label={t('Edit {c}', { c: copySummary(c) })} onClick={() => setEditing(c)}><Pencil aria-hidden /></button>
        <button className="icon-button" aria-label={t('Remove {c}', { c: copySummary(c) })} disabled={remove.pending} onClick={async () => { if (await confirm({ title: t('Remove this copy?'), description: t('Only the record of this copy is removed.'), label: t('Remove copy'), danger: true })) void remove.run(c.id); }}><Trash2 aria-hidden /></button></div>
    </li>)}</ul> : <p className="subtle">{t('Record the discs, books or files you own, separately from what you have watched or read.')}</p>}
    <div className="actions">{cursor && <button className="button small" onClick={() => setCursor(undefined)}>{t('Back')}</button>}{copies.data?.next_cursor && <button className="button small" onClick={() => setCursor(copies.data!.next_cursor!)}>{t('Next')}</button>}</div>
    <ErrorNote error={copies.error || remove.error} />
    {editing && <CopyEditor entryID={entryID} copy={editing === 'new' ? undefined : editing} seasons={seasons} onClose={() => setEditing(null)} />}
  </section>;
}

function CopyEditor({ entryID, copy, seasons, onClose }: { entryID: string; copy?: Copy; seasons?: { id: string; title: string }[]; onClose: () => void }) {
  const user = useUser(); const refresh = useRefresh();
  const fields = useQuery({ queryKey: privateKey(user.id, 'fields'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collection-fields', { signal })).items });
  const [draft, setDraft] = useState<Schema['CopyInput']>(() => copy
    ? { catalog_item_id: copy.catalog_item_id, format: copy.format, edition: copy.edition, resolution: copy.resolution as Schema['CopyInput']['resolution'], hdr: copy.hdr as Schema['CopyInput']['hdr'], audio: copy.audio, bitrate_kbps: copy.bitrate_kbps, acquired_on: copy.acquired_on, price_cents: copy.price_cents, currency: copy.currency, store: copy.store, location: copy.location, notes: copy.notes, custom: copy.custom, version: copy.version }
    : { format: 'bluray', custom: {} });
  const [dirty, setDirty] = useState(false);
  const set = <K extends keyof Schema['CopyInput']>(k: K, v: Schema['CopyInput'][K]) => { setDraft(d => ({ ...d, [k]: v })); setDirty(true); };
  const action = useAction(async (body: Schema['CopyInput'], key) => {
    if (copy) unwrap(await api.PUT('/api/v1/library/{entryID}/copies/{copyID}', { params: { path: { entryID, copyID: copy.id }, header: keyHeader(key) }, body }));
    else unwrap(await api.POST('/api/v1/library/{entryID}/copies', { params: { path: { entryID }, header: keyHeader(key) }, body }));
    await refresh(); onClose();
  });
  const video = ['uhd_bluray', 'bluray', 'dvd', 'digital', 'vhs'].includes(draft.format);
  const text = (k: 'edition' | 'audio' | 'store' | 'location', label: string, max: number) => <Field label={label}>{id => <input id={id} maxLength={max} value={draft[k] ?? ''} onChange={e => set(k, e.target.value)} />}</Field>;
  return <Modal title={copy ? t('Edit copy') : t('Add a copy')} description={t('What you own is recorded separately from what you have watched, read or played.')} onClose={onClose} dirty={dirty} busy={action.pending} wide>
    <form onSubmit={submit(() => void action.run(draft))}><fieldset disabled={action.pending}>
      <div className="form-grid">
        <Field label={t('Format')}>{id => <select id={id} value={draft.format} onChange={e => set('format', e.target.value as Copy['format'])}>{formats.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>
        {seasons && seasons.length > 0 && <Field label={t('Covers')}>{id => <select id={id} value={draft.catalog_item_id ?? ''} onChange={e => set('catalog_item_id', e.target.value || null)}><option value="">{t('The whole title')}</option>{seasons.map(s => <option key={s.id} value={s.id}>{s.title}</option>)}</select>}</Field>}
        {text('edition', t('Edition'), 200)}
        {video && <Field label={t('Resolution')}>{id => <select id={id} value={draft.resolution ?? ''} onChange={e => set('resolution', e.target.value as Schema['CopyInput']['resolution'])}>{resolutions.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>}
        {video && <Field label="HDR">{id => <select id={id} value={draft.hdr ?? ''} onChange={e => set('hdr', e.target.value as Schema['CopyInput']['hdr'])}>{hdrs.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>}
        {video && text('audio', t('Audio'), 100)}
        <Field label={t('Acquired')}>{id => <input id={id} type="date" value={draft.acquired_on ?? ''} onChange={e => set('acquired_on', e.target.value || null)} />}</Field>
        <Field label={t('Price')}>{id => <input id={id} type="number" min={0} step="0.01" value={draft.price_cents == null ? '' : draft.price_cents / 100} onChange={e => set('price_cents', e.target.value === '' ? null : Math.round(Number(e.target.value) * 100))} />}</Field>
        <Field label={t('Currency')} hint={t('Three letters, such as EUR')}>{(id, hint) => <input id={id} aria-describedby={hint} maxLength={3} value={draft.currency ?? ''} onChange={e => set('currency', e.target.value.toUpperCase())} />}</Field>
        {text('store', t('Bought from'), 200)}
        {text('location', t('Kept at'), 200)}
        {(fields.data ?? []).map(f => <Field key={f.id} label={f.name}>{id => {
          const value = draft.custom?.[f.name];
          const put = (v: unknown) => set('custom', { ...(draft.custom ?? {}), [f.name]: v });
          if (f.kind === 'boolean') return <select id={id} value={value === true ? 'yes' : value === false ? 'no' : ''} onChange={e => put(e.target.value === '' ? null : e.target.value === 'yes')}><option value="">{t('Not set')}</option><option value="yes">{t('Yes')}</option><option value="no">{t('No')}</option></select>;
          if (f.kind === 'choice') return <select id={id} value={String(value ?? '')} onChange={e => put(e.target.value || null)}><option value="">{t('Not set')}</option>{f.choices.map(c => <option key={c} value={c}>{c}</option>)}</select>;
          return <input id={id} type={f.kind === 'number' ? 'number' : f.kind === 'date' ? 'date' : 'text'} value={String(value ?? '')} onChange={e => put(e.target.value === '' ? null : f.kind === 'number' ? Number(e.target.value) : e.target.value)} />;
        }}</Field>)}
      </div>
      <Field label={t('Notes')}>{id => <textarea id={id} rows={3} maxLength={2000} value={draft.notes ?? ''} onChange={e => set('notes', e.target.value)} />}</Field>
    </fieldset>
    <ErrorNote error={action.error} />
    <div className="dialog-actions"><button className="button primary" type="submit" disabled={action.pending}>{action.pending ? t('Saving…') : copy ? t('Save copy') : t('Add copy')}</button></div></form>
  </Modal>;
}

// ---------------------------------------------------------------- smart-list rules

export function RuleEditor({ rules, onChange }: { rules: Schema['Rules']; onChange: (next: Schema['Rules']) => void }) {
  const tags = useTags();
  const toggle = <T extends string>(list: T[] | undefined, value: T) => (list ?? []).includes(value) ? (list ?? []).filter(v => v !== value) : [...(list ?? []), value];
  const num = (v: string) => v === '' ? undefined : Number(v);
  // Only the media types the profile has titles of; one already in the rules stays so it can be cleared.
  const facets = useLibraryFacets();
  const order = mediaTypes.map(([v]) => v);
  const types = facets.error ? order : facetChoices(order, facetCounts(facets.data).types, rules.media_types ?? []).map(c => c.value);
  return <div className="rule-editor">
    {types.length > 0 && <fieldset><legend>{t('Media types')} <span className="subtle">{t('(any)')}</span></legend><div className="chip-filter">{types.map(v => <button key={v} type="button" aria-pressed={!!rules.media_types?.includes(v)} onClick={() => onChange({ ...rules, media_types: toggle(rules.media_types, v) })}>{typeLabel(v)}</button>)}</div></fieldset>}
    <fieldset><legend>{t('Statuses')} <span className="subtle">{t('(any)')}</span></legend><div className="chip-filter">{statuses.map(([v, l]) => <button key={v} type="button" aria-pressed={!!rules.statuses?.includes(v)} onClick={() => onChange({ ...rules, statuses: toggle(rules.statuses, v) })}>{l}</button>)}</div></fieldset>
    {(tags.data?.length ?? 0) > 0 && <fieldset><legend>{t('Tags')} <span className="subtle">{t('(all)')}</span></legend><div className="chip-filter">{tags.data!.map(item => <button key={item.id} type="button" aria-pressed={!!rules.tag_ids?.includes(item.id)} onClick={() => onChange({ ...rules, tag_ids: toggle(rules.tag_ids, item.id) })}>{item.name}</button>)}</div></fieldset>}
    <div className="form-grid">
      <Field label={t('Title words')} hint={t('Optional')}>{(id, hint) => <input id={id} aria-describedby={hint} maxLength={100} value={rules.query ?? ''} onChange={e => onChange({ ...rules, query: e.target.value || undefined })} />}</Field>
      <Field label={t('Minimum rating')} hint={t('0 to 10')}>{(id, hint) => <input id={id} aria-describedby={hint} type="number" min={0} max={10} value={rules.min_rating ?? ''} onChange={e => onChange({ ...rules, min_rating: num(e.target.value) })} />}</Field>
      <Field label={t('Released from')}>{id => <input id={id} type="number" min={1} max={9999} value={rules.year_from ?? ''} onChange={e => onChange({ ...rules, year_from: num(e.target.value) })} />}</Field>
      <Field label={t('Released until')}>{id => <input id={id} type="number" min={1} max={9999} value={rules.year_to ?? ''} onChange={e => onChange({ ...rules, year_to: num(e.target.value) })} />}</Field>
      <Field label={t('Ownership')}>{id => <select id={id} value={rules.owned === undefined ? '' : String(rules.owned)} onChange={e => onChange({ ...rules, owned: e.target.value === '' ? undefined : e.target.value === 'true' })}><option value="">{t('Owned or not')}</option><option value="true">{t('Only titles I own')}</option><option value="false">{t('Only titles I don’t own')}</option></select>}</Field>
    </div>
  </div>;
}
