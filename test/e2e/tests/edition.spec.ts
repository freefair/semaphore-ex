import fs from 'fs';
import path from 'path';
import { expect, test } from '@playwright/test';

test('production bundle exposes the expected edition', async ({ page }) => {
  const expectedEdition = process.env.EDITION_EXPECTED;
  test.skip(!expectedEdition, 'EDITION_EXPECTED is only set by edition smoke jobs');

  const runtimeErrors: string[] = [];
  let submittedCredentials: Record<string, unknown> | undefined;
  page.on('pageerror', (error) => runtimeErrors.push(error.message));
  page.on('console', (message) => {
    const expectedAuthenticationRejection = message.text()
      .includes('server responded with a status of 401 (Unauthorized)');
    if (message.type() === 'error' && !expectedAuthenticationRejection) {
      runtimeErrors.push(message.text());
    }
  });

  await page.route('**/api/user', async (route) => {
    await route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'UNAUTHENTICATED' }),
    });
  });
  await page.route('**/api/auth/login', async (route) => {
    if (route.request().method() === 'POST') {
      submittedCredentials = route.request().postDataJSON();
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
  await expect(page.locator('html')).toHaveAttribute('data-edition', expectedEdition as string);
  await expect(page.getByTestId('auth-username')).toBeVisible();
  await expect(page.getByTestId('auth-password')).toBeVisible();
  await expect(page.getByTestId('auth-signin')).toBeEnabled();

  const evidenceDirectory = path.resolve(__dirname, '..', 'test-results');
  fs.mkdirSync(evidenceDirectory, { recursive: true });
  await page.screenshot({
    path: path.join(evidenceDirectory, `${expectedEdition}-edition.png`),
    fullPage: true,
  });

  await page.getByTestId('auth-username').fill('edition-smoke');
  await page.getByTestId('auth-password').fill('not-a-real-password');
  await page.getByTestId('auth-signin').click();
  await expect.poll(() => submittedCredentials).toEqual({
    auth: 'edition-smoke',
    password: 'not-a-real-password',
  });
  expect(runtimeErrors).toEqual([]);
});
