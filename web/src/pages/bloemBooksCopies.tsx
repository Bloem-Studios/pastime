import {useState} from 'react';
import {api,unwrap,ApiError,type Schema} from '../api/client';
import {bookMutation,copyMutation,libraryLeaseValid} from '../bloemLibraryPolicy';
import {useConnectedLibraryPage,useLibraryLease,useBloemCollectionMutation} from './bloemLibrary';
import {ChangeStatus} from './bloemCollections';
import {Empty,ErrorNote,Failed,Loading,Modal,useConfirm,useAction} from '../ui';
import {t} from '../i18n';
import {useQuery} from '@tanstack/react-query';
import {privateKey,useUser,useRefresh} from '../state';

type Copy=Schema['BloemLibraryCopy'];
type Book=Schema['BloemBookState'];
function useSourcePage(instance:string,view:'copies'|'book_states',parent=''){
 const [cursors,setCursors]=useState(['']);const [refresh,setRefresh]=useState(0);const now=useLibraryLease();
 const query=useConnectedLibraryPage({instance_id:instance,view,parent_id:parent,cursor:cursors.at(-1)!,limit:24,kind:'',query:''},refresh);
 const restart=()=>{setCursors(['']);setRefresh(n=>n+1)};
 const page=query.data?.page;const valid=libraryLeaseValid(page?.expires_at,now);
 const navigation=<div className="toolbar"><button className="button" onClick={restart}>{t('Refresh')}</button><button className="button" disabled={cursors.length===1} onClick={()=>setCursors(c=>c.slice(0,-1))}>{t('Previous')}</button><button className="button" disabled={!valid||!page?.next_cursor||query.data?.status==='pending'} onClick={()=>{if(page?.next_cursor)setCursors(c=>[...c,page.next_cursor])}}>{t('Next')}</button></div>;
 const unavailable=query.error?<Failed error={query.error} retry={restart}/>:query.isPending||query.data?.status==='pending'?<Loading/>:!valid?<Empty title={t('This page has expired')}><p>{t('Refresh to check your current Silo access.')}</p></Empty>:null;
 return {page,valid,restart,navigation,unavailable};
}
export function BloemCopies({instance}:{instance:string}){
 const source=useSourcePage(instance,'copies');const [selected,setSelected]=useState<Copy>();const [importing,setImporting]=useState<Copy>();const [imported,setImported]=useState(false);
 return <>{imported&&<p role="status">{t('Copy imported into Owned.')}</p>}<p className="subtle">{t('Only copies explicitly recorded in Silo appear here. Available media does not imply ownership.')}</p>{source.navigation}{source.unavailable??(source.page?.copies.length?<div className="list-cards">{source.page.copies.filter(copy=>!copy.removed).map(copy=><article className="list-card" key={copy.id}><div className="list-card-body"><strong>{copy.title}</strong><span>{[copy.format,copy.edition,copy.store].filter(Boolean).join(' · ')}</span><span className="subtle">{copy.notes}</span><button className="button" onClick={()=>setSelected(copy)}>{t('Edit copy')}</button><button className="button" onClick={()=>{setImported(false);setImporting(copy)}}>{t('Import into Owned')}</button></div></article>)}</div>:<Empty title={t('Nothing recorded yet')}>{t('Only record a copy you own.')}</Empty>)}
 {selected&&source.valid&&<CopyEditor key={selected.id} instance={instance} copy={selected} expires={source.page!.expires_at} onClose={()=>setSelected(undefined)} onApplied={()=>{setSelected(undefined);source.restart()}}/>}{importing&&source.valid&&<NativeCopyImporter key={importing.id} copy={importing} requestID={source.page!.request_id} expires={source.page!.expires_at} onClose={()=>setImporting(undefined)} onApplied={()=>{setImporting(undefined);setImported(true)}}/>}</>;
}

