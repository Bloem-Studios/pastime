import { test, expect, spaNavigate } from './review-regressions-fixtures';

for (const [name, path] of [['person', '/people/tmdb/9'], ['studio', '/studios/tmdb/company/9']] as const) {
  test(`${name}: successful Add updates membership without a reload`, async ({ page, responses }) => {
    responses.account = 'B';
    await page.goto(path);
    await page.getByRole('button', { name: 'Add Review title 0 to your library' }).click();
    await expect(page.getByRole('button', { name: /In your library/ })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add Review title 0 to your library' })).toHaveCount(0);
    expect(responses.reads[name]).toBeGreaterThan(1);
  });
  test(`${name}: same-SPA account switch isolates membership and Add refreshes`, async ({ page, responses }) => {
    await page.goto(path);
    await expect(page.getByRole('button', { name: /In your library/ })).toBeVisible();
    await page.evaluate(() => { (window as unknown as { reviewDocument: boolean }).reviewDocument = true; });
    await page.getByRole('button', { name: 'Account menu' }).click();
    await page.getByRole('menuitem', { name: 'Sign out' }).click();
    await expect(page.getByLabel('Username', { exact: true })).toBeVisible();
    await page.getByLabel('Username', { exact: true }).fill('B');
    await page.getByLabel('Password', { exact: true }).fill('synthetic-review-password');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Account menu' })).toBeVisible();
    await spaNavigate(page, path);
    expect(await page.evaluate(() => (window as unknown as { reviewDocument: boolean }).reviewDocument)).toBe(true);
    await expect(page.getByRole('button', { name: 'Add Review title 0 to your library' })).toBeVisible();
    await expect(page.getByRole('button', { name: /In your library/ })).toHaveCount(0);
    expect(responses.reads[name]).toBeGreaterThanOrEqual(2);
    await page.getByRole('button', { name: 'Add Review title 0 to your library' }).click();
    await expect(page.getByRole('button', { name: /In your library/ })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add Review title 0 to your library' })).toHaveCount(0);
    expect(responses.reads[name]).toBeGreaterThan(2);
  });
}

test('Discover → title → personal detail → title preserves the original filtered source', async ({ page, responses }) => {
  void responses;
  const source = '/discover?type=movie&q=Review&provider=tmdb';
  await page.goto(source);
  await page.getByRole('button', { name: 'Review title 0', exact: true }).click();
  await page.getByRole('link', { name: 'Open in your library' }).click();
  await expect(page.locator('main .back-link')).toHaveAttribute('href', source);
  await page.getByRole('link', { name: /Title details/ }).click();
  await expect(page.locator('main .back-link')).toHaveAttribute('href', source);
  await page.locator('main .back-link').click();
  await expect(page).toHaveURL(`http://localhost:18162${source}`);
});

for (const [section, selector] of [['services', 'service'], ['webhooks', 'server']] as const) {
  test(`integration ${selector} query transition resets scroll and focuses the heading on mobile`, async ({ page, responses }) => {
    void responses;
    await page.setViewportSize({ width: 320, height: 480 });
    await page.goto(`/integrations?section=${section}`);
    const link = page.locator(`main .integration-directory a[href*="${selector}="]`).last();
    await link.scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => window.scrollY)).toBeGreaterThan(50);
    await link.click();
    await expect(page.locator('main h1')).toBeFocused();
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(321);
  });
}

test('cold source response after a detail reload restores captured scroll beyond two seconds', async ({ page, responses }) => {
  await page.goto('/discover?type=movie&q=Review');
  const card = page.getByRole('button', { name: 'Review title 36', exact: true });
  await card.scrollIntoViewIfNeeded();
  const position = await page.evaluate(() => window.scrollY);
  expect(position).toBeGreaterThan(500);
  await card.click();
  await expect(page).toHaveURL(/\/title\/137$/);
  // Reload discards React Query's source cache but preserves history.state.
  await page.reload();
  await expect(page.locator('main .back-link')).toBeVisible();
  responses.searchDelay = 2600;
  await page.locator('main .back-link').click();
  await expect(page.getByText('Searching', { exact: true })).toBeVisible();
  await expect(card).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY), { timeout: 6000 }).toBeGreaterThan(position - 3);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeLessThan(position + 3);
});

test('persisted hidden suggestions can be reviewed and restored after a fresh mount', async ({ page, responses }) => {
  await page.goto('/discover?type=movie');
  await page.getByRole('button', { name: 'Review hidden suggestions' }).click();
  await expect(page.locator('.discovery-hidden')).toContainText('Review title 0');
  expect(responses.reads.hidden).toBe(1);
  await page.getByRole('button', { name: 'Restore', exact: true }).click();
  await expect(page.locator('.discovery-hidden')).toContainText('No hidden suggestions for this media type.');
  expect(responses.writes).toContain('DELETE /api/v1/discover/dismissals');
});

