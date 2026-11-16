// Library commands affect these private projections. Account administration,
// credentials, settings and provider configuration have separate refresh domains.
export const libraryResources = new Set([
 'episode-ratings', 'derived-rating', 'status-history', 'library', 'entry', 'catalog', 'plays', 'history', 'stats', 'home', 'activity',
 'calendar', 'discover', 'person', 'studio', 'collection', 'copies', 'entry-tags', 'tags', 'lists',
 'list', 'list-entries', 'list-picker', 'bulk-lists', 'pref-lists', 'recommendations', 'collab',
]);
export function affectsQuery(key: readonly unknown[], userID: string, resources?: readonly string[]) {
 return key[0] === 'private' && key[1] === userID && typeof key[2] === 'string'
  && (resources ? resources.includes(key[2]) : libraryResources.has(key[2]));
}
