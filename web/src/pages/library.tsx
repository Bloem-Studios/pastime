import { SourceLink } from '../navigation';
import { SavedViewControls, savedViewURL, useSavedViews } from '../savedViews';
import * as Dropdown from '@radix-ui/react-dropdown-menu';
import { useEffect, useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowUpDown, Bookmark, Check, LayoutGrid, Plus, Rows3, Search, SlidersHorizontal, SquareCheck } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema, type Summary } from '../api/client';
import type { paths } from '../api/schema.gen';
import { privateKey, useRefresh, useUser } from '../state';
import { Cover, EntryStatus, entryPath, lastPlayedLabel, mediaTypes, meterValue, noValue, PosterCard, progressAmount, progressLabel, shortDate, statuses, typeLabel, unitLabel, useLibraryFacets, type FacetQuery } from '../media';
import { facetChoices, facetCounts, lastPlayed } from '../browsePolicy';
import { rating, usePreferences, useTags, type Preferences } from '../organize';
import { Empty, ErrorNote, Failed, Loading, Modal, PageTitle, Pager, submit, Timestamp, useAction, useConfirm, statusText } from '../ui';
import { useWorkspace } from '../layout';
import { NowPlaying } from '../checkins';
import { locale, plural, t } from '../i18n';

const librarySorts: [NonNullable<LibraryQuery['sort']>, string][] = [['added', t('Recently added')], ['played', t('Last played')], ['activity', t('Recent activity')], ['title', t('Title')], ['rating', t('Your rating')], ['year', t('Release year')]];
type LibraryQuery = NonNullable<paths['/api/v1/library']['get']['parameters']['query']>;
// "Rated only" is a way of keeping a title, not a stage of following it, so it lives in Filters.
const chipStatuses = statuses.filter(([value]) => value !== 'rated');

