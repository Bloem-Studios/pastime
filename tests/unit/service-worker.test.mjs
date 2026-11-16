import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
test('asset network response survives cache failures', async () => {
 for (const failure of ['open','match','put','keys']) {
 const handlers = {};
 const cache = { match: async () => {if(failure==='match') throw Error('cache');}, put: async () => {if(failure==='put') throw Error('quota');}, keys: async () => {if(failure==='keys') throw Error('cache');return [];}, delete: async () => true };
 runInNewContext(readFileSync('web/public/sw.js','utf8'), {URL, Response, self:{location:{origin:'https://synthetic.invalid'},addEventListener:(name,fn)=>handlers[name]=fn},caches:{open:async()=>{if(failure==='open') throw Error('cache');return cache;}},fetch:async()=>new Response('asset')});
 let response;const tasks=[];
 handlers.fetch({request:{method:'GET',url:'https://synthetic.invalid/assets/main.js'},respondWith:p=>response=p,waitUntil:p=>tasks.push(p)});
 assert.equal(await (await response).text(),'asset');await Promise.all(tasks);
 }
});
