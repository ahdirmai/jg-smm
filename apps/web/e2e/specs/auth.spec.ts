import { test, expect } from '../fixtures/base.js';

/**
 * Auth (P6-03). The login page is the only public surface, so it gets its own
 * spec: session bootstrap, the open-redirect guard on `next`, and the 401 path.
 *
 * These specs deliberately do NOT use the `authedPage` fixture — they need an
 * anonymous browser.
 */

test.describe('login page', () => {
  test('signs the owner in and lands on the dashboard', async ({ page }) => {
    await page.goto('/login');

    await page.getByLabel('Email').fill('owner@smm.local');
    await page.getByLabel('Password').fill('changeme-changeme');
    await page.getByRole('button', { name: 'Sign in' }).click();

    // safeNext(null) defaults to '/', so a plain sign-in lands on the dashboard
    // index (Getting started / KPI strip), not /workers. The contract under test
    // is that login left its page and the authenticated shell rendered — assert
    // it is no longer on /login (still fails if the sign-in didn't take) and the
    // shell's session block is present.
    await expect(page).not.toHaveURL(/\/login/);
    await expect(page.getByTestId('session-role')).toBeVisible();
  });

  test('rejects a bad password inline without leaving the page', async ({ page }) => {
    await page.goto('/login');

    await page.getByLabel('Email').fill('owner@smm.local');
    await page.getByLabel('Password').fill('definitely-wrong');
    await page.getByRole('button', { name: 'Sign in' }).click();

    // role=alert is the form's error slot; the message is server-chosen.
    await expect(page.getByRole('alert')).toBeVisible();
    await expect(page).toHaveURL(/.*\/login/);
  });

  test('blocks navigation until both fields are filled (native required)', async ({ page }) => {
    await page.goto('/login');

    // An empty submit must not navigate away.
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).toHaveURL(/.*\/login/);
  });

  test('an unauthenticated visit to a dashboard route bounces to /login', async ({ page }) => {
    await page.context().clearCookies();
    await page.goto('/workers');

    await expect(page).toHaveURL(/.*\/login/);
  });

  test('sanitises the `next` redirect: an absolute URL is ignored', async ({ page }) => {
    await page.goto('/login?next=//evil.example.com');

    await page.getByLabel('Email').fill('owner@smm.local');
    await page.getByLabel('Password').fill('changeme-changeme');
    await page.getByRole('button', { name: 'Sign in' }).click();

    // Lands on the app root, never on the third-party host.
    await expect(page).toHaveURL(/^(?!.*evil\.example\.com).*$/);
  });
});

test.describe('session', () => {
  test('a logged-in visit to /login does not show the form again', async ({ authedPage }) => {
    await authedPage.goto('/login');
    await expect(authedPage.getByRole('button', { name: 'Sign in' })).toBeHidden();
  });
});