export function Library() {
  const user = useUser(); const [params, setParams] = useSearchParams(); const { addMedia } = useWorkspace();
  const { prefs, save } = usePreferences();
  const q = params.get('q') ?? ''; const [search, setSearch] = useState(q); useEffect(() => setSearch(q), [q]);
  const list = (key: string) => (params.get(key) ?? '').split(',').filter(Boolean);
  const view = (params.get('view') as 'grid' | 'list' | null) ?? prefs.library_view;
  // The chips count under the same filters; the cursor, page size and order do not change a count.
  const facetFilters: FacetQuery = { q: q || undefined, status: params.get('status') || undefined, media_type: params.get('media_type') || undefined, tag_id: params.get('tag_id') || undefined,
    genre: params.get('genre') || undefined, min_rating: params.has('min_rating') ? Number(params.get('min_rating')) : undefined, year_from: params.has('year_from') ? Number(params.get('year_from')) : undefined, year_to: params.has('year_to') ? Number(params.get('year_to')) : undefined, owned: params.get('owned') === null ? undefined : params.get('owned') === 'true' };
  const filters: LibraryQuery = { ...facetFilters, sort: (params.get('sort') || prefs.library_sort) as LibraryQuery['sort'], cursor: params.get('cursor') || undefined, limit: 30 };
  const query = useQuery({ queryKey: privateKey(user.id, 'library', filters), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: filters } })) });
  const facets = useLibraryFacets(facetFilters); const counts = facetCounts(facets.data);
  const [selecting, setSelecting] = useState(false); const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => { setSelected(new Set()); }, [params.toString()]);
  function change(key: string, value: string, replace = false) { setParams(previous => { const next = new URLSearchParams(previous); if (value) next.set(key, value); else next.delete(key); if (key !== 'cursor' && key !== 'view') next.delete('cursor'); return next; }, { replace }); }
  function toggle(key: string, value: string) { const current = list(key); change(key, (current.includes(value) ? current.filter(v => v !== value) : [...current, value]).join(',')); }
  // The search filters as you type; Enter applies at once.
  useEffect(() => { const value = search.trim(); if (value === q) return; const timer = window.setTimeout(() => change('q', value, true), 300); return () => window.clearTimeout(timer); }, [search]);
  const types = list('media_type'), chosen = list('status');
  const other = !!(q || filters.tag_id || filters.genre || filters.min_rating !== undefined || filters.year_from !== undefined || filters.year_to !== undefined || filters.owned !== undefined);
  const filtered = other || types.length > 0 || chosen.length > 0;
  const clear = () => setParams(params.get('view') ? { view: params.get('view')! } : {});
  // Until the first counts arrive only the chosen values show, so the chips never shrink from a full set.
  const typeChoices = facetChoices(mediaTypes.map(([v]) => v), counts.types, types);
  const statusChoices = facetChoices(chipStatuses.map(([v]) => v), counts.statuses, chosen);
  const counted = !!facets.data;
  const chip = (key: string, value: string, label: string, count?: number) => <button key={value || 'all'} aria-pressed={value ? list(key).includes(value) : list(key).length === 0} onClick={() => value ? toggle(key, value) : change(key, '')}>{label}{count !== undefined && <span className="chip-count">{count.toLocaleString(locale)}</span>}</button>;
  const pick = (id: string) => setSelected(s => { const n = new Set(s); if (n.has(id)) n.delete(id); else if (n.size < 200) n.add(id); return n; });
  const items = query.data?.items ?? [];
  const paged = !!(filters.cursor || query.data?.next_cursor);
  const shown = counted && !facets.isPlaceholderData ? plural(counts.total, '{n} title', '{n} titles') : paged ? plural(items.length, '{n} title on this page', '{n} titles on this page') : plural(items.length, '{n} title', '{n} titles');
  return <>
    <PageTitle title={t('Library')} />
    <NowPlaying />
    <div className="library-toolbar">
      <form className="search-form library-search" role="search" onSubmit={submit(() => change('q', search.trim()))}><Search size={18} aria-hidden /><input name="q" type="search" aria-label={t('Search your library')} maxLength={100} placeholder={t('Search your library')} value={search} onChange={e => setSearch(e.target.value)} /></form>
      <label className="select-pill library-sort"><span className="sr-only">{t('Sort by')}</span><ArrowUpDown size={16} aria-hidden /><select name="sort" value={filters.sort} onChange={e => change('sort', e.target.value)}>{librarySorts.filter(([v]) => v !== 'rating' || prefs.rating_scale !== 'off').map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></label>
      <FilterMenu filters={filters} chosen={chosen} rated={counts.statuses.get('rated') ?? 0} change={change} toggle={toggle} list={list} />
      <SavedViewSelect params={params} sort={filters.sort ?? 'added'} layout={view} />
      <div className="segmented" role="group" aria-label={t('View')}>
        <button aria-pressed={view === 'grid'} aria-label={t('Grid view')} onClick={() => { change('view', 'grid'); if (prefs.version && prefs.library_view !== 'grid') void save.run({ ...prefs, library_view: 'grid' }); }}><LayoutGrid aria-hidden /></button>
        <button aria-pressed={view === 'list'} aria-label={t('List view')} onClick={() => { change('view', 'list'); if (prefs.version && prefs.library_view !== 'list') void save.run({ ...prefs, library_view: 'list' }); }}><Rows3 aria-hidden /></button>
      </div>
    </div>
    <div className="library-chips">
      <div className="chip-filter" role="group" aria-label={t('Media type')}>{chip('media_type', '', t('All media'), counted ? counts.anyType : undefined)}{typeChoices.map(({ value, count }) => chip('media_type', value, typeLabel(value), counted ? count : undefined))}</div>
      <div className="chip-filter" role="group" aria-label={t('Status')}>{chip('status', '', t('Any status'))}{statusChoices.map(({ value, count }) => chip('status', value, statusText(value), counted ? count : undefined))}</div>
      {filtered && <button className="text-button library-clear" onClick={clear}>{t('Clear filters')}</button>}
    </div>
    {query.isPending ? <Loading label={t('Loading media')} /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : items.length
      ? <><div className="results-head"><p className="results-note" role="status">{shown}{selecting && t(' · {size} selected', { size: selected.size })}</p>
          <button className="button ghost small" aria-pressed={selecting} onClick={() => { setSelecting(!selecting); setSelected(new Set()); }}><SquareCheck aria-hidden />{selecting ? t('Done') : t('Select')}</button></div>
        {selecting && <p className="results-note">{t('Select up to 200 titles on this page. Changing the page or filters clears the selection.')}</p>}
        {view === 'list' ? <LibraryTable items={items} selecting={selecting} selected={selected} pick={pick} scale={prefs.rating_scale} /> :
          <div className={`media-grid ${selecting ? 'selecting' : ''}`}>{items.map(entry => { const when = filters.sort === 'played' ? lastPlayed(entry) : null; return <div key={entry.id} className="selectable">{selecting && <input type="checkbox" className="select-box" aria-label={t('Select {title}', { title: entry.title })} checked={selected.has(entry.id)} onChange={() => pick(entry.id)} />}<PosterCard entry={entry} meta={when ? lastPlayedLabel(entry.media_type, when) : undefined} /></div>; })}</div>}
        <Pager current={filters.cursor} next={query.data.next_cursor} change={value => change('cursor', value)} /></>
      : filtered ? <Empty title={q ? t('No titles match “{q}”.', { q }) : t('No titles match these filters.')}>{q ? t('Check the spelling or search for fewer words.') : t('Choose another media type or status.')}</Empty>
      : <Empty title={t('Make room for your next favorite')} action={<button className="button primary" onClick={addMedia}><Plus aria-hidden />{t('Add media')}</button>}>{t('Add a title, then keep its progress and your thoughts in one place.')}</Empty>}
    {selecting && selected.size > 0 && <BulkBar entries={items.filter(item => selected.has(item.id))} onDone={() => setSelected(new Set())} />}
  </>;
}

