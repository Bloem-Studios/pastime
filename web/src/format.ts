import { locale, t } from './i18n';

/** One way to write a length of time: "47 min", "2 h 47 min", "25 h 10 min", "112 h". */
export function duration(minutes: number): string {
  const m = Math.max(0, Math.round(minutes));
  const glue = (text: string) => text.replace(/ /g, '\u00a0');
  if (m < 60) return glue(t('{minutes} min', { minutes: m }));
  const h = Math.floor(m / 60), rest = m % 60;
  if (h >= 100 || rest === 0) return glue(t('{hours} h', { hours: h.toLocaleString(locale) }));
  return glue(t('{hours} h {minutes} min', { hours: h, minutes: rest }));
}

/** One way to name an episode: "S1 E4". Specials are season 0: "S0 E2". */
export function episodeCode(season?: number | null, episode?: number | null): string {
  if (season == null || episode == null) return '';
  return `S${season} E${episode}`;
}

/** A run of episodes: "S1 E1–E4" within a season, "S1 E9 – S2 E1" across seasons. */
export function episodeRange(first: { season_number?: number | null; episode_number?: number | null }, last: { season_number?: number | null; episode_number?: number | null }): string {
  const a = episodeCode(first.season_number, first.episode_number), b = episodeCode(last.season_number, last.episode_number);
  if (!a || !b) return a || b;
  if (a === b) return a;
  if (first.season_number === last.season_number) return `${a}–E${last.episode_number}`;
  return `${a} – ${b}`;
}

/** A clock time in the person's locale: "8:38 PM". */
export function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' });
}

/** A local calendar date key, "2026-11-04", for grouping by the day a play happened where the person is. */
export function localDay(iso: string): string {
  return new Date(iso).toLocaleDateString('en-CA');
}

/** A span of clock times, sharing what both ends share: "8:38 – 9:31 PM". */
export function clockRange(fromISO: string, toISO: string): string {
  const format = new Intl.DateTimeFormat(locale, { hour: 'numeric', minute: '2-digit' });
  const from = new Date(fromISO), to = new Date(toISO);
  return to.getTime() > from.getTime() ? format.formatRange(from, to) : format.format(from);
}

/** How long ago, or how long until: "3 h 13 min ago", "in 5 min", "just now". */
export function relative(iso: string): string {
  const seconds = (Date.now() - new Date(iso).getTime()) / 1000;
  if (Math.abs(seconds) < 60) return t('just now');
  return seconds > 0 ? t('{time} ago', { time: duration(seconds / 60) }) : t('in {time}', { time: duration(-seconds / 60) });
}
