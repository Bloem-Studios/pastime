// Pure rules for the Connect Silo page, its banner and the links into Silo.
// Kept free of React and translations so the unit tests can import it directly.

export type SiloProfile = { profile_id: string; name: string; connected: boolean; expires_at?: string | null };
export type SiloState = { available: boolean; service_access: boolean; silo_url: string; profiles: SiloProfile[] };

/** A page on the registered Silo server, or null when its address is unusable. */
export function siloPage(siloURL: string | null | undefined, path: string, query: Record<string, string> = {}): string | null {
  if (!siloURL) return null;
  let base: URL;
  try { base = new URL(siloURL); } catch { return null; }
  if ((base.protocol !== 'https:' && base.protocol !== 'http:') || base.username || base.password || !base.hostname) return null;
  // A Silo served below a path keeps that prefix; its own query and fragment are dropped.
  const url = new URL(base.pathname.replace(/\/+$/, '') + path, base.origin);
  for (const [key, value] of Object.entries(query)) url.searchParams.set(key, value);
  return url.toString();
}

export function siloProfile(state: SiloState | null | undefined, profileID: string): SiloProfile | undefined {
  return state?.profiles.find(profile => profile.profile_id === profileID);
}

/** Whether the selected profile already sends Silo's plays here. */
export function siloConnected(state: SiloState | null | undefined, profileID: string): boolean {
  return !!siloProfile(state, profileID)?.connected;
}

export type SiloNotice = { otherConnected: boolean };
/**
 * The "not connected" banner shows only when the person can act on it: Silo is
 * registered, the account may track, and the selected profile has no current key.
 */
export function siloConnectNotice(state: SiloState | null | undefined, profileID: string): SiloNotice | null {
  if (!state?.available || !state.service_access) return null;
  const profile = siloProfile(state, profileID);
  if (!profile || profile.connected) return null;
  return { otherConnected: state.profiles.some(other => other.profile_id !== profileID && other.connected) };
}

export type SiloProblem = 'service_access' | 'not_registered' | 'excluded' | 'not_found' | 'ambiguous' | 'rate_limited' | 'other';
/** Classifies a refused key request so the page can say what to do next. */
export function siloProblem(status: number, code: string): SiloProblem {
  if (code === 'service_access_required') return 'service_access';
  if (code === 'silo_not_registered') return 'not_registered';
  if (code === 'profile_excluded') return 'excluded';
  if (status === 404) return 'not_found';
  if (status === 422) return 'ambiguous';
  if (status === 429) return 'rate_limited';
  return 'other';
}
