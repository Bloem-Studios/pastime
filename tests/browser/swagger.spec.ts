import { expect, test } from '@playwright/test';

for (const width of [1366, 390]) {
  test(`self-hosted Swagger and real request at ${width}px`, async ({ page, baseURL }) => {
    // Documentation requires an administrator browser session (shared from global setup).
    await page.setViewportSize({ width, height: width === 390 ? 844 : 768 });
    const pageErrors: string[] = [];
    const externalRequests: string[] = [];
    page.on('pageerror', error => pageErrors.push(error.message));
    page.on('request', request => {
      if (new URL(request.url()).origin !== new URL(baseURL!).origin) externalRequests.push(request.url());
    });
    const response = await page.goto('/docs/');
    expect(response?.status()).toBe(200);
    await expect(page.locator('.info .title')).toContainText('Pastime API');
    const operation = page.locator('#operations-Runtime-getServerInfo');
    await operation.locator('.opblock-summary-control').click();
    await operation.getByRole('button', { name: 'Try it out', exact: true }).click();
    const requestResult = page.waitForResponse(response => response.url() === `${baseURL}/api/v1/info` && response.request().method() === 'GET');
    await operation.getByRole('button', { name: 'Execute', exact: true }).click();
    const result = await requestResult;
    expect(result.status()).toBe(200);
    expect(await result.json()).toMatchObject({ name: 'pastime', stage: 'preview' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width + 1);
    expect(pageErrors).toEqual([]);
    expect(externalRequests).toEqual([]);
    await page.screenshot({ path: `test-results/swagger-${width}.png`, fullPage: true });
  });
}
