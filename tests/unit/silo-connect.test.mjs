import { test } from 'node:test';
import assert from 'node:assert/strict';
import { siloConnectNotice, siloConnected, siloPage, siloProblem } from '../../web/src/siloConnectPolicy.ts';

const state = (profiles, extra = {}) => ({ available: true, service_access: true, silo_url: 'https://media.example.test', profiles, ...extra });
const main = { profile_id: '1', name: 'Main', connected: false, expires_at: null };
const child = { profile_id: '2', name: 'Child', connected: true, expires_at: '2027-01-12T00:00:00Z' };

test('the banner asks only an unconnected profile that can act to connect', () => {
  assert.deepEqual(siloConnectNotice(state([main]), '1'), { otherConnected: false });
  assert.deepEqual(siloConnectNotice(state([main, child]), '1'), { otherConnected: true });
  assert.equal(siloConnectNotice(state([main, child]), '2'), null, 'connected profile');
  assert.equal(siloConnectNotice(state([main], { available: false }), '1'), null, 'no Silo registered');
  assert.equal(siloConnectNotice(state([main], { service_access: false }), '1'), null, 'no tracking access');
  assert.equal(siloConnectNotice(state([main]), '9'), null, 'profile not listed');
  assert.equal(siloConnectNotice(null, '1'), null, 'server without the route');
  assert.equal(siloConnectNotice(undefined, '1'), null, 'still loading');
});

test('connection follows the current key, not provisioning flags', () => {
  assert.equal(siloConnected(state([main, child]), '2'), true);
  assert.equal(siloConnected(state([main, child]), '1'), false);
  assert.equal(siloConnected(null, '2'), false);
});

test('Silo links stay on the registered Silo origin', () => {
  assert.equal(siloPage('https://media.example.test', '/recommendations'), 'https://media.example.test/recommendations');
  assert.equal(siloPage('https://media.example.test/', '/settings/watch-providers'), 'https://media.example.test/settings/watch-providers');
  assert.equal(siloPage('https://example.test/silo/?x=1#y', '/recommendations'), 'https://example.test/silo/recommendations');
  assert.equal(siloPage('https://media.example.test', '/search', { q: 'Dune: Part Two & more' }), 'https://media.example.test/search?q=Dune%3A+Part+Two+%26+more');
  for (const unusable of ['', null, undefined, 'not a url', 'javascript:alert(1)', 'data:text/html,x', 'https://user:secret@media.example.test', 'ftp://media.example.test']) {
    assert.equal(siloPage(unusable, '/recommendations'), null, String(unusable));
  }
});

test('refused key requests say what to do next', () => {
  assert.equal(siloProblem(403, 'service_access_required'), 'service_access');
  assert.equal(siloProblem(409, 'silo_not_registered'), 'not_registered');
  assert.equal(siloProblem(409, 'profile_excluded'), 'excluded');
  assert.equal(siloProblem(404, 'not_found'), 'not_found');
  assert.equal(siloProblem(422, 'validation_error'), 'ambiguous');
  assert.equal(siloProblem(429, 'rate_limited'), 'rate_limited');
  assert.equal(siloProblem(500, 'internal'), 'other');
  assert.equal(siloProblem(409, 'conflict'), 'other');
});
