import { defineConfig } from '@playwright/test';
export default defineConfig({ testDir: '.', testMatch: 'calendar.spec.ts', workers: 1, fullyParallel: false, retries: 0, timeout: 30000, reporter: 'list', use: { baseURL: 'http://127.0.0.1:18165', browserName: 'chromium', storageState: { cookies: [], origins: [] }, trace: 'retain-on-failure' } });
