// Profile identifiers are UI context, never credentials or login identity.
let selected = '';
let revision = 0;
export const trackingProfile = () => selected;
export const profileGeneration = () => revision;
export function setTrackingProfile(id: string) {
 if (id !== selected) { selected = id; revision++; }
}
export function profileQueryKey(accountID: string, ...parts: unknown[]) {
 if (!parts.length) return ['private', accountID] as const;
 return ['private', accountID, parts[0], { profile: selected || 'default' }, ...parts.slice(1)] as const;
}
