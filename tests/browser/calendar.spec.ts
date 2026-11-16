import { test, expect, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';

const password = 'synthetic-test-password-2026';
async function login(page: Page, username: string) {
  await page.goto('/login');
  await page.getByLabel('Username', { exact: true }).fill(username);
  await page.getByLabel('Password', { exact: true }).fill(password);
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.getByRole('link', { name: 'Calendar', exact: true }).first()).toBeVisible();
}
async function capture(page: Page, name: string) {
  if (!process.env.PASTIME_CALENDAR_EVIDENCE) return;
  await mkdir(process.env.PASTIME_CALENDAR_EVIDENCE, { recursive: true });
  await page.screenshot({ path: `${process.env.PASTIME_CALENDAR_EVIDENCE}/${name}.png`, fullPage: true });
}

const title = (page: Page, name: string) => page.locator('.media-title').filter({ hasText: new RegExp(`^${name}$`) });

// The fixture keeps a live Silo calendar snapshot; native favourites and followed
// titles appear, the Silo rows and their playback links never do.
test('native calendar lists favourites and followed titles, not Silo rows', async ({ page }) => {
  await page.request.post('/fixture/renew');
  await login(page, 'calendar_viewer');
  await page.goto('/calendar');
  await expect(title(page, 'A Synthetic Premiere')).toBeVisible();
  await expect(title(page, 'A Synthetic Series')).toBeVisible();
  await expect(title(page, 'A Silo-only Release')).toHaveCount(0);
  await expect(page.getByText('Available in Silo', { exact: true })).toHaveCount(0);
  await expect(page.locator('a.calendar-handoff')).toHaveCount(0);
  await expect(page.locator('a[href="/title/0"],a[href="/title/undefined"]')).toHaveCount(0);
  await capture(page, 'calendar-desktop');
  const filter = page.getByRole('combobox', { name: 'Calendar filter' });
  await filter.selectOption('following');
  await expect(title(page, 'A Synthetic Premiere')).toHaveCount(0);
  await expect(title(page, 'A Synthetic Series')).toBeVisible();
  await filter.selectOption('favourites');
  await expect(title(page, 'A Synthetic Premiere')).toBeVisible();
  await expect(title(page, 'A Synthetic Series')).toHaveCount(0);
  await filter.selectOption('watchlist');
  await expect(title(page, 'A Synthetic Series')).toBeVisible();
  await expect(title(page, 'A Synthetic Premiere')).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
  await capture(page, 'calendar-mobile');
});

test('administrator playback-link settings still persist as plain text', async ({ page }) => {
  await login(page, 'fixture_owner');
  await page.goto('/calendar');
  await page.getByText('Silo playback links', { exact: true }).click();
  const label = '<here>' + 'W'.repeat(90);
  await page.getByLabel('Playback button label', { exact: true }).fill(label);
  await page.getByLabel('Silo web address', { exact: true }).fill('https://player.example.test');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  await page.reload();
  await page.getByText('Silo playback links', { exact: true }).click();
  await expect(page.getByLabel('Playback button label', { exact: true })).toHaveValue(label);
  await expect(page.getByLabel('Example playback address', { exact: true })).toHaveText('https://player.example.test/item/example-content-id');
  await capture(page, 'calendar-admin');
});

test('month calendar navigates date ranges and opens poster details on desktop and phone', async ({ page }) => {
  await page.request.post('/fixture/renew');
  await login(page, 'calendar_viewer');
  await page.goto('/calendar');
  await page.getByRole('button', { name: 'Month', exact: true }).click();
  const month = page.getByLabel('Month', { exact: true });
  const current = await month.inputValue();
  const today = new Date(); const tomorrow = new Date(Date.now() + 864e5);
  const sameMonth = today.getUTCMonth() === tomorrow.getUTCMonth();
  await expect(page.locator('.calendar-day-events')).toHaveCount(sameMonth ? 2 : 1);
  await expect.poll(() => page.locator('.calendar-day-posters img').evaluateAll(imgs => imgs.every(img => (img as HTMLImageElement).naturalWidth > 0))).toBe(true);
  await page.getByRole('button', { name: 'Next month', exact: true }).click();
  await expect(month).not.toHaveValue(current);
  await expect(page.locator('.calendar-day-events')).toHaveCount(sameMonth ? 0 : 1);
  await page.getByRole('button', { name: 'Previous month', exact: true }).click();
  await expect(month).toHaveValue(current);
  await capture(page, 'calendar-month-desktop');
  await page.getByRole('combobox', { name: 'Calendar filter' }).selectOption('favourites');
  await expect(page.locator('.calendar-day-events')).toHaveCount(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
  await capture(page, 'calendar-month-phone');
  await page.locator('.calendar-day-events').click();
  const dialog = page.getByRole('dialog');
  await expect(dialog.getByText('A Synthetic Premiere', { exact: true })).toBeVisible();
  await expect(dialog.locator('a.calendar-handoff')).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(390);
  await capture(page, 'calendar-month-day-phone');
});

test('recommendations point to Silo instead of the retired mirror', async ({ page }) => {
  await login(page, 'calendar_viewer');
  await page.goto('/recommendations');
  await expect(page.getByRole('heading', { name: 'Recommendations', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Not interested' })).toHaveCount(0);
  await expect(page.getByRole('main').getByRole('link', { name: /Connect Silo|For you in Silo/ }).first()).toBeVisible();
});
