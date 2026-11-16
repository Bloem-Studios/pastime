import type {Schema} from './api/client';
type Book=Schema['BloemBookState'];
type Copy=Schema['BloemLibraryCopy'];
type Intent=Omit<Schema['BloemLibraryMutationIntent'],'instance_id'|'event_id'>;
export function libraryLeaseValid(expires:string|undefined,now=Date.now()):boolean{
 return !!expires&&Date.parse(expires)>now;
}
function requireLease(expires:string,now:number){if(!libraryLeaseValid(expires,now))throw new Error('Refresh to check your current Silo access.');}
export function bookMutation(book:Book,operation:'book.progress'|'book.completed'|'book.reset',expires:string,now=Date.now(),edit?:{position:number;location?:string}):Intent{
 requireLease(expires,now);
 if(!book.edition_id||!Number.isSafeInteger(book.file_id)||book.file_id<=0||!Number.isSafeInteger(book.revision)||book.revision<0)throw new Error('Refresh the book edition before editing.');
 const state={...book,changed_at:new Date(now).toISOString(),completed:operation==='book.completed',reset:operation==='book.reset'};
 if(operation==='book.progress'){
  if(!edit||!Number.isFinite(edit.position)||edit.position<0)throw new Error('Enter a valid progress position.');
  if(book.kind==='ebook'){
   if(edit.position>1||!edit.location?.trim())throw new Error('Enter the exact ebook locator and a fraction from 0 to 1.');
   state.ebook_location=edit.location.trim();state.ebook_fraction=edit.position;state.audiobook_seconds=0;state.audiobook_duration_seconds=0;
  }else{
   if(!Number.isFinite(book.audiobook_duration_seconds)||book.audiobook_duration_seconds<=0||edit.position>book.audiobook_duration_seconds)throw new Error('Enter seconds within this recording.');
   state.audiobook_seconds=edit.position;state.ebook_location='';state.ebook_fraction=0;
  }
 }
 return {operation,target_id:book.content_id,expected_revision:book.revision,name:'',description:'',items:[],book_state:state};
}
export function copyMutation(copy:Copy,operation:'copy.upsert'|'copy.remove',expires:string,now=Date.now(),edit:Partial<Copy>={}):Intent{
 requireLease(expires,now);
 const value={...copy,...edit,id:copy.id,content_id:copy.content_id,kind:copy.kind,revision:copy.revision,external_ids:copy.external_ids};
 if(!Number.isSafeInteger(value.price_cents)||value.price_cents<0)throw new Error('Enter a price in whole cents.');
 return {operation,target_id:copy.id,expected_revision:copy.revision,name:'',description:'',items:[],copy:value};
}
