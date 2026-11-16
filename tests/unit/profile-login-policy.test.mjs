import {test} from 'node:test';
import assert from 'node:assert/strict';
import {householdOnly, isProfileLogin, withCacheIdentity} from '../../web/src/profileLoginPolicy.ts';

const user = {id: '7', username: 'family', display_name: 'Family', email: 'family@example.test', role: 'owner', active: true, password_ready: true, version: 1, created_at: '2026-11-06T00:00:00Z', last_login_at: null};
const account = {user, csrf_token: 'a'.repeat(64), profile_login: null};
const sam = {user: {...user, username: 'sam', display_name: 'Sam', email: '', role: 'member'}, csrf_token: 'b'.repeat(64), profile_login: {profile_id: '5', login_name: 'sam'}};

test('a profile login is told apart from the account session', () => {
 assert.equal(isProfileLogin(account), false);
 assert.equal(isProfileLogin(sam), true);
 assert.equal(isProfileLogin(null), false);
 assert.equal(isProfileLogin(undefined), false);
});

test('household-only values are withheld from a profile login', () => {
 assert.equal(householdOnly(sam, 'Collaborative lists'), null);
 assert.equal(householdOnly(account, 'Collaborative lists'), 'Collaborative lists');
});

test('a profile login never shares the account session cache identity', () => {
 // Both report the household's account ID; cached data is keyed by it.
 const own = withCacheIdentity(sam);
 assert.notEqual(own.user.id, withCacheIdentity(account).user.id);
 assert.equal(withCacheIdentity(account), account);
 assert.equal(own.profile_login, sam.profile_login);
 assert.equal(withCacheIdentity(own).user.id, own.user.id, 'applying it twice changes nothing');
});
