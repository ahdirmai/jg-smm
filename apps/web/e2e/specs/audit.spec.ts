import { test, expect } from '../fixtures/base.js';

/**
 * Audit (P6-10). Every state-changing /api call is written by the API's audit
 * middleware, so the trail is a side effect of seeding state elsewhere: create
 * an account here and the row turns up without a dedicated fixture.
 *
 * The page renders the entity *id*, which the API leaves empty on a create, so
 * the row is matched on the action verb and the actor, never on the target
 * name. Matching on anything the row does not actually show is how this spec
 * used to hang.
 */

const ACCOUNT = `e2e_audit_${Date.now().toString(36)}`;

test.describe('audit page', () => {
  test('renders the filter surface and the trail of a real write', async ({ authedPage, api }) => {
    // /api/auth/me carries no email; the owner's address comes from the roster.
    const ownerEmail = (await api.listUsers()).users.find((u) => u.role === 'OWNER')!.email;
    // Do the audited write first so the row is on the first page by ts order.
    const account = await api.createAccount(ACCOUNT, 'instagram');

    await authedPage.goto('/audit');
    await expect(authedPage.getByRole('heading', { name: 'Audit Log' })).toBeVisible();

    // The actor dropdown is built from /api/users; the seeded owner must be an
    // option, not only a row value.
    await authedPage.getByLabel('Actor').click();
    await expect(authedPage.getByRole('option', { name: new RegExp(ownerEmail) })).toBeVisible();
    await authedPage.keyboard.press('Escape');

    // The row for the write just made: an `account.create` verb with an `ok`
    // outcome. A row with no outcome column hides a silent failure.
    const row = authedPage.getByRole('row').filter({ hasText: 'account.create' }).first();
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expect(row.getByText('ok', { exact: true })).toBeVisible();
    await expect(row.getByText(ownerEmail)).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('the actor filter narrows to that actor\'s rows', async ({ authedPage, api }) => {
    const ownerEmail = (await api.listUsers()).users.find((u) => u.role === 'OWNER')!.email;
    const account = await api.createAccount(ACCOUNT, 'instagram');

    await authedPage.goto('/audit');

    await authedPage.getByLabel('Actor').click();
    await authedPage.getByRole('option', { name: new RegExp(ownerEmail) }).click();

    // The filtered page must still carry the owner's own write — and it must
    // carry the count, otherwise the filter silently returned nothing.
    await expect(
      authedPage.getByRole('row').filter({ hasText: 'account.create' }).first(),
    ).toBeVisible({ timeout: 30_000 });
    await expect(authedPage.getByText(/of \d+ entries/)).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('the action filter is populated from real audit actions', async ({ authedPage, api }) => {
    const account = await api.createAccount(ACCOUNT, 'instagram');

    await authedPage.goto('/audit');

    await authedPage.getByLabel('Action').click();
    // The options are the distinct actions on the loaded page, so an empty
    // server renders only "All actions" — this asserts the trail is live.
    const option = authedPage.getByRole('option', { name: /create/i }).first();
    await expect(option).toBeVisible();
    await option.click();

    await expect(
      authedPage.getByRole('row').filter({ hasText: 'account.create' }).first(),
    ).toBeVisible({ timeout: 30_000 });

    await api.removeAccount(account.id);
  });

  test('refresh is a real round trip and keeps the client filter', async ({ authedPage, api }) => {
    const account = await api.createAccount(ACCOUNT, 'instagram');

    await authedPage.goto('/audit');
    await authedPage
      .getByPlaceholder('Filter by actor, action, target or IP…')
      .fill('account.create');
    await authedPage.getByRole('button', { name: 'Refresh' }).click();

    // The client-side filter must survive a server refetch, otherwise the rows
    // the operator was looking at silently vanish.
    await expect(
      authedPage.getByRole('row').filter({ hasText: 'account.create' }).first(),
    ).toBeVisible({ timeout: 30_000 });

    await api.removeAccount(account.id);
  });
});
