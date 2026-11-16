import AxeBuilder from '@axe-core/playwright';
import { execFileSync } from 'node:child_process';
import { expect, test, type Page } from '@playwright/test';

const owner = { username: 'e2e_owner', password: 'synthetic-e2e-password-2026' };

test.describe('sign-in form', () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  test('signs in and lands in the workspace', async ({ page }) => {
    await signIn(page);
    await expect(page.getByRole('link', { name: 'Discover' }).first()).toBeVisible();
  });
});

async function signIn(page: Page) {
  await page.goto('/login');
  await page.getByLabel('Username').fill(owner.username);
  await page.getByLabel('Password', { exact: true }).fill(owner.password);
  await page.getByRole('button', { name: /sign in/i }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

async function noHorizontalScroll(page: Page, width: number) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1);
}

for (const width of [1366, 390]) {
  test(`sign in, add media and search the catalog at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 768 });
    const pageErrors: string[] = [];
    page.on('pageerror', error => pageErrors.push(error.message));

    // Deep links resolve through the embedded application.
    await page.goto('/discover?type=movie&q=synthetic');
    await expect(page.getByRole('heading', { level: 1, name: 'Discover' })).toBeVisible();
    // Without a TMDB key, search falls back to the local catalog and says so.
    await expect(page.getByText('This source is not set up. Search titles already on this server or add a title manually.', { exact: true })).toBeVisible();
    await noHorizontalScroll(page, width);

    const title = `Synthetic Atlas ${width}-${Date.now()}`;
    await page.getByRole('button', { name: 'Add manually' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Title').fill(title);
    await dialog.getByLabel('Media type').selectOption('book');
    await dialog.getByRole('button', { name: 'Add media' }).click();
    await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible();

    await page.goto(`/library?q=${encodeURIComponent(title.split(' ').slice(-1)[0])}`);
    await expect(page.getByRole('link', { name: title })).toBeVisible();
    await noHorizontalScroll(page, width);

    if (width === 390) await expect(page.locator('.mobile-brand')).toHaveAccessibleName('Pastime');

    const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(scan.violations.map(v => `${v.id}: ${v.nodes.map(n => n.target.join(' ')).join(', ')}`)).toEqual([]);
    expect(pageErrors).toEqual([]);
    await page.screenshot({ path: `test-results/library-${width}.png`, fullPage: true });
  });
}

test('unknown API paths stay JSON and client routes fall back to the app', async ({ request }) => {
  const api = await request.get('/api/v1/nope');
  expect(api.status()).toBe(404);
  expect(api.headers()['content-type']).toContain('application/json');
  const app = await request.get('/join');
  expect(app.status()).toBe(200);
  expect(await app.text()).toContain('id="root"');
});

// German runs longest of the translations, so it checks that labels still fit.
for (const [width, language] of [[1366, 'en'], [390, 'en'], [390, 'de']] as const) {
  test(`workspace pages are accessible and fit at ${width}px in ${language}`, async ({ page }) => {
    // This test performs twenty full accessibility scans; allow their aggregate work.
    test.setTimeout(60_000);
    if (language !== 'en') await page.addInitScript(value => localStorage.setItem('pastime-language', value), language);
    await page.setViewportSize({ width, height: width === 390 ? 844 : 768 });
    const pageErrors: string[] = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    for (const path of ['/', '/discover', '/library', '/library?view=list', '/history', '/calendar', '/stats', '/lists', '/collection', '/activity', '/more', '/account', '/account?tab=preferences', '/account?tab=tags', '/account?tab=fields', '/account?tab=notifications', '/account?tab=security', '/integrations', '/integrations/unmatched', '/recommendations']) {
      await page.goto(path);
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      await expect(page.locator('.loading')).toHaveCount(0);
      await noHorizontalScroll(page, width);
      const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
      expect(scan.violations.map(v => `${path} ${v.id}: ${v.nodes.map(n => n.target.join(' ')).join(', ')}`)).toEqual([]);
      if (language !== 'en') expect(await page.locator('html').getAttribute('lang')).toBe(language);
    }
    expect(pageErrors).toEqual([]);
  });
}

test('late session response cannot resurrect a signed-out session', async ({ browser }) => {
 const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
 const page = await context.newPage();
 await signIn(page);
 const sessionResponse = await context.request.get('http://127.0.0.1:18161/api/v1/session');
 const session = await sessionResponse.json();
 let release!: () => void;
 const hold = new Promise<void>(resolve => { release = resolve; });
 let intercepted!: () => void;
 const started = new Promise<void>(resolve => { intercepted = resolve; });
 await page.route('**/api/v1/session', async route => {
   intercepted(); await hold;
   await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(session) }).catch(() => {});
 });
 await page.reload({ waitUntil: 'domcontentloaded' });
 await started;
 const logout = await context.request.post('http://127.0.0.1:18161/api/v1/logout', { headers: { Origin: 'http://127.0.0.1:18161', 'X-CSRF-Token': session.csrf_token } });
 expect(logout.status()).toBe(204);
 await page.evaluate(() => { const channel = new BroadcastChannel('pastime-session'); channel.postMessage('logout'); setTimeout(() => channel.close(), 100); });
 await expect(page).toHaveURL(/\/login/);
 release();
 await expect(page.getByLabel('Username')).toBeVisible();
 await expect(page.getByRole('link', { name: 'Discover' })).toHaveCount(0);
 await context.close();
});

test('bulk selection discloses titles and clears when filters change', async ({ page }) => {
 await page.goto('/library');
 await page.getByRole('button', { name: 'Select', exact: true }).click();
 const box = page.getByRole('checkbox').first();
 const title = (await box.getAttribute('aria-label'))!.replace(/^Select /, '');
 await box.check();
 const bulk = page.getByRole('region', { name: 'Bulk actions' });
 await bulk.getByText('Review selected titles').click();
 await expect(bulk).toContainText(title);
 await bulk.getByRole('combobox').selectOption('delete:');
 const dialog = page.getByRole('dialog');
 await expect(dialog).toContainText(title);
 await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
 const search = page.locator('form.library-search').getByRole('searchbox', { name: 'Search your library' });
 await search.fill('synthetic-no-match-selection');
 await search.press('Enter');
 await expect(bulk).toHaveCount(0);
});

test('saved views preserve filters and layout, appear in navigation and can be deleted', async ({ page }) => {
  const name=`Weekend books ${Date.now()}`;
  await page.goto('/library?media_type=book&status=planned&sort=title&view=list');
  // Saved views live in one select; its last option opens the Saved views dialog.
  const manage = () => page.getByRole('combobox', { name: 'Saved views', exact: true }).selectOption('manage');
  const controls = page.getByRole('dialog', { name: 'Saved views' });
  await manage();
  await controls.getByLabel('View name').fill(name);
  await controls.getByRole('button', { name: 'Save current view' }).click();
  await expect(controls.getByRole('link', { name })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(controls).toHaveCount(0);
  await page.goto('/library');
  await page.getByRole('navigation', { name: 'Saved views', exact: true }).getByRole('link', { name }).click();
  await expect(page).toHaveURL(/media_type=book/);
  await expect(page.getByRole('button', { name: 'List view', exact: true })).toHaveAttribute('aria-pressed','true');
  await manage();
  await controls.getByRole('button', { name: 'Edit', exact: true }).click();
  await controls.getByLabel('View name').fill(name+' updated');
  await controls.getByRole('button', { name: 'Save changes', exact: true }).click();
  await expect(controls.getByRole('link', { name: name+' updated', exact:true })).toBeVisible();
  await controls.getByRole('button', { name: `Delete ${name} updated`, exact: true }).click();
  await expect(controls.getByRole('link', { name })).toHaveCount(0);
});

test('a TV app signs in with a code approved on the device page', async ({ page, request }) => {
  const started = await request.post('/api/v1/device/authorize', { data: { client_name: 'Synthetic TV', scopes: ['library:read'] } });
  expect(started.status()).toBe(200);
  const { device_code, user_code } = await started.json();
  expect((await (await request.post('/api/v1/device/token', { data: { device_code } })).json()).status).toBe('pending');

  await page.goto('/device');
  await page.getByLabel('Code').fill(user_code.replace('-', '').toLowerCase());
  await page.getByRole('button', { name: 'Continue' }).click();
  await expect(page.getByText('Synthetic TV')).toBeVisible();
  await expect(page.getByText('Read your library')).toBeVisible();
  const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(scan.violations.map(v => v.id)).toEqual([]);
  await page.getByRole('button', { name: 'Approve' }).click();
  await expect(page.getByRole('status')).toContainText('Done');

  // Poll as an app would: wait the interval the server asks for between polls.
  let status = 'pending'; let interval = 5;
  for (let attempt = 0; attempt < 6 && status !== 'approved'; attempt++) {
    await page.waitForTimeout(interval * 1000 + 200);
    const answer = await (await request.post('/api/v1/device/token', { data: { device_code } })).json();
    status = answer.status; interval = answer.interval;
  }
  expect(status).toBe('approved');
});

test('a manual list becomes a tier board and ratings can be turned off', async ({ page }) => {
  const stamp = Date.now();
  const titles = [`Tier One ${stamp}`, `Tier Two ${stamp}`];
  const ids: string[] = [];
  for (const title of titles) {
    await page.goto('/discover');
    await page.getByRole('button', { name: 'Add manually' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByLabel('Title').fill(title);
    await dialog.getByRole('button', { name: 'Add media' }).click();
    await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible();
    ids.push(page.url().split('/').pop()!);
  }
  // Build the list through the API the page itself uses.
  const created = await page.evaluate(async ([name, entries]) => {
    const session = await (await fetch('/api/v1/session')).json();
    const headers = { 'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf_token, 'Idempotency-Key': crypto.randomUUID() };
    const list = await (await fetch('/api/v1/lists', { method: 'POST', headers, body: JSON.stringify({ name, kind: 'manual', visibility: 'private' }) })).json();
    let version = list.version;
    for (const id of entries as string[]) {
      const r = await (await fetch(`/api/v1/lists/${list.id}/entries/${id}`, { method: 'PUT', headers: { ...headers, 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ version, present: true }) })).json();
      version = r.version;
    }
    return list.id as string;
  }, [`Tier list ${stamp}`, ids] as const);

  await page.goto(`/lists/${created}?view=tiers`);
  await expect(page.getByRole('region', { name: 'Tier board' })).toBeVisible();
  await page.getByRole('button', { name: 'Add tier' }).click();
  await page.locator('.tier-pick', { hasText: titles[0] }).click();
  await page.getByRole('button', { name: 'Place here' }).first().click();
  await page.getByRole('button', { name: 'Save board' }).click();
  await expect(page.getByRole('button', { name: 'Save board' })).toBeDisabled();
  await page.reload();
  // After a reload the title is still in the first tier, not unranked.
  await expect(page.locator('.tier-row:not(.unranked) .tier-pick', { hasText: titles[0] })).toBeVisible();
  const scan = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(scan.violations.map(v => v.id)).toEqual([]);

  await page.goto('/account?tab=preferences');
  await page.getByLabel('Ratings').selectOption('off');
  await page.getByRole('button', { name: 'Save preferences' }).click();
  await expect(page.getByText('Preferences saved.')).toBeVisible();
  await page.goto('/library?view=list');
  await expect(page.getByRole('columnheader', { name: 'Rating' })).toHaveCount(0);
  await page.goto('/account?tab=preferences');
  await page.getByLabel('Ratings').selectOption('ten');
  await page.getByRole('button', { name: 'Save preferences' }).click();
  await expect(page.getByText('Preferences saved.')).toBeVisible();
});

test('a profile person signs in with their own login', async ({ page, browser }) => {
  test.setTimeout(120_000);
  const stamp = Date.now().toString(36);
  const profile = `Sam ${stamp}`;
  const login = `sam_${stamp}`;
  const first = 'synthetic-e2e-profile-2026';
  const changed = 'synthetic-e2e-profile-changed-2026';
  const reset = 'synthetic-e2e-profile-reset-2026';

  // The household's own title, which Sam must never see.
  const household = `Household Atlas ${stamp}`;
  await page.goto('/discover?type=movie&q=synthetic');
  await page.getByRole('button', { name: 'Add manually' }).click();
  await page.getByRole('dialog').getByLabel('Title').fill(household);
  await page.getByRole('dialog').getByLabel('Media type').selectOption('book');
  await page.getByRole('dialog').getByRole('button', { name: 'Add media' }).click();
  await expect(page.getByRole('heading', { level: 1, name: household })).toBeVisible();

  // 1. Sam's profile exists (profiles come from Bloem; Pastime can't add one),
  // and the primary user turns on Sam's sign-in.
  execFileSync('psql', [process.env.PASTIME_TEST_DATABASE_URL!, '-v', 'ON_ERROR_STOP=1', '-qc',
    `INSERT INTO tracking_profiles(account_id,name) SELECT id,'${profile}' FROM users WHERE username='${owner.username}'`]);
  await page.goto('/account?tab=profiles');
  await expect(page.getByRole('button', { name: 'Add profile' })).toHaveCount(0);
  const row = page.getByRole('listitem').filter({ hasText: profile });
  await row.getByRole('button', { name: 'Turn on sign-in' }).click();
  await page.getByRole('dialog').getByLabel('Sign-in name').fill(login);
  await page.getByRole('dialog').getByLabel('Password', { exact: true }).fill(first);
  await page.getByRole('dialog').getByRole('button', { name: 'Turn on sign-in' }).click();
  await expect(row.getByText(`Signs in as ${login}`, { exact: true })).toBeVisible();
  // The Profiles tab fits a phone.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/account?tab=profiles');
  await noHorizontalScroll(page, 390);
  await page.screenshot({ path: 'test-results/profile-logins-390.png', fullPage: true });
  await page.setViewportSize({ width: 1366, height: 768 });
  await page.goto('/account?tab=profiles');
  await expect(row.getByText(`Signs in as ${login}`, { exact: true })).toBeVisible();
  await page.screenshot({ path: 'test-results/profile-logins-1366.png', fullPage: true });

  // 2. Sam signs in on another browser and sees only Sam's own things.
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const sam = await context.newPage();
  const signInAs = async (target: Page, name: string, password: string) => {
    await target.goto('/login');
    await target.getByLabel('Username').fill(name);
    await target.getByLabel('Password', { exact: true }).fill(password);
    await target.getByRole('button', { name: /sign in/i }).click();
    await expect(target).not.toHaveURL(/\/login/);
  };
  await signInAs(sam, login, first);
  await expect(sam.getByRole('link', { name: 'Discover' }).first()).toBeVisible();
  await expect(sam.getByRole('combobox', { name: 'Profile' })).toHaveCount(0);
  await expect(sam.getByRole('link', { name: 'Administration' })).toHaveCount(0);
  await sam.goto('/account?tab=security');
  await expect(sam.getByRole('heading', { name: 'Password' })).toBeVisible();
  await expect(sam.getByText('Single sign-on')).toHaveCount(0);
  await expect(sam.getByRole('heading', { name: 'Authenticator recovery' })).toHaveCount(0);
  await sam.goto('/account?tab=profiles');
  await expect(sam).toHaveURL(/\/account$/);
  await sam.goto('/lists');
  await expect(sam.getByText('Collaborative lists')).toHaveCount(0);

  // 3. Sam's library holds only Sam's titles.
  await sam.goto(`/library?q=${encodeURIComponent(stamp)}`);
  await expect(sam.getByRole('heading', { level: 1, name: 'Library' })).toBeVisible();
  await expect(sam.getByRole('link', { name: household })).toHaveCount(0);

  // 4. Sam changes their own password, which signs them out; the new one works.
  await sam.goto('/account?tab=security');
  await sam.getByRole('button', { name: 'Change password' }).click();
  await sam.getByRole('dialog').getByLabel('Current password').fill(first);
  await sam.getByRole('dialog').getByLabel('New password').fill(changed);
  await sam.getByRole('dialog').getByRole('button', { name: 'Change password' }).click();
  await expect(sam).toHaveURL(/\/login/);
  await signInAs(sam, login, changed);

  // 5. The primary user resets Sam's password; Sam's next navigation asks them to sign in.
  await row.getByRole('button', { name: 'Change sign-in' }).click();
  await page.getByRole('dialog').getByLabel('New password').fill(reset);
  await page.getByRole('dialog').getByRole('button', { name: 'Save sign-in' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await sam.goto('/library');
  await expect(sam).toHaveURL(/\/login/);
  await context.close();

  // 6. Same browser, different people: after the primary signs out, Sam sees only Sam's library.
  const shared = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const desk = await shared.newPage();
  await signInAs(desk, owner.username, owner.password);
  await desk.goto(`/library?q=${encodeURIComponent(stamp)}`);
  await expect(desk.getByRole('link', { name: household })).toBeVisible();
  await desk.goto('/more');
  await desk.getByRole('button', { name: 'Sign out' }).click();
  await expect(desk).toHaveURL(/\/login/);
  await signInAs(desk, login, reset);
  await desk.goto(`/library?q=${encodeURIComponent(stamp)}`);
  await expect(desk.getByRole('heading', { level: 1, name: 'Library' })).toBeVisible();
  await expect(desk.getByRole('link', { name: household })).toHaveCount(0);
  await expect(desk.getByRole('combobox', { name: 'Profile' })).toHaveCount(0);
  await shared.close();
});
