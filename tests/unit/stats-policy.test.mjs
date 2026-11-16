import { test } from 'node:test';
import assert from 'node:assert/strict';
import { activityRows, axisLabels, change, grainFor, lastDays, minuteTicks, previousEvents, previousRange, reviewYear, timeBins } from '../../web/src/statsPolicy.ts';

test('ranges count whole UTC days with today included', () => {
 const r = lastDays(30, Date.parse('2026-11-05T13:00:00Z'));
 assert.deepEqual(r, { from: '2026-10-07', to: '2026-11-05', toExclusive: '2026-11-06', days: 30 });
 assert.deepEqual(previousRange(r), { from: '2026-09-07', to: '2026-10-06', toExclusive: '2026-10-07', days: 30 });
 assert.equal(reviewYear(new Date('2027-01-10T00:00:00Z')), 2026);
 assert.equal(reviewYear(new Date('2026-11-05T00:00:00Z')), 2026);
});

test('the chart bins follow the range and count every day once', () => {
 assert.equal(grainFor('2026-10-07', '2026-11-05'), 'day');
 assert.equal(grainFor('2025-11-06', '2026-11-05'), 'week');
 assert.equal(grainFor('2016-11-08', '2026-11-05'), 'month');
 const days = [{ date: '2026-11-02', minutes: 60, plays: 1 }, { date: '2026-11-05', minutes: 30, plays: 2 }, { date: '2026-10-31', minutes: 10, plays: 1 }];
 const daily = timeBins(days, '2026-10-07', '2026-11-05', 'day');
 assert.equal(daily.length, 30);
 assert.equal(daily.at(-1).minutes, 30);
 const weekly = timeBins(days, '2026-10-07', '2026-11-05', 'week', 'monday');
 assert.equal(weekly[0].start, '2026-10-07'); assert.equal(weekly[0].end, '2026-10-11');
 assert.equal(weekly.at(-1).start, '2026-11-02'); assert.equal(weekly.at(-1).minutes, 90);
 assert.equal(weekly.reduce((n, b) => n + b.minutes, 0), 100);
 const monthly = timeBins(days, '2026-10-07', '2026-11-05', 'month');
 assert.deepEqual(monthly.map(b => [b.start, b.end, b.minutes]), [['2026-10-07', '2026-10-31', 10], ['2026-11-01', '2026-11-05', 90]]);
});

test('axis labels land on one step, four to eight of them', () => {
 const month = timeBins([], '2026-10-07', '2026-11-05', 'day');
 const weekly = axisLabels(month, 'day', 'monday');
 assert.deepEqual(weekly.map(l => l.date), ['2026-10-12', '2026-10-19', '2026-10-26', '2026-11-02']);
 const quarter = axisLabels(timeBins([], '2026-08-08', '2026-11-05', 'day'), 'day');
 assert.deepEqual(quarter.map(l => l.date), ['2026-08-15', '2026-09-01', '2026-09-15', '2026-10-01', '2026-10-15', '2026-11-01']);
 const year = axisLabels(timeBins([], '2025-11-06', '2026-11-05', 'week'), 'week');
 assert.ok(year.length >= 4 && year.length <= 8, `year labels ${year.length}`);
 assert.ok(year.every(l => l.date.endsWith('-01')));
 const decade = axisLabels(timeBins([], '2016-11-08', '2026-11-05', 'month'), 'month');
 assert.ok(decade.length >= 4 && decade.length <= 8, `decade labels ${decade.length}`);
});

test('y ticks are round and end above the maximum', () => {
 assert.deepEqual(minuteTicks(59), [0, 15, 30, 45, 60]);
 assert.deepEqual(minuteTicks(230), [0, 60, 120, 180, 240]);
 assert.deepEqual(minuteTicks(240), [0, 120, 240, 360]);
 assert.deepEqual(minuteTicks(0), [0, 5, 10]);
});

test('changes against nothing are not signals', () => {
 assert.deepEqual(change(5), { kind: 'none' });
 assert.deepEqual(change(0, 0), { kind: 'same' });
 assert.deepEqual(change(4, 0), { kind: 'new' });
 assert.deepEqual(change(10, 10), { kind: 'same' });
 assert.deepEqual(change(15, 10), { kind: 'percent', value: 50 });
});

test('activity drops plays, folds bursts of additions and import runs into one row', () => {
 const e = (id, entry, kind, at) => ({ id, entry_id: entry, kind, created_at: at });
 const events = [
  e('9', 'c', 'progress', '2026-11-05T10:00:00Z'),
  e('8', 'c', 'played', '2026-11-04T21:11:39Z'), e('7', 'c', 'added', '2026-11-04T21:11:39Z'),
  e('6', 'b', 'played', '2026-11-04T21:11:38Z'), e('5', 'b', 'added', '2026-11-04T21:11:37Z'),
  e('4', 'a', 'added', '2026-11-04T21:11:35Z'),
  e('3', 'x', 'added', '2026-11-03T08:00:00Z'), e('2', 'y', 'added', '2026-11-03T07:00:00Z'),
 ];
 const rows = activityRows(events);
 assert.deepEqual(rows.map(r => [r.kind, r.events.map(x => x.id)]), [['event', ['9']], ['bulk', ['7', '5', '4']], ['event', ['3']], ['event', ['2']]]);
 // Events an import run wrote fold into one row for that run, whatever their spacing.
 const run = (x, id) => ({ ...x, import_id: id, import_source: 'trakt' });
 const imported = activityRows([events[0], run(events[2], 'r1'), run(events[4], 'r1'), events[5], run(events[6], 'r2'), events[7]]);
 assert.deepEqual(imported.map(r => [r.kind, r.source, r.events.map(x => x.id)]), [['event', undefined, ['9']], ['import', 'trakt', ['7', '5']], ['event', undefined, ['4']], ['import', 'trakt', ['3']], ['event', undefined, ['2']]]);
 const before = previousEvents(events);
 assert.equal(before.get('9').id, '8');
 assert.equal(before.get('4'), undefined);
});
