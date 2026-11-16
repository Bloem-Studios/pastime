// Pure helpers for Statistics, Year in Review and Activity. No i18n or DOM here, so the unit
// tests can run them under Node.

/** The year Year in Review opens on: last year during January, this year otherwise. */
export function reviewYear(now = new Date()) { return now.getUTCMonth() === 0 ? now.getUTCFullYear() - 1 : now.getUTCFullYear(); }

const DAY = 864e5;
const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);
const ms = (date: string) => Date.parse(`${date}T00:00:00Z`);

/** A statistics range in UTC days: `from` and `to` inclusive, `toExclusive` for the API. */
export type StatsRange = { from: string; to: string; toExclusive: string; days: number };
/** The last `days` UTC days, today included. */
export function lastDays(days: number, now = Date.now()): StatsRange {
  const today = ms(iso(now));
  return { from: iso(today - (days - 1) * DAY), to: iso(today), toExclusive: iso(today + DAY), days };
}
/** The `days` UTC days just before `range`, for comparison. */
export function previousRange(range: StatsRange): StatsRange {
  const from = ms(range.from);
  return { from: iso(from - range.days * DAY), to: iso(from - DAY), toExclusive: range.from, days: range.days };
}

export type Grain = 'day' | 'week' | 'month';
export type Bin = { start: string; end: string; minutes: number; plays: number };
type Day = { date: string; minutes: number; plays: number };

/** Daily bars up to three months, weekly up to two years, monthly beyond. */
export function grainFor(from: string, to: string): Grain {
  const span = (ms(to) - ms(from)) / DAY + 1;
  return span <= 92 ? 'day' : span <= 731 ? 'week' : 'month';
}

/**
 * Time per bin over [from, to] (UTC dates, inclusive). Weeks start on `weekStart`; the first
 * and last bins are clipped to the range, so every day is counted once.
 */
export function timeBins(days: Day[], from: string, to: string, grain: Grain, weekStart: 'monday' | 'sunday' = 'monday'): Bin[] {
  const bins: Bin[] = [];
  const byDay = new Map(days.map(d => [d.date, d]));
  let current: Bin | null = null;
  for (let t = ms(from), end = ms(to); t <= end; t += DAY) {
    const date = iso(t); const d = new Date(t);
    const starts = !current || (grain === 'day') || (grain === 'week' && d.getUTCDay() === (weekStart === 'monday' ? 1 : 0)) || (grain === 'month' && d.getUTCDate() === 1);
    if (starts) { current = { start: date, end: date, minutes: 0, plays: 0 }; bins.push(current); }
    current!.end = date;
    const v = byDay.get(date);
    if (v) { current!.minutes += v.minutes; current!.plays += v.plays; }
  }
  return bins;
}

/** Which bins carry an x label, 4 to 8 of them on one step. */
export function axisLabels(bins: Bin[], grain: Grain, weekStart: 'monday' | 'sunday' = 'monday'): { index: number; date: string; unit: 'day' | 'month' }[] {
  const at = (pred: (b: Bin) => string | null, unit: 'day' | 'month') => bins.flatMap((b, index) => { const date = pred(b); return date ? [{ index, date, unit }] : []; });
  // The first day of a month inside the bin, if any.
  const monthStart = (b: Bin) => { const s = ms(b.start), e = ms(b.end); for (let t = s; t <= e; t += DAY) if (new Date(t).getUTCDate() === 1) return iso(t); return null; };
  const thin = <T extends { date: string }>(list: T[], steps: number[], key: (date: string) => number) => {
    for (const step of steps) { const kept = list.filter(l => key(l.date) % step === 0); if (kept.length <= 8) return kept; }
    return list.filter(l => key(l.date) % steps[steps.length - 1] === 0);
  };
  const monthIndex = (date: string) => { const d = new Date(ms(date)); return d.getUTCFullYear() * 12 + d.getUTCMonth(); };
  if (grain === 'day') {
    if (bins.length <= 35) {
      const first = weekStart === 'monday' ? 1 : 0;
      return at(b => new Date(ms(b.start)).getUTCDay() === first ? b.start : null, 'day');
    }
    const halves = at(b => { const d = new Date(ms(b.start)).getUTCDate(); return d === 1 || d === 15 ? b.start : null; }, 'day');
    return halves.length <= 8 ? halves : at(b => new Date(ms(b.start)).getUTCDate() === 1 ? b.start : null, 'day');
  }
  const months = at(monthStart, 'month');
  return thin(months, [1, 2, 3, 6, 12, 24, 36, 60], monthIndex);
}

