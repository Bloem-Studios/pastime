import {test} from 'node:test';
import assert from 'node:assert/strict';
import {bookMutation,copyMutation,libraryLeaseValid} from '../../web/src/bloemLibraryPolicy.ts';
const now=Date.parse('2026-10-13T12:00:00Z');
const lease='2026-10-13T12:01:00Z';
const book={content_id:'book',kind:'ebook',title:'Book',external_ids:{isbn:'9780000000000'},edition_id:'edition',isbn:'9780000000000',file_id:7,ebook_location:'epubcfi(/6/2)',ebook_fraction:0.25,audiobook_seconds:0,audiobook_duration_seconds:0,completed:false,reset:false,changed_at:null,revision:12};
test('book edits bind exact source edition, locator and revision without converting units',()=>{
 const intent=bookMutation(book,'book.progress',lease,now,{location:'epubcfi(/6/8)',position:0.5});
 assert.equal(intent.expected_revision,12);assert.equal(intent.target_id,'book');
 assert.equal(intent.book_state.edition_id,'edition');assert.equal(intent.book_state.file_id,7);
 assert.equal(intent.book_state.ebook_location,'epubcfi(/6/8)');assert.equal(intent.book_state.ebook_fraction,0.5);
 assert.equal(intent.book_state.audiobook_seconds,0);assert.equal(book.ebook_fraction,0.25);
 const audio=bookMutation({...book,kind:'audiobook',ebook_location:'',ebook_fraction:0,audiobook_duration_seconds:3600},'book.progress',lease,now,{position:72});
 assert.equal(audio.book_state.audiobook_seconds,72);assert.equal(audio.book_state.ebook_fraction,0);
});
test('expired authority and unsupported book positions never queue an edit',()=>{
 assert.equal(libraryLeaseValid(lease,now+60000),false);
 assert.equal(libraryLeaseValid('bad',now),false);
 for(const changes of [{position:1.5,location:'cfi'},{position:NaN,location:'cfi'},{position:0.5,location:''}])assert.throws(()=>bookMutation(book,'book.progress',lease,now,changes));
 assert.throws(()=>bookMutation(book,'book.reset',lease,now+60000));
 assert.throws(()=>bookMutation({...book,edition_id:''},'book.completed',lease,now));
 assert.throws(()=>bookMutation({...book,kind:'audiobook',audiobook_duration_seconds:60},'book.progress',lease,now,{position:61}));
});
test('explicit copy edits preserve technical and custom fields and reject stale authority',()=>{
 const copy={id:'copy',content_id:'movie',revision:9,custom:{shelf:'A'},price_cents:1999,format:'disc',bitrate_kbps:7000};
 const intent=copyMutation(copy,'copy.upsert',lease,now,{notes:'new note'});
 assert.equal(intent.expected_revision,9);assert.equal(intent.copy.price_cents,1999);assert.deepEqual(intent.copy.custom,{shelf:'A'});assert.equal(intent.copy.bitrate_kbps,7000);assert.equal(copy.notes,undefined);
 assert.throws(()=>copyMutation(copy,'copy.remove',lease,now+60000));
 assert.throws(()=>copyMutation(copy,'copy.upsert',lease,now,{price_cents:1.5}));
});
