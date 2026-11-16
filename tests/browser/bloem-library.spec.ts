import {test,expect} from '@playwright/test';
import {mkdir} from 'node:fs/promises';

// In Silo browsing is hidden since release A (its route redirects to Home); the page
// and this spec retire together in release B.
test.skip(true, 'In Silo browsing is hidden; retires with the page in release B');

test('source selection, exact book edits and owned-copy fields work on desktop and phone',async({page})=>{
 const queries=new Map<string,any>();const mutations:any[]=[];const imports:any[]=[];
 const book={content_id:'book',kind:'ebook',title:'Synthetic edition',external_ids:{isbn:'9780000000000'},edition_id:'edition-exact',isbn:'9780000000000',file_id:7,ebook_location:'epubcfi(/6/2)',ebook_fraction:0.25,audiobook_seconds:0,audiobook_duration_seconds:0,completed:false,reset:false,changed_at:null,revision:12};
 const copy={id:'copy',content_id:'movie',kind:'movie',title:'Synthetic owned disc',external_ids:{tmdb:'123'},format:'bluray',edition:'Collector',resolution:'1080p',hdr:'',audio:'DTS',bitrate_kbps:7000,acquired_on:'2026-10-01',price_cents:1999,currency:'EUR',store:'Synthetic shop',location:'Shelf A',notes:'Original note',custom:{shelf:'A'},removed:false,revision:9};
 await page.route('**/api/**',async route=>{
  const path=new URL(route.request().url()).pathname;if(!path.startsWith('/api/'))return route.continue();let out:any={items:[]};
  if(path==='/api/v1/session')out={user:{id:'1',username:'synthetic',display_name:'Synthetic member',role:'member',active:true},csrf_token:'synthetic'};
  else if(path==='/api/v1/profiles')out={items:[{id:'1',account_id:'1',name:'Main',default:true}]};
  else if(path==='/api/v1/info')out={name:'Pastime',version:'test'};
  else if(path==='/api/v1/bloem/library/sources')out={items:[{instance_id:'a',name:'Source A'},{instance_id:'b',name:'Source B'}]};
  else if(path==='/api/v1/bloem/library/queries'){
   const body=route.request().postDataJSON();const id=String(queries.size+1);queries.set(id,body);out={request_id:id,status:"pending"};
  }else if(path.startsWith('/api/v1/bloem/library/queries/')){
   const q=queries.get(path.split('/').at(-1)!);out={request_id:path.split('/').at(-1),status:'ready',page:{...q,request_id:path.split('/').at(-1),generated_at:new Date().toISOString(),expires_at:new Date(Date.now()+120000).toISOString(),next_cursor:'',complete:true,items:[],collections:q.view==='collections'?[{id:'personal:'+q.instance_id,name:'Collection '+q.instance_id,scope:'personal',writable:true,description:'',revision:1}]:[],copies:q.view==='copies'?[copy]:[],book_states:q.view==='book_states'?[q.instance_id==='a'?{...book,kind:'audiobook',title:'Synthetic recording',ebook_location:'',ebook_fraction:0,audiobook_seconds:10,audiobook_duration_seconds:3600}:book]:[],revision:1}};
  }else if(path==='/api/v1/library')out={items:[{id:'42',title:'Synthetic native film',media_type:'movie'}],next_cursor:null};
  else if(path==='/api/v1/bloem/library/copies:import'){imports.push(route.request().postDataJSON());if(imports.length===2){await route.fulfill({status:409,json:{code:'conflict',message:'This record changed. Refresh and try again.'}});return;}out={id:'99',version:1,replayed:false,deleted:false};}
  else if(path==='/api/v1/bloem/library/mutations'){mutations.push(route.request().postDataJSON());out={request_id:String(mutations.length)};}
  else if(path.startsWith('/api/v1/bloem/library/mutations/'))out={status:'applied',request_id:path.split('/').at(-1),event_id:'event',target_id:'book',revision:13};
  await route.fulfill({json:out});
 });
 await page.goto('/library/available');
 await page.getByRole('button',{name:'Collections',exact:true}).click();
 await expect(page.getByText('Collection a',{exact:true})).toBeVisible();
 await page.getByLabel('Source',{exact:true}).selectOption('b');
 await expect(page.getByText('Collection b',{exact:true})).toBeVisible();
 await expect(page.getByText('Collection a',{exact:true})).toHaveCount(0);
 await page.getByRole('button',{name:'Book progress',exact:true}).click();
 await page.getByRole('button',{name:'Edit book progress',exact:true}).click();
 await page.getByLabel('Exact ebook locator',{exact:true}).fill('epubcfi(/6/8)');
 await page.getByLabel('Read fraction (0 to 1)',{exact:true}).fill('0.5');
 await page.getByRole('button',{name:'Save progress',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(mutations[0].instance_id).toBe('b');expect(mutations[0].expected_revision).toBe(12);expect(mutations[0].book_state.edition_id).toBe('edition-exact');expect(mutations[0].book_state.ebook_location).toBe('epubcfi(/6/8)');expect(mutations[0].book_state.ebook_fraction).toBe(0.5);
 await page.getByRole('button',{name:'Owned copies',exact:true}).click();
 await page.getByRole('button',{name:'Edit copy',exact:true}).click();
 await page.getByLabel('Notes',{exact:true}).fill('Updated note');
 await page.getByRole('button',{name:'Save',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(mutations[1].copy.custom).toEqual({shelf:'A'});expect(mutations[1].copy.price_cents).toBe(1999);expect(mutations[1].copy.audio).toBe('DTS');expect(mutations[1].copy.notes).toBe('Updated note');
 await page.getByRole('button',{name:'Import into Owned',exact:true}).click();
 await page.getByLabel('Synthetic native film',{exact:true}).check();
 await page.getByRole('button',{name:'Import copy',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 await expect(page.getByText('Copy imported into Owned.',{exact:true})).toBeVisible();
 expect(imports).toHaveLength(1);expect(imports[0].entry_id).toBe('42');expect(imports[0].copy_id).toBe('copy');expect(queries.get(imports[0].request_id).instance_id).toBe('b');
 const evidence=process.env.PASTIME_LIBRARY_EVIDENCE;
 if(evidence){await mkdir(evidence,{recursive:true});await page.screenshot({path:evidence+'/copies-desktop.png',fullPage:true,animations:'disabled'});}
 await page.setViewportSize({width:390,height:844});
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth)).toBe(390);
 if(evidence)await page.screenshot({path:evidence+'/copies-phone.png',fullPage:true,animations:'disabled'});
 await page.getByRole('button',{name:'Import into Owned',exact:true}).click();
 await page.getByLabel('Synthetic native film',{exact:true}).check();
 if(evidence)await page.screenshot({path:evidence+'/import-phone.png',fullPage:true,animations:'disabled'});
 await page.getByRole('button',{name:'Import copy',exact:true}).click();
 await expect(page.getByRole('button',{name:'Import copy',exact:true})).toBeDisabled();
 expect(imports).toHaveLength(2);
 await page.getByRole('button',{name:'Close dialog',exact:true}).click();
 await page.getByRole('button',{name:'Book progress',exact:true}).click();
 await page.getByRole('button',{name:'Edit book progress',exact:true}).click();
 if(evidence)await page.screenshot({path:evidence+'/book-phone.png',fullPage:true,animations:'disabled'});
 await page.clock.install();await page.clock.fastForward(121000);
 await expect(page.getByRole('button',{name:'Save progress',exact:true})).toHaveCount(0);
 expect(mutations).toHaveLength(2);
 await page.clock.setSystemTime(new Date());
 await page.getByLabel('Source',{exact:true}).selectOption('a');
 await page.getByRole('button',{name:'Edit book progress',exact:true}).click();
 await page.getByLabel('Listening position in seconds',{exact:true}).fill('72');
 await page.getByRole('button',{name:'Save progress',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(mutations[2].book_state.audiobook_seconds).toBe(72);expect(mutations[2].book_state.audiobook_duration_seconds).toBe(3600);expect(mutations[2].book_state.ebook_fraction).toBe(0);expect(mutations[2].instance_id).toBe('a');
});

test('large inventory is search first and projects cached posters into a bounded grid',async({page})=>{
 const submitted:any[]=[];let reads=0;let stateReads=0;
 const items=Array.from({length:24},(_,i)=>({content_id:`movie-${i}`,kind:'movie',title:`Synthetic film ${i+1}`,external_ids:i===23?null:{tmdb:String(100+i)},year:2025,available:true,catalog_id:i===23?undefined:String(100+i),image_url:i===23?undefined:'/fixture/poster.svg'}));
 const sourcePage=()=>({instance_id:'a',request_id:'page',view:'catalog',parent_id:'',generated_at:new Date().toISOString(),expires_at:new Date(Date.now()+120000).toISOString(),complete:false,next_cursor:'next',items,collections:[],copies:[],book_states:[],revision:1});
 await page.route('**/fixture/poster.svg',route=>route.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="200" height="300"><rect width="200" height="300" fill="#164d59"/><circle cx="100" cy="125" r="60" fill="#487d82"/><text x="100" y="240" text-anchor="middle" fill="white" font-size="18">Synthetic film</text></svg>'}));
 await page.route('**/api/**',async route=>{
  const path=new URL(route.request().url()).pathname;if(!path.startsWith('/api/'))return route.continue();let out:any={items:[]};
  if(path==='/api/v1/session')out={user:{id:'1',username:'synthetic',display_name:'Synthetic member',role:'member',active:true},csrf_token:'synthetic'};
  else if(path==='/api/v1/profiles')out={items:[{id:'1',account_id:'1',name:'Main',default:true}]};
  else if(path==='/api/v1/info')out={name:'Pastime',version:'test'};
  else if(path==='/api/v1/bloem/library/sources')out={items:[{instance_id:'a',name:'Silo'}]};
  else if(path==='/api/v1/bloem/library/queries'){submitted.push(route.request().postDataJSON());out={request_id:'page',status:'ready',page:{...sourcePage(),items:items.map(({image_url,catalog_id,...item})=>item)}};}
  else if(path==='/api/v1/bloem/library/queries/page'){reads++;out={request_id:'page',status:'ready',page:sourcePage()};}
  else if(path.endsWith('/personal-state')){stateReads++;out={favourite:false,watchlist:false};}
  await route.fulfill({json:out});
 });
 await page.goto('/library/available');
 await expect(page.getByRole('heading',{name:'Search your Silo library',exact:true})).toBeVisible();
 expect(submitted).toHaveLength(0);
 const heading=await page.getByRole('heading',{name:'In Silo',exact:true}).boundingBox();const banner=await page.getByRole('banner').boundingBox();expect(heading!.y).toBeGreaterThanOrEqual(banner!.y+banner!.height);
 await page.getByLabel('Type',{exact:true}).selectOption('movie');
 await page.getByLabel('Search',{exact:true}).fill('Synthetic');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page.locator('.inventory-grid .poster-card')).toHaveCount(24);
 await expect(page.getByText('The response was not received. Retry the same action to check its outcome.',{exact:true})).toHaveCount(0);
 expect(submitted[0].query).toBe('Synthetic');expect(submitted[0].kind).toBe('movie');expect(submitted[0].limit).toBe(24);expect(reads).toBe(1);expect(stateReads).toBe(0);
 const cover=page.locator('.inventory-grid img').first();await expect(cover).toHaveAttribute('src','/fixture/poster.svg');
 await expect.poll(()=>cover.evaluate((node:HTMLImageElement)=>node.naturalWidth)).toBe(200);
 const cards=page.locator('.inventory-grid .poster-card');const first=await cards.nth(0).boundingBox();const sixth=await cards.nth(5).boundingBox();expect(sixth!.y).toBeGreaterThan(first!.y);
 await cards.first().getByText('Actions',{exact:true}).click();await expect.poll(()=>stateReads).toBe(1);
 expect(page.url()).toContain('q=Synthetic');
 const evidence=process.env.PASTIME_LIBRARY_EVIDENCE;if(evidence){await mkdir(evidence,{recursive:true});await page.screenshot({path:evidence+'/inventory-desktop.png',fullPage:true,animations:'disabled'});}
 await page.setViewportSize({width:390,height:844});await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth)).toBe(390);
 if(evidence)await page.screenshot({path:evidence+'/inventory-phone.png',fullPage:true,animations:'disabled'});
 await page.getByLabel('Type',{exact:true}).selectOption('series');
 await expect(page).toHaveURL(/type=series/);
 await page.getByRole('button',{name:'Next',exact:true}).click();
 await expect(page.getByText('Page 2',{exact:true})).toBeVisible();
 await page.getByLabel('Search',{exact:true}).fill('Another');
 await page.getByRole('button',{name:'Search',exact:true}).click();
 await expect(page).toHaveURL(/q=Another/);
 await expect(page.getByText('Page 1',{exact:true})).toBeVisible();
 await page.goBack();
 await expect(page.getByLabel('Search',{exact:true})).toHaveValue('Synthetic');
 await expect(page.getByLabel('Type',{exact:true})).toHaveValue('series');
 await expect(page.getByText('Page 1',{exact:true})).toBeVisible();
 await page.goBack();
 await expect(page.getByLabel('Search',{exact:true})).toHaveValue('Synthetic');
 await expect(page.getByLabel('Type',{exact:true})).toHaveValue('movie');
 await expect(page.getByText('Page 1',{exact:true})).toBeVisible();
 await page.goForward();
 await expect(page.getByLabel('Search',{exact:true})).toHaveValue('Synthetic');
 await expect(page.getByLabel('Type',{exact:true})).toHaveValue('series');
 await expect(page.getByText('Page 1',{exact:true})).toBeVisible();
 await page.goForward();
 await expect(page.getByLabel('Search',{exact:true})).toHaveValue('Another');
 await expect(page.getByLabel('Type',{exact:true})).toHaveValue('series');
 await expect(page.getByText('Page 1',{exact:true})).toBeVisible();
 expect(submitted.filter(body=>body.query==='Another').every(body=>body.cursor==='')).toBe(true);
 await expect(page.locator('.inventory-grid .poster-card')).toHaveCount(24);
 await page.clock.install();
 await page.clock.fastForward(121000);
 await expect(page.getByRole('heading',{name:'This page has expired',exact:true})).toBeVisible();
 await expect(page.locator('.inventory-grid .poster-card')).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Next',exact:true})).toBeDisabled();
});
