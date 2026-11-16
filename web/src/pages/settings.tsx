import { countryOptions } from './formTasks';
import { PasswordInput } from './formControls';
import { useEffect, useState, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowDown, ArrowUp, Eye, EyeOff, Pencil, Plus, Send, Trash2 } from 'lucide-react';
import { renderSVG } from 'uqr';
import { useAuthMethods } from '../authMethods';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { privateKey, useUnsaved, useUser } from '../state';
import { TagChip, usePreferences, useTags, type Preferences } from '../organize';
import { readTheme, saveTheme, type ThemePreference } from '../theme';
import { Empty, ErrorNote, Field, Modal, submit, useAction, useConfirm, statusText } from '../ui';
import { mediaTypes } from '../media';
import type { CardField } from './insights';
import { languages, locale, plural, setLanguage, t, type Language } from '../i18n';

/** One settings area: a label column with what it is for, beside the panel that holds it. */
export function SettingsSection({ title, description, action, children, id }: { title: string; description?: ReactNode; action?: ReactNode; children: ReactNode; id?: string }) {
  return <section className="settings-section" aria-labelledby={id}>
    <div className="settings-label"><h2 id={id}>{title}</h2>{description && <p>{description}</p>}{action && <div className="settings-label-action">{action}</div>}</div>
    <div className="settings-panel">{children}</div>
  </section>;
}

/** A panel's own Save at its bottom right, with a quiet stamp once saved. */
export function PanelActions({ dirty, pending, saved, label, onDiscard }: { dirty: boolean; pending: boolean; saved: boolean; label?: string; onDiscard?: () => void }) {
  return <div className="panel-actions">
    {saved && !dirty && <span className="save-confirmation" role="status">{t('Saved')}</span>}
    {dirty && onDiscard && <button type="button" className="button ghost" disabled={pending} onClick={onDiscard}>{t('Discard')}</button>}
    <button className={dirty ? 'button primary' : 'button'} type="submit" disabled={pending || !dirty}>{pending ? t('Saving…') : label ?? t('Save')}</button>
  </div>;
}

type Setter = <K extends keyof Preferences>(key: K, value: Preferences[K]) => void;
type Extra = { dirty: boolean; commit: () => void; discard: () => void };

/**
 * One preferences view with one Save. The bar appears only while something is unsaved (and briefly
 * after saving), so a settled view carries no chrome.
 */
function PreferencesForm({ children, extra }: { children: (draft: Preferences, set: Setter) => ReactNode; extra?: Extra }) {
  const { prefs, loaded, save, query } = usePreferences(); const unsaved = useUnsaved();
  const [draft, setDraft] = useState<Preferences>(prefs); const [dirty, setDirty] = useState(false); const [stamp, setStamp] = useState(false);
  useEffect(() => { if (!dirty) setDraft(prefs); }, [prefs, dirty]);
  useEffect(() => { if (!stamp) return; const id = setTimeout(() => setStamp(false), 4000); return () => clearTimeout(id); }, [stamp]);
  const set: Setter = (key, value) => { setDraft(d => ({ ...d, [key]: value })); setDirty(true); unsaved.markDirty(); save.reset(); setStamp(false); };
  const changed = dirty || !!extra?.dirty;
  async function commit() {
    if (dirty) {
      const out = await save.run(draft);
      if (!out) return;
      setDraft(out); setDirty(false);
      if ((draft.language ?? '') !== (prefs.language ?? '')) setLanguage((draft.language || 'auto') as Language | 'auto');
    }
    extra?.commit(); unsaved.markSaved(); setStamp(true);
  }
  function discard() { setDraft(prefs); setDirty(false); extra?.discard(); save.reset(); unsaved.markSaved(); }
  return <form className="settings-form" onSubmit={submit(() => void commit())}>
    <fieldset disabled={!loaded || save.pending}>{children(draft, set)}</fieldset>
    <ErrorNote error={query.error} />
    {(changed || save.pending || !!save.error || stamp) && <div className={changed ? 'save-bar dirty' : 'save-bar'}>
      <span role="status">{changed ? t('Unsaved changes') : t('Preferences saved.')}</span>
      <ErrorNote error={save.error} />
      {changed && <div className="save-bar-actions"><button type="button" className="button ghost" disabled={save.pending} onClick={discard}>{t('Discard')}</button>
        <button className="button primary" type="submit" disabled={!loaded || save.pending}>{save.pending ? t('Saving…') : t('Save preferences')}</button></div>}
    </div>}
  </form>;
}

const titleLanguages: [string, string][] = [['en', t('English')], ['de', t('German')], ['fr', t('French')], ['es', t('Spanish')], ['it', t('Italian')], ['pt', t('Portuguese')], ['nl', t('Dutch')],
  ['sv', t('Swedish')], ['da', t('Danish')], ['no', t('Norwegian')], ['fi', t('Finnish')], ['pl', t('Polish')], ['cs', t('Czech')], ['tr', t('Turkish')], ['ru', t('Russian')], ['uk', t('Ukrainian')],
  ['ja', t('Japanese')], ['ko', t('Korean')], ['zh', t('Chinese')], ['ar', t('Arabic')], ['he', t('Hebrew')], ['hi', t('Hindi')]];

