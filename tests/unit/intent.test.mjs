import { test } from 'node:test';
import assert from 'node:assert/strict';
import { commandKey } from '../../web/src/api/intent.ts';
test('command keys bind retries to method, URL, body and original intent', async () => {
 const key = await commandKey('intent', 'PATCH', '/entry/1', '{"version":1}');
 assert.equal(key, await commandKey('intent', 'PATCH', '/entry/1', '{"version":1}'));
 for (const args of [['intent','PATCH','/entry/1','{"version":2}'],['intent','PATCH','/entry/2','{"version":1}'],['another','PATCH','/entry/1','{"version":1}']]) assert.notEqual(key, await commandKey(...args));
 assert.match(key,/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/);
});