/** Round y ticks in minutes: 0 first, three to five ticks, the top one above the maximum. */
export function minuteTicks(max: number): number[] {
  const steps = [5, 10, 15, 20, 30, 60, 120, 180, 240, 300, 600, 1200, 1800, 3000, 6000, 12000, 30000, 60000, 120000, 300000];
  const peak = Math.max(1, max);
  const step = steps.find(s => Math.floor(peak / s) + 1 <= 4) ?? steps[steps.length - 1];
  const count = Math.max(2, Math.floor(peak / step) + 1);
  return Array.from({ length: count + 1 }, (_, i) => i * step);
}

/** The change against a previous period; `none` when there is nothing to compare. */
export function change(now: number, before?: number): { kind: 'none' } | { kind: 'same' } | { kind: 'new' } | { kind: 'percent'; value: number } {
  if (before === undefined) return { kind: 'none' };
  if (before === 0) return now === 0 ? { kind: 'same' } : { kind: 'new' };
  const value = Math.round(((now - before) / before) * 100);
  return value === 0 ? { kind: 'same' } : { kind: 'percent', value };
}

type Event = { id: string; entry_id: string; kind: string; created_at: string; import_id?: string | null; import_source?: string | null };
/** One line of the activity log: one change, a burst of additions, or one import. */
export type ActivityRow<E extends Event> = { key: string; kind: 'event' | 'bulk' | 'import'; events: E[]; source?: string };

const BURST = 60_000;

/**
 * The library changes worth reading, newest first. Plays are left out (History lists them).
 * Consecutive changes written by one import run become one row for that import; three or more
 * titles added within a minute of each other become one "added N titles" row.
 */
export function activityRows<E extends Event>(events: E[]): ActivityRow<E>[] {
  const out: ActivityRow<E>[] = [];
  type Run = { importID?: string; source?: string; events: E[] };
  // Assigned inside flush() too, so keep the declared type instead of narrowing to null.
  let run = null as Run | null;
  const flush = () => {
    if (!run) return;
    if (run.importID) out.push({ key: run.events[0].id, kind: 'import', events: run.events, source: run.source });
    else if (new Set(run.events.map(e => e.entry_id)).size >= 3) out.push({ key: run.events[0].id, kind: 'bulk', events: run.events });
    else for (const e of run.events) out.push({ key: e.id, kind: 'event', events: [e] });
    run = null;
  };
  for (const e of events) {
    if (e.kind === 'played') continue;
    if (e.import_id) {
      if (run?.importID !== e.import_id) { flush(); run = { importID: e.import_id, source: e.import_source ?? undefined, events: [] }; }
      run!.events.push(e);
      continue;
    }
    if (e.kind === 'added') {
      const at = Date.parse(e.created_at);
      const prev: E | undefined = run && !run.importID ? run.events[run.events.length - 1] : undefined;
      if (!prev || Math.abs(Date.parse(prev.created_at) - at) > BURST) { flush(); run = { events: [] }; }
      run!.events.push(e);
      continue;
    }
    flush();
    out.push({ key: e.id, kind: 'event', events: [e] });
  }
  flush();
  return out;
}

/** For each event, the next older event of the same title, to say what changed. */
export function previousEvents<E extends Event>(events: E[]): Map<string, E> {
  const out = new Map<string, E>(); const later = new Map<string, E>();
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]; const before = later.get(e.entry_id);
    if (before) out.set(e.id, before);
    later.set(e.entry_id, e);
  }
  return out;
}
