import fs from 'fs';
import path from 'path';
import { randomUUID } from 'crypto';
import { expect, test } from '@playwright/test';

test('full-product production bundle loads', async ({ page }) => {
  const runtimeErrors: string[] = [];
  const initialRequests = new Set<string>();
  const fixturePassword = randomUUID();
  let submittedExpectedCredentials = false;
  page.on('pageerror', (error) => runtimeErrors.push(error.message));
  page.on('console', (message) => {
    const expectedAuthenticationRejection = message.text()
      .includes('server responded with a status of 401 (Unauthorized)');
    if (message.type() === 'error' && !expectedAuthenticationRejection) {
      runtimeErrors.push(message.text());
    }
  });

  // The real backend rejects all three parallel bootstrap requests without a session.
  await page.route(/\/api\/(user(?:\/options)?|info)$/, async (route) => {
    initialRequests.add(new URL(route.request().url()).pathname);
    await route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'UNAUTHENTICATED' }),
    });
  });
  await page.route('**/api/auth/login', async (route) => {
    if (route.request().method() === 'POST') {
      const submitted = route.request().postDataJSON();
      submittedExpectedCredentials = submitted.auth === 'edition-smoke'
        && submitted.password === fixturePassword;
      await route.fulfill({
        status: 401,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'INVALID_CREDENTIALS' }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        oidc_providers: [],
        ldap_providers: [],
        login_with_password: true,
        login_with_ldap: false,
        auth_methods: {},
      }),
    });
  });

  await page.goto('/');
  await expect(page.getByTestId('auth-username')).toBeVisible();
  await expect(page.getByTestId('auth-password')).toBeVisible();
  await expect(page.getByTestId('auth-signin')).toBeEnabled();
  await expect.poll(() => [...initialRequests].sort()).toEqual([
    '/api/info', '/api/user', '/api/user/options',
  ]);

  const evidenceDirectory = path.resolve(__dirname, '..', 'test-results');
  fs.mkdirSync(evidenceDirectory, { recursive: true });
  await page.screenshot({
    path: path.join(evidenceDirectory, 'full-product.png'),
    fullPage: true,
  });

  await page.getByTestId('auth-username').locator('input').fill('edition-smoke');
  await page.getByTestId('auth-password').locator('input').fill(fixturePassword);
  await page.getByTestId('auth-signin').click();
  await expect.poll(() => submittedExpectedCredentials).toBe(true);
  expect(runtimeErrors).toEqual([]);
});
