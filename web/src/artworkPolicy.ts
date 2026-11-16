// Shared failure memory stops a grid of duplicate covers repeatedly retrying an
// unavailable host. Browsers coalesce successful URL requests in their HTTP cache.
const failures = new Map<string, number>();
export function artworkAllowed(url: string, now = Date.now()) {
 const until=failures.get(url); if (until===undefined) return true;
 if (now>=until) { failures.delete(url); return true; } return false;
}
export function artworkFailed(url: string, now=Date.now()) {
 if (failures.size>=1000) failures.delete(failures.keys().next().value!);
 failures.set(url,now+5*60*1000);
}
