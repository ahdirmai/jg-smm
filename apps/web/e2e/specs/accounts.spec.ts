import { test, expect } from '../fixtures/base.js';

/**
 * Accounts (P3-05, P5-02). The fleet accounts table: filters, bulk select, row
 * ops, and the auth badge.
 *
 * The login flow itself is not asserted here — it needs a human in the noVNC
 * view, so it is covered in the README as a manual gate, not a spec.
 */

const USER = `e2e_user_${Date.now().toString(36)}`;

test.describe('accounts page', () => {
  test('renders the empty state and the platform filter', async ({ authedPage, api }) => {
    const list = await api.listAccounts();
    test.skip(list.accounts.length > 0, 'empty state needs no accounts');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText('No accounts yet')).toBeVisible();

    await authedPage.getByLabel('All platforms').click();
    await expect(authedPage.getByRole('option', { name: 'Instagram' })).toBeVisible();
  });

  test('the new-account route renders its form', async ({ authedPage }) => {
    await authedPage.goto('/accounts/new');
    await expect(authedPage.getByLabel(/Username/i)).toBeVisible();
    await expect(authedPage.getByRole('button', { name: /Add account|Create/i })).toBeVisible();
  });

  test('a row created via the API renders with its auth badge', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText(`@${USER}`)).toBeVisible();
    await expect(authedPage.getByText('PENDING')).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('pause and resume flip the status badge', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText(`@${USER}`)).toBeVisible();

    await authedPage
      .getByRole('row')
      .filter({ hasText: USER })
      .getByRole('button', { name: 'Open account menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Pause' }).click();

    // The badge is the operator's only signal; it must settle without a reload.
    await expect(
      authedPage.getByRole('row').filter({ hasText: USER }).getByText('PAUSED'),
    ).toBeVisible({ timeout: 30_000 });

    await authedPage
      .getByRole('row')
      .filter({ hasText: USER })
      .getByRole('button', { name: 'Open account menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Resume' }).click();
    await expect(
      authedPage.getByRole('row').filter({ hasText: USER }).getByText('ACTIVE'),
    ).toBeVisible({ timeout: 30_000 });

    await api.removeAccount(account.id);
  });

  test('bulk select drives the action bar and counts only visible rows', async ({
    authedPage,
    api,
  }) => {
    const a = await api.createAccount(USER, 'instagram');
    const b = await api.createAccount(`${USER}2`, 'tiktok');

    await authedPage.goto('/accounts');
    await authedPage
      .getByRole('row')
      .filter({ hasText: USER })
      .getByRole('checkbox', { name: `Select @${USER}` })
      .check();

    await expect(authedPage.getByText('1 selected')).toBeVisible();
    await expect(authedPage.getByRole('button', { name: 'Pause' })).toBeEnabled();

    // Filtering hides the TikTok row; the bar still counts only what is visible.
    await authedPage.getByLabel('All platforms').click();
    await authedPage.getByRole('option', { name: 'TikTok' }).click();
    await expect(authedPage.getByText(`@${USER}2`)).toBeVisible();
    await expect(authedPage.getByText(`@${USER}`)).toBeHidden();

    // Clearing the selection is a first-class escape hatch.
    await authedPage.getByRole('button', { name: 'Clear' }).click();
    await expect(authedPage.getByText('selected')).toBeHidden();

    await api.removeAccount(a.id);
    await api.removeAccount(b.id);
  });

  test('the status filter narrows the table', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await authedPage.getByLabel('All statuses').click();
    await authedPage.getByRole('option', { name: 'Paused' }).click();

    // A PENDING row is hidden under the Paused filter.
    await expect(authedPage.getByText(`@${USER}`)).toBeHidden();
    await expect(authedPage.getByText('0 of')).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('removes a row from the table', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await authedPage
      .getByRole('row')
      .filter({ hasText: USER })
      .getByRole('button', { name: 'Open account menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Remove' }).click();

    await expect(authedPage.getByText(`@${USER}`)).toBeHidden({ timeout: 30_000 });
    await expect
      .poll(async () => (await api.listAccounts()).accounts.some((a) => a.id === account.id))
      .toBe(false);
  });
});

test.afterEach(async ({ api }) => {
  const list = await api.listAccounts();
  for (const a of list.accounts.filter((a) => a.username.startsWith('e2e_'))) {
    await api.removeAccount(a.id).catch(() => undefined);
  }
});
