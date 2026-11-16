import { defineConfig } from '@playwright/test';

// Response-only fixtures: no bootstrap account, database, or server startup.
export default defineConfig({
  testDir: '.', testMatch: 'review-regressions.spec.ts', workers: 1,
  fullyParallel: false, retries: 0, timeout: 20_000, reporter: 'list',
  outputDir: '/tmp/pastime-review-r2-results',
  use: { baseURL: 'http://localhost:18162', browserName: 'chromium',
    storageState: { cookies: [], origins: [] }, serviceWorkers: 'block', trace: 'retain-on-failure' },
});
