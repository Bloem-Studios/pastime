import { test } from 'node:test';
import assert from 'node:assert/strict';
import { visibleCalendarReleases, calendarReleaseKey, calendarTitlePath, calendarEntryPath, calendarIsEpisode } from '../../web/src/calendarPolicy.ts';

test('source-only rows expire in cached/offline calendars without losing native releases', () => {
 const now = Date.parse('2026-10-12T12:00:00Z');
 const source = {source_instance_id:'one',source_event_id:'event',source_authority_expires_at:'2026-10-12T12:01:00Z',title:'Source'};
 const native = {catalog_id:'7',title_catalog_id:'7',title:'Native'};
 assert.equal(visibleCalendarReleases([source,native],now).length,2);
 assert.deepEqual(visibleCalendarReleases([source,native],now+60000),[native]);
 assert.deepEqual(visibleCalendarReleases([{...source,source_authority_expires_at:'bad'}],now),[]);
 const merged = {...native,...source,playable:true,watched:true};
 const [expired] = visibleCalendarReleases([merged],now+60000);
 assert.equal(expired.catalog_id,'7');
 assert.equal(expired.source_event_id,undefined);
 assert.equal(expired.playable,false);
});

test('source-only releases never navigate to a guessed native title and release keys are distinct', () => {
 assert.equal(calendarTitlePath({source_event_id:'event'}),null);
 assert.equal(calendarTitlePath({title_catalog_id:'7'}),'/title/7');
 assert.equal(calendarEntryPath({entry_id:'3',title_catalog_id:'7'}),'/library/3');
 assert.equal(calendarEntryPath({entry_id:'0',title_catalog_id:'7'}),'/title/7');
 assert.equal(calendarEntryPath({source_event_id:'event'}),null);
 assert.notEqual(calendarReleaseKey({catalog_id:'7',release_type:'digital'}),calendarReleaseKey({catalog_id:'7',release_type:'theatrical'}));
 assert.notEqual(calendarReleaseKey({source_instance_id:'a',source_event_id:'event'}),calendarReleaseKey({source_instance_id:'b',source_event_id:'event'}));
});


test('source-only episodes retain their details when native title IDs are absent', () => {
 assert.equal(calendarIsEpisode({kind:'episode',title:'A New Chapter'}),true);
 assert.equal(calendarIsEpisode({kind:'movie'}),false);
 assert.equal(calendarIsEpisode({catalog_id:'8',title_catalog_id:'7'}),true);
 assert.equal(calendarIsEpisode({}),false);
});

test('month navigation handles leap days and year boundaries', async () => {
 const { calendarMonthRange, shiftCalendarMonth, calendarMonthDays } = await import('../../web/src/calendarPolicy.ts');
 assert.deepEqual(calendarMonthRange('2024-02'), { from:'2024-02-01',to:'2024-02-29' });
 assert.deepEqual(calendarMonthRange('2026-02'), { from:'2026-02-01',to:'2026-02-28' });
 assert.equal(shiftCalendarMonth('2026-12',1),'2027-01');
 assert.equal(shiftCalendarMonth('2026-01',-1),'2025-12');
 assert.equal(calendarMonthRange('2026-13'),null);
 const monday=calendarMonthDays('2026-10','monday');
 assert.equal(monday[0],'2026-09-28');assert.equal(monday.at(-1),'2026-11-01');
 const sunday=calendarMonthDays('2026-02','sunday');
 assert.equal(sunday.length,28);assert.equal(sunday[0],'2026-02-01');assert.equal(sunday.at(-1),'2026-02-28');
});
