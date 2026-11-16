import { defineConfig } from '@playwright/test';

// Response-only fixtures: serve the built web bundle (for example with
// `pnpm exec vite preview --config web/vite.config.ts --port <port>`) and point
// PASTIME_SILO_UI_URL at it. No account, database or backend is used.
export default defineConfig({
  testDir: '.', testMatch: 'silo-connect.spec.ts', workers: 1, fullyParallel: false,
  retries: 0, timeout: 30_000, reporter: 'list',
  use: { baseURL: process.env.PASTIME_SILO_UI_URL ?? 'http://127.0.0.1:18177', browserName: 'chromium',
    storageState: { cookies: [], origins: [] }, serviceWorkers: 'block', trace: 'retain-on-failure', viewport: { width: 1280, height: 900 } },
});
