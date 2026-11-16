import { defineConfig } from '@playwright/test';
import { usabilityURL } from './usability-fixtures';

// Parent starts the task-owned app after building the repairs. Never reuse a
// production session or invoke the existing 18161 webServer/global setup.
export default defineConfig({
  testDir: '.', testMatch: 'usability.spec.ts', fullyParallel: false, workers: 1,
  retries: 0, maxFailures: 1, timeout: 30_000, reporter: 'list',
  use: { baseURL: usabilityURL, browserName: 'chromium', storageState: { cookies: [], origins: [] }, trace: 'retain-on-failure' },
});
