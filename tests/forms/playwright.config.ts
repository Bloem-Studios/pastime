import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: '.', outputDir: '/tmp/pastime-forms-test-results', workers: 1, reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:18172', browserName: 'chromium' },
  webServer: { command: 'pnpm exec vite --config tests/forms/vite.config.ts', cwd: '../..', url: 'http://127.0.0.1:18172', reuseExistingServer: false },
});
