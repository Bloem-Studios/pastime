import { request, type FullConfig } from '@playwright/test';

// Sign in once per run: login is rate limited per address, so every test sharing one
// session keeps quick reruns under the limit. One test still exercises the form.
export default async function globalSetup(config: FullConfig) {
  const baseURL = config.projects[0].use.baseURL!;
  const context = await request.newContext({ baseURL });
  const response = await context.post('/api/v1/login', { headers: { Origin: baseURL }, data: { username: 'e2e_owner', password: 'synthetic-e2e-password-2026' } });
  if (!response.ok()) throw new Error(`e2e sign-in failed with ${response.status()}`);
  await context.storageState({ path: 'test-results/.auth/owner.json' });
  await context.dispose();
}