/** Filters that are not a media type or a stage: owned copies, rated-only titles and tags. */
function FilterMenu({ filters, chosen, rated, change, toggle, list }: { filters: LibraryQuery; chosen: string[]; rated: number; change: (key: string, value: string) => void; toggle: (key: string, value: string) => void; list: (key: string) => string[] }) {
  const tags = useTags();
  const tagIDs = list('tag_id');
  const active = (filters.owned !== undefined ? 1 : 0) + (chosen.includes('rated') ? 1 : 0) + tagIDs.length;
  const keep = (event: Event) => event.preventDefault();
  return <Dropdown.Root>
    <Dropdown.Trigger className="button library-filters" aria-label={active ? plural(active, 'Filters, {n} on', 'Filters, {n} on') : t('Filters')}><SlidersHorizontal aria-hidden /><span className="library-control-label">{t('Filters')}</span>{active > 0 && <span className="chip-count">{active}</span>}</Dropdown.Trigger>
    <Dropdown.Portal><Dropdown.Content className="dropdown-menu library-filter-menu" sideOffset={8} align="end">
      <Dropdown.CheckboxItem checked={filters.owned === true} onSelect={keep} onCheckedChange={on => change('owned', on ? 'true' : '')}><Dropdown.ItemIndicator className="menu-check"><Check aria-hidden /></Dropdown.ItemIndicator>{t('Owned copies')}</Dropdown.CheckboxItem>
      {filters.owned === false && <Dropdown.CheckboxItem checked onSelect={keep} onCheckedChange={() => change('owned', '')}><Dropdown.ItemIndicator className="menu-check"><Check aria-hidden /></Dropdown.ItemIndicator>{t('No owned copy')}</Dropdown.CheckboxItem>}
      {(rated > 0 || chosen.includes('rated')) && <Dropdown.CheckboxItem checked={chosen.includes('rated')} onSelect={keep} onCheckedChange={() => toggle('status', 'rated')}><Dropdown.ItemIndicator className="menu-check"><Check aria-hidden /></Dropdown.ItemIndicator>{t('Rated only')}<span className="chip-count">{rated.toLocaleString(locale)}</span></Dropdown.CheckboxItem>}
      {(tags.data?.length ?? 0) > 0 && <><Dropdown.Separator className="menu-separator" /><Dropdown.Label className="menu-label">{t('Tags')}</Dropdown.Label>
        {tags.data!.map(tag => <Dropdown.CheckboxItem key={tag.id} checked={tagIDs.includes(tag.id)} onSelect={keep} onCheckedChange={() => toggle('tag_id', tag.id)}><Dropdown.ItemIndicator className="menu-check"><Check aria-hidden /></Dropdown.ItemIndicator><span className="tag-dot" style={tag.color ? { background: tag.color } : undefined} aria-hidden />{tag.name}</Dropdown.CheckboxItem>)}</>}
    </Dropdown.Content></Dropdown.Portal>
  </Dropdown.Root>;
}