function NativeCopyImporter({copy,requestID,expires,onClose,onApplied}:{copy:Copy;requestID:string;expires:string;onClose:()=>void;onApplied:()=>void}){
 const user=useUser();const refresh=useRefresh();const now=useLibraryLease();const valid=libraryLeaseValid(expires,now);
 const [search,setSearch]=useState(copy.title);const [query,setQuery]=useState(copy.title);const [cursors,setCursors]=useState(['']);const [entryID,setEntryID]=useState('');
 const mediaType=copy.kind==='ebook'?'book':copy.kind;
 const entries=useQuery({queryKey:privateKey(user.id,'bloem-copy-targets',mediaType,query,cursors.at(-1)),enabled:valid,retry:false,queryFn:async({signal})=>unwrap(await api.GET('/api/v1/library',{signal,params:{query:{q:query,media_type:mediaType,cursor:cursors.at(-1),limit:20}}}))});
 const action=useAction(async(id:string)=>{
  if(!libraryLeaseValid(expires))throw new ApiError(409,'expired_source_page',t('Refresh to check your current Silo access.'));
  const out=unwrap(await api.POST('/api/v1/bloem/library/copies:import',{body:{request_id:requestID,copy_id:copy.id,entry_id:id}}));
  void refresh();onApplied();return out;
 });
 const blocked=action.error instanceof ApiError&&[401,403,409,422].includes(action.error.status);
 return <Modal title={t('Import into Owned')} description={t('Choose an existing library title. Its catalog identity must match this copy exactly.')} onClose={()=>{if(!action.pending)onClose()}}>
 <p><strong>{copy.title}</strong> · {copy.format}</p>
 <form onSubmit={e=>{e.preventDefault();if(!action.pending){setQuery(search);setCursors(['']);setEntryID('')}}}><label className="field">{t('Search your library')}<input value={search} maxLength={256} disabled={action.pending} onChange={e=>setSearch(e.target.value)}/></label><button className="button" disabled={!valid||action.pending}>{t('Search')}</button></form>
 {entries.isPending?<Loading/>:entries.error?<Failed error={entries.error} retry={()=>void entries.refetch()}/>:entries.data?.items.length?<div className="list-cards">{entries.data.items.map(entry=><label className="list-card" key={entry.id}><input type="radio" name="native-copy-target" checked={entryID===entry.id} disabled={action.pending} onChange={()=>setEntryID(entry.id)}/><span>{entry.title}</span></label>)}</div>:<Empty title={t('No matching title was found.')}>{t('Search your library')}</Empty>}
 <div className="toolbar"><button className="button" disabled={cursors.length===1||action.pending} onClick={()=>{setEntryID('');setCursors(c=>c.slice(0,-1))}}>{t('Previous')}</button><button className="button" disabled={!entries.data?.next_cursor||action.pending} onClick={()=>{if(entries.data?.next_cursor){setEntryID('');setCursors(c=>[...c,entries.data!.next_cursor!])}}}>{t('Next')}</button></div>
 <ErrorNote error={action.error}/>{!valid&&<p role="alert">{t('Refresh to check your current Silo access.')}</p>}
 <button className="button primary" disabled={!valid||!entryID||action.pending||blocked} onClick={()=>void action.run(entryID)}>{t('Import copy')}</button>
 </Modal>;
}

