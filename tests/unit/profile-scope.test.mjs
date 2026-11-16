import {test} from 'node:test';
import assert from 'node:assert/strict';
import {trackingProfile, setTrackingProfile, profileGeneration, profileQueryKey} from '../../web/src/profileScope.ts';

test('profile switches isolate cache keys and invalidate pending response generations',()=>{
 setTrackingProfile('main');
 const before=profileGeneration();
 const main=profileQueryKey('account','library','page1');
 setTrackingProfile('child');
 assert.equal(trackingProfile(),'child');
 assert.notEqual(before,profileGeneration());
 assert.notDeepEqual(main,profileQueryKey('account','library','page1'));
 assert.deepEqual(profileQueryKey('account'),['private','account']);
 assert.notDeepEqual(profileQueryKey('account','library'),profileQueryKey('another-account','library'));
 const stable=profileGeneration();setTrackingProfile('child');assert.equal(stable,profileGeneration());
});
