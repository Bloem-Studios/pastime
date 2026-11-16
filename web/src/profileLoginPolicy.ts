import type { Session } from './api/client';

// A profile login is a browser session that acts only for one profile of the
// household. The server refuses it everything household-wide; these helpers
// keep the interface from offering what it would refuse.

/** True when this browser signed in with a profile's own login. */
export function isProfileLogin(session: Session | null | undefined): boolean {
  return !!session?.profile_login;
}

/** value for the household's own session; null for a profile login. */
export function householdOnly<T>(session: Session | null | undefined, value: T): T | null {
  return isProfileLogin(session) ? null : value;
}

/**
 * A profile login reports the household's account ID, and cached data is keyed
 * by that ID. Give it an identity of its own, so a browser shared by the
 * primary user and a profile person never shows one the other's cached data.
 * The ID is a cache key only; it is never sent to the server.
 */
export function withCacheIdentity(session: Session): Session {
  const login = session.profile_login;
  if (!login) return session;
  const id = `${session.user.id.split('~')[0]}~${login.profile_id}`;
  return id === session.user.id ? session : { ...session, user: { ...session.user, id } };
}
