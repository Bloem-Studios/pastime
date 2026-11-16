export const discoveryKinds = ['movie', 'series', 'anime', 'manga', 'book', 'audiobook', 'comic', 'game', 'boardgame', 'music', 'podcast', 'video'] as const;
export type DiscoveryKind = typeof discoveryKinds[number];
export function discoveryKind(value: string | null): DiscoveryKind {
  return discoveryKinds.includes(value as DiscoveryKind) ? value as DiscoveryKind : 'movie';
}
export function discoverySearchKind(kind: DiscoveryKind, shape: string | undefined): DiscoveryKind {
  return kind === 'anime' && shape === 'grouped' ? 'series' : kind;
}
export function discoveryPage(value: string | null): number {
  const page = Number(value);
  return Number.isInteger(page) && page >= 1 && page <= 500 ? page : 1;
}
export type DiscoveryCapabilities = { supported?: boolean; pagination: boolean; year: boolean; genre: boolean; sorts: string[] };
export function discoveryOptions(params: URLSearchParams, caps: DiscoveryCapabilities | undefined) {
  const options: { page?: number; year?: number; genre?: string; sort?: 'popular' | 'rating' | 'newest' } = {};
  if (caps?.pagination) options.page = discoveryPage(params.get('page'));
  const year = Number(params.get('year'));
  if (caps?.year && Number.isInteger(year) && year >= 1800 && year <= 2200) options.year = year;
  const genre = params.get('genre')?.trim();
  if (caps?.genre && genre && genre.length <= 80) options.genre = genre;
  const sort = params.get('sort');
  if ((sort === 'popular' || sort === 'rating' || sort === 'newest') && caps?.sorts.includes(sort)) options.sort = sort;
  return options;
}
export function discoveryReason(reason: { code: string; title?: string | null; genre?: string | null; source: string }) {
  const values: Record<string, string> = {};
  let key = 'Suggestions';
  if (reason.code === 'popular') key = 'Popular now';
  if (reason.code === 'catalog') key = 'From the existing catalog';
  if (reason.code === 'related' && reason.title) { key = 'Because you liked {title}'; values.title = reason.title; }
  if (reason.code === 'genre' && reason.genre) { key = reason.source === 'local' ? 'More {genre} from this server' : 'More {genre}'; values.genre = reason.genre; }
  return { key, values };
}
export function discoveryMembership<T extends string>(result: { library_entry_id?: string | null; library_status?: T | null }, legacy?: { id: string; status: T } | null) {
  // An explicit null annotation is authoritative, even if the detail cache is stale.
  return result.library_entry_id !== undefined
    ? { id: result.library_entry_id, status: result.library_status ?? null }
    : { id: legacy?.id ?? null, status: legacy?.status ?? null };
}
