import { test } from 'node:test';
import assert from 'node:assert/strict';
import { artworkAllowed,artworkFailed } from '../../web/src/artworkPolicy.ts';
test('failed artwork backs off across remounts without blocking unrelated URLs',()=>{
 artworkFailed('https://synthetic.test/a',1000);
 assert.equal(artworkAllowed('https://synthetic.test/a',2000),false);
 assert.equal(artworkAllowed('https://synthetic.test/b',2000),true);
 assert.equal(artworkAllowed('https://synthetic.test/a',301001),true);
});