/** Saved views as one select: choosing a view opens it; the last option saves or edits them. */
function SavedViewSelect({ params, sort, layout }: { params: URLSearchParams; sort: NonNullable<LibraryQuery['sort']>; layout: Schema['SavedView']['layout'] }) {
  const views = useSavedViews(); const navigate = useNavigate(); const [managing, setManaging] = useState(false);
  const normal = (search: string) => { const p = new URLSearchParams(search); p.delete('cursor'); p.sort(); return p.toString(); };
  const here = normal(params.toString());
  const current = views.data?.find(v => normal(savedViewURL(v).split('?')[1] ?? '') === here);
  return <><label className="select-pill library-saved"><Bookmark size={16} aria-hidden /><span className="sr-only">{t('Saved views')}</span>
    <select name="saved_view" value={current?.id ?? ''} onChange={e => { const value = e.target.value; if (value === 'manage') setManaging(true); else { const v = views.data?.find(x => x.id === value); if (v) navigate(savedViewURL(v)); } }}>
      <option value="" disabled={!!current}>{t('Saved views')}</option>
      {views.data?.map(v => <option key={v.id} value={v.id}>{v.name}</option>)}
      <option value="manage">{views.data?.length ? t('Save or edit views…') : t('Save this view…')}</option>
    </select></label>
    {managing && <Modal title={t('Saved views')} description={t('A saved view keeps these filters, the sort and the layout under a name.')} onClose={() => setManaging(false)}><SavedViewControls params={params} sort={sort === 'position' ? null : sort} layout={layout} /></Modal>}</>;
}

function LibraryTable({ items, selecting, selected, pick, scale }: { items: Summary[]; selecting: boolean; selected: Set<string>; pick: (id: string) => void; scale: Preferences['rating_scale'] }) {
  return <div className="library-list"><table className="data-table library-table">
    <caption className="sr-only">{t('Library titles')}</caption>
    <thead><tr>{selecting && <th scope="col" className="lib-select"><span className="sr-only">{t('Selected')}</span></th>}<th scope="col">{t('Title')}</th><th scope="col" className="lib-type">{t('Type')}</th><th scope="col" className="lib-status">{t('Status')}</th><th scope="col" className="lib-progress">{t('Progress')}</th>{scale !== 'off' && <th scope="col" className="num lib-rating">{t('Rating')}</th>}<th scope="col" className="num lib-when">{t('Last played')}</th></tr></thead>
    <tbody>{items.map(e => {
      // A finished title's date is in Last played; its progress cell says only how far it got.
      const label = e.status === 'completed' ? progressAmount(e) : progressLabel(e), value = meterValue(e), when = lastPlayed(e);
      const sub = [typeLabel(e.media_type), statusText(e.status), label !== noValue ? label : '', scale !== 'off' && e.rating != null ? rating(e.rating, scale) : ''].filter(Boolean).join(' · ');
      return <tr key={e.id}>
        {selecting && <td className="lib-select"><input type="checkbox" aria-label={t('Select {title}', { title: e.title })} checked={selected.has(e.id)} onChange={() => pick(e.id)} /></td>}
        <td className="lib-title"><SourceLink className="table-title" to={entryPath(e)}><Cover entry={e} compact /><span className="lib-name"><span className="lib-name-line">{e.localized_title ?? e.title}{e.year ? <span className="subtle"> {e.year}</span> : null}</span><span className="lib-sub">{sub}</span></span></SourceLink></td>
        <td className="lib-type">{typeLabel(e.media_type)}</td><td className="lib-status"><EntryStatus entry={e} /></td>
        <td className="lib-progress">{value !== undefined && <span className="meter" aria-hidden><span style={{ width: `${value}%` }} /></span>}<span className={label === noValue ? 'subtle' : ''}>{label}</span></td>
        {scale !== 'off' && <td className="num lib-rating">{e.rating != null ? rating(e.rating, scale) : <span className="subtle">{noValue}</span>}</td>}
        <td className="num lib-when">{when ? <time dateTime={when} title={new Date(when).toLocaleString(locale)}>{shortDate(when)}</time> : <span className="subtle">{noValue}</span>}</td>
      </tr>;
    })}</tbody>
  </table></div>;
}

