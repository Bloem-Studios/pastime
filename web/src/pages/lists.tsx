import { useState } from 'react';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import * as Dropdown from '@radix-ui/react-dropdown-menu';
import { ArrowDown, ArrowLeft, ArrowUp, Check, ChevronDown, ChevronRight, Copy, Globe, Layers, Link2, ListFilter, Lock, MessageSquareText, Pencil, Plus, RefreshCw, Rss, Trash2, Users, X } from 'lucide-react';
import { api, keyHeader, unwrap, type Schema } from '../api/client';
import { privateKey, useAuth, useRefresh, useUnsaved, useUser } from '../state';
import { isProfileLogin } from '../profileLoginPolicy';
import { Cover, typeLabel } from '../media';
import { RuleEditor, useRatingsOn } from '../organize';
import { CollabEditor, CollabSection } from './collab';
import { TierBoard } from './tiers';
import { Empty, ErrorNote, Failed, Field, Loading, Modal, PageTitle, Pager, submit, Timestamp, useAction, useConfirm, statusText } from '../ui';
import { plural, t, tx } from '../i18n';

type List = Schema['List'];
const sortLabels: [List['sort'], string][] = [['position', t('Your order')], ['added', t('Recently added')], ['activity', t('Recent activity')], ['title', t('Title')], ['rating', t('Your rating')], ['year', t('Release year')]];
const slugify = (name: string) => name.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60).replace(/-+$/, '');
const visibilityLabel = { private: t('Private'), unlisted: t('Anyone with the link'), public: t('Public on your profile') } as const;

