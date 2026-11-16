import { test, expect } from '../browser/review-regressions-fixtures';

for (const width of [1280, 390]) {
  test(`catalog browsing appends pages automatically and resets filters at ${width}px`, async ({ page, responses }) => {
    await page.setViewportSize({ width, height: 844 });
    const reads: string[] = [];
    await page.route('**/api/v1/catalog/providers', route => route.fulfill({ json: { items: [{ name: 'tmdb', kinds: ['movie'], defaults: ['movie'], configured: true, search: { supported: true, pagination: true, year: true, genre: false, sorts: [] }, browse: { supported: true, pagination: true, year: true, genre: false, sorts: [] } }] } }));
    await page.route('**/api/v1/catalog/browse?*', route => {
      const url = new URL(route.request().url()); reads.push(url.search);
      const n = Number(url.searchParams.get('page'));
      const indexes = n === 1 ? Array.from({ length: 20 }, (_, i) => i) : n === 2 ? Array.from({ length: 21 }, (_, i) => i + 19) : [40];
      return route.fulfill({ json: { items: indexes.map(i => responses.result(i)), source: 'provider', degraded: false, availability: 'available', next_page: n < 3 ? String(n + 1) : null } });
    });
    await page.goto('/discover?type=movie&browse=1');
    await expect(page.locator('.result-card')).toHaveCount(20);
    await page.locator('.discovery-paging').scrollIntoViewIfNeeded();
    await expect(page.locator('.result-card')).toHaveCount(40);
    await expect(page.getByRole('button', { name: 'Review title 0', exact: true })).toHaveCount(1);
    await page.getByRole('button', { name: 'Review title 0', exact: true }).click();
    await page.locator('main .back-link').click();
    await expect(page.locator('.result-card')).toHaveCount(40);
    await page.locator('.discovery-paging').scrollIntoViewIfNeeded();
    await expect(page.locator('.result-card')).toHaveCount(41);
    await expect(page.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0);
    expect(reads.map(q => new URLSearchParams(q).get('page'))).toEqual(['1', '2', '3']);
    await page.getByLabel('Year', { exact: true }).fill('2025');
    await page.getByRole('button', { name: 'Apply filters', exact: true }).click();
    await expect(page.locator('.result-card')).toHaveCount(20);
    expect(new URLSearchParams(reads.at(-1)).get('year')).toBe('2025');
    expect(new URLSearchParams(reads.at(-1)).get('page')).toBe('1');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
}

test('a failed next page retains titles and waits for an explicit retry', async ({ page, responses }) => {
  let attempts = 0;
  await page.route('**/api/v1/catalog/browse?*', route => {
    const n = Number(new URL(route.request().url()).searchParams.get('page'));
    if (n === 2 && ++attempts === 1) return route.fulfill({ json: { items: [], source: 'provider', degraded: true, availability: 'temporarily_unavailable', next_page: null } });
    return route.fulfill({ json: { items: Array.from({ length: 20 }, (_, i) => responses.result(i + (n - 1) * 20)), source: 'provider', degraded: false, availability: 'available', next_page: n === 1 ? '2' : null } });
  });
  await page.goto('/discover?type=movie&q=Review');
  // Exercise the same infinite flow with search pagination rather than browse.
  await page.route('**/api/v1/catalog/search?*', async route => {
    const n = Number(new URL(route.request().url()).searchParams.get('page'));
    if (n === 2 && ++attempts === 1) return route.fulfill({ json: { items: [], source: 'provider', degraded: true, availability: 'temporarily_unavailable', next_page: null } });
    return route.fulfill({ json: { items: Array.from({ length: 20 }, (_, i) => responses.result(i + (n - 1) * 20)), source: 'provider', degraded: false, availability: 'available', next_page: n === 1 ? '2' : null } });
  });
  await page.reload();
  await expect(page.locator('.result-card')).toHaveCount(20);
  await page.locator('.discovery-paging').scrollIntoViewIfNeeded();
  await expect(page.getByRole('button', { name: 'Retry', exact: true })).toBeVisible();
  await expect(page.locator('.result-card')).toHaveCount(20);
  expect(attempts).toBe(1);
  await page.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(page.locator('.result-card')).toHaveCount(40);
  expect(attempts).toBe(2);
});
