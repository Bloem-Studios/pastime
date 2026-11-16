/** The item page, /library/:entryID: your record of one title. */
import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { ChevronDown, ChevronRight, ListPlus, Lock, Pencil, Plus, Trash2 } from 'lucide-react';
import { api, keyHeader, unwrap, type Entry, type Schema } from '../api/client';
import { privateKey, useRefresh, useUnsaved, useUser } from '../state';
import { mediaTypes, typeLabel, unitLabel } from '../media';
import { CopiesSection, TagPicker } from '../organize';
import { ErrorNote, Failed, Field, Loading, Modal, submit, useAction, useConfirm } from '../ui';
import { ContextBackLink, ReturnLink } from '../navigation';
import { StatusHistory, sourceName } from '../ratingEvidence';
import { EntryMenu } from './entryTools';
import { dayText, episodeCount, finishedVerb, hierarchical, latestEntry, sentence, TitleHead, TrackingPanel, useEpisodes, type EpisodeIndex, type Item } from './tracking';
import { clock, clockRange, duration, episodeCode, episodeRange, localDay } from '../format';
import { locale, plural, t } from '../i18n';

export function MediaDetail() {
  const { entryID = '' } = useParams(); const user = useUser();
  const query = useQuery({ queryKey: privateKey(user.id, 'entry', entryID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}', { signal, params: { path: { entryID } } })) });
  if (query.isPending) return <Loading label={t('Loading media detail')} />;
  if (query.error) return <><ContextBackLink fallback="/library" label={t('Library')} /><Failed error={query.error} retry={() => void query.refetch()} /></>;
  return <ItemContent key={entryID} entry={query.data} />;
}

function ItemContent({ entry }: { entry: Entry }) {
  const user = useUser();
  const [edit, setEdit] = useState(false); const [lists, setLists] = useState(false);
  const catalog = useQuery({ queryKey: privateKey(user.id, 'catalog', entry.catalog_id ?? ''), enabled: !!entry.catalog_id,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/catalog/{catalogID}', { signal, params: { path: { catalogID: entry.catalog_id! } } })) });
  const item = catalog.data?.item ?? null;
  const index = useEpisodes(item && item.kind === 'series' ? item : null);
  const title = entry.localized_title ?? entry.title;
  return <div className="item-page">
    <ContextBackLink fallback="/library" label={t('Library')} />
    <TitleHead title={title} original={entry.localized_title && entry.localized_title !== entry.title ? entry.title : null}
      cover={entry} backdrop={entry.backdrop_url || undefined}
      facts={[typeLabel(entry.media_type), entry.year, item?.kind === 'movie' && item.runtime_minutes ? duration(item.runtime_minutes) : null]}
      links={entry.catalog_id ? <ReturnLink className="text-link" to={`/title/${entry.catalog_id}`}>{hierarchical.has(entry.media_type) ? t('Title details and episodes') : t('Title details')}<ChevronRight size={16} aria-hidden /></ReturnLink> : null}
      actions={<><button className="button small" onClick={() => setLists(true)}><ListPlus aria-hidden />{t('Add to list')}</button><button className="button small" onClick={() => setEdit(true)}><Pencil aria-hidden />{t('Edit details')}</button><EntryMenu entry={entry} /></>} />
    <div className="item-layout">
      <div className="item-main">
        <TrackingPanel entry={entry} item={item} index={index} />
        <PlaysSection entry={entry} index={index} />
        <section className="item-section" aria-labelledby="notes-heading"><div className="item-section-head"><h2 id="notes-heading"><Lock size={16} aria-hidden />{t('Private notes')}</h2></div><Notes entry={entry} /></section>
        <StatusHistory entryID={entry.id} />
      </div>
      <aside className="item-rail" aria-label={t('About this title')}>
        <ItemFacts entry={entry} item={item} index={index} />
        <ItemLists entry={entry} />
        <section className="rail-block" aria-labelledby="tags-heading"><h2 id="tags-heading">{t('Tags')}</h2><TagPicker entryID={entry.id} /></section>
        <div className="rail-block"><CopiesSection entryID={entry.id} seasons={index?.seasons.map(s => ({ id: s.id, title: s.title }))} /></div>
      </aside>
    </div>
    <DangerSection entry={entry} />
    {edit && <EditDialog entry={entry} onClose={() => setEdit(false)} />}
    {lists && <ListPicker entry={entry} onClose={() => setLists(false)} />}
  </div>;
}

/** The record's dates and numbers, in a quiet two-column list. */
function ItemFacts({ entry, item, index }: { entry: Entry; item: Item | null; index: EpisodeIndex | null }) {
  const episodes = episodeCount(index);
  const rows: [string, string][] = [
    [t('Type'), typeLabel(entry.media_type)],
    ...(entry.year ? [[t('Year'), String(entry.year)] as [string, string]] : []),
    ...(item?.release_status ? [[t('Production'), sentence(item.release_status)] as [string, string]] : []),
    ...(episodes ? [[t('Episodes'), episodes] as [string, string]] : entry.total != null && entry.unit !== 'minutes' ? [[sentence(unitLabel(entry.unit)), entry.total.toLocaleString(locale)] as [string, string]] : []),
    [t('Added'), dayText(entry.created_at, true)],
    ...(entry.started_at ? [[t('Started'), dayText(entry.started_at, true)] as [string, string]] : []),
    ...(entry.finished_at ? [[t('Finished'), dayText(entry.finished_at, true)] as [string, string]] : []),
    ...(entry.completed_cycles > 1 ? [[t('Times completed'), entry.completed_cycles.toLocaleString(locale)] as [string, string]] : []),
  ];
  return <section className="rail-block" aria-labelledby="facts-heading"><h2 id="facts-heading">{t('Details')}</h2>
    <dl className="fact-list">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
  </section>;
}

/** The lists this title is on: manual lists holding it and smart lists that select it. */
function ItemLists({ entry }: { entry: Entry }) {
  const user = useUser();
  const lists = useQuery({ queryKey: privateKey(user.id, 'lists', 'entry', entry.id),
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/lists', { signal, params: { path: { entryID: entry.id } } })).items });
  return <section className="rail-block" aria-labelledby="lists-heading"><h2 id="lists-heading">{t('Lists')}</h2>
    {lists.isPending ? <div className="skeleton skeleton-line" aria-busy="true" />
      : lists.error ? <Failed error={lists.error} retry={() => void lists.refetch()} />
      : !lists.data.length ? <p className="subtle item-rail-note">{t('Not on any list yet.')}</p>
      : <ul className="item-lists">{lists.data.map(list => <li key={list.id}>
          <ReturnLink to={`/lists/${list.id}`}>{list.name}</ReturnLink>
          {list.kind === 'smart' && <span className="subtle">{t('Smart list')}</span>}
        </li>)}</ul>}
  </section>;
}

// ---------------------------------------------------------------- plays

type Line = { key: string; at: string; minutes: number | null; marked: boolean; playID?: string; code: string; name: string; season: number | null; episode: number | null; session: boolean; amount?: number; source: string; details: Record<string, unknown>; again: boolean };
type Group = { key: string; kind: 'one' | 'run' | 'marked'; lines: Line[] };

/**
 * The plays of this title, newest first, in History's pattern: plays of one show on one day form a
 * run ("S1 E1–E4"), a bulk "mark as played" is one row that counts no viewing time.
 */
function PlaysSection({ entry, index }: { entry: Entry; index: EpisodeIndex | null }) {
  const user = useUser(); const refresh = useRefresh(); const confirm = useConfirm();
  const [all, setAll] = useState(false);
  const history = useQuery({ queryKey: privateKey(user.id, 'plays', 'history', entry.id),
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library/{entryID}/play-history', { signal, params: { path: { entryID: entry.id } } })) });
  const groups = useMemo(() => {
    const items = history.data?.items ?? [];
    const singles = items.filter(h => h.kind === 'play' && !h.item_title).length;
    let seenSingles = 0;
    const lines: Line[] = items.map((h, i) => {
      if (h.kind === 'play' && !h.item_title) seenSingles++;
      return { key: h.play_id ?? `${h.kind}-${h.at}-${i}`, at: h.at, minutes: h.minutes ?? index?.byID.get(h.catalog_id)?.runtime_minutes ?? null, marked: h.marked, playID: h.play_id ?? undefined,
        code: episodeCode(h.season_number, h.episode_number), name: h.item_title ?? '', season: h.season_number, episode: h.episode_number,
        session: h.kind === 'session', amount: h.amount, source: h.source, details: h.details, again: h.kind === 'play' && !h.item_title && seenSingles < singles };
    });
    const out: Group[] = [];
    for (const line of lines) {
      const last = out[out.length - 1]; const head = last?.lines[0];
      const joins = last && head && localDay(head.at) === localDay(line.at) && !line.session && !head.session && head.marked === line.marked && (line.marked || (!!line.name && !!head.name));
      if (joins) { last.lines.push(line); if (!line.marked) last.kind = 'run'; }
      else out.push({ key: line.key, kind: line.marked ? 'marked' : 'one', lines: [line] });
    }
    return out;
  }, [history.data, index]);
  const remove = useAction(async (ids: string[]) => {
    for (const playID of ids) {
      const current = await latestEntry(entry.id);
      unwrap(await api.DELETE('/api/v1/library/{entryID}/plays/{playID}', { params: { path: { entryID: entry.id, playID }, header: keyHeader(crypto.randomUUID()) }, body: { version: current.version } }));
    }
    await refresh();
  });
  const verb = finishedVerb(entry.media_type);
  const what = (l: Line) => l.session ? [t('Session'), l.amount ? `${l.amount.toLocaleString(locale)} ${unitLabel(entry.unit)}` : ''].filter(Boolean).join(' · ')
    : l.name ? [l.code, l.name].filter(Boolean).join(' · ')
    : verb === 'watched' ? (l.again ? t('Watched again') : t('Watched')) : verb === 'read' ? (l.again ? t('Read again') : t('Read')) : (l.again ? t('Played again') : t('Finished'));
  const deletePlay = async (l: Line) => {
    if (!l.playID) return;
    if (await confirm({ title: t('Delete this play?'), description: t('{what}, {when}. Progress for this title is recalculated.', { what: what(l), when: `${dayText(l.at, true)} ${clock(l.at)}` }), label: t('Delete play'), danger: true })) void remove.run([l.playID]);
  };
  const removeMark = async (g: Group) => {
    const ids = g.lines.flatMap(l => l.playID ? [l.playID] : []);
    if (ids.length && await confirm({ title: t('Remove this mark?'), description: t('{count} episodes were marked watched at once on {when}. Their plays are deleted and progress is recalculated.', { count: g.lines.length, when: dayText(g.lines[0].at, true) }), label: t('Remove mark'), danger: true })) void remove.run(ids);
  };
  const h = history.data;
  const totals = h && h.items.length ? [h.plays || !h.sessions ? plural(h.plays, '{n} play', '{n} plays') : '', h.sessions ? plural(h.sessions, '{n} session', '{n} sessions') : '', h.minutes ? duration(h.minutes) : ''].filter(Boolean).join(' · ') : '';
  const shown = all ? groups : groups.slice(0, 6);
  return <section className="item-section" aria-labelledby="plays-heading">
    <div className="item-section-head"><h2 id="plays-heading">{t('Plays')}</h2>{totals && <span className="num">{totals}</span>}</div>
    <ErrorNote error={remove.error} />
    {history.isPending ? <div className="plays-loading" aria-busy="true"><div className="skeleton skeleton-line" /><div className="skeleton skeleton-line" /></div>
      : history.error ? <Failed error={history.error} retry={() => void history.refetch()} />
      : !groups.length ? <p className="item-empty">{entry.catalog_id ? (hierarchical.has(entry.media_type) || entry.media_type === 'anime' ? t('No plays yet. Mark an episode watched and it appears here with the day and time.') : t('No plays yet. When you mark it watched, the day and time appear here.')) : t('No plays yet. Plays are recorded for titles matched to the catalog.')}</p>
      : <ul className="plays-list">{shown.map(g => <PlayRow key={g.key} group={g} entry={entry} what={what} busy={remove.pending} onDelete={deletePlay} onRemoveMark={removeMark} />)}</ul>}
    {groups.length > 6 && <button className="text-button" onClick={() => setAll(!all)}>{all ? t('Show fewer') : plural(groups.length, 'Show all {n} rows', 'Show all {n} rows')}</button>}
  </section>;
}

function PlayRow({ group, entry, what, busy, onDelete, onRemoveMark }: { group: Group; entry: Entry; what: (l: Line) => string; busy: boolean; onDelete: (l: Line) => void; onRemoveMark: (g: Group) => void }) {
  const [open, setOpen] = useState(false);
  const newest = group.lines[0], oldest = group.lines[group.lines.length - 1];
  const many = group.lines.length > 1;
  const minutes = group.lines.reduce((n, l) => n + (l.minutes ?? 0), 0);
  const ordered = [...group.lines].sort((a, b) => (a.season ?? 0) - (b.season ?? 0) || (a.episode ?? 0) - (b.episode ?? 0) || Date.parse(a.at) - Date.parse(b.at));
  const first = ordered[0], last = ordered[ordered.length - 1];
  const range = episodeRange({ season_number: first.season, episode_number: first.episode }, { season_number: last.season, episode_number: last.episode });
  const extra = (l: Line) => [typeof l.details.platform === 'string' ? l.details.platform : '', typeof l.details.location === 'string' ? l.details.location : '',
    Array.isArray(l.details.players) ? (l.details.players as { name?: string; score?: number; winner?: boolean }[]).map(p => `${p.name ?? ''}${typeof p.score === 'number' ? ` ${p.score.toLocaleString(locale)}` : ''}${p.winner ? ` (${t('won')})` : ''}`).join(', ') : ''];
  const source = (l: Line) => l.source === 'manual' || l.source === '' ? '' : l.source === 'checkin' ? t('Check-in') : l.source.startsWith('import:') ? t('Imported from {name}', { name: sourceName(l.source.slice(7)) }) : t('From {name}', { name: sourceName(l.source) });
  let title: string; let detail: string; let time: string;
  if (group.kind === 'marked') {
    title = [t('Marked watched'), range].filter(Boolean).join(' · ');
    detail = [plural(group.lines.length, '{n} episode', '{n} episodes'), t('counted toward progress, not viewing time')].join(' · ');
    time = clock(newest.at);
  } else if (group.kind === 'run') {
    title = [range, plural(group.lines.length, '{n} episode', '{n} episodes')].filter(Boolean).join(' · ');
    detail = [minutes ? duration(minutes) : '', source(newest)].filter(Boolean).join(' · ');
    time = clockRange(oldest.at, newest.at);
  } else {
    title = what(newest);
    detail = [newest.minutes ? duration(newest.minutes) : '', ...extra(newest), source(newest)].filter(Boolean).join(' · ');
    time = clock(newest.at);
  }
  const label = title;
  return <li className={`plays-row ${group.kind}`}>
    <div className="plays-main"><p className="plays-what">{title}</p>{detail && <p className="plays-detail">{detail}</p>}</div>
    <p className="plays-when num"><time dateTime={newest.at}>{dayText(newest.at)}</time><span>{time}</span></p>
    <div className="plays-action">{many
      ? <button className="icon-button" aria-expanded={open} aria-label={open ? t('Hide the plays in {row}', { row: label }) : t('Show the plays in {row}', { row: label })} onClick={() => setOpen(!open)}><ChevronDown aria-hidden className={open ? 'flip' : ''} /></button>
      : group.kind === 'marked' ? <button className="icon-button" aria-label={t('Remove the mark {row}', { row: label })} disabled={busy || !newest.playID} onClick={() => onRemoveMark(group)}><Trash2 aria-hidden /></button>
      : newest.playID ? <button className="icon-button" aria-label={t('Delete the play {row}', { row: label })} disabled={busy} onClick={() => onDelete(newest)}><Trash2 aria-hidden /></button> : null}
    </div>
    {open && many && <div className="plays-episodes">
      {group.kind === 'marked' && <p className="plays-mark-note">{t('These were marked watched at once, so they count toward progress but not toward time or plays.')} <button className="text-button danger" disabled={busy} onClick={() => onRemoveMark(group)}>{t('Remove the whole mark')}</button></p>}
      <ol>{ordered.map(l => <li key={l.key}>
        <span>{l.name ? [l.code, l.name].filter(Boolean).join(' · ') : what(l)}</span>
        <span className="num">{l.minutes && group.kind !== 'marked' ? duration(l.minutes) : ''}</span>
        <time className="num" dateTime={l.at}>{clock(l.at)}</time>
        {l.playID ? <button className="icon-button" aria-label={t('Delete the play {row}', { row: [l.code, l.name].filter(Boolean).join(' ') || entry.title })} disabled={busy} onClick={() => onDelete(l)}><Trash2 aria-hidden /></button> : <span />}
      </li>)}</ol>
    </div>}
  </li>;
}

// ---------------------------------------------------------------- notes, removal, dialogs

function Notes({ entry }: { entry: Entry }) {
  const [baseline, setBaseline] = useState(entry); const [notes, setNotes] = useState(entry.notes); const [dirty, setDirty] = useState(false); const unsaved = useUnsaved(); const refresh = useRefresh(); const confirm = useConfirm();
  useEffect(() => { if (entry.version !== baseline.version && (entry.notes === baseline.notes || !dirty)) { setBaseline(entry); if (!dirty) setNotes(entry.notes); } }, [entry, baseline, dirty]);
  const action = useAction(async (value: string, key) => {
    const current = await latestEntry(entry.id);
    if (current.notes !== baseline.notes) { setBaseline(current); throw new Error(t('These notes changed elsewhere. Reload them before saving.')); }
    const body: Schema['EntryInput'] = { title: current.title, media_type: current.media_type, year: current.year, unit: current.unit, total: current.total, status: current.status, progress: current.progress, rating: current.rating, notes: value, version: current.version };
    unwrap(await api.PUT('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body }));
    unsaved.markSaved(); setDirty(false); await refresh();
  });
  const changed = baseline.notes !== entry.notes && dirty;
  return <form className="notes-form item-notes" onSubmit={submit(() => void action.run(notes))}>
    <div className="field"><textarea name="notes" aria-labelledby="notes-heading" aria-describedby="notes-hint" rows={4} maxLength={10000} value={notes} disabled={action.pending} placeholder={t('What you thought, where you stopped, who recommended it…')} onChange={e => { setNotes(e.target.value); setDirty(true); unsaved.markDirty(); action.reset(); }} /><p className="field-hint" id="notes-hint">{t('Only you can see these notes. Up to 10,000 characters.')}</p></div>
    {changed && <p className="notice">{t('Notes changed elsewhere.')} <button className="text-button" type="button" onClick={async () => { if (await confirm({ title: t('Reload notes?'), description: t('Replace your unsaved notes with the latest saved version.'), label: t('Reload notes') })) { setBaseline(entry); setNotes(entry.notes); setDirty(false); unsaved.markSaved(); } }}>{t('Reload notes')}</button></p>}
    <ErrorNote error={action.error} />
    <div className="actions"><button className="button small" disabled={action.pending || !dirty} type="submit">{action.pending ? t('Saving…') : t('Save notes')}</button>{action.saved && !dirty && <span role="status" className="save-confirmation">{t('Notes saved.')}</span>}</div>
  </form>;
}

function DangerSection({ entry }: { entry: Entry }) {
  const refresh = useRefresh(); const navigate = useNavigate(); const confirm = useConfirm();
  const silo = entry.media_type === 'movie' || entry.media_type === 'series';
  const remove = useAction(async (_: void, key) => { const current = await latestEntry(entry.id); unwrap(await api.DELETE('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { version: current.version } })); await refresh(); navigate('/library', { replace: true }); });
  return <section className="item-section danger-zone" aria-labelledby="remove-heading">
    <h2 id="remove-heading">{t('Remove from library')}</h2>
    <p>{t('This permanently deletes this item, its notes, plays and list memberships.')}{silo && <> {t('If this profile is connected to Silo, Silo removes it from this profile’s watch history too.')}</>}</p>
    <ErrorNote error={remove.error} />
    <button className="button small danger-outline" disabled={remove.pending} onClick={async () => { if (await confirm({ title: t('Delete “{title}”?', { title: entry.title }), description: [t('Its notes, plays and list memberships will be permanently removed.'), ...(silo ? [t('If this profile is connected to Silo, Silo removes it from this profile’s watch history too.')] : [])].join(' '), label: t('Delete item'), danger: true })) void remove.run(); }}><Trash2 aria-hidden />{remove.pending ? t('Deleting…') : t('Delete item')}</button>
  </section>;
}

/** Title, type, year and total. Status, progress, rating and notes are edited on the page itself. */
function EditDialog({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const refresh = useRefresh(); const unsaved = useUnsaved();
  const [draft, setDraft] = useState({ title: entry.title, media_type: entry.media_type, year: entry.year, unit: entry.unit, total: entry.total });
  const [dirty, setDirty] = useState(false);
  const set = (next: Partial<typeof draft>) => { setDraft(d => ({ ...d, ...next })); setDirty(true); unsaved.markDirty(); save.reset(); };
  const save = useAction(async (value: typeof draft, key) => {
    const c = await latestEntry(entry.id);
    unwrap(await api.PUT('/api/v1/library/{entryID}', { params: { path: { entryID: entry.id }, header: keyHeader(key) }, body: { ...value, status: c.status, progress: c.progress, rating: c.rating, notes: c.notes, version: c.version } }));
    unsaved.markSaved(); setDirty(false); await refresh(); onClose();
  });
  return <Modal title={t('Edit details')} description={t('Correct how this title is named and counted. Progress, rating and notes are on the page.')} onClose={onClose} dirty={dirty} busy={save.pending}>
    <form className="item-dialog-form" onSubmit={submit(() => void save.run(draft))}><fieldset disabled={save.pending}>
      <Field label={t('Title')}>{id => <input id={id} required maxLength={200} value={draft.title} onChange={e => set({ title: e.target.value })} />}</Field>
      <div className="form-grid">
        <Field label={t('Media type')}>{id => <select id={id} value={draft.media_type} onChange={e => { const value = e.target.value as Entry['media_type']; set({ media_type: value, unit: mediaTypes.find(m => m[0] === value)![2] }); }}>{mediaTypes.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select>}</Field>
        <Field label={t('Release year')}>{id => <input id={id} type="number" inputMode="numeric" min={1} max={9999} value={draft.year ?? ''} onChange={e => set({ year: e.target.value === '' ? null : Number(e.target.value) })} />}</Field>
      </div>
      <Field label={t('Total {unit}', { unit: unitLabel(draft.unit) })} hint={t('Leave empty if unknown.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="number" inputMode="numeric" min={1} max={100000000} value={draft.total ?? ''} onChange={e => set({ total: e.target.value === '' ? null : Number(e.target.value) })} />}</Field>
    </fieldset>
    <ErrorNote error={save.error} />
    <div className="dialog-actions item-dialog-actions"><button className="button" type="button" disabled={save.pending} onClick={onClose}>{t('Cancel')}</button><button className="button primary" type="submit" disabled={save.pending || !dirty}>{save.pending ? t('Saving…') : t('Save changes')}</button></div>
    </form>
  </Modal>;
}

/** Adds the title to one of your manual lists, or to a new one named here. */
function ListPicker({ entry, onClose }: { entry: Entry; onClose: () => void }) {
  const user = useUser(); const refresh = useRefresh();
  const [selected, setSelected] = useState(''); const [creating, setCreating] = useState(false); const [name, setName] = useState('');
  const query = useInfiniteQuery({ queryKey: privateKey(user.id, 'list-picker'), initialPageParam: undefined as string | undefined, queryFn: async ({ pageParam, signal }) => unwrap(await api.GET('/api/v1/lists', { signal, params: { query: { cursor: pageParam, limit: 100 } } })), getNextPageParam: page => page.next_cursor ?? undefined });
  const options = (query.data?.pages.flatMap(page => page.items) ?? []).filter(l => l.kind === 'manual');
  const add = useAction(async (listID: string, key) => {
    const list = options.find(l => l.id === listID); if (!list) return;
    unwrap(await api.PUT('/api/v1/lists/{listID}/entries/{entryID}', { params: { path: { listID, entryID: entry.id }, header: keyHeader(key) }, body: { version: list.version, present: true } }));
    await refresh(); onClose();
  });
  const create = useAction(async (value: string, key) => {
    const list = unwrap(await api.POST('/api/v1/lists', { params: { header: keyHeader(key) }, body: { name: value, kind: 'manual', visibility: 'private' } }));
    unwrap(await api.PUT('/api/v1/lists/{listID}/entries/{entryID}', { params: { path: { listID: list.id, entryID: entry.id }, header: keyHeader(crypto.randomUUID()) }, body: { version: list.version, present: true } }));
    await refresh(); onClose();
  });
  const fresh = creating || (query.isSuccess && !options.length);
  const busy = add.pending || create.pending;
  return <Modal title={t('Add to list')} description={t('Lists are private unless you share them.')} onClose={onClose} busy={busy}>
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
      : fresh ? <form onSubmit={submit(() => void create.run(name.trim()))}>
        {!options.length && <p className="item-dialog-note">{t('You have no lists yet. Name one and “{title}” goes in it.', { title: entry.title })}</p>}
        <Field label={t('New list')}>{id => <input id={id} required maxLength={100} placeholder={t('For example: Rainy Sunday films')} value={name} onChange={e => { setName(e.target.value); create.reset(); }} />}</Field>
        <ErrorNote error={create.error} />
        <div className="dialog-actions">{options.length > 0 ? <button type="button" className="button" disabled={busy} onClick={() => setCreating(false)}>{t('Back')}</button> : <button type="button" className="button" disabled={busy} onClick={onClose}>{t('Cancel')}</button>}<button className="button primary" type="submit" disabled={busy || !name.trim()}><Plus aria-hidden />{create.pending ? t('Creating…') : t('Create and add')}</button></div>
      </form>
      : <form onSubmit={submit(() => void add.run(selected))}>
        <Field label={t('List')}>{id => <select id={id} value={selected} onChange={e => setSelected(e.target.value)} required><option value="">{t('Choose a list')}</option>{options.map(list => <option key={list.id} value={list.id}>{list.name}</option>)}</select>}</Field>
        {query.hasNextPage && <button type="button" className="text-button" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>{t('Load more lists')}</button>}
        <ErrorNote error={add.error} />
        <div className="dialog-actions item-dialog-split"><button type="button" className="text-button" onClick={() => setCreating(true)}><Plus size={16} aria-hidden />{t('New list')}</button><button type="button" className="button" disabled={busy} onClick={onClose}>{t('Cancel')}</button><button className="button primary" disabled={!selected || busy} type="submit">{add.pending ? t('Adding…') : t('Add to list')}</button></div>
      </form>}
  </Modal>;
}