function BulkBar({ entries, onDone }: { entries: Summary[]; onDone: () => void }) {
  const ids = entries.map(entry => entry.id);
  const user = useUser(); const refresh = useRefresh(); const confirm = useConfirm(); const tags = useTags();
  const lists = useQuery({ queryKey: privateKey(user.id, 'bulk-lists'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists', { signal, params: { query: { limit: 100 } } })).items.filter(l => l.kind === 'manual') });
  const run = useAction(async (body: Omit<Schema['BulkInput'], 'entry_ids'>, key) => {
    const out = unwrap(await api.POST('/api/v1/library/bulk', { params: { header: keyHeader(key) }, body: { ...body, entry_ids: ids } }));
    await refresh(); onDone(); return out;
  });
  const pickAction = async (value: string) => {
    const [kind, arg] = value.split(':');
    if (kind === 'status') void run.run({ action: 'set_status', status: arg as Summary['status'] });
    if (kind === 'tag') void run.run({ action: 'add_tags', tag_ids: [arg] });
    if (kind === 'untag') void run.run({ action: 'remove_tags', tag_ids: [arg] });
    if (kind === 'list') void run.run({ action: 'add_to_list', list_id: arg });
    if (kind === 'delete' && await confirm({ title: t('Delete {length} titles?', { length: ids.length }), description: t('Their notes, activity, copies and list memberships are removed permanently.') + ' ' + entries.map(entry => entry.title).join('; '), label: t('Delete titles'), danger: true })) void run.run({ action: 'delete' });
  };
  return <div className="bulk-bar" role="region" aria-label={t('Bulk actions')}>
    <strong>{ids.length} {t('selected')}</strong>
    <details><summary>{t('Review selected titles')}</summary><ul>{entries.map(entry => <li key={entry.id}>{entry.title}</li>)}</ul></details>
    <label><span className="sr-only">{t('Action for selected titles')}</span><select value="" disabled={run.pending} onChange={e => void pickAction(e.target.value)}>
      <option value="">{t('Choose an action…')}</option>
      <optgroup label={t('Set status')}>{statuses.map(([v, l]) => <option key={v} value={`status:${v}`}>{l}</option>)}</optgroup>
      {(tags.data?.length ?? 0) > 0 && <optgroup label={t('Add tag')}>{tags.data!.map(item => <option key={item.id} value={`tag:${item.id}`}>{item.name}</option>)}</optgroup>}
      {(tags.data?.length ?? 0) > 0 && <optgroup label={t('Remove tag')}>{tags.data!.map(item => <option key={item.id} value={`untag:${item.id}`}>{item.name}</option>)}</optgroup>}
      {(lists.data?.length ?? 0) > 0 && <optgroup label={t('Add to list')}>{lists.data!.map(l => <option key={l.id} value={`list:${l.id}`}>{l.name}</option>)}</optgroup>}
      <option value="delete:">{t('Delete from library…')}</option>
    </select></label>
    {run.pending && <span className="subtle">{t('Working…')}</span>}
    <ErrorNote error={run.error} />
  </div>;
}

export function ActivityFeed({ entryID }: { entryID?: string }) {
  const user = useUser(); const [params, setParams] = useSearchParams(); const cursorKey = entryID ? 'activity_cursor' : 'cursor'; const cursor = params.get(cursorKey) || undefined;
  const query = useQuery({ queryKey: privateKey(user.id, 'activity', entryID, cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/activity', { signal, params: { query: { entry_id: entryID, cursor, limit: 30 } } })) });
  if (query.isPending) return <Loading label={t('Loading activity')} />; if (query.error) return <Failed error={query.error} retry={() => void query.refetch()} />;
  return query.data.items.length ? <><ol className="activity-list">{query.data.items.map(event => <li key={event.id}><span className="activity-dot" aria-hidden /><div><Link to={`/library/${event.entry_id}`}>{event.title}</Link><p>{event.kind === 'added' ? t('Added to library') : event.kind === 'restarted' ? t('Started cycle {cycle}', { cycle: event.cycle }) : event.kind === 'updated' ? t('Updated media details') : event.kind === 'played' ? t('Played · {value} {unit}', { value: event.progress.toLocaleString(locale), unit: unitLabel(event.unit) }) : event.kind === 'unplayed' ? t('Removed a play · {value} {unit}', { value: event.progress.toLocaleString(locale), unit: unitLabel(event.unit) }) : `${event.progress.toLocaleString(locale)} ${unitLabel(event.unit)} · ${statusText(event.status)}`}</p></div><Timestamp value={event.created_at} /></li>)}</ol><Pager current={cursor} next={query.data.next_cursor} change={value => { const next = new URLSearchParams(params); if (value) next.set(cursorKey, value); else next.delete(cursorKey); setParams(next); }} /></> : <p className="subtle">{t('No activity yet. Updates will appear here as you track your media.')}</p>;
}
