import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, unwrap, type Schema } from '../api/client';
import { privateKey, useUser } from '../state';
import { useTrackingProfile } from '../profileContext';
import { SourceLink } from '../navigation';
import { Cover, typeLabel } from '../media';
import {useSearchParams} from 'react-router-dom';
import { Empty, Failed, Loading, PageTitle } from '../ui';
import { PersonalMembershipControls } from './discover';
import { t } from '../i18n';
import {BloemCollections,AddToBloemCollection} from './bloemCollections';
import {useConnectedLibraryPage} from './bloemLibrary';
import {BloemBooks,BloemCopies,BloemBookButton,RecordBloemCopy} from './bloemBooksCopies';

export function AvailableMediaPage() {
 const { active } = useTrackingProfile();
 return <AvailableMedia key={active.id} />;
}
function AvailableMedia() {
 const user=useUser();const [mode,setMode]=useState<'media'|'collections'|'copies'|'books'>('media');const [selected,setSelected]=useState('');
 const sources=useQuery({queryKey:privateKey(user.id,'bloem-library-sources'),queryFn:async({signal})=>unwrap(await api.GET('/api/v1/bloem/library/sources',{signal})).items});
 return <><PageTitle title={t('In Silo')} description={t('Browse the titles your linked Silo profile can access. Track the ones you want to watch, read or listen to.')} />
 {sources.data&&sources.data.length>1&&<label className="field">{t('Source')}<select aria-label={t('Source')} value={sources.data.some(s=>s.instance_id===selected)?selected:sources.data[0].instance_id} onChange={e=>setSelected(e.target.value)}>{sources.data.map((s,i)=><option key={s.instance_id} value={s.instance_id}>{s.name} {i+1}</option>)}</select></label>}
 <div className="chip-filter" role="group" aria-label={t('Browse')}><button aria-pressed={mode==='media'} onClick={()=>setMode('media')}>{t('Media')}</button><button aria-pressed={mode==='collections'} onClick={()=>setMode('collections')}>{t('Collections')}</button><button aria-pressed={mode==='copies'} onClick={()=>setMode('copies')}>{t('Owned copies')}</button><button aria-pressed={mode==='books'} onClick={()=>setMode('books')}>{t('Book progress')}</button></div>
 {sources.isPending?<Loading/>:sources.error?<Failed error={sources.error} retry={()=>void sources.refetch()}/>:sources.data.length?<ConnectedViews key={selected||sources.data[0].instance_id} instance={sources.data.some(s=>s.instance_id===selected)?selected:sources.data[0].instance_id} mode={mode}/>:<Empty title={t('No active Silo connection')}><p>{t('Connect your Silo profile to browse its media.')}</p><SourceLink className="button" to="/integrations">{t('Integrations')}</SourceLink></Empty>}
 </>;
}
function ConnectedViews({instance,mode}:{instance:string;mode:'media'|'collections'|'copies'|'books'}){
 return mode==='media'?<ConnectedInventory instance={instance}/>:mode==='collections'?<BloemCollections instance={instance}/>:mode==='copies'?<BloemCopies instance={instance}/>:<BloemBooks instance={instance}/>;
}
function ConnectedInventory({instance}:{instance:string}){
 const [params]=useSearchParams();
 return <InventoryResults key={JSON.stringify([params.get('q'),params.get('type'),params.get('browse')])} instance={instance}/>;
}
function InventoryResults({instance}:{instance:string}){
 const [params,setParams]=useSearchParams();
 const search=params.get('q')?.trim()??'';
 const active=!!search||params.get('browse')==='all';
 const initialKind=params.get('type')??'';
 const [kind,setKind]=useState<Schema['BloemLibraryQuery']['kind']>(['movie','series','ebook','audiobook'].includes(initialKind)?initialKind as Schema['BloemLibraryQuery']['kind']:'');
 const [text,setText]=useState(search);const [cursors,setCursors]=useState<string[]>(['']);const [refresh,setRefresh]=useState(0);const [now,setNow]=useState(Date.now);
 useEffect(()=>{const timer=window.setInterval(()=>setNow(Date.now()),1000);return()=>window.clearInterval(timer)},[]);
 const cursor=cursors[cursors.length-1];
 const query=useConnectedLibraryPage({instance_id:instance,view:'catalog',parent_id:'',cursor,limit:24,query:search,kind},refresh,active);
 const page=query.data?.page;const valid=page&&Date.parse(page.expires_at)>now;
 const restart=()=>{setCursors(['']);setRefresh(n=>n+1)};
 const submitSearch=(browse=false)=>{
  const value=browse?'':text.trim();setCursors(['']);
  setParams(previous=>{const next=new URLSearchParams(previous);if(value)next.set('q',value);else next.delete('q');if(kind)next.set('type',kind);else next.delete('type');if(browse)next.set('browse','all');else next.delete('browse');return next;});
  if(browse)setText('');
 };
 return <>
 <form className="discovery-filters inventory-filters" onSubmit={e=>{e.preventDefault();submitSearch()}}>
  <label>{t('Type')}<select aria-label={t('Type')} value={kind} onChange={e=>{const value=e.target.value as typeof kind;setKind(value);setCursors(['']);if(active)setParams(previous=>{const next=new URLSearchParams(previous);if(value)next.set('type',value);else next.delete('type');return next;});}}><option value="">{t('All')}</option><option value="movie">{t('Movies')}</option><option value="series">{t('TV shows')}</option><option value="ebook">{t('Books')}</option><option value="audiobook">{t('Audiobooks')}</option></select></label>
  <label>{t('Search')}<input type="search" value={text} maxLength={256} onChange={e=>setText(e.target.value)} placeholder={t('Search titles')}/></label>
  <button className="button primary" type="submit" disabled={!text.trim()}>{t('Search')}</button>
  <button className="button" type="button" onClick={()=>submitSearch(true)}>{t('Browse all')}</button>
  {active&&<button className="button" type="button" onClick={restart}>{t('Refresh')}</button>}
 </form>
 {!active?<Empty title={t('Search your Silo library')}><p>{t('Search for a title and narrow the results by media type.')}</p></Empty>:
 query.error?<Failed error={query.error} retry={restart}/>:query.isPending||query.data?.status==='pending'?<Loading label={t('Loading titles from Silo…')}/>:!valid?<Empty title={t('This page has expired')}><p>{t('Refresh to check your current Silo access.')}</p><button className="button" onClick={restart}>{t('Refresh')}</button></Empty>:page.items.length?<>
 <div className="shelf-head"><h2>{t('Results')} <span className="count">{page.items.length}</span></h2></div>
 <div className="media-grid inventory-grid">{page.items.map(item=><InventoryCard key={item.content_id} instance={instance} item={item} expires={page.expires_at}/>)}</div>
 </>:<Empty title={t('No titles found')}><p>{t('Try another search or media type.')}</p></Empty>}
 {active&&<nav className="toolbar inventory-pagination" aria-label={t('Pagination')}><button className="button" disabled={cursors.length===1||query.isFetching} onClick={()=>setCursors(rows=>rows.slice(0,-1))}>{t('Previous')}</button><span className="subtle">{t('Page {page}',{page:cursors.length})}</span><button className="button" disabled={!valid||page?.complete!==false||query.isFetching} onClick={()=>{if(page?.next_cursor)setCursors(rows=>[...rows,page.next_cursor])}}>{t('Next')}</button></nav>}
 </>;
}
function InventoryCard({instance,item,expires}:{instance:string;item:Schema['BloemLibraryItem'];expires:string}){
 const [actionsOpen,setActionsOpen]=useState(false);
 const cover=<Cover entry={{title:item.title,media_type:item.kind==='ebook'?'book':item.kind,image_url:item.image_url}}/>;
 return <article className="poster-card inventory-card">
 {item.catalog_id?<SourceLink className="art" to={`/title/${item.catalog_id}`} tabIndex={-1} aria-hidden>{cover}</SourceLink>:<span className="art">{cover}</span>}
 <div>{item.catalog_id?<SourceLink className="card-title" to={`/title/${item.catalog_id}`}>{item.title}</SourceLink>:<strong className="card-title">{item.title}</strong>}<span className="card-meta">{typeLabel(item.kind==='ebook'?'book':item.kind)}{item.year>0&&` · ${item.year}`}</span><span className="card-meta">{item.available?t('Available in Silo'):t('Catalog only')}</span></div>
 {item.handoff&&<a className="button small" href={item.handoff.url} target="_blank" rel="noopener noreferrer">{item.handoff.label}</a>}
 <details className="inventory-actions" open={actionsOpen} onToggle={e=>setActionsOpen(e.currentTarget.open)}><summary className="button ghost small">{t('Actions')}</summary>{actionsOpen&&<div>
 {item.catalog_id&&(item.kind==='movie'||item.kind==='series')&&<PersonalMembershipControls item={{id:item.catalog_id}}/>}
 <AddToBloemCollection instance={instance} item={item}/><RecordBloemCopy instance={instance} item={item} expires={expires}/><BloemBookButton instance={instance} item={item}/>
 </div>}</details>
 </article>;
}
