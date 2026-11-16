import { test } from 'node:test';
import assert from 'node:assert/strict';
import { discoveryKind, discoveryPage, discoverySearchKind } from './discoveryPolicy.ts';

test('untrusted URL media types fall back to movies', () => {
  for (const value of [null, '', 'season', 'bad', 'Movie']) assert.equal(discoveryKind(value), 'movie');
  for (const value of ['series', 'anime', 'video', 'boardgame']) assert.equal(discoveryKind(value), value);
});
test('grouped anime uses the same whole-show kind for search and browse', () => {
  assert.equal(discoverySearchKind('anime', 'grouped'), 'series');
  assert.equal(discoverySearchKind('anime', 'separate'), 'anime');
  assert.equal(discoverySearchKind('book', 'grouped'), 'book');
});
test('paging cannot forward invalid, fractional or unbounded URL values', () => {
  for (const value of [null, '', '-1', '1.2', 'Infinity', 'NaN', '501']) assert.equal(discoveryPage(value), 1);
  assert.equal(discoveryPage('2'), 2);
});
test('unsupported filter and paging parameters are never sent to a provider', async () => {
  const { discoveryOptions } = await import('./discoveryPolicy.ts');
  const params = new URLSearchParams('page=3&year=2020&genre=Drama&sort=rating');
  assert.deepEqual(discoveryOptions(params, { pagination: false, year: false, genre: false, sorts: [] }), {});
  assert.deepEqual(discoveryOptions(params, { pagination: true, year: true, genre: true, sorts: ['popular'] }), { page: 3, year: 2020, genre: 'Drama' });
});
test('filter values from links are bounded and validated before query submission', async () => {
  const { discoveryOptions } = await import('./discoveryPolicy.ts');
  const caps = { pagination: true, year: true, genre: true, sorts: ['rating'] };
  assert.deepEqual(discoveryOptions(new URLSearchParams('page=-2&year=notyear&sort=bad'), caps), { page: 1 });
  assert.deepEqual(discoveryOptions(new URLSearchParams('year=2020&sort=rating'), caps), { page: 1, year: 2020, sort: 'rating' });
});
test('structured shelf reasons use localization templates and label the local catalog', async () => {
  const { discoveryReason } = await import('./discoveryPolicy.ts');
  assert.deepEqual(discoveryReason({ code: 'related', title: 'Synthetic Atlas', source: 'provider' }), { key: 'Because you liked {title}', values: { title: 'Synthetic Atlas' } });
  assert.deepEqual(discoveryReason({ code: 'genre', genre: 'Drama', source: 'local' }), { key: 'More {genre} from this server', values: { genre: 'Drama' } });
  assert.deepEqual(discoveryReason({ code: 'popular', source: 'provider' }), { key: 'Popular now', values: {} });
  assert.deepEqual(discoveryReason({ code: 'unexpected', source: 'local' }), { key: 'Suggestions', values: {} });
});
test('explicit absent membership overrides stale title-detail cache, while legacy credits can use it', async () => {
  const { discoveryMembership } = await import('./discoveryPolicy.ts');
  const old = { id: 'stale-entry', status: 'completed' };
  assert.deepEqual(discoveryMembership({ library_entry_id: null, library_status: null }, old), { id: null, status: null });
  assert.deepEqual(discoveryMembership({}, old), { id: 'stale-entry', status: 'completed' });
  assert.deepEqual(discoveryMembership({ library_entry_id: 'current', library_status: 'planned' }, old), { id: 'current', status: 'planned' });
});
