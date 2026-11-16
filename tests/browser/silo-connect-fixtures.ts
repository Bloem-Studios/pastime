import { test as base, expect, type Page } from '@playwright/test';

// Response-only fixtures for the Connect Silo release: no account, database or
// server state. Every /api request is answered here; unknown writes fail closed.
const date = '2026-10-14T09:00:00Z';
export const siloURL = 'https://silo.example.test';
export const syntheticKey = 'ptbs_synthetic-fixture-key-not-a-credential';
type Profile = { profile_id: string; name: string; connected: boolean; expires_at: string | null };
const item = { id: '101', kind: 'movie', provider: 'tmdb', provider_id: '901', title: 'Synthetic Premiere', original_title: '', overview: 'A synthetic title for the Silo link check.', image_url: '', backdrop_url: '', parent_id: null, season_number: null, episode_number: null, release_date: null, year: 2026, runtime_minutes: 100, unit: 'minutes', total: 100, release_status: '', genres: [], credits: [], external_ids: {}, details: {}, provider_rating: null, children_loaded: true, refreshed_at: date };

export class SiloResponses {
  available = true; serviceAccess = true; url = siloURL; routeMissing = false;
  profiles: Profile[] = [
    { profile_id: '11', name: 'Main', connected: false, expires_at: null },
    { profile_id: '12', name: 'Child', connected: true, expires_at: '2027-01-12T09:00:00Z' },
  ];
  refuse: { status: number; code: string; message: string } | null = null;
  reads: string[] = []; writes: string[] = [];
  connect() { return { available: this.available, service_access: this.serviceAccess, silo_url: this.url, profiles: this.profiles }; }
  async install(page: Page) {
    await page.route('**/api/**', async route => {
      const request = route.request(); const url = new URL(request.url()); const path = url.pathname;
      if (!path.startsWith('/api/')) { await route.fallback(); return; }
      const method = request.method(); let body: unknown = { items: [], next_cursor: null }; let status = 200;
      const credential = /^\/api\/v1\/integrations\/silo\/profiles\/(\d+)\/credential$/.exec(path);
      if (method === 'GET') this.reads.push(path); else this.writes.push(`${method} ${path}`);
      if (credential && method === 'POST') {
        const profile = this.profiles.find(p => p.profile_id === credential[1]);
        if (this.refuse) { status = this.refuse.status; body = { code: this.refuse.code, message: this.refuse.message }; }
        else if (!profile) { status = 404; body = { code: 'not_found', message: 'Not found.' }; }
        else { profile.connected = true; profile.expires_at = '2027-01-12T09:00:00Z'; body = { credential: syntheticKey, expires_at: profile.expires_at, profile_name: profile.name }; }
      } else if (credential && method === 'DELETE') {
        const profile = this.profiles.find(p => p.profile_id === credential[1]);
        if (profile) { profile.connected = false; profile.expires_at = null; status = 204; } else { status = 404; body = { code: 'not_found', message: 'Not found.' }; }
      } else if (method !== 'GET') { status = 409; body = { code: 'synthetic_rejection', message: 'Synthetic write rejected' }; }
      else if (path === '/api/v1/session') body = { csrf_token: 'synthetic-csrf', user: { id: '701', username: 'household', display_name: 'Household Owner', email: '', role: 'member', active: true, password_ready: true, version: 1, created_at: date, last_login_at: date } };
      else if (path === '/api/v1/profiles') body = { items: this.profiles.map((p, i) => ({ id: p.profile_id, account_id: '701', name: p.name, default: i === 0, created_at: date, bloem_available: true, bloem_linked: false, bloem_active: false })) };
      else if (path === '/api/v1/integrations/silo/connect') { if (this.routeMissing) { status = 404; body = { code: 'not_found', message: 'Not found.' }; } else body = this.connect(); }
      else if (path === '/api/v1/auth/methods') body = { password: true, recovery: false, registration: false, oidc: { enabled: false, name: 'SSO', start_url: '/api/v1/auth/oidc/start' } };
      else if (path === '/api/v1/me/preferences') body = { home_rows: [], rating_scale: 'off', title_language: 'localized', progress_display: 'count', library_view: 'grid', library_sort: 'added', week_start: 'monday', public_profile: false, onboarded: true, version: 0 };
      else if (path === '/api/v1/home') body = { continue: [], up_next: [], upcoming: [], planned: [], recently_finished: [], stats: { total: 0, in_progress: 0, planned: 0, completed: 0, completed_cycles: 0 } };
      else if (path === '/api/v1/me/service-kinds') body = { items: (['trakt', 'simkl', 'jellyfin', 'plex', 'audiobookshelf', 'listenbrainz', 'kavita'] as const).map(kind => ({ kind, name: kind, fields: ['token'], auth: '', available: true })) };
      else if (path === '/api/v1/me/services') body = { items: [] };
      else if (path === '/api/v1/connections') body = { items: [] };
      else if (path === '/api/v1/catalog/101') body = { item, entry: null };
      else if (path === '/api/v1/catalog/101/personal-state') body = { favourite: false, watchlist: false };
      await route.fulfill({ status, contentType: 'application/json', ...(status === 204 ? {} : { body: JSON.stringify(body) }) });
    });
  }
}

// Installed for every test, so no request can reach a backend even when a test
// does not inspect the responses.
export const test = base.extend<{ silo: SiloResponses }>({ silo: [async ({ page }, use) => { const silo = new SiloResponses(); await silo.install(page); await use(silo); }, { auto: true }] });
export { expect };

/** Opens the app with the given tracking profile selected, as the profile menu would. */
export async function openAs(page: Page, profileID: string, path: string) {
  await page.addInitScript(id => { try { localStorage.setItem('pastime-profile:701', id); localStorage.setItem('pastime-language', 'en'); } catch { /* storage unavailable */ } }, profileID);
  await page.goto(path);
}
