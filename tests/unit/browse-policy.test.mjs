import { test } from 'node:test';
import assert from 'node:assert/strict';
import { arrangeHome, byLatestPlay, facetChoices, facetCounts, groupReleases, lastPlayed, searchSources } from '../../web/src/browsePolicy.ts';

test('titles rank by their latest real play and unplayed ones keep their order', () => {
  const at = { c: '2026-11-02T00:00:00Z', b: '2026-10-01T00:00:00Z' };
  const items = ['a', 'b', 'c', 'd'].map(id => ({ id, last_played_at: at[id] ?? null }));
  assert.deepEqual(byLatestPlay(items).map(x => x.id), ['c', 'b', 'a', 'd']);
});

test('home shows each followed title once and features the one played last', () => {
  const at = { 5: '2026-11-02T19:09:00Z', 4: '2026-10-01T00:00:00Z' };
  const entry = (id, status = 'in_progress') => ({ id, status, last_played_at: at[id] ?? null });
  const home = {
    up_next: ['5', '4', '6'].map(id => ({ entry: entry(id), episode: { id: 'ep' + id } })),
    continue: [entry('6'), entry('5'), entry('4'), entry('9')],
  };
  const out = arrangeHome(home);
  assert.equal(out.featured.id, '5');
  assert.equal(out.featuredNext.episode.id, 'ep5');
  assert.deepEqual(out.upNext.map(u => u.entry.id), ['4', '6']);
  assert.deepEqual(out.continue.map(e => e.id), ['9']);
});

test('home features a continued title played more recently than every up next show', () => {
  const entry = (id, at) => ({ id, status: 'in_progress', last_played_at: at });
  const out = arrangeHome({ up_next: [{ entry: entry('1', '2026-10-01T00:00:00Z'), episode: {} }], continue: [entry('2', '2026-11-01T00:00:00Z')] });
  assert.equal(out.featured.id, '2');
  assert.equal(out.featuredNext, undefined);
  assert.deepEqual(out.upNext.map(u => u.entry.id), ['1']);
  assert.deepEqual(out.continue, []);
});

test('home features nothing when no title is being followed', () => {
  const out = arrangeHome({ up_next: [], continue: [] });
  assert.equal(out.featured, undefined);
  assert.deepEqual(out.upNext, []);
});

test('upcoming releases group by show in the order of their first release', () => {
  const groups = groupReleases([
    { date: '2026-11-06', entry_id: '3', entry_title: 'Silo' },
    { date: '2026-11-08', entry_id: '6', entry_title: 'One Piece' },
    { date: '2026-11-13', entry_id: '3', entry_title: 'Silo' },
  ]);
  assert.deepEqual(groups.map(g => [g[0].entry_title, g.length]), [['Silo', 2], ['One Piece', 1]]);
});

test('filters list only values with titles, but keep a selected one at zero', () => {
  const counts = facetCounts({ total: 6, media_types: [{ media_type: 'movie', count: 10 }, { media_type: 'series', count: 6 }], statuses: [{ status: 'completed', count: 10 }] });
  assert.deepEqual(facetChoices(['movie', 'series', 'book'], counts.types, []), [{ value: 'movie', count: 10 }, { value: 'series', count: 6 }]);
  assert.deepEqual(facetChoices(['planned', 'completed'], counts.statuses, ['planned']).map(c => c.value), ['planned', 'completed']);
  assert.equal(counts.anyType, 16);
  assert.equal(counts.total, 6);
  assert.equal(facetCounts(undefined).total, 0);
});

test('last played is the later of the latest real play and the finish', () => {
  assert.equal(lastPlayed({ last_played_at: null, finished_at: '2026-09-13T20:00:00Z' }), '2026-09-13T20:00:00Z');
  assert.equal(lastPlayed({ last_played_at: '2026-10-01T00:00:00Z', finished_at: '2026-09-13T20:00:00Z' }), '2026-10-01T00:00:00Z');
  assert.equal(lastPlayed({ last_played_at: '2026-09-01T00:00:00Z', finished_at: '2026-09-13T20:00:00Z' }), '2026-09-13T20:00:00Z');
  assert.equal(lastPlayed({ last_played_at: null, finished_at: null }), null);
});

test('only configured sources that search the media type are offered', () => {
  const providers = [
    { name: 'tmdb', kinds: ['movie', 'series'], configured: false, search: { supported: true } },
    { name: 'tvdb', kinds: ['series'], configured: true, search: { supported: true } },
    { name: 'anilist', kinds: ['anime'], configured: true, search: { supported: true } },
  ];
  assert.deepEqual(searchSources(providers, 'series').map(p => p.name), ['tvdb']);
  assert.deepEqual(searchSources(providers, 'movie'), []);
});
