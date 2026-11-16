import {useState} from 'react';
import {type Schema} from '../api/client';
import {useConnectedLibraryPage,useLibraryLease,useBloemCollectionMutation} from './bloemLibrary';
import {Cover,typeLabel} from '../media';
import {SourceLink} from '../navigation';
import {Empty,ErrorNote,Failed,Loading,Modal,useConfirm} from '../ui';
import {PersonalMembershipControls} from './discover';
import {t} from '../i18n';

type Collection=Schema['BloemLibraryCollection'];
export function ChangeStatus({change}:{change:ReturnType<typeof useBloemCollectionMutation>}){
 return <><ErrorNote error={change.error??change.result.error}/>{change.busy&&<p className="notice" role="status">{t('Saving your change in Silo…')}</p>}{change.blocked&&<p className="notice" role="alert">{t('This change could not be confirmed. Refresh the collection and try again.')} <button className="button small" onClick={change.clear}>{t('Dismiss')}</button></p>}</>;
}
export function BloemCollections({instance}:{instance:string}){
 const [cursor,setCursor]=useState('');const [refresh,setRefresh]=useState(0);const [selected,setSelected]=useState<Collection>();const [creating,setCreating]=useState(false);const [name,setName]=useState('');
 const now=useLibraryLease();const query=useConnectedLibraryPage({instance_id:instance,view:'collections',parent_id:'',cursor,limit:100,kind:'',query:''},refresh);
 const page=query.data?.page;const valid=page&&Date.parse(page.expires_at)>now;
 const applied=()=>{setCreating(false);setName('');setSelected(undefined);setCursor('');setRefresh(n=>n+1)};
 const change=useBloemCollectionMutation(instance,applied);
 if(selected)return <BloemCollectionItems key={selected.id} instance={instance} collection={selected} onBack={()=>{setSelected(undefined);setRefresh(n=>n+1)}}/>;
 return <>
 <div className="toolbar"><button className="button primary" onClick={()=>setCreating(true)} disabled={change.busy}>{t('Create collection in Silo')}</button><button className="button" onClick={()=>{setCursor('');setRefresh(n=>n+1)}}>{t('Refresh')}</button></div>
 <ChangeStatus change={change}/>
 {query.error?<Failed error={query.error} retry={()=>setRefresh(n=>n+1)}/>:query.isPending||query.data?.status==='pending'?<Loading/>:!valid?<Empty title={t('This page has expired')}><p>{t('Refresh to check your current Silo access.')}</p></Empty>:page.collections.length?<div className="list-cards">{page.collections.map(c=><button className="list-card" key={c.id} onClick={()=>setSelected(c)}><span className="list-card-body"><strong>{c.name}</strong>{c.description&&<span className="subtle clamp">{c.description}</span>}<span className="list-meta">{c.scope==='personal'?t('Personal'):c.scope==='shared'?t('Shared'):t('Server collection')} · {c.writable?t('Editable'):t('Read only')}</span></span></button>)}</div>:<Empty title={t('No Silo collections')}><p>{t('Create a personal collection or ask your administrator about shared collections.')}</p></Empty>}
 {valid&&!page.complete&&<button className="button" onClick={()=>setCursor(page.next_cursor)}>{t('Next')}</button>}
 {creating&&<Modal description={t('Changes are saved to your Silo profile.')} title={t('Create collection in Silo')} onClose={()=>{if(!change.busy)setCreating(false)}}><form onSubmit={e=>{e.preventDefault();void change.run({operation:'collection.create',target_id:'',expected_revision:0,name:name.trim(),description:'',items:[]})}}><label>{t('Name')}<input value={name} maxLength={200} onChange={e=>setName(e.target.value)} required/></label><ChangeStatus change={change}/><button className="button primary" disabled={change.busy||!name.trim()}>{t('Create collection')}</button></form></Modal>}
 </>;
}
function BloemCollectionItems({instance,collection,onBack}:{instance:string;collection:Collection;onBack:()=>void}){
 const [cursor,setCursor]=useState('');const [refresh,setRefresh]=useState(0);const [renaming,setRenaming]=useState(false);const [name,setName]=useState(collection.name);const confirm=useConfirm();const now=useLibraryLease();
 const query=useConnectedLibraryPage({instance_id:instance,view:'collection_items',parent_id:collection.id,cursor,limit:24,kind:'',query:''},refresh);
 const page=query.data?.page;const valid=page&&Date.parse(page.expires_at)>now;
 const change=useBloemCollectionMutation(instance,()=>{setCursor('');setRefresh(n=>n+1);setRenaming(false)});
 const intent=(operation:Schema['BloemLibraryMutationIntent']['operation'],items:Schema['BloemLibraryItem'][]=[])=>({operation,target_id:collection.id,expected_revision:page?.collections[0]?.revision??collection.revision,name:operation==='collection.update'?name.trim():'',description:collection.description,items});
 return <>
 <div className="toolbar"><button className="button" onClick={onBack}>{t('Back to collections')}</button><h2>{name}</h2>{collection.writable&&<><button className="button" onClick={()=>setRenaming(true)} disabled={change.busy}>{t('Rename')}</button><button className="button" disabled={change.busy} onClick={async()=>{if(await confirm({title:t('Delete collection?'),description:t('This removes the collection from Silo.'),danger:true}))void change.run(intent('collection.delete'))}}>{t('Delete')}</button></>}</div>
 <ChangeStatus change={change}/>
 {query.error?<Failed error={query.error} retry={()=>{setCursor('');setRefresh(n=>n+1)}}/>:query.isPending||query.data?.status==='pending'?<Loading/>:!valid?<Empty title={t('This page has expired')}><p>{t('Refresh to check your current Silo access.')}</p><button className="button" onClick={()=>{setCursor('');setRefresh(n=>n+1)}}>{t('Refresh')}</button></Empty>:page.items.length?<div className="poster-grid">{page.items.map(item=><article className="poster-card" key={item.content_id}><span className="art"><Cover entry={{title:item.title,media_type:item.kind==='ebook'?'book':item.kind,image_url:item.image_url}}/></span><strong>{item.title}</strong><span className="card-meta">{typeLabel(item.kind==='ebook'?'book':item.kind)}</span>{item.catalog_id&&<><SourceLink className="button small" to={`/title/${item.catalog_id}`}>{t('View title')}</SourceLink><PersonalMembershipControls item={{id:item.catalog_id}}/></>}{collection.writable&&<button className="button small" disabled={change.busy} onClick={()=>void change.run(intent('collection.remove',[{content_id:item.content_id,kind:item.kind,title:item.title,external_ids:item.external_ids,year:item.year,available:false}]))}>{t('Remove')}</button>}</article>)}</div>:<Empty title={t('This collection is empty')}><p>{t('Browse In Silo to add titles.')}</p></Empty>}
 {valid&&!page.complete&&<button className="button" onClick={()=>setCursor(page.next_cursor)}>{t('Next')}</button>}
 {renaming&&<Modal description={t('Changes are saved to your Silo profile.')} title={t('Rename collection')} onClose={()=>{if(!change.busy)setRenaming(false)}}><form onSubmit={e=>{e.preventDefault();void change.run(intent('collection.update'))}}><label>{t('Name')}<input value={name} maxLength={200} onChange={e=>setName(e.target.value)} required/></label><ChangeStatus change={change}/><button className="button primary" disabled={change.busy||!name.trim()}>{t('Save changes')}</button></form></Modal>}
 </>;
}
export function AddToBloemCollection({instance,item}:{instance:string;item:Schema['BloemLibraryItem']}){
 const [open,setOpen]=useState(false);const [cursor,setCursor]=useState('');const [refresh,setRefresh]=useState(0);const [selected,setSelected]=useState('');const now=useLibraryLease();
 const query=useConnectedLibraryPage({instance_id:instance,view:'collections',parent_id:'',cursor,limit:100,kind:'',query:''},refresh,open);
 const page=query.data?.page;const valid=page&&Date.parse(page.expires_at)>now;
 const change=useBloemCollectionMutation(instance,()=>{setOpen(false);setRefresh(n=>n+1)});
 const choices=valid?page.collections.filter(c=>c.writable):[];const choice=choices.find(c=>c.id===selected);
 return <><button className="button small" onClick={()=>setOpen(true)}>{t('Add to Silo collection')}</button>{open&&<Modal description={t('Changes are saved to your Silo profile.')} title={t('Add to Silo collection')} onClose={()=>{if(!change.busy)setOpen(false)}}><ChangeStatus change={change}/>{query.error?<Failed error={query.error}/>:query.isPending||query.data?.status==='pending'?<Loading/>:choices.length?<form onSubmit={e=>{e.preventDefault();if(choice)void change.run({operation:'collection.add',target_id:choice.id,expected_revision:choice.revision,name:'',description:'',items:[{content_id:item.content_id,kind:item.kind,title:item.title,external_ids:item.external_ids,year:item.year,available:false}]})}}><label>{t('Collection')}<select value={selected} onChange={e=>setSelected(e.target.value)} required><option value="">{t('Choose a collection')}</option>{choices.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></label><button className="button primary" disabled={change.busy||!choice}>{t('Add')}</button></form>:<p>{t('Create a personal collection in the Collections tab first.')}</p>}{valid&&!page.complete&&<button className="button" onClick={()=>{setSelected('');setCursor(page.next_cursor)}}>{t('Next')}</button>}</Modal>}</>;
}
