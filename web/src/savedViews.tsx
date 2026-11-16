import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Bookmark } from 'lucide-react';
import { api, unwrap, type Schema } from './api/client';
import { privateKey, useUser } from './state';
import { ErrorNote, submit, useAction } from './ui';
import { t } from './i18n';

type View = Schema['SavedView'];
export function savedViewURL(v: View) {
  const p = new URLSearchParams({ sort: v.sort, view: v.layout });
  const r = v.rules;
  const fields: [string, string | undefined][] = [['q', r.query], ['media_type', r.media_types?.join(',')], ['status', r.statuses?.join(',')], ['tag_id', r.tag_ids?.join(',')], ['genre', r.genres?.join(',')], ['min_rating', r.min_rating?.toString()], ['year_from', r.year_from?.toString()], ['year_to', r.year_to?.toString()], ['owned', r.owned?.toString()]];
  for (const [key, value] of fields) if (value) p.set(key, value);
  return `/library?${p}`;
}
export function useSavedViews() {
 const user = useUser();
 return useQuery({ queryKey: privateKey(user.id, 'saved-views'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/v1/saved-views', { signal })).items });
}
export function SavedViewLinks() {
 const views = useSavedViews();
 if (!views.data?.length) return null;
 return <div className="nav-group"><p className="nav-label">{t('Saved views')}</p><nav aria-label={t('Saved views')}>{views.data.map(v => <Link key={v.id} to={savedViewURL(v)} title={v.name}><Bookmark size={20} aria-hidden /><span className="nav-text">{v.name}</span></Link>)}</nav></div>;
}
/** Saves the current filters under a name. `sort` is null for list order, which only a list keeps. */
export function SavedViewControls({ params, sort, layout }: { params: URLSearchParams; sort: View['sort'] | null; layout: View['layout'] }) {
 const navigate=useNavigate(); const views = useSavedViews(); const user = useUser(); const client = useQueryClient(); const [name, setName] = useState(''); const [editing,setEditing]=useState<View|null>(null);
 const refresh = () => client.invalidateQueries({ queryKey: privateKey(user.id, 'saved-views') });
 const save = useAction(async () => {
  if (!sort) return;
  const list = (k: string) => params.get(k)?.split(',').filter(Boolean);
  const number = (k: string) => params.has(k) ? Number(params.get(k)) : undefined;
  const body: Schema['SavedViewInput'] = { name, sort, layout, position: editing?.position ?? views.data?.length ?? 0, version: editing?.version, rules: { query: params.get('q') ?? undefined, media_types: list('media_type') as Schema['Rules']['media_types'], statuses: list('status') as Schema['Rules']['statuses'], tag_ids: list('tag_id'), genres: list('genre'), min_rating: number('min_rating'), year_from: number('year_from'), year_to: number('year_to'), owned: params.has('owned') ? params.get('owned') === 'true' : undefined } };
  if(editing) unwrap(await api.PUT('/api/v1/saved-views/{viewID}',{params:{path:{viewID:editing.id}},body})); else unwrap(await api.POST('/api/v1/saved-views',{body}));
  setName(''); setEditing(null); await refresh();
 });
 const remove = useAction(async (v: View) => { unwrap(await api.DELETE('/api/v1/saved-views/{viewID}', { params: { path: { viewID: v.id } }, body: { version: v.version } })); await refresh(); });
 const reorder = useAction(async (index: number) => { const ids = (views.data ?? []).map(v => v.id); [ids[index-1],ids[index]]=[ids[index],ids[index-1]]; unwrap(await api.PUT('/api/v1/saved-views/order', { body: { ids } })); await refresh(); });
 return <div className="saved-view-controls">
  <form className="toolbar" onSubmit={submit(() => save.run(undefined))}><label className="saved-view-name">{t('View name')}<input maxLength={80} required value={name} onChange={e => setName(e.target.value)} /></label><button className="button" disabled={save.pending || !sort}>{editing ? t('Save changes') : t('Save current view')}</button>{editing && <button type="button" className="button" onClick={()=>{setEditing(null);setName('')}}>{t('Cancel')}</button>}</form>
  <ErrorNote error={save.error ?? remove.error ?? reorder.error ?? views.error} />
  {!!views.data?.length && <ul>{views.data.map((v, i) => <li key={v.id}><Link to={savedViewURL(v)}>{v.name}</Link><button className="text-button" onClick={()=>{navigate(savedViewURL(v));setEditing(v);setName(v.name)}}>{t('Edit')}</button>{i > 0 && <button className="text-button" disabled={reorder.pending} onClick={() => void reorder.run(i)} aria-label={t('Move {name} up', { name: v.name })}>{t('Move up')}</button>}<button className="text-button" disabled={remove.pending} onClick={() => void remove.run(v)} aria-label={t('Delete {name}', { name: v.name })}>{t('Delete')}</button></li>)}</ul>}
 </div>;
}
