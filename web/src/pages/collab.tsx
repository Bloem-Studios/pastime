import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, Check, ChevronRight, Crown, Eye, Pencil, Plus, Trash2, UserPlus, X } from 'lucide-react';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useAuth, useUser } from '../state';
import { isProfileLogin } from '../profileLoginPolicy';
import { Cover, typeLabel } from '../media';
import { Empty, ErrorNote, Failed, Field, Loading, Modal, NotFound, PageTitle, Status, submit, useAction, useConfirm } from '../ui';
import { plural, t } from '../i18n';

type Detail = Schema['CollabDetail'];
const roleLabel: Record<Schema['CollabList']['role'], string> = { owner: t('Owner'), editor: t('Editor'), viewer: t('Viewer') };
const collabKey = (userID: string, ...rest: unknown[]) => privateKey(userID, 'collab', ...rest);

/** Collaborative lists on the Lists page. */
export function CollabSection() {
  const user = useUser();
  const query = useQuery({ queryKey: collabKey(user.id), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collab-lists', { signal })).items });
  return <section className="detail-section collab-section">
    <h2>{t('Collaborative lists')}</h2>
    {query.isPending ? <Loading /> : query.error ? <Failed error={query.error} retry={() => void query.refetch()} />
      : query.data.length ? <ul className="list-rows">{query.data.map(l => <li key={l.id}><Link to={`/collab/${l.id}`}>
        <span className="list-row-text"><strong>{l.name}</strong><span className="subtle">{[roleLabel[l.role], plural(l.members, '{n} person', '{n} people'), l.description].filter(Boolean).join(' · ')}</span></span>
        <span className="list-row-count">{plural(l.items, '{n} title', '{n} titles')}</span>
        <ChevronRight aria-hidden /></Link></li>)}</ul>
      : <Empty compact title={t('No collaborative lists yet')}>{t('Curate a list with other people on this server. Everyone sees their own progress on each title.')}</Empty>}
  </section>;
}

export function CollabEditor({ list, onClose }: { list?: Detail; onClose: () => void }) {
  const user = useUser(); const client = useQueryClient(); const navigate = useNavigate();
  const [name, setName] = useState(list?.name ?? ''); const [description, setDescription] = useState(list?.description ?? '');
  const save = useAction(async (_: void) => {
    if (list) {
      const out = unwrap(await api.PATCH('/api/v1/collab-lists/{collabID}', { params: { path: { collabID: list.id } }, body: { name, description, version: list.version } }));
      client.setQueryData(collabKey(user.id, list.id), out);
    } else {
      const out = unwrap(await api.POST('/api/v1/collab-lists', { body: { name, description } }));
      navigate(`/collab/${out.id}`);
    }
    await client.invalidateQueries({ queryKey: collabKey(user.id) }); onClose();
  });
  return <Modal title={list ? t('Edit collaborative list') : t('New collaborative list')} description={t('Invite people after creating it. Owners manage members; editors add and order titles.')} onClose={onClose} busy={save.pending} dirty={name !== (list?.name ?? '') || description !== (list?.description ?? '')}>
    <form onSubmit={submit(() => void save.run())}><fieldset disabled={save.pending}>
      <Field label={t('Name')}>{id => <input id={id} required maxLength={100} value={name} onChange={e => setName(e.target.value)} autoFocus />}</Field>
      <Field label={t('Description')} hint={t('Optional.')}>{(id, hint) => <textarea id={id} aria-describedby={hint} rows={3} maxLength={2000} value={description} onChange={e => setDescription(e.target.value)} />}</Field>
    </fieldset><ErrorNote error={save.error} />
    <div className="dialog-actions"><button className="button primary" type="submit" disabled={save.pending}>{save.pending ? t('Saving…') : list ? t('Save changes') : t('Create list')}</button></div></form>
  </Modal>;
}

/** Collaborative lists belong to the household's accounts; a profile login has none. */
export function CollabDetailPage() {
  return isProfileLogin(useAuth().session) ? <NotFound /> : <CollabDetail />;
}

function CollabDetail() {
  const { collabID = '' } = useParams(); const user = useUser(); const client = useQueryClient(); const navigate = useNavigate(); const confirm = useConfirm();
  const query = useQuery({ queryKey: collabKey(user.id, collabID), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collab-lists/{collabID}', { signal, params: { path: { collabID } } })) });
  const [editing, setEditing] = useState(false); const [members, setMembers] = useState(false); const [adding, setAdding] = useState(false);
  const set = (d: Detail) => client.setQueryData(collabKey(user.id, collabID), d);
  const remove = useAction(async (catalogID: string) => { unwrap(await api.DELETE('/api/v1/collab-lists/{collabID}/items/{catalogID}', { params: { path: { collabID, catalogID } } })); await query.refetch(); });
  const destroy = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/collab-lists/{collabID}', { params: { path: { collabID } } })); await client.invalidateQueries({ queryKey: collabKey(user.id) }); navigate('/lists'); });
  const leave = useAction(async (_: void) => { unwrap(await api.DELETE('/api/v1/collab-lists/{collabID}/members/{username}', { params: { path: { collabID, username: user.username } } })); await client.invalidateQueries({ queryKey: collabKey(user.id) }); navigate('/lists'); });
  if (query.isPending) return <Loading label={t('Loading list')} />;
  if (query.error) return <><Link className="back-link" to="/lists"><ArrowLeft size={16} aria-hidden />{t('Lists')}</Link><Failed title={t('This list is not available')} error={query.error} retry={() => void query.refetch()} /></>;
  const list = query.data; const canEdit = list.role !== 'viewer'; const owner = list.role === 'owner';
  return <>
    <Link className="back-link" to="/lists"><ArrowLeft size={16} aria-hidden />{t('Lists')}</Link>
    <PageTitle title={list.name} description={list.description || t('Curated by {members}.', { members: plural(list.members, '{n} person', '{n} people') })}
      actions={<>
        <button className="member-stack" onClick={() => setMembers(true)} aria-label={t('{members} people; manage members', { members: list.members })}>
          {list.member_list.slice(0, 4).map(m => <span key={m.username} className="member-avatar" title={m.display_name}>{m.display_name.slice(0, 1).toUpperCase()}</span>)}
          <span className="member-count">{list.members}</span></button>
        {canEdit && <button className="button primary" onClick={() => setAdding(true)}><Plus aria-hidden />{t('Add titles')}</button>}
        {owner && <button className="button" onClick={() => setEditing(true)}><Pencil aria-hidden />{t('Edit')}</button>}
      </>} />
    <ErrorNote error={remove.error || destroy.error || leave.error} />
    {list.item_list.length ? <div className="media-grid collab-grid">{list.item_list.map(item => <article key={item.catalog_id} className="poster-card collab-item">
        <Link to={`/title/${item.catalog_id}`} className="art" tabIndex={-1} aria-hidden="true"><Cover entry={{ title: item.title, media_type: item.media_type, image_url: item.image_url }} /></Link>
        <Link className="card-title media-title" to={`/title/${item.catalog_id}`}>{item.title}</Link>
        <span className="card-meta">{[typeLabel(item.media_type), item.year].filter(Boolean).join(' · ')}</span>
        {item.status ? <Status value={item.status} /> : <span className="subtle collab-untracked">{t('Not in your library')}</span>}
        {item.note && <p className="collab-note">{item.note}</p>}
        <span className="subtle collab-by">{item.added_by ? t('Added by {displayName}', { displayName: item.added_by.display_name }) : t('Added by a former member')}</span>
        {canEdit && <button className="icon-button collab-remove" aria-label={t('Remove {title}', { title: item.title })} title={t('Remove')} disabled={remove.pending} onClick={() => void remove.run(item.catalog_id)}><X aria-hidden /></button>}
      </article>)}</div>
      : <Empty title={t('Nothing here yet')} action={canEdit ? <button className="button primary" onClick={() => setAdding(true)}><Plus aria-hidden />{t('Add titles')}</button> : undefined}>
          {canEdit ? t('Add titles from your library, or open any title and choose Add to collaborative list.') : t('Editors have not added anything yet.')}</Empty>}
    <div className="actions collab-footer">
      {owner ? <button className="button danger-outline" disabled={destroy.pending} onClick={async () => { if (await confirm({ title: t('Delete {name}?', { name: list.name }), description: t('The list is removed for everyone. Titles stay in each person’s library.'), label: t('Delete list'), danger: true })) void destroy.run(); }}><Trash2 aria-hidden />{t('Delete list')}</button>
        : <button className="button danger-outline" disabled={leave.pending} onClick={async () => { if (await confirm({ title: t('Leave {name}?', { name: list.name }), description: t('You will need to be added again to see it.'), label: t('Leave'), danger: true })) void leave.run(); }}>{t('Leave list')}</button>}
    </div>
    {editing && <CollabEditor list={list} onClose={() => setEditing(false)} />}
    {members && <MembersDialog list={list} onChange={set} onClose={() => setMembers(false)} />}
    {adding && <AddFromLibrary list={list} onChange={set} onClose={() => setAdding(false)} />}
  </>;
}

function MembersDialog({ list, onChange, onClose }: { list: Detail; onChange: (d: Detail) => void; onClose: () => void }) {
  const owner = list.role === 'owner'; const [q, setQ] = useState(''); const [role, setRole] = useState<'editor' | 'viewer'>('editor');
  const people = useQuery({ queryKey: ['recipients', q.trim().toLowerCase()], enabled: owner && q.trim().length >= 2, staleTime: 60_000,
    queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/recommendation-recipients', { signal, params: { query: { q: q.trim() } } })).items });
  const setMember = useAction(async (value: { username: string; role: 'editor' | 'viewer' }) => { onChange(unwrap(await api.PUT('/api/v1/collab-lists/{collabID}/members', { params: { path: { collabID: list.id } }, body: value }))); setQ(''); });
  const removeMember = useAction(async (username: string) => { unwrap(await api.DELETE('/api/v1/collab-lists/{collabID}/members/{username}', { params: { path: { collabID: list.id, username } } }));
    onChange({ ...list, members: list.members - 1, member_list: list.member_list.filter(m => m.username !== username) }); });
  const existing = new Set(list.member_list.map(m => m.username));
  return <Modal title={t('People')} description={owner ? t('Editors add, remove and order titles. Viewers can only look.') : t('Only the owner manages who is on this list.')} onClose={onClose} busy={setMember.pending || removeMember.pending}>
    <ul className="member-list">{list.member_list.map(m => <li key={m.username}>
      <span className="member-avatar">{m.display_name.slice(0, 1).toUpperCase()}</span>
      <span className="member-text"><strong>{m.display_name}</strong><span className="subtle">@{m.username}</span></span>
      {m.role === 'owner' ? <span className="role-tag"><Crown aria-hidden />{t('Owner')}</span>
        : owner ? <><label className="sr-only" htmlFor={`role-${m.username}`}>{t('Role for')} {m.display_name}</label>
          <select id={`role-${m.username}`} value={m.role} onChange={e => void setMember.run({ username: m.username, role: e.target.value as 'editor' | 'viewer' })}><option value="editor">{t('Editor')}</option><option value="viewer">{t('Viewer')}</option></select>
          <button className="icon-button" aria-label={t('Remove {displayName}', { displayName: m.display_name })} onClick={() => void removeMember.run(m.username)}><X aria-hidden /></button></>
        : <span className="role-tag">{m.role === 'editor' ? <Pencil aria-hidden /> : <Eye aria-hidden />}{roleLabel[m.role]}</span>}
    </li>)}</ul>
    {owner && <form className="member-add" onSubmit={submit(() => undefined)}>
      <Field label={t('Add someone')} hint={t('Type at least two letters of a name or username.')}>{(id, hint) => <input id={id} aria-describedby={hint} type="search" autoComplete="off" maxLength={80} value={q} onChange={e => setQ(e.target.value)} />}</Field>
      <Field label={t('As')}>{id => <select id={id} value={role} onChange={e => setRole(e.target.value as typeof role)}><option value="editor">{t('Editor')}</option><option value="viewer">{t('Viewer')}</option></select>}</Field>
      {people.data && <ul className="pick-list">{people.data.filter(p => !existing.has(p.username)).map(p => <li key={p.username}><button type="button" className="pick-row" disabled={setMember.pending} onClick={() => void setMember.run({ username: p.username, role })}><strong><UserPlus aria-hidden className="inline-icon" /> {p.display_name}</strong><span className="subtle">@{p.username}</span></button></li>)}</ul>}
    </form>}
    <ErrorNote error={setMember.error || removeMember.error} />
  </Modal>;
}

function AddFromLibrary({ list, onChange, onClose }: { list: Detail; onChange: (d: Detail) => void; onClose: () => void }) {
  const user = useUser(); const [q, setQ] = useState('');
  const results = useQuery({ queryKey: privateKey(user.id, 'library', 'collab-pick', q), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/library', { signal, params: { query: { q: q.trim() || undefined, limit: 20, sort: 'activity' } } })).items });
  const add = useAction(async (catalogID: string) => onChange(unwrap(await api.POST('/api/v1/collab-lists/{collabID}/items', { params: { path: { collabID: list.id } }, body: { catalog_id: catalogID } }))));
  const onList = new Set(list.item_list.map(i => i.catalog_id));
  return <Modal title={t('Add titles')} description={t('Pick from your library. To add something you do not track, open its page and choose Add to collaborative list.')} onClose={onClose} busy={add.pending}>
    <Field label={t('Search your library')}>{id => <input id={id} type="search" value={q} onChange={e => setQ(e.target.value)} autoFocus />}</Field>
    {results.isPending ? <Loading label={t('Searching')} /> : results.error ? <Failed error={results.error} retry={() => void results.refetch()} />
      : <ul className="pick-list">{results.data.filter(e => e.catalog_id).map(e => { const added = onList.has(e.catalog_id!); return <li key={e.id}>
        <button className="pick-row" disabled={added || add.pending} onClick={() => void add.run(e.catalog_id!)}><strong>{added && <Check aria-hidden className="inline-icon" />} {e.title}</strong><span className="subtle">{[typeLabel(e.media_type), e.year].filter(Boolean).join(' · ')}{added ? ' · on the list' : ''}</span></button></li>; })}</ul>}
    <ErrorNote error={add.error} />
  </Modal>;
}

/** Adds a catalogued title to one of your collaborative lists. */
export function AddToCollabDialog({ catalogID, title, onClose }: { catalogID: string; title: string; onClose: () => void }) {
  const user = useUser(); const client = useQueryClient(); const [done, setDone] = useState<string | null>(null); const [note, setNote] = useState('');
  const lists = useQuery({ queryKey: collabKey(user.id), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/collab-lists', { signal })).items });
  const add = useAction(async (l: Schema['CollabList']) => { unwrap(await api.POST('/api/v1/collab-lists/{collabID}/items', { params: { path: { collabID: l.id } }, body: { catalog_id: catalogID, note: note.trim() } })); setDone(l.name); await client.invalidateQueries({ queryKey: collabKey(user.id) }); });
  const editable = (lists.data ?? []).filter(l => l.role !== 'viewer');
  return <Modal title={t('Add to collaborative list')} description={t('Add {title} to a list you curate with others.', { title: title })} onClose={onClose} busy={add.pending}>
    {done ? <><p className="save-confirmation"><Check aria-hidden />{t('Added to {list}.', { list: done })}</p><div className="dialog-actions"><button className="button primary" onClick={onClose}>{t('Done')}</button></div></>
      : lists.isPending ? <Loading /> : lists.error ? <Failed error={lists.error} retry={() => void lists.refetch()} />
      : editable.length ? <>
        <Field label={t('Note')} hint={t('Optional; everyone on the list sees it.')}>{(id, hint) => <input id={id} aria-describedby={hint} maxLength={500} value={note} onChange={e => setNote(e.target.value)} />}</Field>
        <ul className="pick-list">{editable.map(l => <li key={l.id}><button className="pick-row" disabled={add.pending} onClick={() => void add.run(l)}><strong>{l.name}</strong><span className="subtle">{t('{titles} titles · {people} people', { titles: l.items, people: l.members })}</span></button></li>)}</ul></>
      : <p className="subtle">{t('You are not an owner or editor of any collaborative list. Create one from the Lists page.')}</p>}
    <ErrorNote error={add.error} />
  </Modal>;
}