test('Notifications unavailable copy describes notifications rather than password recovery', async ({ page, responses }) => {
  void responses;
  await page.goto('/account?tab=notifications');
  const section = page.locator('.account-section').filter({ has: page.getByRole('heading', { name: 'Notifications', exact: true }) });
  await expect(section).toBeVisible();
  // Wait for the asynchronous auth-methods gate, not just the nav label.
  await expect(section).toContainText(/not (set up|available)/);
  await expect(section).not.toContainText('local password reset link');
  await expect(section).not.toContainText('Authenticator recovery is not available');
});

for (const field of ['schedule', 'push'] as const) {
  test(`service ${field}-only draft requires confirmation and survives cancel`, async ({ page, responses }) => {
    void responses;
    await page.goto('/integrations?section=services&service=jellyfin');
    await page.getByRole('button', { name: 'Link', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Link Jellyfin', exact: true });
    if (field === 'schedule') await dialog.getByLabel('Sync', { exact: true }).selectOption('weekly');
    else await dialog.getByRole('checkbox').check();
    await dialog.getByRole('button', { name: 'Close dialog' }).click();
    const confirmation = page.getByRole('dialog', { name: 'Discard changes?' });
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
    if (field === 'schedule') await expect(dialog.getByLabel('Sync', { exact: true })).toHaveValue('weekly');
    else await expect(dialog.getByRole('checkbox')).toBeChecked();
    await dialog.getByRole('button', { name: 'Close dialog' }).click();
    await confirmation.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(dialog).toHaveCount(0);
  });
}

test('sync-token name-only draft survives cancellation', async ({ page, responses }) => {
  void responses;
  await page.goto('/integrations?section=assistant');
  await page.getByRole('button', { name: 'Create assistant token', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Create sync token', exact: true });
  await dialog.getByLabel('Token name').fill('Synthetic renamed token');
  await dialog.getByRole('button', { name: 'Close dialog' }).click();
  await page.getByRole('dialog', { name: 'Discard changes?' }).getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog.getByLabel('Token name')).toHaveValue('Synthetic renamed token');
});

test('failed webhook save retains filter and route dirty guard', async ({ page, responses }) => {
  void responses;
  await page.route('**/api/v1/connections', route => route.fulfill({ json: { items: [{ id: '801', kind: 'plex', settings: { username: '' }, events_recorded: 0, last_event_at: null, last_outcome: '' }] } }));
  await page.goto('/integrations?section=webhooks&server=plex');
  await page.getByLabel('Plex account filter').fill('Synthetic draft');
  await page.getByRole('button', { name: 'Save filter', exact: true }).click();
  await expect(page.locator('main [role="alert"]')).toBeVisible();
  await page.getByRole('link', { name: 'All webhooks', exact: true }).click();
  await page.getByRole('dialog', { name: 'Discard changes?' }).getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByLabel('Plex account filter')).toHaveValue('Synthetic draft');
});

test('Home remains usable when all library titles are paused, dropped or rated', async ({ page, responses }) => {
  void responses;
  const errors: string[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.route('**/api/v1/home', route => route.fulfill({ json: {
    continue: [], up_next: [], upcoming: [], planned: [], recently_finished: [],
    stats: { total: 3 },
  } }));
  await page.goto('/');
  await expect(page.getByRole('heading', { level: 1, name: 'Home', exact: true })).toBeVisible();
  await expect(page.getByText('Your paused, dropped and rated titles are in your library.')).toBeVisible();
  await page.getByRole('link', { name: 'Browse library', exact: true }).click();
  await expect(page).toHaveURL(/\/library$/);
  expect(errors).toEqual([]);
});

for (const width of [390, 320]) {
  test(`populated Jobs tables contain offscreen accessible action labels at ${width}px`, async ({ page, responses }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('**/api/v1/session', route => route.fulfill({ json: { ...responses.session(), user: { ...responses.session().user, role: 'admin' } } }));
    await page.route('**/api/admin/v1/overview', route => route.fulfill({ json: { nodes: [{ id: 'w1', role: 'worker' }], jobs: { queued: 0, running: 0, failed: 0, blocked: 0, oldest_queued_seconds: null, failed_last_day: 0, throughput: [] } } }));
    await page.route('**/api/admin/v1/job-kinds', route => route.fulfill({ json: { items: [{ kind: 'statistics_warm', queued: 0, running: 0, failed: 0, blocked: 0, succeeded: 19 }] } }));
    await page.route('**/api/admin/v1/jobs?*', route => route.fulfill({ json: { items: [{ id: '801', kind: 'statistics_warm', status: 'succeeded', attempts: 1, max_attempts: 5, last_error: null, created_at: '2026-10-09T00:00:00Z', run_after: '2026-10-09T00:00:00Z' }], next_cursor: null } }));
    await page.goto('/admin/jobs');
    const table = page.getByRole('table', { name: 'Jobs, newest first' });
    await expect(table).toContainText('Statistics warm-up');
    await expect(page.getByRole('columnheader', { name: 'Actions' })).toHaveCount(1);
    await expect(page.getByRole('link', { name: 'Background jobs: Queue idle' })).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    // Phone rows are two lines inside the page width: the table never scrolls sideways.
    expect(await table.evaluate(e => e.getBoundingClientRect().right)).toBeLessThanOrEqual(width);
  });
}
