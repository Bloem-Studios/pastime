import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { test as base, expect, request, type APIRequestContext, type Page } from '@playwright/test';
import type { components } from '../../web/src/api/schema.gen';
type Schema = components['schemas'];

type Entry = Schema['Entry'];
type List = Schema['List'];
export const usabilityURL = process.env.PASTIME_USABILITY_BASE_URL ?? 'http://localhost:18162';
const password = 'synthetic-e2e-password-2026';
const privateNote = 'Synthetic private note must never appear in a share';

// Deliberately fail closed. This helper never loads .env or provider credentials.
function databaseURL() {
  const raw = process.env.PASTIME_TEST_DATABASE_URL;
  if (!raw) throw new Error('PASTIME_TEST_DATABASE_URL is required for synthetic usability fixtures');
  const url = new URL(raw);
  if (!['postgres:', 'postgresql:'].includes(url.protocol) || url.hostname !== '127.0.0.1' || url.port !== '18543' || url.pathname !== '/pastime_test') {
    throw new Error('Usability fixtures require task-owned 127.0.0.1:18543/pastime_test');
  }
  const app = new URL(usabilityURL);
  if (app.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(app.hostname) || !['18161', '18162'].includes(app.port)) {
    throw new Error('Usability fixtures require the task loopback app on 18161 or 18162');
  }
  return raw;
}
function sql(statement: string) {
  return execFileSync('psql', [databaseURL(), '-X', '-qAt', '-v', 'ON_ERROR_STOP=1'], { input: statement, encoding: 'utf8', stdio: ['pipe', 'pipe', 'pipe'] }).trim();
}

export class SyntheticAPI {
  constructor(readonly context: APIRequestContext, private readonly csrf: string) {}
  async get<T>(path: string): Promise<T> {
    const result = await this.context.get(path);
    expect(result.ok(), `GET ${path}: ${result.status()}`).toBeTruthy();
    return result.json();
  }
  async write<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, data?: unknown): Promise<T> {
    const result = await this.context.fetch(path, { method, data, headers: { Origin: usabilityURL, 'X-CSRF-Token': this.csrf, 'Idempotency-Key': randomUUID() } });
    // Do not print response bodies: account setup responses contain one-use secrets.
    expect(result.ok(), `${method} ${path}: ${result.status()}`).toBeTruthy();
    return result.status() === 204 ? undefined as T : result.json();
  }
}

type Fixture = {
  api: SyntheticAPI; username: string; book: Entry; movie: Entry; series: Entry;
  catalogID: string; episodeID: string; futureTitle: string; privateNote: string;
  manual: List; smart: List; collabID: string; ownerStorage: Awaited<ReturnType<APIRequestContext['storageState']>>; storage: Awaited<ReturnType<APIRequestContext['storageState']>>;
};

