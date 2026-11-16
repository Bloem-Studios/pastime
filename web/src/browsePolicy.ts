// Pure rules behind Home, Library and Discover. No React and no i18n, so they run under node --test.

type Ranked = { id: string; last_played_at: string | null };
/**
 * Titles by their latest real play, newest first; titles never played keep their given order after
 * them. The server leaves a bulk "mark as played" out of `last_played_at`: it is not a viewing.
 */
export function byLatestPlay<T extends Ranked>(items: readonly T[]): T[] {
  return items.map((item, index) => ({ item, index, at: item.last_played_at ? Date.parse(item.last_played_at) : -Infinity }))
    .sort((a, b) => b.at - a.at || a.index - b.index).map(x => x.item);
}

type HomeShape<E extends Ranked, U extends { entry: E }> = { up_next: readonly U[]; continue: readonly E[] };
/**
 * Home's first screen shows each title once. The featured title is the followed one played most
 * recently; Up next (already in that order from the server) lists the others that have an aired
 * next episode; Continue keeps only what Up next does not already show.
 */
export function arrangeHome<E extends Ranked & { status: string }, U extends { entry: E }>(home: HomeShape<E, U>) {
  const started = byLatestPlay(dedupe([...home.up_next.map(u => u.entry), ...home.continue]).filter(e => e.status === 'in_progress'));
  const featured = started[0];
  const shown = new Set(home.up_next.map(u => u.entry.id));
  return {
    featured,
    featuredNext: featured ? home.up_next.find(u => u.entry.id === featured.id) : undefined,
    upNext: home.up_next.filter(u => u.entry.id !== featured?.id),
    continue: home.continue.filter(e => e.id !== featured?.id && !shown.has(e.id)),
  };
}
function dedupe<T extends { id: string }>(items: T[]): T[] {
  const seen = new Set<string>();
  return items.filter(item => !seen.has(item.id) && !!seen.add(item.id));
}

type Release = { date: string; entry_id?: string; title_catalog_id?: string; entry_title: string };
/** Upcoming releases, one group per show in the order of its first release. */
export function groupReleases<R extends Release>(releases: readonly R[]): R[][] {
  const groups = new Map<string, R[]>();
  for (const release of releases) {
    const key = release.entry_id ?? release.title_catalog_id ?? release.entry_title;
    groups.set(key, [...(groups.get(key) ?? []), release]);
  }
  return [...groups.values()];
}

type Facets = { total: number; media_types: readonly { media_type: string; count: number }[]; statuses: readonly { status: string; count: number }[] };
/** Filter values that have titles, in the given order, with their counts. Selected values stay even at zero. */
export function facetChoices<V extends string>(order: readonly V[], counts: ReadonlyMap<string, number>, selected: readonly string[]): { value: V; count: number }[] {
  return order.filter(value => (counts.get(value) ?? 0) > 0 || selected.includes(value)).map(value => ({ value, count: counts.get(value) ?? 0 }));
}
/**
 * The library's facets as lookups. Each media type is counted under every other filter, so `anyType`
 * is what "All media" gives; `total` is what the current filters give.
 */
export function facetCounts(facets: Facets | undefined) {
  const types = new Map((facets?.media_types ?? []).map(t => [t.media_type, t.count]));
  return {
    types,
    statuses: new Map((facets?.statuses ?? []).map(s => [s.status, s.count])),
    anyType: [...types.values()].reduce((sum, n) => sum + n, 0),
    total: facets?.total ?? 0,
  };
}

/** How the library list dates a title: its latest real play or when it was finished, whichever is later. */
export function lastPlayed(entry: { last_played_at: string | null; finished_at: string | null }): string | null {
  const play = entry.last_played_at;
  if (!play) return entry.finished_at;
  if (!entry.finished_at) return play;
  return Date.parse(play) >= Date.parse(entry.finished_at) ? play : entry.finished_at;
}

type Provider = { name: string; kinds: readonly string[]; configured?: boolean; search?: { supported?: boolean } };
/** Sources a person can choose for a media type: configured ones that search it. */
export function searchSources<P extends Provider>(providers: readonly P[], kind: string): P[] {
  return providers.filter(p => p.kinds.includes(kind) && p.configured !== false && p.search?.supported !== false);
}
