import { test as base, expect, type Page } from '@playwright/test';

const date = '2026-10-09T00:00:00Z';
export const entry = { id: '501', catalog_id: '101', title: 'Review title 0', media_type: 'movie', year: 2026, unit: 'minutes', total: 100, status: 'planned', progress: 0, rating: null, cycle: 1, completed_cycles: 0, version: 1, created_at: date, updated_at: date, image_url: '', backdrop_url: '', original_title: '', started_at: null, finished_at: null, last_activity_at: null, localized_title: null, notes: '', edition: null, episode_order: null, scored_at: null };
export const item = { id: '101', kind: 'movie', provider: 'tmdb', provider_id: '901', title: entry.title, original_title: '', overview: 'Synthetic review fixture', image_url: '', backdrop_url: '', parent_id: null, season_number: null, episode_number: null, release_date: null, year: 2026, runtime_minutes: 100, unit: 'minutes', total: 100, release_status: '', genres: [], credits: [], external_ids: {}, details: {}, provider_rating: null, children_loaded: true, refreshed_at: date };
export class ReviewResponses {
  account = 'A'; signedIn = true; tracked = false; searchDelay = 0; hidden = true;
  reads = { person: 0, studio: 0, search: 0, hidden: 0 };
  writes: string[] = [];
  session() { return { csrf_token: 'synthetic-review-csrf', user: { id: this.account === 'A' ? '701' : '702', username: this.account, display_name: `Reviewer ${this.account}`, email: '', role: 'member', active: true, password_ready: true, version: 1, created_at: date, last_login_at: date } }; }
  result(index = 0, membership = true) { return { provider: 'tmdb', provider_id: String(901 + index), catalog_id: String(101 + index), media_type: 'movie', title: `Review title ${index}`, year: 2026, image_url: '', overview: '', credits: [], library_entry_id: membership ? '501' : null, library_status: membership ? 'planned' : null, role: '' }; }
  async install(page: Page) {
    // Every API request is fulfilled here. Unknown writes fail closed and can
    // never reach the loopback backend, let alone a production system.
    await page.route('**/api/**', async route => {
      const request = route.request(); const url = new URL(request.url()); const path = url.pathname;
      if (!path.startsWith('/api/')) { await route.fallback(); return; }
      const method = request.method(); let body: unknown = { items: [], next_cursor: null }; let status = 200;
      if (method !== 'GET') {
        this.writes.push(`${method} ${path}`);
        if (path === '/api/v1/logout') { this.signedIn = false; status = 204; }
        else if (path === '/api/v1/login') { this.account = request.postDataJSON().username; this.signedIn = true; body = this.session(); }
        else if (path === '/api/v1/library/track') { this.tracked = true; body = { id: '502' }; }
        else if (path === '/api/v1/discover/dismissals' && method === 'DELETE') { this.hidden = false; status = 204; }
        else { status = 409; body = { error: { code: 'synthetic_rejection', message: 'Synthetic write rejected' } }; }
      } else if (path === '/api/v1/session') { body = this.session(); if (!this.signedIn) status = 401; }
      else if (path === '/api/v1/auth/methods') body = { password: true, recovery: false, registration: false, oidc: { enabled: false, name: 'Authentik' } };
      else if (path === '/api/v1/me/preferences') body = { home_rows: [], rating_scale: 'off', title_language: 'localized', progress_display: 'count', library_view: 'grid', library_sort: 'added', week_start: 'monday', public_profile: false, version: 0 };
      else if (path === '/api/v1/catalog/providers') body = { items: [{ name: 'tmdb', kinds: ['movie'], defaults: ['movie'], configured: true, search: { supported: true, pagination: true, year: true, genre: false, sorts: [] }, browse: { supported: false } }] };
      else if (path === '/api/v1/catalog/search') { this.reads.search++; if (this.searchDelay) await new Promise(resolve => setTimeout(resolve, this.searchDelay)); body = { items: Array.from({ length: 60 }, (_, i) => this.result(i)), source: 'provider', degraded: false, provider: 'tmdb', availability: 'available', next_page: null }; }
      else if (path.startsWith('/api/v1/people/')) { this.reads.person++; body = { provider: 'tmdb', id: '9', name: 'Review Person', biography: '', birthday: null, deathday: null, known_for: '', image_url: '', credits: [this.result(0, this.account === 'A' || this.tracked)] }; }
      else if (path.startsWith('/api/v1/studios/')) { this.reads.studio++; body = { provider: 'tmdb', id: '9', kind: 'company', name: 'Review Studio', country: '', description: '', logo_url: '', titles: [this.result(0, this.account === 'A' || this.tracked)] }; }
      else if (/^\/api\/v1\/catalog\/\d+$/.test(path)) body = { item: { ...item, id: path.split('/').at(-1) }, entry };
      else if (path === '/api/v1/library/501') body = entry;
      else if (path === '/api/v1/me/service-kinds') body = { items: ['plex', 'jellyfin', 'emby', 'steam', 'kitsu', 'kavita', 'kapowarr'].map(kind => ({ kind, fields: ['url', 'token'], available: true, auth: false })) };
      else if (path === '/api/v1/discover') body = { media_type: 'movie', rows: [], degraded: false, availability: 'available' };
      else if (path === '/api/v1/discover/dismissals') { this.reads.hidden++; body = { items: this.hidden ? [{ ...this.result(0, false), created_at: date }] : [], next_cursor: null }; }
      await route.fulfill({ status, contentType: 'application/json', ...(status === 204 ? {} : { body: JSON.stringify(body) }) });
    });
  }
}
export const test = base.extend<{ responses: ReviewResponses }>({ responses: async ({ page }, use) => { const responses = new ReviewResponses(); await responses.install(page); await use(responses); } });
export { expect };

export async function spaNavigate(page: Page, path: string) {
  // Click a real React Router anchor so the QueryClient survives this transition.
  const link = page.locator(`a[href="${path}"]`).first();
  if (await link.count()) { await link.click(); return; }
  await page.evaluate(path => { window.history.pushState({}, '', path); window.dispatchEvent(new PopStateEvent('popstate')); }, path);
}
