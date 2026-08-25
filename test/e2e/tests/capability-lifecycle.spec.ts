import fs from 'fs';
import path from 'path';
import { expect, test } from '@playwright/test';

test('system information shows the backend capability state and reason', async ({ page }) => {
  const runtimeErrors: string[] = [];
  page.on('pageerror', (error) => runtimeErrors.push(error.message));
  page.on('console', (message) => {
    if (message.type() === 'error') runtimeErrors.push(message.text());
  });

  await page.addInitScript(() => {
    class TestWebSocket {
      static OPEN = 1;

      readyState = TestWebSocket.OPEN;

      close() {}

      send() {}
    }
    Object.defineProperty(window, 'WebSocket', { value: TestWebSocket });
  });

  await page.route('**/api/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const responses: Record<string, unknown> = {
      '/api/user': {
        id: 7,
        name: 'Capability Admin',
        username: 'capability-admin',
        email: 'admin@example.invalid',
        admin: true,
        pro: true,
      },
      '/api/info': {
        version: 'test',
        auth_methods: {},
        login_with_password: true,
        features: {},
        edition: 'enhanced',
        teams: {},
        roles: [],
        capabilities: {
          resolved_at: '2026-08-25T10:00:00Z',
          capabilities: [{
            id: 'lifecycle_test',
            state: 'disabled',
            reason: 'disabled_by_admin',
            access: [],
            limits: {},
          }],
        },
      },
      '/api/user/options': {},
      '/api/projects': [],
      '/api/admin/info': {
        system: {
          version: 'test',
          go_version: 'go-test',
          go_os: 'test-os',
          go_arch: 'test-arch',
          git_client: 'go-git',
          tmp_path: '/tmp',
          home_dir_mode: '0700',
          schedule_timezone: 'UTC',
          ansible: '',
        },
        database: { dialect: 'sqlite' },
        auth: {
          password_login_enabled: true,
          totp_enabled: false,
          email_otp_enabled: false,
          ldap_enabled: false,
          oidc_providers: [],
        },
        notifications: {},
        cluster: { ha_enabled: false },
        runners: { use_remote_runner: false },
        task_settings: {},
        features: { non_admin_can_create_project: false },
      },
    };
    if (!(pathname in responses)) {
      await route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(responses[pathname]),
    });
  });

  await page.goto('/');
  await page.getByText('Capability Admin', { exact: true }).click();
  await page.getByTestId('menu-system-info').click();

  await expect(page.getByTestId('capability-lifecycle')).toBeVisible();
  await expect(page.getByTestId('capability-state')).toHaveText('disabled');
  await expect(page.getByTestId('capability-reason')).toHaveText('disabled_by_admin');

  const evidenceDirectory = path.resolve(__dirname, '..', 'test-results');
  fs.mkdirSync(evidenceDirectory, { recursive: true });
  await page.screenshot({
    path: path.join(evidenceDirectory, 'capability-lifecycle.png'),
    fullPage: true,
  });
  expect(runtimeErrors).toEqual([]);
});