function choice<K extends keyof Preferences>(draft: Preferences, set: Setter, key: K, label: string, options: [string, string][], hint?: string) {
  return <Field label={label} hint={hint}>{(id, h) => <select id={id} aria-describedby={h} value={String(draft[key] ?? '')} onChange={e => set(key, e.target.value as Preferences[K])}>{options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>;
}

/** Display: how titles, ratings, progress, the calendar and the theme look. */
export function DisplaySection() {
  const [theme, setTheme] = useState<ThemePreference>(readTheme); const [stored, setStored] = useState<ThemePreference>(readTheme); const [kept, setKept] = useState(true);
  const unsaved = useUnsaved();
  const [countrySearch, setCountrySearch] = useState('');
  const countries = countryOptions(locale);
  const extra: Extra = { dirty: theme !== stored, commit: () => { if (theme !== stored) { setKept(saveTheme(theme)); setStored(theme); } }, discard: () => setTheme(stored) };
  return <PreferencesForm extra={extra}>{(draft, set) => <>
    <SettingsSection title={t('Language and titles')} description={t('The interface language and the language titles are shown in.')}>
      <div className="form-grid">
        {choice(draft, set, 'language', t('Language'), [['', t('Same as the browser')], ...languages.map(([code, name]) => [code, name] as [string, string])])}
        {choice(draft, set, 'title_language', t('Titles'), [['localized', t('In the metadata language')], ['original', t('Original titles where known')], ...titleLanguages], t('A language shows titles in it where the catalog has a translation, else the usual title.'))}
      </div>
    </SettingsSection>
    <SettingsSection title={t('Ratings and progress')} description={t('Ratings are stored out of 10 and shown on this scale. Off hides ratings everywhere and keeps the ones you saved.')}>
      <div className="form-grid">
        {choice(draft, set, 'rating_scale', t('Ratings'), [['ten', t('Out of 10')], ['five', t('Out of 5 stars')], ['hundred', t('Percent')], ['off', t('Off')]])}
        {choice(draft, set, 'progress_display', t('Progress'), [['count', t('Counts, such as 3 of 10 episodes')], ['percent', t('Percent complete')]])}
      </div>
    </SettingsSection>
    <SettingsSection title={t('Browsing')} description={t('How the library opens, how Discover finds anime, and the first day of a week in the calendar and statistics.')}>
      <div className="form-grid">
        {choice(draft, set, 'library_view', t('Library view'), [['grid', t('Artwork grid')], ['list', t('Table')]])}
        {choice(draft, set, 'library_sort', t('Library order'), [['added', t('Recently added')], ['activity', t('Recent activity')], ['title', t('Title')], ['rating', t('Your rating')], ['year', t('Release year')]])}
        {choice(draft, set, 'anime_shape', t('Anime'), [['flat', t('Season by season (AniList)')], ['grouped', t('Whole shows with seasons (TMDB)')]], t('Titles you already track stay as they are.'))}
        {choice(draft, set, 'week_start', t('Week starts on'), [['monday', t('Monday')], ['sunday', t('Sunday')]])}
      </div>
    </SettingsSection>
    <SettingsSection title={t('Calendar')} description={t('Movies show this country’s release dates in the calendar.')}>
      <div className="form-grid">
        <Field label={t('Release dates for')}>{id => <select id={id} value={draft.release_region ?? ''} onChange={e => set('release_region', e.target.value)}><option value="">{t('No regional release dates')}</option>{countries.filter(([code, name]) => code === draft.release_region || `${name} ${code}`.toLocaleLowerCase(locale).includes(countrySearch.toLocaleLowerCase(locale))).map(([code, name]) => <option key={code} value={code}>{name} ({code})</option>)}</select>}</Field>
        <Field label={t('Find a country')} hint={t('Filter the country choices by name or code.')}>{(id, h) => <input id={id} aria-describedby={h} type="search" value={countrySearch} onChange={e => setCountrySearch(e.target.value)} />}</Field>
      </div>
      <fieldset className="check-set"><legend>{t('Movie releases in the calendar')}</legend><div className="check-grid">{([['theatrical', t('In cinemas')], ['digital', t('Digital')], ['physical', t('Disc')]] as const).map(([v, l]) => <label key={v} className="check-row"><input type="checkbox" disabled={!draft.release_region} checked={draft.calendar_release_types?.includes(v) ?? false} onChange={e => set('calendar_release_types', e.target.checked ? [...(draft.calendar_release_types ?? []), v] : (draft.calendar_release_types ?? []).filter(x => x !== v))} /><span>{l}</span></label>)}</div>
        {!draft.release_region && <p className="field-hint">{t('Choose a country to pick release types.')}</p>}</fieldset>
    </SettingsSection>
    <SettingsSection title={t('Theme')} description={t('Saved only in this browser. The interface respects your system’s reduced-motion setting.')}>
      <div className="form-grid"><Field label={t('Theme')}>{id => <select id={id} value={theme} onChange={e => { setTheme(e.target.value as ThemePreference); unsaved.markDirty(); }}><option value="dark">{t('Dark')}</option><option value="light">{t('Light')}</option><option value="system">{t('Follow system')}</option></select>}</Field></div>
      {!kept && <p role="status" className="field-hint">{t('Your browser did not allow saving this preference. It applies for this visit.')}</p>}
    </SettingsSection>
  </>}</PreferencesForm>;
}

const rowLabels: Record<string, string> = { up_next: t('Up next'), continue: t('Continue'), upcoming: t('Coming soon'), planned: t('On your list'), recently_finished: t('Recently finished') };
const cardFields: [CardField, string][] = [['type', t('Type')], ['year', t('Year')], ['status', t('Status')], ['progress', t('Progress')], ['rating', t('Rating')]];
const mixSorts: [NonNullable<Schema['HomeRow']['sort']>, string][] = [['activity', t('Recent activity')], ['added', t('Recently added')], ['title', t('Title')], ['rating', t('Your rating')], ['year', t('Release year')]];

/** Edits one mixed Home row: its title, media types, statuses, order and length. */
function MixRowEditor({ row, onClose, onDone }: { row: Schema['HomeRow']; onClose: () => void; onDone: (row: Schema['HomeRow']) => void }) {
  const [draft, setDraft] = useState(row);
  const toggle = <K extends 'media_types' | 'statuses'>(k: K, v: string) => setDraft(d => { const now = (d[k] ?? []) as string[]; return { ...d, [k]: now.includes(v) ? now.filter(x => x !== v) : [...now, v] }; });
  return <Modal title={t('Mixed Home row')} description={t('Show titles from your library across several kinds of media.')} onClose={onClose} dirty={JSON.stringify(draft) !== JSON.stringify(row)} busy={false} wide>
    <form onSubmit={submit(() => onDone(draft))}>
      <Field label={t('Title')}>{id => <input id={id} required maxLength={60} value={draft.title ?? ''} onChange={e => setDraft(d => ({ ...d, title: e.target.value }))} />}</Field>
      <fieldset className="check-set"><legend>{t('Media types')}</legend><div className="check-grid">{mediaTypes.map(([v, l]) => <label key={v} className="check-row"><input type="checkbox" checked={draft.media_types?.includes(v) ?? false} onChange={() => toggle('media_types', v)} /><span>{l}</span></label>)}</div></fieldset>
      <fieldset className="check-set"><legend>{t('Statuses')} <span className="subtle">{t('(any)')}</span></legend><div className="check-grid">{(['planned', 'in_progress', 'completed', 'paused', 'dropped'] as const).map(v => <label key={v} className="check-row"><input type="checkbox" checked={draft.statuses?.includes(v) ?? false} onChange={() => toggle('statuses', v)} /><span>{statusText(v)}</span></label>)}</div></fieldset>
      <div className="form-grid">
        <Field label={t('Order')}>{id => <select id={id} value={draft.sort ?? 'activity'} onChange={e => setDraft(d => ({ ...d, sort: e.target.value as Schema['HomeRow']['sort'] }))}>{mixSorts.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>
        <Field label={t('Titles shown')}>{id => <input id={id} type="number" min={1} max={30} value={draft.limit ?? 20} onChange={e => setDraft(d => ({ ...d, limit: Number(e.target.value) }))} />}</Field>
      </div>
      <div className="dialog-actions"><button className="button primary" type="submit" disabled={!draft.media_types?.length || !draft.title?.trim()}>{t('Done')}</button></div>
    </form>
  </Modal>;
}

/** Home screen: which rows Home shows and what each poster card says. */
export function HomeScreenSection() {
  const user = useUser();
  const lists = useQuery({ queryKey: privateKey(user.id, 'pref-lists'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists', { signal, params: { query: { limit: 100 } } })).items });
  const [mixing, setMixing] = useState<{ index: number; row: Schema['HomeRow'] } | null>(null);
  const listName = (id?: string) => lists.data?.find(l => l.id === id)?.name ?? t('Deleted list');
  return <PreferencesForm>{(draft, set) => {
    const rows = draft.home_rows ?? [];
    const rowName = (r: Schema['HomeRow']) => r.kind === 'list' ? listName(r.list_id) : r.kind === 'mix' ? r.title ?? '' : rowLabels[r.kind];
    const moveRow = (i: number, d: number) => { const next = [...rows]; const [r] = next.splice(i, 1); next.splice(i + d, 0, r); set('home_rows', next); };
    const addable = (lists.data ?? []).filter(l => !rows.some(r => r.kind === 'list' && r.list_id === l.id));
    return <>
      <SettingsSection title={t('Rows')} description={t('Which rows Home shows, in this order. Lists can be rows too.')}>
        <ol className="row-editor">{rows.map((r, i) => <li key={r.kind + (r.list_id ?? '') + (r.id ?? '')} className={r.visible ? '' : 'hidden-row'}>
          <span className="row-name">{r.kind === 'list' ? <>{rowName(r)}<span className="subtle"> · {t('List')}</span></> : r.kind === 'mix' ? <>{rowName(r)}<span className="subtle"> · {t('Mixed row')}</span></> : rowName(r)}{!r.visible && <span className="subtle"> · {t('Hidden')}</span>}</span>
          <div className="row-actions">
            <button type="button" className="icon-button" aria-label={t('Move {value} up', { value: rowName(r) || t('row') })} disabled={i === 0} onClick={() => moveRow(i, -1)}><ArrowUp aria-hidden /></button>
            <button type="button" className="icon-button" aria-label={t('Move {value} down', { value: rowName(r) || t('row') })} disabled={i === rows.length - 1} onClick={() => moveRow(i, 1)}><ArrowDown aria-hidden /></button>
            <button type="button" className="icon-button" aria-pressed={r.visible} aria-label={`${r.visible ? t('Hide') : t('Show')} ${rowName(r)}`} onClick={() => set('home_rows', rows.map((x, j) => j === i ? { ...x, visible: !x.visible } : x))}>{r.visible ? <Eye aria-hidden /> : <EyeOff aria-hidden />}</button>
            {r.kind === 'mix' && <button type="button" className="icon-button" aria-label={t('Edit {value}', { value: rowName(r) })} onClick={() => setMixing({ index: i, row: r })}><Pencil aria-hidden /></button>}
            {(r.kind === 'mix' || r.kind === 'list') && <button type="button" className="icon-button" aria-label={t('Remove {listId} from home', { listId: rowName(r) })} onClick={() => set('home_rows', rows.filter((_, j) => j !== i))}><Trash2 aria-hidden /></button>}
          </div>
        </li>)}</ol>
        {rows.length < 20 && <div className="row-editor-add">
          {addable.length > 0 && <label className="select-pill add-row"><span className="sr-only">{t('Add a list to home')}</span><Plus size={16} aria-hidden /><select value="" onChange={e => { if (e.target.value) set('home_rows', [...rows, { kind: 'list', list_id: e.target.value, visible: true }]); }}><option value="">{t('Add a list row…')}</option>{addable.map(l => <option key={l.id} value={l.id}>{l.name}</option>)}</select></label>}
          <button type="button" className="button add-row" onClick={() => setMixing({ index: -1, row: { kind: 'mix', id: Math.random().toString(36).slice(2, 10), title: t('Mixed shelf'), media_types: ['movie', 'series'], statuses: [], sort: 'activity', limit: 20, visible: true } })}><Plus aria-hidden />{t('Add a mixed row')}</button>
        </div>}
        {mixing && <MixRowEditor row={mixing.row} onClose={() => setMixing(null)} onDone={row => { set('home_rows', mixing.index < 0 ? [...rows, row] : rows.map((x, j) => j === mixing.index ? row : x)); setMixing(null); }} />}
      </SettingsSection>
      <SettingsSection title={t('Poster details')} description={t('What shows under posters on Home, for each kind of media.')}>
        <table className="card-fields"><thead><tr><th scope="col">{t('Media type')}</th>{cardFields.map(([f, l]) => <th key={f} scope="col">{l}</th>)}</tr></thead>
          <tbody>{mediaTypes.map(([type, label]) => { const chosen: CardField[] = (draft.card_fields?.[type] as CardField[] | undefined) ?? ['type', 'year']; return <tr key={type}><th scope="row">{label}</th>
            {cardFields.map(([f, l]) => <td key={f}><label className="card-field"><input type="checkbox" aria-label={`${label}: ${l}`} checked={chosen.includes(f)} onChange={e => set('card_fields', { ...(draft.card_fields ?? {}), [type]: e.target.checked ? cardFields.map(c => c[0]).filter(c => c === f || chosen.includes(c)) : chosen.filter(c => c !== f) })} /><span className="card-field-label" aria-hidden>{l}</span></label></td>)}</tr>; })}</tbody></table>
      </SettingsSection>
    </>;
  }}</PreferencesForm>;
}

/** Who can see your lists and send you titles: one panel with its own Save. */
export function SharingSection() {
  const { prefs, loaded, save } = usePreferences(); const user = useUser(); const unsaved = useUnsaved();
  const [draft, setDraft] = useState({ public_profile: prefs.public_profile, decline_recommendations: prefs.decline_recommendations ?? false }); const [dirty, setDirty] = useState(false);
  useEffect(() => { if (!dirty) setDraft({ public_profile: prefs.public_profile, decline_recommendations: prefs.decline_recommendations ?? false }); }, [prefs, dirty]);
  const change = (next: typeof draft) => { setDraft(next); setDirty(true); unsaved.markDirty(); save.reset(); };
  return <SettingsSection title={t('Sharing')} description={t('Your public lists appear at /u/{username}. Unlisted and private lists never do.', { username: user.username })}>
    <form onSubmit={submit(async () => { const out = await save.run({ ...prefs, ...draft }); if (out) { setDirty(false); unsaved.markSaved(); } })}>
      <fieldset disabled={!loaded || save.pending}>
        <label className="check-row"><input name="public_profile" type="checkbox" checked={draft.public_profile} onChange={e => change({ ...draft, public_profile: e.target.checked })} /><span><strong>{t('Public profile')}</strong>{prefs.public_profile && <Link className="text-link" to={`/u/${user.username}`}>{t('Open your public profile')}</Link>}</span></label>
        <label className="check-row"><input name="decline_recommendations" type="checkbox" checked={draft.decline_recommendations} onChange={e => change({ ...draft, decline_recommendations: e.target.checked })} /><span><strong>{t('Decline new recommendations')}</strong></span></label>
      </fieldset>
      <ErrorNote error={save.error} />
      <PanelActions dirty={dirty} pending={save.pending} saved={save.saved} />
    </form>
  </SettingsSection>;
}

export function TagsSection() {
  const tags = useTags(); const client = useQueryClient(); const user = useUser(); const confirm = useConfirm();
  const [editing, setEditing] = useState<Schema['Tag'] | 'new' | null>(null);
  const remove = useAction(async (id: string, key) => { unwrap(await api.DELETE('/api/v1/tags/{tagID}', { params: { path: { tagID: id }, header: keyHeader(key) } })); await client.invalidateQueries({ queryKey: privateKey(user.id) }); });
  const create = <button className="button" onClick={() => setEditing('new')}><Plus aria-hidden />{t('New tag')}</button>;
  return <SettingsSection title={t('Tags')} description={t('Tags are yours alone. Filter your library by them, use them in smart lists and apply them to many titles at once from the library.')} action={tags.data?.length ? create : undefined}>
    <ErrorNote error={remove.error || tags.error} />
    {tags.data?.length ? <ul className="record-list tag-records">{tags.data.map(item => <li key={item.id} className="record-row"><div><TagChip tag={item} /><p>{plural(item.entries, '{n} title', '{n} titles')}</p></div>
      <div className="actions"><button className="icon-button" aria-label={t('Edit tag {name}', { name: item.name })} onClick={() => setEditing(item)}><Pencil aria-hidden /></button>
        <button className="icon-button" aria-label={t('Delete tag {name}', { name: item.name })} onClick={async () => { if (await confirm({ title: t('Delete “{name}”?', { name: item.name }), description: t('The tag is removed from every title. The titles stay in your library.'), label: t('Delete tag'), danger: true })) void remove.run(item.id); }}><Trash2 aria-hidden /></button></div></li>)}</ul>
      : tags.isPending ? <p className="subtle">{t('Loading')}</p> : <Empty compact title={t('No tags yet')} action={create} />}
    {editing && <TagEditor tag={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
  </SettingsSection>;
}

const swatches = ['', '#5f74ee', '#169e88', '#bf7f22', '#c2578f', '#3f97cf', '#8a6fd1', '#d0584a'];

function TagEditor({ tag, onClose }: { tag?: Schema['Tag']; onClose: () => void }) {
  const client = useQueryClient(); const user = useUser();
  const [name, setName] = useState(tag?.name ?? ''); const [color, setColor] = useState(tag?.color ?? '');
  const save = useAction(async (_: void, key) => {
    const body = { name, color };
    if (tag) unwrap(await api.PUT('/api/v1/tags/{tagID}', { params: { path: { tagID: tag.id }, header: keyHeader(key) }, body }));
    else unwrap(await api.POST('/api/v1/tags', { params: { header: keyHeader(key) }, body }));
    await client.invalidateQueries({ queryKey: privateKey(user.id) }); onClose();
  });
  return <Modal title={tag ? t('Edit tag') : t('New tag')} description={t('Names are unique, ignoring capitals.')} onClose={onClose} busy={save.pending} dirty={name !== (tag?.name ?? '') || color !== (tag?.color ?? '')}>
    <form onSubmit={submit(() => void save.run())}>
      <Field label={t('Name')}>{id => <input id={id} required maxLength={50} value={name} onChange={e => setName(e.target.value)} />}</Field>
      <fieldset className="swatches"><legend>{t('Colour')}</legend>{swatches.map(c => <button key={c || 'none'} type="button" aria-label={c ? t('Colour {c}', { c: c }) : t('No colour')} aria-pressed={color === c} style={c ? { background: c } : undefined} className={c ? '' : 'none'} onClick={() => setColor(c)} />)}</fieldset>
      <ErrorNote error={save.error} />
      <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{tag ? t('Save tag') : t('Create tag')}</button></div>
    </form>
  </Modal>;
}

export function FieldsSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm();
  const fields = useQuery({ queryKey: privateKey(user.id, 'fields'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collection-fields', { signal })).items });
  const [editing, setEditing] = useState<Schema['Field'] | 'new' | null>(null);
  const remove = useAction(async (id: string, key) => { unwrap(await api.DELETE('/api/v1/collection-fields/{fieldID}', { params: { path: { fieldID: id }, header: keyHeader(key) } })); await client.invalidateQueries({ queryKey: privateKey(user.id, 'fields') }); });
  const kinds: Record<string, string> = { text: t('Text'), number: t('Number'), date: t('Date'), boolean: t('Yes or no'), choice: t('Choice') };
  const create = <button className="button" onClick={() => setEditing('new')}><Plus aria-hidden />{t('New field')}</button>;
  return <SettingsSection title={t('Collection fields')} description={t('Your own details on owned copies, such as a shelf, a region code or whether a copy is signed.')} action={fields.data?.length ? create : undefined}>
    <ErrorNote error={remove.error || fields.error} />
    {fields.data?.length ? <ul className="record-list">{fields.data.map(f => <li key={f.id} className="record-row"><div><h3>{f.name}</h3><p>{kinds[f.kind]}{f.kind === 'choice' && `: ${f.choices.join(', ')}`}</p></div>
      <div className="actions"><button className="icon-button" aria-label={t('Edit field {name}', { name: f.name })} onClick={() => setEditing(f)}><Pencil aria-hidden /></button>
        <button className="icon-button" aria-label={t('Delete field {name}', { name: f.name })} onClick={async () => { if (await confirm({ title: t('Delete “{name}”?', { name: f.name }), description: t('The field is hidden from copies. Values already entered are kept.'), label: t('Delete field'), danger: true })) void remove.run(f.id); }}><Trash2 aria-hidden /></button></div></li>)}</ul>
      : fields.isPending ? <p className="subtle">{t('Loading')}</p> : <Empty compact title={t('No custom fields yet')} action={create} />}
    {editing && <FieldEditor field={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
  </SettingsSection>;
}

function FieldEditor({ field, onClose }: { field?: Schema['Field']; onClose: () => void }) {
  const client = useQueryClient(); const user = useUser();
  const initial: Schema['FieldInput'] = { name: field?.name ?? '', kind: field?.kind ?? 'text', choices: field?.choices ?? [], position: field?.position ?? 0 };
  const [draft, setDraft] = useState<Schema['FieldInput']>(initial);
  const save = useAction(async (_: void, key) => {
    if (field) unwrap(await api.PUT('/api/v1/collection-fields/{fieldID}', { params: { path: { fieldID: field.id }, header: keyHeader(key) }, body: draft }));
    else unwrap(await api.POST('/api/v1/collection-fields', { params: { header: keyHeader(key) }, body: draft }));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'fields') }); onClose();
  });
  return <Modal title={field ? t('Edit field') : t('New field')} description={t('Fields appear on every copy you record.')} onClose={onClose} busy={save.pending} dirty={JSON.stringify(draft) !== JSON.stringify(initial)}>
    <form onSubmit={submit(() => void save.run())}>
      <Field label={t('Name')}>{id => <input id={id} required maxLength={60} value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} />}</Field>
      <Field label={t('Type')}>{id => <select id={id} value={draft.kind} onChange={e => setDraft({ ...draft, kind: e.target.value as Schema['FieldInput']['kind'] })}><option value="text">{t('Text')}</option><option value="number">{t('Number')}</option><option value="date">{t('Date')}</option><option value="boolean">{t('Yes or no')}</option><option value="choice">{t('Choice')}</option></select>}</Field>
      {draft.kind === 'choice' && <Field label={t('Choices')} hint={t('One per line')}>{(id, hint) => <textarea id={id} aria-describedby={hint} rows={4} value={(draft.choices ?? []).join('\n')} onChange={e => setDraft({ ...draft, choices: e.target.value.split('\n').map(v => v.trim()).filter(Boolean) })} />}</Field>}
      <ErrorNote error={save.error} />
      <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{field ? t('Save field') : t('Create field')}</button></div>
    </form>
  </Modal>;
}

// ---------------------------------------------------------------- authenticator recovery

export function AuthenticatorSection() {
  const user = useUser(); const confirm = useConfirm();
  const status = useQuery({ queryKey: privateKey(user.id, 'totp'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/totp', { signal })) });
  const methods = useAuthMethods();
  const [dialog, setDialog] = useState<'setup' | 'disable' | null>(null);
  const description = t('Resets your local Pastime password if you forget it. It does not add a second factor to sign-in or change Authentik MFA.');
  if (methods.data && !methods.data.recovery) return <SettingsSection title={t('Authenticator recovery')} description={description}><p className="subtle">{t('Authenticator recovery is not available on this server. Ask your administrator for a local password reset link.')}</p></SettingsSection>;
  const enabled = status.data?.enabled;
  return <SettingsSection title={t('Authenticator recovery')} description={description}>
    <ErrorNote error={status.error} />
    <div className="setting-row">
      <div><strong>{enabled ? t('On') : status.isPending ? t('Loading') : t('Off')}</strong>{enabled && <p className="subtle">{plural(status.data!.recovery_remaining, '{n} recovery code left', '{n} recovery codes left')}</p>}</div>
      {enabled ? <button className="button" onClick={async () => { if (await confirm({ title: t('Turn off authenticator recovery?'), description: t('You will need an administrator to reset a forgotten password.'), label: t('Continue') })) setDialog('disable'); }}>{t('Turn off')}</button>
        : <button className="button" disabled={status.isPending} onClick={() => setDialog('setup')}>{t('Set up')}</button>}
    </div>
    {dialog === 'setup' && <AuthenticatorSetup onClose={() => setDialog(null)} />}
    {dialog === 'disable' && <AuthenticatorDisable onClose={() => setDialog(null)} />}
  </SettingsSection>;
}

function AuthenticatorSetup({ onClose }: { onClose: () => void }) {
  const user = useUser(); const client = useQueryClient();
  const [setup, setSetup] = useState<Schema['TOTPSetup'] | null>(null); const [codes, setCodes] = useState<string[] | null>(null);
  const [password, setPassword] = useState(''); const [code, setCode] = useState('');
  const begin = useAction(async (_: void) => { setSetup(unwrap(await api.POST('/api/v1/me/totp', { body: { password } }))); setPassword(''); });
  const verify = useAction(async (_: void) => { setCodes(unwrap(await api.POST('/api/v1/me/totp/confirm', { body: { code } })).recovery_codes); setCode(''); await client.invalidateQueries({ queryKey: privateKey(user.id, 'totp') }); });
  if (codes) return <Modal title={t('Save your recovery codes')} description={t('Each code resets your password once if you lose your authenticator. They are shown only now.')} onClose={onClose}>
    <ul className="recovery-codes mono">{codes.map(c => <li key={c}>{c}</li>)}</ul>
    <div className="dialog-actions"><button className="button" onClick={() => void navigator.clipboard.writeText(codes.join('\n')).catch(() => undefined)}>{t('Copy')}</button><button className="button primary" onClick={onClose}>{t('I saved them')}</button></div>
  </Modal>;
  return <Modal title={t('Set up authenticator recovery')} description={setup ? t('Scan the code, then type the six-digit code the app shows.') : t('Confirm your current password to start.')} onClose={onClose} busy={begin.pending || verify.pending} dirty={!!password || !!setup}>
    {setup ? <div className="totp-setup">
      <div className="qr" aria-label={t('QR code for your authenticator app')} role="img" dangerouslySetInnerHTML={{ __html: renderSVG(setup.otpauth_uri, { border: 1 }) }} />
      <div><ol className="setup-steps"><li><a className="text-link" href={setup.otpauth_uri}>{t('Open it on this device')}</a></li><li>{t('Can’t scan? Enter this key:')} <code className="mono">{setup.secret.match(/.{1,4}/g)?.join(' ')}</code></li></ol>
        <form onSubmit={submit(() => void verify.run())}><Field label={t('Code')}>{id => <input id={id} inputMode="numeric" autoComplete="one-time-code" required maxLength={7} value={code} onChange={e => setCode(e.target.value)} />}</Field>
          <ErrorNote error={verify.error} /><div className="dialog-actions"><button className="button primary" type="submit" disabled={verify.pending}>{t('Confirm')}</button></div></form></div>
    </div> : <form onSubmit={submit(() => void begin.run())}><Field label={t('Current password')}>{id => <PasswordInput id={id} type="password" required autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      <ErrorNote error={begin.error} /><div className="dialog-actions"><button className="button primary" type="submit" disabled={begin.pending}>{t('Continue')}</button></div></form>}
  </Modal>;
}

function AuthenticatorDisable({ onClose }: { onClose: () => void }) {
  const user = useUser(); const client = useQueryClient(); const [password, setPassword] = useState('');
  const disable = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/me/totp', { body: { password } })); setPassword(''); await client.invalidateQueries({ queryKey: privateKey(user.id, 'totp') }); onClose(); });
  return <Modal title={t('Turn off authenticator recovery')} description={t('Confirm your current password. A forgotten password then needs an administrator.')} onClose={onClose} busy={disable.pending} dirty={!!password}>
    <form onSubmit={submit(() => void disable.run())}><Field label={t('Current password')}>{id => <PasswordInput id={id} type="password" required autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} />}</Field>
      <ErrorNote error={disable.error} /><div className="dialog-actions"><button className="button danger" type="submit" disabled={disable.pending}>{t('Turn off')}</button></div></form>
  </Modal>;
}

export function SignInSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm(); const methods = useAuthMethods();
  const links = useQuery({ queryKey: privateKey(user.id, 'oidc'), enabled: !!methods.data?.oidc.enabled, queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/oidc', { signal })).items });
  const link = useAction(async (_: void) => { window.location.assign(unwrap(await api.POST('/api/v1/me/oidc/link')).url); });
  const unlink = useAction(async (issuer: string) => { unwrap(await api.DELETE('/api/v1/me/oidc', { params: { query: { issuer } } })); await client.invalidateQueries({ queryKey: privateKey(user.id, 'oidc') }); });
  if (!methods.data?.oidc.enabled) return null;
  return <SettingsSection title={t('Single sign-on')} description={t('Sign in with {name} instead of your password.', { name: methods.data.oidc.name })}>
    {links.data?.length ? <ul className="record-list">{links.data.map(l => <li key={l.issuer} className="record-row"><div><h3>{methods.data!.oidc.name}</h3><p>{l.email || new URL(l.issuer).host} · {l.last_login_at ? t('last used {date}', { date: new Date(l.last_login_at).toLocaleDateString(locale) }) : t('never used')}</p></div>
      <button className="button small danger-outline" disabled={unlink.pending} onClick={async () => { if (await confirm({ title: t('Unlink this sign-in?'), description: t('You can link it again later.'), label: t('Unlink'), danger: true })) void unlink.run(l.issuer); }}>{t('Unlink')}</button></li>)}</ul>
      : <div className="setting-row"><div><strong>{t('Not linked')}</strong></div><button className="button" disabled={link.pending} onClick={() => void link.run()}>{t('Link')} {methods.data.oidc.name}</button></div>}
    <ErrorNote error={link.error || unlink.error} />
  </SettingsSection>;
}

// ---------------------------------------------------------------- notifications

const destinationKinds: [Schema['NotificationTarget']['kind'], string, string][] = [
  ['ntfy', 'ntfy', t('Push to your phone with the ntfy app.')], ['discord', t('Discord'), t('Post to a channel through a webhook.')], ['gotify', t('Gotify'), t('Self-hosted push notifications.')],
  ['apprise', t('Apprise'), t('Any service your Apprise API server supports.')], ['webhook', t('Webhook'), t('A signed JSON POST to your own endpoint.')],
];

export function NotificationsSection() {
  const user = useUser(); const client = useQueryClient(); const confirm = useConfirm(); const methods = useAuthMethods(); const unsaved = useUnsaved();
  const settings = useQuery({ queryKey: privateKey(user.id, 'notify-settings'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/notification-settings', { signal })) });
  const targets = useQuery({ queryKey: privateKey(user.id, 'notify-targets'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/me/notification-targets', { signal })).items });
  const [draft, setDraft] = useState<Schema['NotificationSettings'] | null>(null); const [editing, setEditing] = useState<Schema['NotificationTarget'] | 'new' | null>(null);
  const value = draft ?? settings.data;
  const reload = () => client.invalidateQueries({ queryKey: privateKey(user.id) });
  const save = useAction(async (body: Schema['NotificationSettings']) => { unwrap(await api.PUT('/api/v1/me/notification-settings', { body })); setDraft(null); unsaved.markSaved(); await reload(); });
  const test = useAction(async (id: string) => { unwrap(await api.POST('/api/v1/me/notification-targets/{targetID}/test', { params: { path: { targetID: id } } })); await reload(); });
  const remove = useAction(async (id: string) => { unwrap(await api.DELETE('/api/v1/me/notification-targets/{targetID}', { params: { path: { targetID: id } } })); await reload(); });
  const change = (next: Schema['NotificationSettings']) => { setDraft(next); unsaved.markDirty(); save.reset(); };
  if (methods.data && !methods.data.recovery) return <SettingsSection title={t('Notifications')}><p className="subtle">{t('Notifications are not set up on this server. Ask your administrator to enable them.')}</p></SettingsSection>;
  const add = <button className="button" onClick={() => setEditing('new')}><Plus aria-hidden />{t('Add destination')}</button>;
  return <>
    <SettingsSection title={t('Destinations')} description={t('Release notices go to every enabled destination. Addresses and tokens are encrypted on the server.')} action={targets.data?.length ? add : undefined}>
      <ErrorNote error={test.error || remove.error || targets.error} />
      {targets.data?.length ? <ul className="record-list">{targets.data.map(item => <li key={item.id} className="record-row"><div><h3>{item.name} <span className="subtle">· {destinationKinds.find(k => k[0] === item.kind)?.[1]}</span></h3><p>{[item.description, item.last_status ? statusText(item.last_status) : '', item.enabled ? '' : t('Paused')].filter(Boolean).join(' · ')}</p></div>
        <div className="actions"><button className="button small" disabled={test.pending} onClick={() => void test.run(item.id)}><Send aria-hidden />{t('Test')}</button>
          <button className="icon-button" aria-label={t('Edit {name}', { name: item.name })} onClick={() => setEditing(item)}><Pencil aria-hidden /></button>
          <button className="icon-button" aria-label={t('Remove {name}', { name: item.name })} onClick={async () => { if (await confirm({ title: t('Remove {name}?', { name: item.name }), description: t('Notices stop going there.'), label: t('Remove'), danger: true })) void remove.run(item.id); }}><Trash2 aria-hidden /></button></div></li>)}</ul>
        : targets.isPending ? <p className="subtle">{t('Loading')}</p> : <Empty compact title={t('No destinations yet')} action={add} />}
    </SettingsSection>
    {value && <SettingsSection title={t('When to notify')} description={t('Mute a single title from its page. Each release is announced once, even if several of your titles point at it.')}>
      <form onSubmit={submit(() => void save.run(value))}>
        <label className="check-row"><input type="checkbox" checked={value.releases} onChange={e => change({ ...value, releases: e.target.checked })} /><span><strong>{t('New releases')}</strong><span className="subtle">{t('Episodes, films, issues and podcast episodes of titles you follow.')}</span></span></label>
        <div className="form-grid">
          <Field label={t('Send')}>{id => <select id={id} value={value.days_ahead} onChange={e => change({ ...value, days_ahead: Number(e.target.value) })}><option value={0}>{t('On release day')}</option><option value={1}>{t('A day before')}</option><option value={3}>{t('Three days before')}</option><option value={7}>{t('A week before')}</option></select>}</Field>
          <Field label={t('Grouping')}>{id => <select id={id} value={value.digest ? 'digest' : 'each'} onChange={e => change({ ...value, digest: e.target.value === 'digest' })}><option value="digest">{t('One summary per day')}</option><option value="each">{t('One notice per release')}</option></select>}</Field>
        </div>
        <fieldset className="rule-editor"><legend>{t('For titles that are')}</legend><div className="chip-filter">{(['planned', 'in_progress', 'paused', 'completed', 'dropped'] as const).map(st => <button key={st} type="button" aria-pressed={value.statuses.includes(st)} onClick={() => change({ ...value, statuses: value.statuses.includes(st) ? value.statuses.filter(x => x !== st) : [...value.statuses, st] })}>{statusText(st)}</button>)}</div></fieldset>
        <ErrorNote error={save.error} />
        <PanelActions dirty={!!draft} pending={save.pending} saved={save.saved} onDiscard={() => { setDraft(null); unsaved.markSaved(); }} />
      </form>
    </SettingsSection>}
    {editing && <DestinationEditor target={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
  </>;
}

function DestinationEditor({ target, onClose }: { target?: Schema['NotificationTarget']; onClose: () => void }) {
  const client = useQueryClient(); const user = useUser();
  const [kind, setKind] = useState<Schema['NotificationTarget']['kind']>(target?.kind ?? 'ntfy'); const [name, setName] = useState(target?.name ?? '');
  const [config, setConfig] = useState<Schema['NotificationConfig']>({}); const [enabled, setEnabled] = useState(target?.enabled ?? true);
  const save = useAction(async (_: void) => {
    const body = { kind, name, enabled, config };
    if (target) unwrap(await api.PUT('/api/v1/me/notification-targets/{targetID}', { params: { path: { targetID: target.id } }, body }));
    else unwrap(await api.POST('/api/v1/me/notification-targets', { body }));
    await client.invalidateQueries({ queryKey: privateKey(user.id, 'notify-targets') }); onClose();
  });
  const field = (k: keyof Schema['NotificationConfig'], label: string, hint?: string, secret = false) => <Field label={label} hint={hint}>{(id, h) => <input id={id} aria-describedby={h} type={secret ? 'password' : 'text'} autoComplete="off" value={config[k] ?? ''} onChange={e => setConfig({ ...config, [k]: e.target.value })} />}</Field>;
  return <Modal title={target ? t('Edit {name}', { name: target.name }) : t('Add a destination')} description={target ? t('Enter the destination details again; they are never shown back.') : t('Where should release notices go?')} onClose={onClose} busy={save.pending} wide dirty={name !== (target?.name ?? '') || Object.values(config).some(Boolean)}>
    <form onSubmit={submit(() => void save.run())}>
      {!target && <div className="kind-cards" role="radiogroup" aria-label={t('Destination type')}>{destinationKinds.map(([v, l, d]) => <button key={v} type="button" role="radio" aria-checked={kind === v} onClick={() => setKind(v)}><strong>{l}</strong><span className="subtle">{d}</span></button>)}</div>}
      <Field label={t('Name')}>{id => <input id={id} required maxLength={80} placeholder={t('My phone')} value={name} onChange={e => setName(e.target.value)} />}</Field>
      {kind === 'ntfy' && <>{field('url', t('Server'), t('Leave empty for ntfy.sh.'))}{field('topic', t('Topic'))}{field('token', t('Access token'), t('Only for protected topics.'), true)}</>}
      {kind === 'discord' && field('url', t('Webhook address'), t('Channel settings → Integrations → Webhooks.'))}
      {kind === 'gotify' && <>{field('url', t('Server'))}{field('token', t('Application token'), undefined, true)}</>}
      {kind === 'apprise' && field('url', t('Apprise API notify address'), t('For example https://apprise.example/notify/pastime'))}
      {kind === 'webhook' && <>{field('url', t('Endpoint'))}{field('secret', t('Signing secret'), t('Optional. Requests carry X-Pastime-Signature: sha256=<HMAC of the body>.'), true)}</>}
      <label className="check-row"><input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} /><span><strong>{t('Enabled')}</strong></span></label>
      <ErrorNote error={save.error} />
      <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{t('Save destination')}</button></div>
    </form>
  </Modal>;
}
