import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readLibraryPage,libraryArtworkPending} from '../../web/src/libraryPageRequest.ts';
test('pending pages poll the original request once and preserve terminal rejection',async()=>{
 let submitted=0;const ids=[];
 const result=await readLibraryPage(async()=>{submitted++;return {request_id:'one',status:'pending'}},async(id)=>{ids.push(id);return {request_id:id,status:ids.length===1?'pending':'denied'}},new AbortController().signal,{pollMs:1,timeoutMs:1000});
 assert.equal(submitted,1);assert.deepEqual(ids,['one','one']);assert.equal(result.status,'denied');
});
test('an unavailable bridge ends with a timeout instead of recreating requests',async()=>{
 let submitted=0;
 await assert.rejects(readLibraryPage(async()=>{submitted++;return {request_id:'one',status:'pending'}},async()=>({request_id:'one',status:'pending'}),new AbortController().signal,{pollMs:1,timeoutMs:10}),{name:'TimeoutError'});
 assert.equal(submitted,1);
});
test('cancellation stops the pending request',async()=>{
 const controller=new AbortController();let reads=0;
 await assert.rejects(readLibraryPage(async()=>({request_id:'one',status:'pending'}),async()=>{reads++;controller.abort();return {request_id:'one',status:'pending'}},controller.signal,{pollMs:1,timeoutMs:1000}),{name:'AbortError'});
 assert.equal(reads,1);
});
test('a cached ready page still reads the viewer projection for resolved posters and title actions',async()=>{
 const raw={request_id:'one',status:'ready',page:{items:[{content_id:'movie',title:'Synthetic film'}]}};
 const projected={...raw,page:{items:[{...raw.page.items[0],catalog_id:'123',image_url:'https://images.example.test/poster.jpg'}]}};
 let reads=0;
 const result=await readLibraryPage(async()=>raw,async id=>{assert.equal(id,'one');reads++;return projected},new AbortController().signal);
 assert.equal(reads,1);assert.equal(result.page.items[0].image_url,projected.page.items[0].image_url);assert.equal(result.page.items[0].catalog_id,'123');
});

test('poster enrichment polls only resolvable visible movies and stops at its deadline',()=>{
 const page={status:'ready',page:{expires_at:'2026-10-13T12:02:00Z',items:[{kind:'movie',external_ids:{tmdb:'123'},catalog_id:undefined}]}};
 const now=Date.parse('2026-10-13T12:00:00Z');
 assert.equal(libraryArtworkPending(page,now+45000,now),true);
 assert.equal(libraryArtworkPending(page,now+45000,now+45000),false);
 assert.equal(libraryArtworkPending({...page,page:{...page.page,items:[{kind:'movie',catalog_id:'123',external_ids:{tmdb:'123'}}]}},now+45000,now),false);
 assert.equal(libraryArtworkPending({...page,page:{...page.page,items:[{kind:'movie',external_ids:{}}]}},now+45000,now),false);
 assert.equal(libraryArtworkPending({...page,page:{...page.page,expires_at:'2026-10-13T11:59:00Z'}},now+45000,now),false);
});

test('book poster polling requires an exact ISBN or audiobook ASIN',()=>{
 const now=Date.parse('2026-10-13T12:00:00Z');
 const result=items=>({status:'ready',page:{expires_at:'2026-10-13T12:02:00Z',items}});
 assert.equal(libraryArtworkPending(result([{kind:'ebook',external_ids:{isbn:'9780140328721'}}]),now+45000,now),true);
 assert.equal(libraryArtworkPending(result([{kind:'audiobook',external_ids:{asin:'B012345678'}}]),now+45000,now),true);
 assert.equal(libraryArtworkPending(result([{kind:'ebook',external_ids:{asin:'B012345678'}}]),now+45000,now),false);
 assert.equal(libraryArtworkPending(result([{kind:'audiobook',catalog_id:'123',external_ids:{asin:'B012345678'}}]),now+45000,now),false);
});

test('source titles without metadata IDs do not fail the page',()=>{
 const now=Date.parse('2026-10-13T12:00:00Z');
 const page={status:'ready',page:{expires_at:'2026-10-13T12:02:00Z',items:[{kind:'movie',external_ids:null},{kind:'ebook',external_ids:null},{kind:'audiobook',external_ids:null}]}};
 assert.equal(libraryArtworkPending(page,now+45000,now),false);
});
