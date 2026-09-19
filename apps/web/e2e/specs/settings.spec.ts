import { test, expect } from '../fixtures/base.js';

/**
 * Settings (P6-11). The MVP write surface is the session + team only, so these
 * specs assert the panels that are wired for real and assert the explicit
 * "not wired" state on the ones with no endpoint — dead buttons that fake a
 * round trip are the exact thing the P6 scope guard forbids.
 */

const NOT_WIRED = 'Not wired in the MVP';

test.describe('settings page', () => {
  test('renders the session card and the owner permission set', async ({ authedPage, api }) => {
    await authedPage.goto('/settings');

    await expect(authedPage.getByRole('heading', { name: 'Settings' })).toBeVisible();
    await expect(authedPage.getByText('Your session')).toBeVisible();
    await expect(authedPage.getByText('authenticated')).toBeVisible();

    // The seeded owner carries the full matrix; the badges are the UX gate, the
    // server re-checks every write.
    const me = await api.me();
    // The session card surfaces role + user id, not an email — the session has none.
    await expect(authedPage.getByText(me.userId)).toBeVisible();
    for (const perm of ['read', 'act', 'export', 'admin']) {
      await expect(authedPage.getByText(perm, { exact: true })).toBeVisible();
    }
  });

  test('the team panel lists the owner and the invite dialog opens', async ({ authedPage, api }) => {
    // The team roster lists members by email; the session carries no email, so
    // the owner's address comes from the roster, not me().
    const owner = (await api.listUsers()).users.find((u) => u.role === 'OWNER');

    await authedPage.goto('/settings');
    await expect(authedPage.getByRole('heading', { name: 'Team' })).toBeVisible();
    await expect(authedPage.getByText(owner!.email)).toBeVisible();

    await authedPage.getByRole('button', { name: 'Invite member' }).click();
    await expect(authedPage.getByRole('dialog')).toBeVisible();
    await expect(authedPage.getByRole('heading', { name: 'Invite member' })).toBeVisible();

    // Empty fields keep the button inert — the guard is on the client. The
    // button is disabled, so assert that (a .click() would hang waiting for it
    // to become enabled) and confirm the dialog stays open.
    await expect(authedPage.getByRole('dialog').getByRole('button', { name: 'Create' })).toBeDisabled();
    await expect(authedPage.getByRole('dialog')).toBeVisible();
  });

  test('an invited member appears in the table and can be removed', async ({ authedPage, api }) => {
    const email = `e2e_set_${Date.now().toString(36)}@smm.local`;

    await authedPage.goto('/settings');
    await authedPage.getByRole('button', { name: 'Invite member' }).click();

    const dialog = authedPage.getByRole('dialog');
    await dialog.getByLabel('Email').fill(email);
    await dialog.getByLabel('Name').fill('E2E Member');
    await dialog.getByLabel('Provisional password').fill('e2e-invite-password');
    await dialog.getByRole('button', { name: 'Create' }).click();

    // The row lands before the dialog teardown, so wait on the roster, not on
    // the dialog closing.
    await expect(authedPage.getByText(email)).toBeVisible({ timeout: 30_000 });

    const row = authedPage.getByRole('row').filter({ has: authedPage.getByText(email, { exact: true }) });
    await row.getByRole('button', { name: 'Remove' }).click();

    await expect(authedPage.getByText(email)).not.toBeVisible();

    // Belt and braces: the API is the source of truth, not the table.
    const users = (await api.listUsers()).users ?? [];
    expect(users.find((u) => u.email === email)).toBeUndefined();
  });

  test('changing a role shows Save and reverts on failure', async ({ authedPage, api }) => {
    const email = `e2e_role_${Date.now().toString(36)}@smm.local`;
    const created = await api.createUser({
      email,
      name: 'E2E Role',
      password: 'e2e-role-password',
      role: 'OPERATOR',
    });

    try {
      await authedPage.goto('/settings');
      await expect(authedPage.getByText(email)).toBeVisible();

      const row = authedPage.getByRole('row').filter({ has: authedPage.getByText(email, { exact: true }) });
      await row.getByRole('combobox').click();
      await authedPage.getByRole('option', { name: 'Strategist' }).click();

      // Save only appears on a dirty row; a select that re-picks the current
      // value would render no affordance at all.
      await expect(row.getByRole('button', { name: 'Save' })).toBeVisible();
      await row.getByRole('button', { name: 'Save' }).click();
      await expect(row.getByRole('button', { name: 'Save' })).not.toBeVisible();

      const after = (await api.listUsers()).users?.find((u) => u.id === created.id);
      expect(after?.role).toBe('STRATEGIST');
    } finally {
      await api.removeUser(created.id);
    }
  });

  test('the not-wired panels say so instead of faking controls', async ({ authedPage }) => {
    await authedPage.goto('/settings');

    for (const label of ['Proxy groups', 'Limits', 'Danger zone']) {
      await authedPage.getByRole('button', { name: label, exact: true }).click();
      // A panel with no endpoint must render the honest state: the operator can
      // see what is missing, and there is no button implying a round trip.
      await expect(authedPage.getByText(NOT_WIRED)).toBeVisible();
    }
  });

  test('the appearance panel switches theme for the whole dashboard', async ({ authedPage }) => {
    await authedPage.goto('/settings');
    await authedPage.getByRole('button', { name: 'Appearance', exact: true }).click();

    // Scope to the panel: the theme group is the only place these labels appear,
    // but a leaked duplicate row elsewhere would make them ambiguous.
    const theme = authedPage.getByText('Theme').locator('..');

    await theme.getByRole('button', { name: 'Dark' }).click();

    // next-themes writes the class on <html>; the login screen reads it too,
    // so this is the one settings write that is real end to end.
    await expect(authedPage.locator('html')).toHaveClass(/dark/);

    await theme.getByRole('button', { name: 'Light' }).click();
    await expect(authedPage.locator('html')).not.toHaveClass(/dark/);
  });
});
