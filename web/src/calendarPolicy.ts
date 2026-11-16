import type { Schema } from './api/client';

type Release = Partial<Schema['CalendarRelease']>;
export function visibleCalendarReleases<T extends Release>(releases: T[], now: number): T[] {
  return releases.flatMap(release => {
    if (!release.source_event_id) return [release];
    const expires = Date.parse(release.source_authority_expires_at ?? '');
    if (Number.isFinite(expires) && expires > now) return [release];
    if (!release.catalog_id) return [];
    // Keep the ordinary native release without expired source metadata or actions.
    const native = { ...release, watched: false, playable: false, favourite: false, watchlist: false, handoff: null };
    delete native.source_instance_id;
    delete native.source_event_id;
    delete native.source_content_id;
    delete native.source_authority_expires_at;
    delete native.air_at;
    delete native.air_timezone;
    return [native];
  });
}
export function calendarTitlePath(release: Release): string | null {
  return release.title_catalog_id ? `/title/${release.title_catalog_id}` : null;
}
/** Where a release row leads: the title as you track it when it is in your library, else its title page. */
export function calendarEntryPath(release: Release): string | null {
  return release.entry_id && release.entry_id !== '0' ? `/library/${release.entry_id}` : calendarTitlePath(release);
}
export function calendarReleaseKey(release: Release): string {
  return JSON.stringify(release.catalog_id ? ['native', release.catalog_id, release.date, release.release_type] : ['source', release.source_instance_id, release.source_event_id]);
}

export function calendarIsEpisode(release: Release): boolean {
  return release.kind === 'episode' || !!(release.catalog_id && release.title_catalog_id && release.catalog_id !== release.title_catalog_id);
}

export function calendarMonthRange(month: string): { from: string; to: string } | null {
  if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(month)) return null;
  const [year, value] = month.split('-').map(Number);
  if (year < 1000) return null;
  const last = new Date(Date.UTC(year, value, 0)).getUTCDate();
  return { from: `${month}-01`, to: `${month}-${String(last).padStart(2, '0')}` };
}
export function shiftCalendarMonth(month: string, amount: number): string {
  if (!calendarMonthRange(month) || !Number.isInteger(amount)) return month;
  const [year, value] = month.split('-').map(Number);
  const next = new Date(Date.UTC(year, value - 1 + amount, 1));
  if (next.getUTCFullYear() < 1000 || next.getUTCFullYear() > 9999) return month;
  return `${next.getUTCFullYear()}-${String(next.getUTCMonth() + 1).padStart(2, '0')}`;
}
export function calendarMonthDays(month: string, start: 'monday' | 'sunday'): string[] {
  const range = calendarMonthRange(month);
  if (!range) return [];
  const first = new Date(`${range.from}T00:00:00Z`), last = new Date(`${range.to}T00:00:00Z`);
  const offset = (first.getUTCDay() - (start === 'monday' ? 1 : 0) + 7) % 7;
  first.setUTCDate(first.getUTCDate() - offset);
  const result: string[] = [];
  while (first <= last || result.length % 7 !== 0) {
    result.push(first.toISOString().slice(0, 10));
    first.setUTCDate(first.getUTCDate() + 1);
  }
  return result;
}
