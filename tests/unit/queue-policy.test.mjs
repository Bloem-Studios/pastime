import { test } from 'node:test';
import assert from 'node:assert/strict';
import { queueHealth, overdueSeconds } from '../../web/src/queuePolicy.ts';

const jobs = (over = {}) => ({ queued: 0, running: 0, failed: 0, blocked: 0, oldest_queued_seconds: null, failed_last_day: 0, ...over });

test('queued jobs with no worker are a problem, never "healthy"', () => {
  const h = queueHealth(jobs({ queued: 16, oldest_queued_seconds: 9300 }), 0);
  assert.equal(h.tone, 'bad');
  assert.equal(h.problem, 'no_worker');
  assert.equal(h.waiting, 16);
  assert.equal(h.to, '/admin/jobs?status=queued');
});

test('a due job waiting past the threshold is a problem even with workers', () => {
  assert.equal(queueHealth(jobs({ queued: 3, oldest_queued_seconds: overdueSeconds + 1 }), 2).problem, 'overdue');
  assert.equal(queueHealth(jobs({ queued: 3, oldest_queued_seconds: overdueSeconds - 1 }), 2).problem, null);
});

test('failures from the last day and blocked jobs need attention; old failures do not', () => {
  assert.equal(queueHealth(jobs({ failed: 40, failed_last_day: 0 }), 1).problem, null);
  const failed = queueHealth(jobs({ failed: 40, failed_last_day: 2, blocked: 1 }), 1);
  assert.equal(failed.problem, 'attention');
  assert.equal(failed.attention, 3);
  assert.equal(failed.to, '/admin/jobs?status=failed');
  assert.equal(queueHealth(jobs({ blocked: 1 }), 1).to, '/admin/jobs?status=blocked');
});

test('an empty queue is fine; without a worker it is a warning, not an error', () => {
  assert.deepEqual([queueHealth(jobs(), 1).tone, queueHealth(jobs(), 1).problem], ['ok', null]);
  assert.deepEqual([queueHealth(jobs(), 0).tone, queueHealth(jobs(), 0).problem], ['warn', null]);
  assert.equal(queueHealth(jobs({ running: 2 }), 1).tone, 'ok');
});
