import { mkdir } from 'node:fs/promises';
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { test, expect, openAs, siloURL, syntheticKey } from './silo-connect-fixtures';

// Whole pages on desktop; the visible screen for dialogs and on phones, where the
// fixed bottom navigation would otherwise be painted mid-page.
async function capture(page: Page, name: string, fullPage = true) {
  const folder = process.env.PASTIME_SILO_EVIDENCE;
  if (!folder) return;
  await mkdir(folder, { recursive: true });
  await page.evaluate(async () => { (document.activeElement as HTMLElement | null)?.blur(); await Promise.all(document.getAnimations().map(animation => animation.finished)); });
  await page.screenshot({ path: `${folder}/${name}.png`, fullPage });
}
const phone = { width: 390, height: 844 };
async function fitsPhone(page: Page) {
  await page.setViewportSize(phone);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(phone.width);
}
async function accessible(page: Page) {
  const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(scan.violations.map(v => `${v.id}: ${v.nodes.map(n => n.target.join(' ')).join(', ')}`)).toEqual([]);
}
const retired = (paths: string[]) => paths.filter(path => /ai-recommendations|bloem-availability|\/library\/available|bloem\/library/.test(path));

test('navigation no longer offers In Silo or the recommendations mirror', async ({ page, silo }) => {
  await openAs(page, '11', '/');
  const sidebar = page.locator('aside.sidebar');
  await expect(sidebar.getByRole('link', { name: 'Library', exact: true })).toBeVisible();
  await expect(sidebar.getByRole('link', { name: 'In Silo', exact: true })).toHaveCount(0);
  await expect(sidebar.getByRole('link', { name: 'Recommendations', exact: true })).toHaveCount(0);
  await page.setViewportSize(phone);
  const bottom = page.getByRole('navigation', { name: 'Mobile workspace' });
  await expect(bottom.getByRole('link', { name: 'Library', exact: true })).toBeVisible();
  await expect(bottom.getByRole('link', { name: 'Recommendations', exact: true })).toHaveCount(0);
  await page.goto('/more');
  await expect(page.getByRole('link', { name: /Recommendations/ })).toHaveCount(0);
  await page.goto('/library/available');
  await expect(page).toHaveURL(/\/$/);
  expect(retired(silo.reads)).toEqual([]);
});

