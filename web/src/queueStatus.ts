import { useQuery } from '@tanstack/react-query';
import { api, unwrap, type Schema } from './api/client';
import { privateKey, useUser } from './state';
import { duration } from './format';
import { locale, plural, t } from './i18n';
import { queueHealth, type QueueHealth } from './queuePolicy';

/** The admin overview, shared by the top bar and the console pages; refreshed every 30 seconds. */
export function useAdminOverview() {
  const user = useUser();
  return useQuery({ queryKey: privateKey(user.id, 'admin', 'overview'), queryFn: async ({ signal }) => unwrap(await api.GET('/api/admin/v1/overview', { signal })), refetchInterval: 30_000 });
}

export const overviewQueue = (overview: Schema['AdminOverview']): QueueHealth =>
  queueHealth(overview.jobs, (overview.nodes ?? []).filter(n => n.role === 'worker').length);

const wait = (seconds: number) => duration(seconds / 60);

/** The queue in a few words: "16 waiting · no worker", "3 running", "Queue idle". */
export function queueLabel(h: QueueHealth): string {
  const n = (value: number) => value.toLocaleString(locale);
  if (h.problem === 'no_worker') return t('{n} waiting · no worker', { n: n(h.waiting) });
  if (h.problem === 'overdue') return t('{n} waiting · oldest {wait}', { n: n(h.waiting), wait: wait(h.oldest ?? 0) });
  if (h.problem === 'attention') return t('{trouble} need attention', { trouble: n(h.attention) });
  if (h.running) return t('{running} running', { running: n(h.running) });
  if (h.waiting) return t('{queued} queued', { queued: n(h.waiting) });
  return h.workers ? t('Queue idle') : t('Queue idle · no worker');
}

/** Why the queue needs someone, in sentences, or null when it does not. */
export function queueProblem(h: QueueHealth): string | null {
  const waiting = h.oldest != null
    ? plural(h.waiting, '{n} job is waiting; it has been due for {wait}.', '{n} jobs are waiting; the oldest has been due for {wait}.', { wait: wait(h.oldest) })
    : plural(h.waiting, '{n} job is waiting.', '{n} jobs are waiting.');
  if (h.problem === 'no_worker') return `${t('No worker is running, so background jobs do not run.')} ${waiting}`;
  if (h.problem === 'overdue') return `${t('Workers are not keeping up.')} ${waiting}`;
  if (h.problem === 'attention') return plural(h.attention, '{n} job failed in the last day or is blocked, and needs a decision.', '{n} jobs failed in the last day or are blocked, and need a decision.');
  return null;
}
