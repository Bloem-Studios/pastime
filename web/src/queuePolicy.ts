/** The job counts the admin overview reports. */
export type QueueJobs = {
  queued: number; running: number; failed: number; blocked: number;
  oldest_queued_seconds: number | null; failed_last_day: number;
};

/**
 * Why the queue needs someone: nothing can run it, a due job has waited too long, or jobs
 * failed or were blocked in the last day. `null` when nothing is wrong.
 */
export type QueueProblem = 'no_worker' | 'overdue' | 'attention' | null;

export type QueueHealth = {
  tone: 'ok' | 'warn' | 'bad';
  problem: QueueProblem;
  /** Queued jobs, due or not. */
  waiting: number;
  running: number;
  /** Failed in the last day plus blocked: the jobs that need a decision. */
  attention: number;
  /** Seconds the oldest due job has waited, or null when none is due. */
  oldest: number | null;
  workers: number;
  /** Where the top bar and the Dashboard link to. */
  to: string;
};

/** A due job waiting longer than this means the workers are not keeping up. */
export const overdueSeconds = 15 * 60;

/** One rule for the admin top bar, the Dashboard and Jobs. */
export function queueHealth(jobs: QueueJobs, workers: number): QueueHealth {
  const attention = jobs.failed_last_day + jobs.blocked;
  const base = { waiting: jobs.queued, running: jobs.running, attention, oldest: jobs.oldest_queued_seconds ?? null, workers };
  if (workers === 0 && jobs.queued > 0) return { ...base, tone: 'bad', problem: 'no_worker', to: '/admin/jobs?status=queued' };
  if (base.oldest != null && base.oldest > overdueSeconds) return { ...base, tone: 'bad', problem: 'overdue', to: '/admin/jobs?status=queued' };
  if (attention > 0) return { ...base, tone: 'bad', problem: 'attention', to: `/admin/jobs?status=${jobs.failed_last_day ? 'failed' : 'blocked'}` };
  return { ...base, tone: workers === 0 ? 'warn' : 'ok', problem: null, to: '/admin/jobs' };
}
