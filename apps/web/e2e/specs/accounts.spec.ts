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
  test('renders the first-run panel', async ({ authedPage, api }) => {
    const list = await api.listAccounts();
    test.skip(list.accounts.length > 0, 'empty state needs no accounts');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText('No accounts yet')).toBeVisible();

    // The two ways in are the whole content of the page until an account
    // exists — the stat strip and the filter row are deliberately absent
    // (zeros and dead controls are not a first run). The platform filter is
    // covered by the bulk-select spec, which drives it against real rows.
    await expect(authedPage.getByRole('link', { name: 'Add account' })).toBeVisible();
    await expect(authedPage.getByRole('button', { name: 'Import CSV' })).toBeVisible();
    await expect(authedPage.getByLabel('All platforms')).toBeHidden();
  });

  test('the new-account route renders its wizard', async ({ authedPage }) => {
    await authedPage.goto('/accounts/new');

    // Step 0 is the container picker (the wizard is container-first: the
    // container's region decides where the account browses from, and a
    // platform already present in that container is disabled). Continue stays
    // disabled until one is chosen.
    await expect(authedPage.getByRole('heading', { name: 'Pick a container' })).toBeVisible();
    await expect(authedPage.getByRole('button', { name: 'Continue' })).toBeDisabled();

    // Pick whatever container the fleet has; with none provisioned the step
    // says so and the credentials form is unreachable, which is the honest
    // state on a fresh stack.
    const firstContainer = authedPage.locator('button', { hasText: /·\s*(READY|IDLE)/ }).first();
    if ((await firstContainer.count()) === 0) {
      await expect(authedPage.getByText('No containers yet')).toBeVisible();
      return;
    }
    await firstContainer.click();
    await authedPage.getByRole('button', { name: 'Continue' }).click();

    // Step 1 picks the platform; step 2 surfaces the credentials form.
    await expect(authedPage.getByRole('heading', { name: 'Pick a platform' })).toBeVisible();
    await authedPage.getByRole('button', { name: 'Instagram' }).click();
    await authedPage.getByRole('button', { name: 'Continue' }).click();
    await expect(authedPage.getByLabel('Username')).toBeVisible();
    await expect(authedPage.getByLabel('Password')).toBeVisible();
  });

  test('a row created via the API renders with its auth badge', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText(`@${USER}`)).toBeVisible();
    // A fresh account is mid-login: the card shows the authStatus the API
    // actually holds, which is AUTHENTICATING until a worker claims it.
    await expect(authedPage.getByText('AUTHENTICATING')).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('pause and resume flip the status badge', async ({ authedPage, api }) => {
    const account = await api.createAccount(USER, 'instagram');

    await authedPage.goto('/accounts');
    await expect(authedPage.getByText(`@${USER}`)).toBeVisible();

    await authedPage
      .getByRole('row')
      .filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) })
      .getByRole('button', { name: 'Open account menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Pause' }).click();

    // The badge is the operator's only signal; it must settle without a reload.
    await expect(
      authedPage.getByRole('row').filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) }).getByText('PAUSED'),
    ).toBeVisible({ timeout: 30_000 });

    await authedPage
      .getByRole('row')
      .filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) })
      .getByRole('button', { name: 'Open account menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Resume' }).click();
    await expect(
      authedPage.getByRole('row').filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) }).getByText('ACTIVE'),
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
    // Both rows must have landed (initial fetch + SSE) before we filter, or the
    // TikTok row can still be in flight when the filter is applied and the
    // narrowed view reads as empty.
    await expect(authedPage.getByText(`@${USER}`, { exact: true })).toBeVisible();
    await expect(authedPage.getByText(`@${USER}2`, { exact: true })).toBeVisible();
    // hasText is a substring match, so `USER` also matches `${USER}2`; scope on
    // the exact label instead, or the locator is ambiguous by one row.
    await authedPage
      .getByRole('row')
      .filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) })
      .getByRole('checkbox', { name: `Select @${USER}` })
      .check();

    await expect(authedPage.getByText('1 selected')).toBeVisible();
    await expect(authedPage.getByRole('button', { name: 'Pause' })).toBeEnabled();

    // Filtering hides the TikTok row; the bar still counts only what is visible.
    await authedPage.getByLabel('All platforms').click();
    await authedPage.getByRole('option', { name: 'TikTok' }).click();
    await expect(authedPage.getByText(`@${USER}2`, { exact: true })).toBeVisible();
    // Exact again, for the same reason as the checkbox above: a substring
    // match resolves to both rows, and an assertion over two elements is a
    // strict-mode violation rather than the "is the Instagram row gone" check
    // this is meant to be.
    await expect(authedPage.getByText(`@${USER}`, { exact: true })).toBeHidden();

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
      .filter({ has: authedPage.getByText(`@${USER}`, { exact: true }) })
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
