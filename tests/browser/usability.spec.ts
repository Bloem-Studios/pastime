import { randomUUID } from 'node:crypto';
import { test, expect, fitsViewport, usabilityURL } from './usability-fixtures';
import type { components } from '../../web/src/api/schema.gen';
type Schema = components['schemas'];

test.use({ baseURL: usabilityURL });

for (const width of [1280, 390, 320]) {
  test(`populated library grids, tables, lists, charts and calendar fit ${width}px`, async ({ page, populated: f }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width, height: width === 1280 ? 800 : 844 });
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    for (const view of ['grid', 'list']) {
      await page.goto(`/library?view=${view}`);
      await expect(page.getByRole('link', { name: f.book.title, exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: f.movie.title, exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
      if (view === 'list') {
        const row = page.getByRole('row').filter({ has: page.getByRole('link', { name: f.book.title, exact: true }) });
        await expect(row).toContainText('In progress');
        await expect(row).toContainText('300');
      }
      await fitsViewport(page, width);
    }
    await page.goto(`/lists/${f.manual.id}`);
    await expect(page.getByRole('heading', { level: 1, name: f.manual.name })).toBeVisible();
    await expect(page.getByRole('link', { name: f.book.title, exact: true })).toBeVisible();
    await expect(page.getByText('Synthetic shared shelf note', { exact: true })).toBeVisible();
    await fitsViewport(page, width);
    await page.goto(`/lists/${f.smart.id}`);
    await expect(page.getByRole('link', { name: f.book.title, exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: f.movie.title, exact: true })).toHaveCount(0);
    await fitsViewport(page, width);
    await page.goto(`/lists/${f.manual.id}?view=tiers`);
    await expect(page.getByRole('region', { name: 'Tier board' })).toBeVisible();
    await fitsViewport(page, width);
    await page.goto(`/collab/${f.collabID}`);
    await expect(page.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
    await fitsViewport(page, width);
    await page.goto(`/library/${f.book.id}`);
    await expect(page.getByLabel('Your notes')).toHaveValue(f.privateNote);
    await fitsViewport(page, width);
    if (width < 1280) {
      await page.goto('/more');
      const destinations = page.getByRole('navigation', { name: 'More destinations' });
      await expect(destinations.getByRole('link', { name: /Owned/ })).toBeVisible();
      await expect(destinations.getByRole('link', { name: /Calendar/ })).toBeVisible();
      await fitsViewport(page, width);
    }
    await page.goto('/stats');
    await expect(page.getByRole('img', { name: /Minutes per day/ })).toBeVisible();
    await expect(page.getByRole('table', { name: 'Time by media type' })).toContainText('Series');
    await expect(page.locator('.metric').filter({ has: page.locator('.label', { hasText: 'Plays' }) }).locator('.value')).toHaveText('1');
    await fitsViewport(page, width);
    await page.goto('/history');
    await expect(page.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
    await expect(page.locator('.history-list')).toContainText('S1 · E1');
    await fitsViewport(page, width);
    await page.goto('/calendar');
    await expect(page.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
    await expect(page.locator('.agenda')).toContainText(f.futureTitle);
    await fitsViewport(page, width);
    expect(errors).toEqual([]);
  });
}

test('manual detail updates progress durably and preserves private notes', async ({ page, populated: f }) => {
  await page.goto(`/library/${f.book.id}`);
  await expect(page.getByRole('heading', { level: 1, name: f.book.title })).toBeVisible();
  await page.getByLabel('Progress', { exact: true }).fill('50');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Progress saved.', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByLabel('Progress', { exact: true })).toHaveValue('50');
  await expect(page.getByLabel('Private notes')).toHaveValue(f.privateNote);
  const entry = await f.api.get<Schema['Entry']>(`/api/v1/library/${f.book.id}`);
  expect(entry.progress).toBe(50);
  expect(entry.notes).toBe(f.privateNote);
});

test('populated episode detail distinguishes watched and upcoming episodes', async ({ page, populated: f }) => {
  await page.goto(`/title/${f.catalogID}`);
  await expect(page.getByRole('heading', { level: 1, name: f.series.title })).toBeVisible();
  // The season holding the next episode opens by itself.
  const season = page.locator('details.season-block').filter({ has: page.locator('summary', { hasText: 'Synthetic Season One' }) });
  if (!await season.evaluate(d => (d as HTMLDetailsElement).open)) await season.locator('summary').click();
  await expect(page.getByRole('button', { name: /Mark .*Synthetic First Contact.* as not watched/ })).toHaveAttribute('aria-pressed', 'true');
  // An episode that has not aired yet says when it airs and cannot be marked.
  await expect(season.locator('.ep-list li').filter({ hasText: f.futureTitle })).toContainText('Airs');
  await expect(season.locator('.ep-list li').filter({ hasText: f.futureTitle }).locator('button.ep-toggle')).toHaveCount(0);
});

test('tier board placement survives reload and cancellation preserves list membership', async ({ page, populated: f }) => {
  await page.goto(`/lists/${f.manual.id}?view=tiers`);
  await expect(page.getByRole('region', { name: 'Tier board' })).toBeVisible();
  await page.getByRole('button', { name: 'Add tier', exact: true }).click();
  await page.locator('.tier-pick').filter({ hasText: f.book.title }).click();
  await page.getByRole('button', { name: 'Place here', exact: true }).first().click();
  await page.getByRole('button', { name: 'Save board', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Save board', exact: true })).toBeDisabled();
  await page.reload();
  await expect(page.locator('.tier-row:not(.unranked) .tier-pick').filter({ hasText: f.book.title })).toBeVisible();
  await page.getByRole('button', { name: 'List', exact: true }).click();
  await page.getByRole('button', { name: `Remove ${f.book.title} from list`, exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Remove from this list?' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByRole('link', { name: f.book.title, exact: true })).toBeVisible();
});

test('collaborative owner sees populated titles while a viewer cannot edit', async ({ page, browser, populated: f }) => {
  await page.goto(`/collab/${f.collabID}`);
  await expect(page.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Add titles', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /people; manage members/ }).click();
  await expect(page.getByRole('dialog', { name: 'People' })).toContainText('e2e_owner');
  await page.keyboard.press('Escape');
  const viewer = await browser.newContext({ baseURL: usabilityURL, storageState: f.ownerStorage });
  try {
    const other = await viewer.newPage();
    await other.goto(`/collab/${f.collabID}`);
    await expect(other.getByRole('link', { name: f.series.title, exact: true })).toBeVisible();
    await expect(other.getByRole('button', { name: 'Add titles', exact: true })).toHaveCount(0);
    await expect(other.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
    await expect(other.getByText('Synthetic collaborative note', { exact: true })).toBeVisible();
    const session = await (await viewer.request.get('/api/v1/session')).json();
    const write = await viewer.request.post(`/api/v1/collab-lists/${f.collabID}/items`, { headers: { Origin: usabilityURL, 'X-CSRF-Token': session.csrf_token }, data: { catalog_id: f.catalogID } });
    expect(write.status()).toBe(403);
  } finally { await viewer.close(); }
});

test('anonymous share reveals list notes and titles without private progress or ratings', async ({ browser, populated: f }) => {
  const anonymous = await browser.newContext({ baseURL: usabilityURL, storageState: { cookies: [], origins: [] }, viewport: { width: 320, height: 844 } });
  try {
    const page = await anonymous.newPage();
    await page.goto(`/u/${f.username}/${f.manual.share_token}`);
    await expect(page.getByRole('heading', { level: 1, name: f.manual.name })).toBeVisible();
    await expect(page.locator('.public-items')).toContainText(f.book.title);
    await expect(page.locator('.public-items')).toContainText('Synthetic shared shelf note');
    await expect(page.getByText(f.privateNote, { exact: true })).toHaveCount(0);
    const result = await anonymous.request.get(`/api/v1/public/${f.username}/lists/${f.manual.share_token}`);
    expect(result.ok()).toBeTruthy();
    const payload = await result.json();
    expect(JSON.stringify(payload)).not.toContain(f.privateNote);
    for (const item of payload.items) {
      expect(item).not.toHaveProperty('progress');
      expect(item).not.toHaveProperty('rating');
      expect(item).not.toHaveProperty('notes');
    }
    await fitsViewport(page, 320);
    await page.goto(`/u/${f.username}/unavailable-synthetic-share`);
    await expect(page.getByRole('heading', { name: 'This list is not available' })).toBeVisible();
  } finally { await anonymous.close(); }
});

for (const width of [320, 390]) {
  test(`manual editor keyboard, dirty cancellation and save fit ${width}px with a short viewport`, async ({ page, populated: f }) => {
    await page.setViewportSize({ width, height: 480 }); // Layout proxy; not a real mobile virtual keyboard.
    await page.goto('/library');
    const opener = page.getByRole('main').getByRole('button', { name: 'Add media', exact: true });
    await opener.click();
    await expect(page).toHaveURL(/\/discover/);
    await page.getByRole('button', { name: 'Add manually', exact: true }).click();
    const editor = page.getByRole('dialog', { name: 'Add media', exact: true });
    await expect(editor.getByLabel('Title', { exact: true })).toBeFocused();
    await expect(editor.getByLabel('Total', { exact: true })).not.toBeVisible();
    const title = `Synthetic keyboard ${width} ${randomUUID().slice(0, 8)}`;
    await editor.getByLabel('Title', { exact: true }).fill(title);
    await page.keyboard.press('Escape');
    const discard = page.getByRole('dialog', { name: 'Discard changes?', exact: true });
    await expect(discard).toBeVisible();
    await discard.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(editor.getByLabel('Title', { exact: true })).toHaveValue(title);
    // Tabbing must stay inside the dialog, including after dirty-confirm cancellation.
    for (let i = 0; i < 12; i++) {
      await page.keyboard.press('Tab');
      expect(await editor.evaluate(element => element.contains(document.activeElement))).toBe(true);
    }
    await editor.getByLabel('Media type').selectOption('book');
    await editor.locator('summary').filter({ hasText: 'Advanced details' }).click();
    await editor.getByLabel('Total', { exact: true }).fill('120');
    await fitsViewport(page, width);
    await editor.getByRole('button', { name: 'Add media', exact: true }).click();
    await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible();
    await page.getByRole('button', { name: 'Edit details', exact: true }).click();
    const edit = page.getByRole('dialog', { name: 'Edit details', exact: true });
    await edit.getByLabel('Title', { exact: true }).fill(`${title} discarded`);
    await page.keyboard.press('Escape');
    await page.getByRole('dialog', { name: 'Discard changes?', exact: true }).getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(edit).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Edit details', exact: true })).toBeFocused();
    await page.reload();
    await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible();
    const saved = await f.api.get<Schema['Entry']>(`/api/v1/library/${new URL(page.url()).pathname.split('/').pop()}`);
    expect(saved.title).toBe(title);
    expect(saved.total).toBe(120);
  });
}

test('integration directory exposes disconnected service details one task at a time', async ({ page, populated: f }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  expect((await f.api.get<{ items: unknown[] }>('/api/v1/me/services')).items).toEqual([]);
  expect((await f.api.get<{ items: unknown[] }>('/api/v1/connections')).items).toEqual([]);
  await page.goto('/integrations');
  await expect(page.getByRole('navigation', { name: 'Integration tasks' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Create sync token', exact: true })).toHaveCount(0);
  await page.goto('/integrations?section=webhooks');
  const plex = page.getByRole('link').filter({ has: page.getByText('Plex', { exact: true }) });
  await expect(plex).toContainText('Not connected');
  await plex.click();
  await expect(page.getByRole('heading', { name: 'Plex', exact: true })).toBeVisible();
  await expect(page.getByLabel('Plex account filter')).toBeVisible();
  await expect(page.getByLabel('Jellyfin account filter')).toHaveCount(0);
  await fitsViewport(page, 390);
  await page.goto('/integrations?section=services&service=trakt');
  await expect(page.getByRole('heading', { name: 'Trakt', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Sync now', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Disconnect|Unlink/ })).toHaveCount(0);
  await fitsViewport(page, 390);
});

test('recovery explains local and single sign-on paths and keeps a failed draft', async ({ browser }) => {
  const anonymous = await browser.newContext({ baseURL: usabilityURL, storageState: { cookies: [], origins: [] }, viewport: { width: 320, height: 480 } });
  try {
    const page = await anonymous.newPage();
    await page.route('**/api/v1/password-recovery', route => route.fulfill({ status: 422, contentType: 'application/json', body: JSON.stringify({ code: 'invalid_recovery', message: 'Synthetic recovery code was rejected.' }) }));
    await page.goto('/recover');
    await expect(page.getByText(/administrator.*reset link/i)).toBeVisible();
    await expect(page.getByText(/Authentik codes and MFA belong to Authentik/i)).toBeVisible();
    await page.getByLabel('Username', { exact: true }).fill('synthetic_recovery');
    await page.getByLabel('Code', { exact: true }).fill('000000');
    await page.getByLabel('New password', { exact: true }).fill('synthetic-recovery-password');
    await page.getByRole('button', { name: 'Set new password', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('Synthetic recovery code was rejected.');
    await expect(page.getByLabel('Username', { exact: true })).toHaveValue('synthetic_recovery');
    await expect(page.getByLabel('Code', { exact: true })).toHaveValue('000000');
    await fitsViewport(page, 320);
  } finally { await anonymous.close(); }
});