export function RecordBloemCopy({instance,item,expires}:{instance:string;item:Schema['BloemLibraryItem'];expires:string}){
 const [copy,setCopy]=useState<Copy>();
 return <><button className="button small" onClick={()=>{if(libraryLeaseValid(expires))setCopy({id:crypto.randomUUID(),content_id:item.content_id,kind:item.kind,title:item.title,external_ids:item.external_ids,format:'',edition:'',resolution:'',hdr:'',audio:'',bitrate_kbps:0,acquired_on:'',price_cents:0,currency:'',store:'',location:'',notes:'',custom:{},removed:false,revision:0})}}>{t('Add a copy')}</button>{copy&&<CopyEditor instance={instance} copy={copy} expires={expires} onClose={()=>setCopy(undefined)} onApplied={()=>setCopy(undefined)}/>}</>;
}
function CopyEditor({instance,copy,expires,onClose,onApplied}:{instance:string;copy:Copy;expires:string;onClose:()=>void;onApplied:()=>void}){
 const [draft,setDraft]=useState(copy);const [error,setError]=useState<Error>();const now=useLibraryLease();const valid=libraryLeaseValid(expires,now);const confirm=useConfirm();const change=useBloemCollectionMutation(instance,onApplied);
 const submit=(operation:'copy.upsert'|'copy.remove')=>{try{setError(undefined);void change.run(copyMutation(copy,operation,expires,Date.now(),draft))}catch(e){setError(new ApiError(400,'invalid_connected_edit',t('Check the entered values and refresh if the page has expired.')))}};
 return <Modal title={copy.revision?t('Edit copy'):t('Add a copy')} description={t('Changes are saved to your Silo profile.')} onClose={()=>{if(!change.busy)onClose()}}><form onSubmit={e=>{e.preventDefault();submit('copy.upsert')}}>
 <p><strong>{copy.title}</strong></p><p className="subtle">{t('Only record a copy you own.')}</p>
 {(['format','edition','acquired_on','currency','store','location','notes'] as const).map(key=><label className="field" key={key}>{{format:t('Format'),edition:t('Edition'),acquired_on:t('Acquired on'),currency:t('Currency'),store:t('Store'),location:t('Location'),notes:t('Notes')}[key]}<input type={key==='acquired_on'?'date':'text'} value={draft[key]} maxLength={key==='notes'?2000:200} onChange={e=>setDraft(d=>({...d,[key]:e.target.value}))}/></label>)}
 <label className="field">{t('Price in cents')}<input type="number" min="0" step="1" value={draft.price_cents} onChange={e=>setDraft(d=>({...d,price_cents:Number(e.target.value)}))}/></label>
 <ErrorNote error={error}/><ChangeStatus change={change}/>{!valid&&<p role="alert">{t('Refresh to check your current Silo access.')}</p>}
 <div className="toolbar"><button className="button primary" disabled={!valid||change.busy||change.blocked||!draft.format.trim()}>{t('Save')}</button>{copy.revision>0&&<button type="button" className="button" disabled={!valid||change.busy||change.blocked} onClick={async()=>{if(await confirm({title:t('Remove copy?'),description:t('This removes your owned copy record from Silo.'),danger:true}))submit('copy.remove')}}>{t('Remove')}</button>}</div>
 </form></Modal>;
}
export function BloemBooks({instance}:{instance:string}){
 const source=useSourcePage(instance,'book_states');const [selected,setSelected]=useState<Book>();
 return <>{source.navigation}{source.unavailable??(source.page?.book_states.length?<div className="list-cards">{source.page.book_states.map(book=><article className="list-card" key={book.content_id}><div className="list-card-body"><strong>{book.title}</strong><span>{book.kind==='ebook'?t('Books'):t('Audiobooks')} · {book.completed?t('Completed'):book.reset?t('Not started'):book.kind==='ebook'?`${Math.round(book.ebook_fraction*100)}%`:`${book.audiobook_seconds} / ${book.audiobook_duration_seconds} ${t('Seconds')}`}</span><button className="button" onClick={()=>setSelected(book)}>{t('Edit book progress')}</button></div></article>)}</div>:<Empty title={t('No book progress recorded')}>{t('Open a book in Media to record progress.')}</Empty>)}{selected&&source.valid&&<BookEditor key={selected.content_id} instance={instance} book={selected} expires={source.page!.expires_at} onClose={()=>setSelected(undefined)} onApplied={()=>{setSelected(undefined);source.restart()}}/>}</>;
}
export function BloemBookButton({instance,item}:{instance:string;item:Schema['BloemLibraryItem']}){
 const [open,setOpen]=useState(false);
 if(item.kind!=='ebook'&&item.kind!=='audiobook')return null;
 return <><button className="button small" onClick={()=>setOpen(true)}>{t('Edit book progress')}</button>{open&&<BookLookup instance={instance} id={item.content_id} onClose={()=>setOpen(false)}/>}</>;
}
function BookLookup({instance,id,onClose}:{instance:string;id:string;onClose:()=>void}){
 const source=useSourcePage(instance,'book_states',id);const book=source.page?.book_states[0];
 return source.valid&&book?<BookEditor instance={instance} book={book} expires={source.page!.expires_at} onClose={onClose} onApplied={onClose}/>:<Modal title={t('Edit book progress')} description={t('Changes are saved to your Silo profile.')} onClose={onClose}>{source.unavailable??<Empty title={t('Book edition unavailable')}>{t('Refresh to check your current Silo access.')}</Empty>} {source.navigation}</Modal>;
}
function BookEditor({instance,book,expires,onClose,onApplied}:{instance:string;book:Book;expires:string;onClose:()=>void;onApplied:()=>void}){
 const [position,setPosition]=useState(book.kind==='ebook'?book.ebook_fraction:book.audiobook_seconds);const [location,setLocation]=useState(book.ebook_location);const [error,setError]=useState<Error>();const now=useLibraryLease();const valid=libraryLeaseValid(expires,now);const confirm=useConfirm();const change=useBloemCollectionMutation(instance,onApplied);
 const submit=(operation:'book.progress'|'book.completed'|'book.reset')=>{try{setError(undefined);void change.run(bookMutation(book,operation,expires,Date.now(),{position,location}))}catch(e){setError(new ApiError(400,'invalid_connected_edit',t('Check the entered values and refresh if the page has expired.')))}};
 return <Modal title={t('Edit book progress')} description={t('Changes are saved to your Silo profile.')} onClose={()=>{if(!change.busy)onClose()}}><form onSubmit={e=>{e.preventDefault();submit('book.progress')}}>
 <p><strong>{book.title}</strong></p>{book.isbn&&<p>ISBN {book.isbn}</p>}
 <p className="subtle">{t('Progress belongs to this exact edition. Newer activity in Silo takes priority.')}</p>
 {book.kind==='ebook'&&<label className="field">{t('Exact ebook locator')}<input value={location} maxLength={4000} onChange={e=>setLocation(e.target.value)}/></label>}
 <label className="field">{book.kind==='ebook'?t('Read fraction (0 to 1)'):t('Listening position in seconds')}<input type="number" min="0" max={book.kind==='ebook'?1:book.audiobook_duration_seconds} step="any" value={position} onChange={e=>setPosition(Number(e.target.value))}/></label>
 <ErrorNote error={error}/><ChangeStatus change={change}/>{!valid&&<p role="alert">{t('Refresh to check your current Silo access.')}</p>}
 <div className="toolbar"><button className="button primary" disabled={!valid||change.busy||change.blocked}>{t('Save progress')}</button><button className="button" type="button" disabled={!valid||change.busy||change.blocked} onClick={()=>submit('book.completed')}>{t('Mark completed')}</button><button className="button" type="button" disabled={!valid||change.busy||change.blocked} onClick={async()=>{if(await confirm({title:t('Reset book progress?'),description:t('This clears the saved reading or listening position in Silo.'),danger:true}))submit('book.reset')}}>{t('Reset progress')}</button></div>
 </form></Modal>;
}
