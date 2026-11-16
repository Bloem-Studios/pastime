import { expect, test } from '@playwright/test';

// Synthetic API replies exercise form behavior without provider secrets or database writes.
const preferences = { home_rows: [], rating_scale: 'ten', title_language: 'localized', progress_display: 'count', library_view: 'grid', library_sort: 'added', week_start: 'monday', public_profile: false, version: 1, release_region: 'NL' };
test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname;
    const payload = path === '/api/v1/session' ? { csrf_token: 'synthetic', user: { id: 'synthetic-user', username: 'synthetic', display_name: 'Synthetic', email: '', role: 'owner', version: 1 } }
      : path === '/api/v1/me/preferences' ? (route.request().method() === 'PUT' ? { ...route.request().postDataJSON(), version: 2 } : preferences)
      : path === '/api/v1/me/service-kinds' ? { items: [{ kind: 'komga', fields: ['url', 'token'], available: true }, { kind: 'trakt', fields: [], available: false, auth: true }] }
      : path === '/api/v1/discover' ? { rows: [], degraded: false, media_type: 'movie' }
      : path === '/api/v1/auth/methods' ? { recovery: true, registration: true, oidc: { enabled: true, name: 'Authentik' } }
      : { items: [] };
    await route.fulfill({ json: payload });
  });
});

for (const width of [1280, 390, 320]) {
  test(`preferences retain drafts, guard leaving and save explicitly at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/account?tab=preferences');
    await expect(page.getByRole('heading', { name: 'Language and titles', exact: true })).toBeVisible();
    await expect(page.getByText('Unsaved changes', { exact: true })).toHaveCount(0);
    await page.getByLabel('Find a country').fill('United States');
    await page.getByLabel('Release dates for').selectOption('US');
    await page.getByLabel('Ratings', { exact: true }).selectOption('five');
    await page.getByRole('link', { name: 'Tags', exact: true }).click();
    const confirm = page.getByRole('dialog', { name: 'Discard changes?' });
    await expect(confirm).toBeVisible();
    await confirm.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.getByLabel('Ratings', { exact: true })).toHaveValue('five');
    await expect(page.getByLabel('Release dates for')).toHaveValue('US');
    const save = page.waitForRequest(r => r.url().endsWith('/api/v1/me/preferences') && r.method() === 'PUT');
    await page.getByRole('button', { name: 'Save preferences', exact: true }).click();
    expect((await save).postDataJSON()).toMatchObject({ rating_scale: 'five', release_region: 'US' });
    await expect(page.getByText('Preferences saved.', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1);
  });
}

test('integration directory opens one service and preserves import source links', async ({ page }) => {
  await page.goto('/integrations');
  await expect(page.getByText('Ready to connect', { exact: true })).toBeVisible();
  await expect(page.getByText('Not set up on this server', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Server address')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Assistants', exact: true })).toHaveCount(0);
  await page.getByRole('link', { name: /Komga/ }).click();
  await page.getByRole('button', { name: 'Link', exact: true }).click();
  await expect(page.getByLabel('Server address')).toBeVisible();
  await expect(page.getByLabel('API key')).toHaveAttribute('type', 'password');
  await page.getByRole('button', { name: 'Show password', exact: true }).click();
  await expect(page.getByLabel('API key')).toHaveAttribute('type', 'text');
  await page.getByRole('dialog').getByRole('button', { name: 'Close dialog', exact: true }).click();
  await page.goto('/integrations?source=letterboxd');
  await expect(page.getByLabel('Source', { exact: true })).toHaveValue('letterboxd');
  await expect(page.getByRole('heading', { name: 'Linked services', exact: true })).toHaveCount(0);
});

test('manual-entry basics show first and advanced values survive collapsing', async ({ page }) => {
  await page.goto('/discover');
  await page.getByRole('button', { name: 'Add manually', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel('Title', { exact: true })).toBeVisible();
  await expect(dialog.getByLabel('Status', { exact: true })).toBeVisible();
  await expect(dialog.getByLabel('Release year')).not.toBeVisible();
  await dialog.getByText('Advanced details', { exact: true }).click();
  await dialog.getByLabel('Release year').fill('2024');
  await dialog.getByText('Advanced details', { exact: true }).click();
  await dialog.getByText('Advanced details', { exact: true }).click();
  await expect(dialog.getByLabel('Release year')).toHaveValue('2024');
});

test('failed preference save keeps the draft and unsaved protection', async ({ page }) => {
  await page.route('**/api/v1/me/preferences', async route => {
    if (route.request().method() === 'PUT') await route.fulfill({ status: 409, json: { code: 'version_conflict', message: 'Preferences changed elsewhere.' } });
    else await route.fallback();
  });
  await page.goto('/account?tab=preferences');
  await page.getByLabel('Ratings', { exact: true }).selectOption('five');
  await page.getByRole('button', { name: 'Save preferences', exact: true }).click();
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.getByText('Unsaved changes', { exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Home screen', exact: true }).click();
  await page.getByRole('dialog', { name: 'Discard changes?' }).getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByLabel('Ratings', { exact: true })).toHaveValue('five');
});

test('webhook filter draft is protected when returning to the directory', async ({ page }) => {
  await page.goto('/integrations?section=webhooks&server=plex');
  await page.getByLabel('Plex account filter').fill('Synthetic viewer');
  await page.getByRole('link', { name: 'All webhooks', exact: true }).click();
  const confirm = page.getByRole('dialog', { name: 'Discard changes?' });
  await expect(confirm).toBeVisible();
  await confirm.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByLabel('Plex account filter')).toHaveValue('Synthetic viewer');
});

test('local authentication explains provider recovery and can reveal entered passwords', async ({ page }) => {
  await page.route('**/api/v1/session', route => route.fulfill({ status: 401, json: { code: 'unauthenticated', message: 'Sign in.' } }));
  await page.goto('/login');
  await expect(page.getByText('Sign in below with your local Pastime username and password.', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Continue with Authentik' })).toBeVisible();
  const password = page.getByLabel('Password', { exact: true });
  await password.fill('synthetic-passphrase');
  await page.getByRole('button', { name: 'Show password', exact: true }).click();
  await expect(password).toHaveAttribute('type', 'text');
  await page.getByRole('button', { name: 'Hide password', exact: true }).click();
  await expect(password).toHaveAttribute('type', 'password');
  await page.getByRole('link', { name: 'Forgot your local Pastime password?' }).click();
  await expect(page.getByRole('heading', { name: 'Reset your local Pastime password' })).toBeVisible();
  await expect(page.getByText(/Authentik codes and MFA belong to Authentik/)).toBeVisible();
});