test('an unconnected profile is pointed to Connect Silo; a connected one to For you in Silo', async ({ page, silo }) => {
  await openAs(page, '11', '/');
  const notice = page.getByRole('complementary', { name: 'Silo connection' });
  await expect(notice.getByText('This profile is not connected to Silo', { exact: true })).toBeVisible();
  await expect(notice).toContainText('Another profile on this account is connected to Silo.');
  await expect(notice.getByRole('link', { name: 'Connect Silo', exact: true })).toHaveAttribute('href', '/integrations?section=silo');
  await expect(page.getByRole('link', { name: 'For you in Silo' })).toHaveCount(0);
  await capture(page, 'home-banner-desktop');
  await page.getByRole('combobox', { name: 'Profile', exact: true }).selectOption({ label: 'Child' });
  await expect(page.getByRole('link', { name: 'For you in Silo' })).toHaveAttribute('href', `${siloURL}/recommendations`);
  await expect(page.getByRole('complementary', { name: 'Silo connection' })).toHaveCount(0);
  // Nothing to act on: no banner without tracking access or a registered Silo.
  silo.serviceAccess = false;
  await openAs(page, '11', '/calendar');
  await expect(page.getByRole('heading', { name: 'Calendar', exact: true })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Silo connection' })).toHaveCount(0);
  silo.serviceAccess = true; silo.routeMissing = true;
  await openAs(page, '11', '/');
  await expect(page.getByRole('heading', { name: 'Your library starts here' })).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Silo connection' })).toHaveCount(0);
  expect(retired(silo.reads)).toEqual([]);
});

test('Connect Silo creates, replaces and revokes a profile key', async ({ page, silo }) => {
  await openAs(page, '11', '/integrations?section=silo');
  await expect(page.getByRole('heading', { name: 'Connect Silo', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Silo', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('link', { name: 'Open Watch Providers in Silo' })).toHaveAttribute('href', `${siloURL}/settings/watch-providers`);
  await expect(page.getByText('Floppy')).toHaveCount(0);
  const main = page.locator('article.record-row').filter({ hasText: 'Main' });
  const child = page.locator('article.record-row').filter({ hasText: 'Child' });
  await expect(main).toContainText('This profile');
  await expect(main).toContainText('Not connected');
  await expect(child).toContainText('Connected');
  await expect(child).toContainText('Stays valid while Silo keeps syncing.');
  await accessible(page);
  await capture(page, 'connect-silo-desktop');

  await main.getByRole('button', { name: 'Create Silo key', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Silo key for Main' });
  await expect(dialog).toContainText('Settings → Watch Providers');
  await expect(dialog.getByLabel('One-time secret')).toHaveValue(syntheticKey);
  await accessible(page);
  await capture(page, 'connect-silo-key-desktop', false);
  await dialog.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(main.getByText('Connected', { exact: true })).toBeVisible();
  expect(silo.writes).toEqual(['POST /api/v1/integrations/silo/profiles/11/credential']);

  // A connected profile asks before its key is replaced.
  await child.getByRole('button', { name: 'Create new key', exact: true }).click();
  await expect(page.getByRole('alertdialog').or(page.getByRole('dialog'))).toContainText('Replace the Silo key for Child?');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  expect(silo.writes).toHaveLength(1);
  await child.getByRole('button', { name: 'Create new key', exact: true }).click();
  await page.getByRole('button', { name: 'Create new key', exact: true }).last().click();
  await expect(page.getByRole('dialog', { name: 'Silo key for Child' })).toBeVisible();
  await page.getByRole('button', { name: 'Done', exact: true }).click();

  await main.getByRole('button', { name: 'Disconnect from Silo', exact: true }).click();
  await expect(page.getByRole('dialog')).toContainText('Disconnect Main from Silo?');
  await page.getByRole('button', { name: 'Disconnect', exact: true }).click();
  await expect(main).toContainText('Not connected');
  await expect(main.getByRole('button', { name: 'Disconnect from Silo' })).toHaveCount(0);
  expect(silo.writes).toEqual(['POST /api/v1/integrations/silo/profiles/11/credential', 'POST /api/v1/integrations/silo/profiles/12/credential', 'DELETE /api/v1/integrations/silo/profiles/11/credential']);
  await fitsPhone(page);
  await accessible(page);
  await page.evaluate(() => window.scrollTo(0, 0));
  await capture(page, 'connect-silo-phone', false);
  await child.scrollIntoViewIfNeeded();
  await capture(page, 'connect-silo-phone-profiles', false);
  await child.getByRole('button', { name: 'Create new key', exact: true }).click();
  await page.getByRole('button', { name: 'Create new key', exact: true }).last().click();
  await expect(page.getByRole('dialog', { name: 'Silo key for Child' })).toBeVisible();
  await capture(page, 'connect-silo-key-phone', false);
});

test('Connect Silo explains refused key requests and missing access', async ({ page, silo }) => {
  silo.refuse = { status: 409, code: 'silo_not_registered', message: 'No Silo server.' };
  await openAs(page, '11', '/integrations?section=silo');
  const main = page.locator('article.record-row').filter({ hasText: 'Main' });
  await main.getByRole('button', { name: 'Create Silo key', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveText('Silo is not set up on this Pastime server yet. Ask your Pastime administrator.');
  silo.refuse = { status: 429, code: 'rate_limited', message: 'Too many requests.' };
  await main.getByRole('button', { name: 'Create Silo key', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveText('Too many key requests. Wait a minute and try again.');

  silo.refuse = null; silo.serviceAccess = false;
  await page.reload();
  await expect(page.getByRole('status').filter({ hasText: 'does not have access to tracking' })).toBeVisible();
  await expect(main.getByRole('button', { name: 'Create Silo key', exact: true })).toBeDisabled();
  await expect(page.locator('article.record-row').filter({ hasText: 'Child' }).getByRole('button', { name: 'Disconnect from Silo' })).toBeEnabled();
  await capture(page, 'connect-silo-no-access-desktop');

  silo.serviceAccess = true; silo.available = false;
  await page.reload();
  await expect(page.getByText('Silo is not set up on this Pastime server yet. Ask your Pastime administrator.')).toBeVisible();
  await expect(page.locator('article.record-row')).toHaveCount(0);
});

test('integrations lists Silo as its own task instead of under Advanced', async ({ page }) => {
  await openAs(page, '11', '/integrations');
  const tasks = page.getByRole('navigation', { name: 'Integration tasks' });
  await expect(tasks.getByRole('link')).toHaveText(['Linked services', 'Silo', 'Import and export', 'Kept exports', 'KOReader', 'Advanced']);
  await expect(tasks.getByRole('link', { name: 'Silo', exact: true })).toHaveAttribute('href', '/integrations?section=silo');
  await capture(page, 'integrations-desktop');
  await fitsPhone(page);
  await capture(page, 'integrations-phone', false);
  await page.setViewportSize({ width: 1280, height: 900 });
  await tasks.getByRole('link', { name: 'Advanced', exact: true }).click();
  const directory = page.locator('.directory-list');
  await expect(directory.getByRole('link', { name: /Navidrome/ })).toBeVisible();
  await expect(directory.getByRole('link', { name: 'Silo', exact: true })).toHaveCount(0);
});

test('titles link to Silo search and recommendations to Silo, without the retired reads', async ({ page, silo }) => {
  await openAs(page, '12', '/title/101');
  await expect(page.getByRole('heading', { name: 'Synthetic Premiere', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Find in Silo', exact: true })).toHaveAttribute('href', `${siloURL}/search?q=Synthetic+Premiere`);
  await expect(page.getByText('Available in Silo')).toHaveCount(0);
  await openAs(page, '12', '/recommendations');
  await expect(page.getByRole('link', { name: 'For you in Silo' })).toHaveAttribute('href', `${siloURL}/recommendations`);
  await openAs(page, '11', '/title/101');
  await expect(page.getByRole('heading', { name: 'Synthetic Premiere', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Find in Silo' })).toHaveCount(0);
  await openAs(page, '11', '/recommendations');
  await expect(page.getByRole('heading', { name: 'Recommendations are in Silo' })).toBeVisible();
  await expect(page.getByRole('main').getByRole('link', { name: 'Connect Silo', exact: true })).toHaveAttribute('href', '/integrations?section=silo');
  expect(retired(silo.reads)).toEqual([]);
});