export const test = base.extend<{ populated: Fixture }, { synthetic: Fixture }>({
  synthetic: [async ({}, use) => {
    const run = randomUUID().replaceAll('-', '').slice(0, 16);
    const username = `usability_${run}`;
    const providerID = `usability-${run}`;
    let member: APIRequestContext | undefined;
    const owner = await request.newContext({ baseURL: usabilityURL, storageState: { cookies: [], origins: [] } });
    databaseURL(); // Validate before even signing in.
    expect(sql('SELECT current_database();')).toBe('pastime_test');
    try {
      const login = await owner.post('/api/v1/login', { headers: { Origin: usabilityURL }, data: { username: 'e2e_owner', password } });
      expect(login.ok(), `synthetic owner login: ${login.status()}`).toBeTruthy();
      const session = await (await owner.get('/api/v1/session')).json();
      const admin = new SyntheticAPI(owner, session.csrf_token);
      const account = await admin.write<{ claim_url: string }>('POST', '/api/admin/v1/users', { username, display_name: 'Synthetic usability member', role: 'member' });
      member = await request.newContext({ baseURL: usabilityURL, storageState: { cookies: [], origins: [] } });
      const claim = await member.post('/api/v1/invitations/claim', { headers: { Origin: usabilityURL }, data: { token: new URL(account.claim_url).hash.slice(1), password } });
      expect(claim.ok(), `synthetic account claim: ${claim.status()}`).toBeTruthy();
      const signedIn = await member.post('/api/v1/login', { headers: { Origin: usabilityURL }, data: { username, password } });
      expect(signedIn.ok(), `synthetic member login: ${signedIn.status()}`).toBeTruthy();
      const memberSession = await (await member.get('/api/v1/session')).json();
      const api = new SyntheticAPI(member, memberSession.csrf_token);
      // Only fixture metadata is inserted directly; all personal state uses the API.
      const catalogID = sql(`INSERT INTO catalog_items(root_id,kind,provider,provider_id,title,unit,total,children_loaded,genres)
        VALUES(0,'series','synthetic','${providerID}','Synthetic Orbit ${run}','episodes',2,true,ARRAY['Science fiction']) RETURNING id;`);
      const seasonID = sql(`INSERT INTO catalog_items(root_id,parent_id,kind,provider,provider_id,title,season_number,unit,total,children_loaded) VALUES(0,${catalogID},'season','synthetic','${providerID}-season','Synthetic Season One',1,'episodes',2,true) RETURNING id;`);
      const episodeID = sql(`INSERT INTO catalog_items(root_id,parent_id,kind,provider,provider_id,title,season_number,episode_number,unit,total,runtime_minutes,release_date)
        VALUES(0,${seasonID},'episode','synthetic','${providerID}-1','Synthetic First Contact ${run}',1,1,'minutes',42,42,current_date-1) RETURNING id;`);
      const futureTitle = `Synthetic Next Contact ${run}`;
      sql(`INSERT INTO catalog_items(root_id,parent_id,kind,provider,provider_id,title,season_number,episode_number,unit,total,runtime_minutes,release_date)
        VALUES(0,${seasonID},'episode','synthetic','${providerID}-2','${futureTitle}',1,2,'minutes',42,42,current_date+2);`);
      async function entry(body: Schema['EntryInput']) {
        const created = await api.write<{ id: string }>('POST', '/api/v1/library', body);
        return api.get<Entry>(`/api/v1/library/${created.id}`);
      }
      const book = await entry({ title: `Synthetic Atlas ${run}`, media_type: 'book', unit: 'pages', total: 300, status: 'in_progress', progress: 25, notes: privateNote, rating: 8 });
      const movie = await entry({ title: `Synthetic Cinema ${run}`, media_type: 'movie', unit: 'minutes', total: 100, status: 'planned' });
      const tracked = await api.write<{ id: string }>('POST', '/api/v1/library/track', { catalog_id: catalogID, status: 'in_progress' });
      let series = await api.get<Entry>(`/api/v1/library/${tracked.id}`);
      await api.write('POST', `/api/v1/library/${series.id}/plays`, { version: series.version, items: [{ catalog_id: episodeID }] });
      series = await api.get<Entry>(`/api/v1/library/${series.id}`);
      let manual = await api.write<List>('POST', '/api/v1/lists', { name: `Synthetic shelf ${run}`, kind: 'manual', visibility: 'unlisted', slug: `shelf-${run}`, sort: 'position' });
      for (const item of [book, movie]) {
        const changed = await api.write<{ version: number }>('PUT', `/api/v1/lists/${manual.id}/entries/${item.id}`, { version: manual.version, present: true });
        manual.version = changed.version;
      }
      await api.write('PUT', `/api/v1/lists/${manual.id}/entries/${book.id}/note`, { version: manual.version, note: 'Synthetic shared shelf note' });
      manual = await api.get<List>(`/api/v1/lists/${manual.id}`);
      const smart = await api.write<List>('POST', '/api/v1/lists', { name: `Synthetic reading ${run}`, kind: 'smart', visibility: 'private', sort: 'title', rules: { media_types: ['book'], statuses: ['in_progress'] } });
      const collab = await api.write<{ id: string }>('POST', '/api/v1/collab-lists', { name: `Synthetic together ${run}`, description: 'Synthetic collaborative populated fixture' });
      await api.write('POST', `/api/v1/collab-lists/${collab.id}/items`, { catalog_id: catalogID, note: 'Synthetic collaborative note' });
      await api.write('PUT', `/api/v1/collab-lists/${collab.id}/members`, { username: 'e2e_owner', role: 'viewer' });
      await use({ api, username, book, movie, series, catalogID, episodeID, futureTitle, privateNote, manual, smart, collabID: collab.id, ownerStorage: await owner.storageState(), storage: await member.storageState() });
    } finally {
      await member?.dispose();
      await owner.dispose();
      // Delete only our random account and metadata. Retain unrelated test fixtures.
      sql(`DELETE FROM users WHERE username='${username}'; DELETE FROM catalog_items WHERE provider='synthetic' AND provider_id='${providerID}';`);
    }
  }, { scope: 'worker' }],
  populated: async ({ synthetic, context }, use) => {
    await context.clearCookies();
    await context.addCookies(synthetic.storage.cookies);
    await use(synthetic);
  },
});
export { expect };

export async function fitsViewport(page: Page, width: number) {
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1);
}
