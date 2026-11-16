import { defineConfig } from '@playwright/test';

const baseURL = 'http://127.0.0.1:18161';

export default defineConfig({
  testDir: '.',
  globalSetup: './global-setup.ts',
  // Other specs require their own explicitly prepared fixtures/configuration.
  testMatch: ['app.spec.ts', 'swagger.spec.ts'],
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: { baseURL, browserName: 'chromium', trace: 'retain-on-failure', storageState: 'test-results/.auth/owner.json' },
  webServer: {
    command: 'sh tests/browser/serve.sh',
    cwd: '../..',
    url: `${baseURL}/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
    env: { LISTEN_ADDR: '127.0.0.1:18161' },
  },
});