function ListEditor({ list, smart = false, onClose }: { list?: List; smart?: boolean; onClose: () => void }) {
  const unsaved = useUnsaved(); const refresh = useRefresh(); const navigate = useNavigate();
  const kind = list?.kind ?? (smart ? 'smart' : 'manual'); const ratingsOn = useRatingsOn();
  const [draft, setDraft] = useState<Schema['ListInput']>(() => ({ name: list?.name ?? '', description: list?.description ?? '', visibility: list?.visibility ?? 'private', slug: list?.slug ?? null, kind, rules: list?.rules ?? {}, sort: list?.sort ?? (kind === 'smart' ? 'added' : 'position') }));
  const [dirty, setDirty] = useState(false);
  const set = <K extends keyof Schema['ListInput']>(k: K, v: Schema['ListInput'][K]) => { setDraft(d => ({ ...d, [k]: v })); setDirty(true); unsaved.markDirty(); };
  const action = useAction(async (body: Schema['ListInput'], key) => {
    const result = list ? unwrap(await api.PUT('/api/v1/lists/{listID}', { params: { path: { listID: list.id }, header: keyHeader(key) }, body: { ...body, version: list.version } }))
      : unwrap(await api.POST('/api/v1/lists', { params: { header: keyHeader(key) }, body }));
    unsaved.markSaved(); setDirty(false); await refresh(); onClose(); if (!list) navigate(`/lists/${result.id}`);
  });
  const shared = draft.visibility !== 'private';
  return <Modal title={list ? t('Edit list') : kind === 'smart' ? t('Create a smart list') : t('Create list')} description={kind === 'smart' ? t('A smart list fills itself from rules and stays current as your library changes.') : t('Keep related titles together, in the order you choose.')} onClose={onClose} dirty={dirty} busy={action.pending} wide>
    <form onSubmit={submit(() => void action.run({ ...draft, slug: shared ? (draft.slug || slugify(draft.name)) : draft.slug }))}><fieldset disabled={action.pending}>
      <Field label={t('List name')}>{id => <input id={id} required maxLength={100} value={draft.name} onChange={e => set('name', e.target.value)} />}</Field>
      <Field label={t('Description')} hint={t('Optional')}>{(id, hint) => <textarea id={id} aria-describedby={hint} rows={3} maxLength={2000} value={draft.description} onChange={e => set('description', e.target.value)} />}</Field>
      {kind === 'smart' && shared && <p className="notice">{t('A shared smart list reveals which titles match your private rules, including ratings, tags and status. Its membership changes as your library changes.')}</p>}
      {kind === 'smart' && <RuleEditor rules={draft.rules ?? {}} onChange={rules => set('rules', rules)} />}
      <div className="form-grid">
        <Field label={t('Sort')}>{id => <select id={id} value={draft.sort} onChange={e => set('sort', e.target.value as List['sort'])}>{sortLabels.filter(([v]) => (kind === 'manual' || v !== 'position') && (ratingsOn || v !== 'rating')).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>
        <Field label={t('Who can see it')}>{id => <select id={id} value={draft.visibility} onChange={e => set('visibility', e.target.value as List['visibility'])}>{Object.entries(visibilityLabel).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>}</Field>
      </div>
      {shared && <Field label={t('Link name')} hint={t('Lowercase letters, digits and hyphens. Shared lists show titles and your list notes only: never ratings, private notes or progress.')}>{(id, hint) =>
        <input id={id} aria-describedby={hint} maxLength={60} pattern="[a-z0-9]([a-z0-9\-]{0,58}[a-z0-9])?" placeholder={slugify(draft.name) || 'my-list'} value={draft.slug ?? ''} onChange={e => set('slug', e.target.value.toLowerCase() || null)} />}</Field>}
    </fieldset>
    <ErrorNote error={action.error} />
    <div className="dialog-actions"><button className="button primary" type="submit" disabled={action.pending}>{action.pending ? t('Saving…') : list ? t('Save changes') : t('Create list')}</button></div></form>
  </Modal>;
}

export function ListsPage() {
  const user = useUser(); const household = !isProfileLogin(useAuth().session); const [params, setParams] = useSearchParams(); const cursor = params.get('cursor') || undefined;
  const [creating, setCreating] = useState<'manual' | 'smart' | 'collab' | null>(null);
  const query = useQuery({ queryKey: privateKey(user.id, 'lists', cursor), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists', { signal, params: { query: { cursor, limit: 30 } } })) });
  const items = query.data?.items ?? [];
  return <div className="lists-page">
    <PageTitle title={t('Lists')} description={query.data && !cursor && !query.data.next_cursor && items.length ? plural(items.length, '{n} list', '{n} lists') : undefined}
      actions={<><Link className="button ghost" to="/recommendations">{t('Recommendations')}</Link><NewListMenu onPick={setCreating} collab={household} /></>} />
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} /> : items.length ? <>
      <ul className="list-rows">{items.map(list => <li key={list.id}><Link to={`/lists/${list.id}`}>
        <span className="list-row-text"><strong>{list.name}</strong>
          <span className="subtle">{list.kind === 'smart' ? t('Smart list') : t('List')} · {visibilityLabel[list.visibility]}{list.description ? ` · ${list.description}` : ''}</span></span>
        <span className="list-row-count">{list.kind === 'manual' ? plural(list.item_count, '{n} title', '{n} titles') : <span className="subtle">{t('Follows rules')}</span>}</span>
        <ChevronRight aria-hidden /></Link></li>)}</ul>
      <Pager current={cursor} next={query.data.next_cursor} change={value => setParams(value ? { cursor: value } : {})} /></>
      : <Empty title={t('No lists yet')}>{t('Add titles from their pages, or let a smart list gather them by rules.')}</Empty>}
    {household && <CollabSection />}
    {(creating === 'manual' || creating === 'smart') && <ListEditor smart={creating === 'smart'} onClose={() => setCreating(null)} />}
    {creating === 'collab' && <CollabEditor onClose={() => setCreating(null)} />}
  </div>;
}

/** The one create action on Lists: each kind of list says what it is. */
function NewListMenu({ onPick, collab }: { onPick: (kind: 'manual' | 'smart' | 'collab') => void; collab: boolean }) {
  const kinds: ['manual' | 'smart' | 'collab', string, string, typeof Plus][] = [
    ['manual', t('List'), t('Titles you choose, in your order'), Layers],
    ['smart', t('Smart list'), t('Fills itself from rules'), ListFilter],
    ...(collab ? [['collab', t('Collaborative list'), t('Curated with other people'), Users] as ['collab', string, string, typeof Plus]] : []),
  ];
  return <Dropdown.Root>
    <Dropdown.Trigger className="button primary"><Plus aria-hidden />{t('New list')}<ChevronDown size={16} aria-hidden /></Dropdown.Trigger>
    <Dropdown.Portal><Dropdown.Content className="dropdown-menu new-list-menu" sideOffset={8} align="end">
      {kinds.map(([kind, label, hint, Icon]) => <Dropdown.Item key={kind} onSelect={() => onPick(kind)}><Icon aria-hidden /><span><strong>{label}</strong><span className="subtle">{hint}</span></span></Dropdown.Item>)}
    </Dropdown.Content></Dropdown.Portal>
  </Dropdown.Root>;
}

function SourcePanel({ list }: { list: List }) {
  const user = useUser(); const client = useQueryClient(); const [address, setAddress] = useState(''); const [open, setOpen] = useState(false); const [exported, setExported] = useState<Schema['TraktExport'] | null>(null);
  const reload = async (next: List) => { client.setQueryData(privateKey(user.id, 'list', list.id), next); await client.invalidateQueries({ queryKey: privateKey(user.id, 'list-entries', list.id) }); };
  const follow = useAction(async (_: void) => { await reload(unwrap(await api.PUT('/api/v1/lists/{listID}/source', { params: { path: { listID: list.id } }, body: { url: address.trim() } }))); setAddress(''); setOpen(false); });
  const unfollow = useAction(async (_: void) => reload(unwrap(await api.DELETE('/api/v1/lists/{listID}/source', { params: { path: { listID: list.id } } }))));
  const exportTrakt = useAction(async (_: void) => setExported(unwrap(await api.POST('/api/v1/lists/{listID}/trakt-export', { params: { path: { listID: list.id } } }))));
  const source = list.source;
  return <section className="panel source-panel" aria-label={t('Sync')}>
    {source ? <div className="source-row"><RefreshCw size={16} aria-hidden /><span>{t('Follows')} <a className="text-link" href={source.url} target="_blank" rel="noreferrer">{source.kind === 'trakt' ? t('a Trakt list') : t('an MDBList list')}</a> · {source.synced_at ? <>{tx('synced {date}', { date: <Timestamp value={source.synced_at} /> })}</> : t('first sync pending')}. {t('Changes you make here are replaced by the source daily.')}</span>
        <button className="button small" disabled={unfollow.pending} onClick={() => void unfollow.run()}>{t('Stop following')}</button></div>
      : open ? <form className="source-form" onSubmit={submit(() => void follow.run())}>
          <Field label={t('Trakt or MDBList list address')} hint={t('For example https://trakt.tv/users/name/lists/slug. The list will mirror it: titles are added to your library as planned.')}>{(id, hint) =>
            <input id={id} aria-describedby={hint} type="url" required maxLength={500} value={address} onChange={e => setAddress(e.target.value)} autoFocus />}</Field>
          <div className="actions"><button className="button primary" type="submit" disabled={follow.pending}>{follow.pending ? t('Following…') : t('Follow list')}</button><button type="button" className="button ghost" onClick={() => setOpen(false)}>{t('Cancel')}</button></div>
        </form>
      : <div className="source-row"><RefreshCw size={16} aria-hidden /><span>{t('Keep this list in step with a public Trakt or MDBList list, or copy it to Trakt.')}</span>
          <button className="button small" onClick={() => setOpen(true)}>{t('Follow a list')}</button>
          <button className="button small" disabled={exportTrakt.pending} onClick={() => void exportTrakt.run()}>{exportTrakt.pending ? t('Exporting…') : t('Export to Trakt')}</button></div>}
    {exported && <p className="save-confirmation" role="status"><Check size={14} aria-hidden />{plural(exported.added, '{n} added to Trakt', '{n} added to Trakt')}{exported.existing ? t(', {existing} already there', { existing: exported.existing }) : ''}{exported.skipped ? t(', {skipped} skipped', { skipped: exported.skipped }) : ''}.{exported.url && <> <a className="text-link" href={exported.url} target="_blank" rel="noreferrer">{t('Open on Trakt')}</a></>}</p>}
    <ErrorNote error={follow.error || unfollow.error || exportTrakt.error} />
  </section>;
}

function SharePanel({ list }: { list: List }) {
  const user = useUser(); const [copied, setCopied] = useState('');
  const base = `${location.origin}`;
  const refresh = useRefresh(['list', 'lists']);
  const rotate = useAction(async (_: void, key) => {
    unwrap(await api.PUT('/api/v1/lists/{listID}', { params: { path: { listID: list.id }, header: keyHeader(key) }, body: { name: list.name, description: list.description, visibility: list.visibility, slug: list.slug, kind: list.kind, rules: list.rules, sort: list.sort, version: list.version, rotate_share: true } }));
    await refresh();
  });
  if (list.visibility === 'private' || !list.slug) return null;
  const address = list.visibility === 'unlisted' ? list.share_token : list.slug;
  const links: [string, string, typeof Rss][] = [[t('Public page'), `${base}/u/${user.username}/${address}`, Globe], [t('Page data (JSON)'), `${base}/api/v1/public/${user.username}/lists/${address}`, Link2], [t('RSS feed'), `${base}/lists/${user.username}/${address}.rss`, Rss], [t('CSV download'), `${base}/lists/${user.username}/${address}.csv`, Copy]];
  return <section className="panel share-panel" aria-label={t('Sharing')}>
    <div className="panel-head"><h2><Globe size={16} aria-hidden />{visibilityLabel[list.visibility]}</h2><span className="subtle">{t('Titles and list notes only')}</span></div>
    {list.visibility === 'unlisted' && <p>{t('Secret links expire after 90 days. Renewing a link revokes the old one.')} {list.share_expires_at && <Timestamp value={list.share_expires_at} />} <button className="button small" disabled={rotate.pending} onClick={() => void rotate.run()}>{t('Renew secret link')}</button></p>}
    <ErrorNote error={rotate.error} />
    <ul>{links.map(([label, url, Icon]) => <li key={label}><Icon size={16} aria-hidden /><span>{label}</span><code>{url.replace(base, '')}</code>
      <button className="button small ghost" onClick={async () => { try { await navigator.clipboard.writeText(url); setCopied(label); } catch { setCopied(''); } }}>{copied === label ? t('Copied') : t('Copy')}</button></li>)}</ul>
  </section>;
}

export function ListDetail() {
  const { listID = '' } = useParams(); const user = useUser(); const [params, setParams] = useSearchParams(); const cursor = params.get('cursor') || undefined;
  const query = useQuery({ queryKey: privateKey(user.id, 'list', listID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/lists/{listID}', { signal, params: { path: { listID } } })) });
  const manual = query.data?.kind === 'manual' && query.data.sort === 'position';
  const entries = useQuery({ queryKey: privateKey(user.id, 'list-entries', listID, cursor, query.data?.version), enabled: !!query.data,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { list_id: listID, cursor, limit: manual ? 100 : 30 } } })) });
  const [editing, setEditing] = useState(false); const [noting, setNoting] = useState<Schema['EntrySummary'] | null>(null);
  const refresh = useRefresh(); const confirm = useConfirm(); const navigate = useNavigate();
  const remove = useAction(async (version: number, key) => { unwrap(await api.DELETE('/api/v1/lists/{listID}', { params: { path: { listID }, header: keyHeader(key) }, body: { version } })); await refresh(); navigate('/lists', { replace: true }); });
  const membership = useAction(async (value: { entryID: string; version: number }, key) => { unwrap(await api.PUT('/api/v1/lists/{listID}/entries/{entryID}', { params: { path: { listID, entryID: value.entryID }, header: keyHeader(key) }, body: { version: value.version, present: false } })); await refresh(); });
  const reorder = useAction(async (value: { ids: string[]; version: number }, key) => { unwrap(await api.PUT('/api/v1/lists/{listID}/order', { params: { path: { listID }, header: keyHeader(key) }, body: { version: value.version, entry_ids: value.ids } })); await refresh(); });
  const freeze = useAction(async (_: void, key) => {
    if (!query.data) return;
    const created = unwrap(await api.POST('/api/v1/lists', { params: { header: keyHeader(key) }, body: { name: query.data.name.slice(0, 90) + ' (copy)', kind: 'manual', visibility: 'private', snapshot_of: listID } }));
    await refresh(); navigate(`/lists/${created.id}`);
  });
  if (query.isPending || (query.data && entries.isPending)) return <Loading label={t('Loading list')} />;
  if (query.error || entries.error) return <><Link className="back-link" to="/lists"><ArrowLeft size={16} aria-hidden />{t('Lists')}</Link><Failed title={t('This list is not available')} error={query.error || entries.error} retry={() => { void query.refetch(); void entries.refetch(); }} /></>;
  const list = query.data!; const items = entries.data!.items; const tiered = list.kind === 'manual' && params.get('view') === 'tiers';
  // A subset can move to the front; remaining members keep their relative order.
  const canMove = manual && !cursor && items.length > 1;
  const move = (index: number, delta: number) => { const ids = items.map(i => i.id); const [id] = ids.splice(index, 1); ids.splice(index + delta, 0, id); void reorder.run({ ids, version: list.version }); };
  return <>
    <Link className="back-link" to="/lists"><ArrowLeft size={16} aria-hidden />{t('Lists')}</Link>
    <PageTitle title={list.name} description={list.description || undefined}
      actions={<button className="button" onClick={() => setEditing(true)}><Pencil aria-hidden />{t('Edit list')}</button>} />
    <p className="private-caption">{list.visibility === 'private' ? <Lock size={14} aria-hidden /> : <Globe size={14} aria-hidden />}{list.kind === 'smart' ? t('Smart list') : t('List')} · {visibilityLabel[list.visibility]} · {t('Sorted by {order}', { order: sortLabels.find(s => s[0] === list.sort)?.[1].toLowerCase() ?? '' })}{list.kind === 'manual' && <> · {plural(list.item_count, '{n} title', '{n} titles')}</>}</p>
    <SharePanel list={list} />
    <button className="button" disabled={freeze.pending} onClick={() => void freeze.run()}>{t('Create a frozen copy')}</button><ErrorNote error={freeze.error} />
    {list.kind === 'manual' && <SourcePanel list={list} />}
    <ErrorNote error={membership.error || reorder.error} />
    {list.kind === 'manual' && <div className="segmented labelled" role="group" aria-label={t('View')}>
      <button type="button" aria-pressed={!tiered} onClick={() => setParams({})}>{t('List')}</button>
      <button type="button" aria-pressed={tiered} onClick={() => setParams({ view: 'tiers' })}>{t('Tiers')}</button>
    </div>}
    {tiered ? <TierBoard list={list} /> : items.length ? <><ol className="media-rows ranked">{items.map((entry, index) => <li className="media-row" key={entry.id}>
      <Link className="row-cover" tabIndex={-1} aria-hidden to={`/library/${entry.id}`}><Cover entry={entry} compact /></Link>
      <div><Link className="media-title" to={`/library/${entry.id}`}>{entry.title}</Link><p>{typeLabel(entry.media_type)}{entry.year ? ` · ${entry.year}` : ''} · {statusText(entry.status)}</p>{entry.list_note && <p className="list-note">{entry.list_note}</p>}</div>
      {list.kind === 'manual' && <div className="row-actions">
        {canMove && <><button className="icon-button" aria-label={t('Move {title} up', { title: entry.title })} disabled={index === 0 || reorder.pending} onClick={() => move(index, -1)}><ArrowUp aria-hidden /></button>
          <button className="icon-button" aria-label={t('Move {title} down', { title: entry.title })} disabled={index === items.length - 1 || reorder.pending} onClick={() => move(index, 1)}><ArrowDown aria-hidden /></button></>}
        <button className="icon-button" aria-label={t('Note on {title}', { title: entry.title })} onClick={() => setNoting(entry)}><MessageSquareText aria-hidden /></button>
        <button className="icon-button" aria-label={t('Remove {title} from list', { title: entry.title })} disabled={membership.pending} onClick={async () => { if (await confirm({ title: t('Remove from this list?'), description: t('The title stays in your library.'), label: t('Remove from list') })) void membership.run({ entryID: entry.id, version: list.version }); }}><X aria-hidden /></button>
      </div>}
    </li>)}</ol><Pager current={cursor} next={entries.data!.next_cursor} change={value => setParams(value ? { cursor: value } : {})} /></>
      : <Empty title={list.kind === 'smart' ? t('Nothing matches yet.') : t('This shelf is waiting.')} action={list.kind === 'smart' ? <button className="button" onClick={() => setEditing(true)}>{t('Adjust rules')}</button> : <Link className="button" to="/library">{t('Browse library')}</Link>}>{list.kind === 'smart' ? t('Titles appear here as soon as they match the rules.') : t('Open a title and choose Add to list, or select several in your library.')}</Empty>}
    <section className="danger-section"><h2>{t('Delete list')}</h2><p>{t('Titles stay in your library.')}</p><ErrorNote error={remove.error} />
      <button className="button danger-outline" disabled={remove.pending} onClick={async () => { if (await confirm({ title: t('Delete this list?'), description: t('“{name}” will be removed. Its titles stay in your library.', { name: list.name }), label: t('Delete list'), danger: true })) void remove.run(list.version); }}><Trash2 aria-hidden />{t('Delete list')}</button></section>
    {editing && <ListEditor list={list} onClose={() => setEditing(false)} />}
    {noting && <NoteEditor list={list} entry={noting} onClose={() => setNoting(null)} />}
  </>;
}

function NoteEditor({ list, entry, onClose }: { list: List; entry: Schema['EntrySummary']; onClose: () => void }) {
  const refresh = useRefresh(); const [note, setNote] = useState(entry.list_note ?? '');
  const save = useAction(async (value: string, key) => { unwrap(await api.PUT('/api/v1/lists/{listID}/entries/{entryID}/note', { params: { path: { listID: list.id, entryID: entry.id }, header: keyHeader(key) }, body: { version: list.version, note: value } })); await refresh(); onClose(); });
  return <Modal title={t('List note')} description={t('A short note about “{title}” in this list. It is shown wherever the list is shared.', { title: entry.title })} onClose={onClose} busy={save.pending}>
    <form onSubmit={submit(() => void save.run(note))}><Field label={t('Note')} hint={t('Up to 500 characters. Leave empty to remove.')}>{(id, hint) => <textarea id={id} aria-describedby={hint} rows={3} maxLength={500} value={note} onChange={e => setNote(e.target.value)} />}</Field>
      <ErrorNote error={save.error} /><div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{t('Save note')}</button></div></form>
  </Modal>;
}
