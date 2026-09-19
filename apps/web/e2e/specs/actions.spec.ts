import { test, expect } from '../fixtures/base.js';

/**
 * Actions (P4-02, P3-13). The queue page composes comment text server-side from
 * templates, so this spec asserts on the enqueue contract — not on a typed-in
 * comment string, which the UI never accepts.
 *
 * The batch is enqueued through the UI exactly as an operator would, then the
 * job rows are read back through the API to confirm the pipeline took them.
 */

const TARGET = 'https://www.instagram.com/p/DdaJ8Y8gp3u/';

test.describe('actions page', () => {
  test('the queue is reachable and shows its empty state', async ({ authedPage, api }) => {
    const jobs = await api.listActions();
    test.skip(jobs.actions.length > 0, 'empty state needs a drained queue');

    await authedPage.goto('/actions');
    await expect(authedPage.getByText('The queue is empty')).toBeVisible();
  });

  test('refuses to enqueue without an account selected', async ({ authedPage }) => {
    await authedPage.goto('/actions');
    await authedPage.getByLabel('Target URL').fill(TARGET);

    // exact:true pins the enqueue-form button. A substring match also hits the
    // "Reply comment" button and any queued job row that renders "Comment".
    await authedPage.getByRole('button', { name: 'Comment', exact: true }).click();
    await expect(authedPage.getByText('Pick an account first.')).toBeVisible();
  });

  test('refuses to enqueue without a target URL', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_act_${Date.now().toString(36)}`, 'instagram');
    // The dropdown only offers ACTIVE accounts; a fresh row is AUTHENTICATING
    // and intentionally absent. Flip it so the row is selectable, and drop it
    // again at the end so the enqueue guards stay the only thing under test.
    await api.setAccountStatus(account.id, 'ACTIVE');

    await authedPage.goto('/actions');
    await authedPage.getByLabel('Account (worker)').click();
    await authedPage.getByRole('option', { name: new RegExp(account.username) }).click();

    await authedPage.getByRole('button', { name: 'Comment', exact: true }).click();
    await expect(authedPage.getByText('Paste at least one post URL.')).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('enqueues a like batch from the UI and the jobs land', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_like_${Date.now().toString(36)}`, 'instagram');
    await api.setAccountStatus(account.id, 'ACTIVE');

    await authedPage.goto('/actions');
    await authedPage.getByLabel('Account (worker)').click();
    await authedPage.getByRole('option', { name: new RegExp(account.username) }).click();
    await authedPage.getByLabel('Target URL').fill(TARGET);

    const [response] = await Promise.all([
      authedPage.waitForResponse((r) => r.url().endsWith('/api/actions') && r.request().method() === 'POST'),
      authedPage.getByRole('button', { name: 'Like', exact: true }).click(),
    ]);
    expect(response.ok(), 'the enqueue succeeded').toBe(true);

    const body = await response.json();
    expect(body.actions, 'one job per pasted URL').toHaveLength(1);
    expect(body.actions[0].actionType).toBe('action_like');

    // The queue renders the new row. A PENDING job has no targetUrl yet
    // (resolved at dispatch — see ActionJob schema), so the row shows its
    // targetId; assert on that, not on the pasted URL.
    await expect(authedPage.getByRole('table', { name: 'Action queue' })).toContainText(
      body.actions[0].targetId,
    );

    await api.removeAccount(account.id);
  });

  test('a batch over the cap is refused with the count', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_cap_${Date.now().toString(36)}`, 'instagram');
    await api.setAccountStatus(account.id, 'ACTIVE');
    const tooMany = Array.from({ length: 51 }, (_, i) => `${TARGET}?${i}`).join('\n');

    await authedPage.goto('/actions');
    await authedPage.getByLabel('Account (worker)').click();
    await authedPage.getByRole('option', { name: new RegExp(account.username) }).click();
    await authedPage.getByLabel('Target URL').fill(tooMany);

    await authedPage.getByRole('button', { name: 'Like', exact: true }).click();
    await expect(authedPage.getByText(/capped at 50/)).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('the status filter narrows the queue and reports the count', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_flt_${Date.now().toString(36)}`, 'instagram');
    await api.setAccountStatus(account.id, 'ACTIVE');
    await api.enqueue([
      { accountId: account.id, targetUrl: TARGET, actionType: 'action_like' },
    ]);

    await authedPage.goto('/actions');
    // "1 of 1 shown" is the subtitle; it only renders once the list has loaded.
    await expect(authedPage.getByText(/1 of 1 shown/)).toBeVisible();

    await authedPage.getByLabel('All statuses').click();
    await authedPage.getByRole('option', { name: 'Pending' }).click();
    await expect(authedPage.getByText(/1 of 1 shown/)).toBeVisible();

    await authedPage.getByLabel('Pending').click();
    await authedPage.getByRole('option', { name: 'Success' }).click();
    await expect(authedPage.getByText(/0 of 1 shown/)).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('a job card opens the detail dialog with its target', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_dlg_${Date.now().toString(36)}`, 'instagram');
    await api.setAccountStatus(account.id, 'ACTIVE');
    await api.enqueue([
      { accountId: account.id, targetUrl: TARGET, actionType: 'action_like' },
    ]);

    await authedPage.goto('/actions');
    const queue = authedPage.getByRole('table', { name: 'Action queue' });
    // The queue is a shared list and jobs targeting the same post share a
    // targetId, so target text is not a unique row handle. Group headers are
    // plain divs, so the only buttons in the table are the job rows: click the
    // first and assert the dialog wiring (target section + an "attempt" heading),
    // which holds whatever job and status the row happens to be.
    await queue.getByRole('button').first().click();

    await expect(authedPage.getByRole('dialog')).toBeVisible();
    await expect(authedPage.getByRole('dialog')).toContainText('Target');
    await expect(authedPage.getByRole('heading', { name: /attempt/ })).toBeVisible();

    // Both the footer button and the dialog's top-right icon expose "Close";
    // the footer one is first in the DOM.
    await authedPage.getByRole('dialog').getByRole('button', { name: 'Close' }).first().click();
    await expect(authedPage.getByRole('dialog')).toBeHidden();

    await api.removeAccount(account.id);
  });
});
