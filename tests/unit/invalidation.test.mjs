import { test } from 'node:test';
import assert from 'node:assert/strict';
import { affectsQuery } from '../../web/src/api/invalidation.ts';
test('library edits refresh projections without refetching account administration', () => {
 for (const resource of ['library','entry','catalog','stats','home','list-entries','collection']) assert.equal(affectsQuery(['private','1',resource],'1'),true);
 for (const key of [['private','2','library'],['private','1','tokens'],['private','1','admin-users'],['catalog-search','movie']]) assert.equal(affectsQuery(key,'1'),false);
 assert.equal(affectsQuery(['private','1','tokens'],'1',['tokens']),true);
 assert.equal(affectsQuery(['private','1','library'],'1',['tokens']),false);
});

test('caller-private credit membership refreshes without crossing accounts', () => {
 for (const resource of ['person', 'studio']) {
  assert.equal(affectsQuery(['private','alice',resource,'tmdb','123'],'alice'),true);
  assert.equal(affectsQuery(['private','alice',resource,'tmdb','123'],'bob'),false);
  assert.equal(affectsQuery([resource,'tmdb','123'],'alice'),false);
 }
});
