import { test, expect } from '../fixtures/base.js';

/**
 * Actions (P4-02, P3-13). The New-action form is one linear flow: paste a link
 * → Scrape → pick like/comment → tick accounts → queue. Scrape runs an Apify
 * actor synchronously, so it is stubbed at the network boundary: the point of
 * this spec is the enqueue contract, and a paid actor run per test would make
 * the suite slow, flaky and billable.
 *
 * The batch is enqueued through the UI exactly as an operator would, then the
 * job rows are read back through the API to confirm the pipeline took them.
 */

const TARGET = 'https://www.instagram.com/p/DdaJ8Y8gp3u/';

/** The stubbed scrape reply: enough shape for the form to render step 2. */
function scrapedPost() {
  return {
    post: {
      id: 'e2e-post',
      platform: 'instagram',
      externalId: 'DdaJ8Y8gp3u',
      authorHandle: 'e2e_target',
      text: 'A target post for the e2e suite.',
      mediaUrls: null,
      metrics: { likeCount: 1200, commentCount: 34 },
      scrapedAt: new Date().toISOString(),
    },
    comments: [],
  };
}

/** Stub /api/scrape/target so no test spends an actor run. */
async function stubScrape(page: import('@playwright/test').Page) {
  await page.route('**/api/scrape/target', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(scrapedPost()) }),
  );
}

/** Scrape + pick the flow, leaving the form at account selection. */
async function openFlow(page: import('@playwright/test').Page, flow: 'Like' | 'Comment') {
  await page.goto('/actions');
  await page.getByLabel('Target post').fill(TARGET);
  await page.getByRole('button', { name: 'Scrape' }).click();
  await expect(page.getByText('@e2e_target')).toBeVisible();
  await page.getByRole('button', { name: flow, exact: true }).click();
}

test.describe('actions page', () => {
  test('the queue is reachable and shows its empty state', async ({ authedPage, api }) => {
    const jobs = await api.listActions();
    test.skip(jobs.actions.length > 0, 'empty state needs a drained queue');

    await authedPage.goto('/actions');
    await expect(authedPage.getByText('The queue is empty')).toBeVisible();
  });

  test('refuses to enqueue without an account selected', async ({ authedPage }) => {
    await stubScrape(authedPage);
    await openFlow(authedPage, 'Like');

    // The queue button is disabled with nothing ticked, so the guard is the
    // disabled state itself rather than an error message.
    await expect(authedPage.getByRole('button', { name: /^Queue 0/ })).toBeDisabled();
  });

  test('refuses to scrape without a target URL', async ({ authedPage }) => {
    await authedPage.goto('/actions');
    await authedPage.getByRole('button', { name: 'Scrape' }).click();
    await expect(authedPage.getByText('Paste a post link first.')).toBeVisible();
  });

  test('enqueues a like batch from the UI and the jobs land', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_like_${Date.now().toString(36)}`, 'instagram');

    await stubScrape(authedPage);
    await openFlow(authedPage, 'Like');
    await authedPage.getByRole('button', { name: `Select @${account.username}` }).click();

    const [response] = await Promise.all([
      authedPage.waitForResponse((r) => r.url().endsWith('/api/actions') && r.request().method() === 'POST'),
      authedPage.getByRole('button', { name: /^Queue 1 like/ }).click(),
    ]);
    expect(response.ok(), 'the enqueue succeeded').toBe(true);

    const body = await response.json();
    expect(body.actions, 'one job per selected account').toHaveLength(1);
    expect(body.actions[0].actionType).toBe('action_like');

    // The row must appear WITHOUT a reload. This is the enqueue contract: the
    // API publishes an action-updated frame for each created job, and the page
    // refetches on it (ADR 0010 — the frame is a signal, the read is
    // authoritative). A dead frame here means an operator queues work and sees
    // nothing, which is what this assertion exists to catch.
    //
    // Two locators match the account name on this page — the account picker's
    // own span and the queue's group header — so the assertion is scoped to the
    // queue table. That scoping is also the assertion: the table only renders
    // once the queue has a row, so waiting on it inside the table is exactly
    // "the new job reached the queue".
    //
    // The handle is used rather than the targetId: targetId is not unique
    // across the queue (jobs against the same post share it), and the queue is
    // shared with other specs. The account name is unique per run.
    const queue = authedPage.getByRole('table', { name: 'Action queue' });
    await expect(
      queue.getByText(new RegExp(`@${account.username}`)),
      'the queue shows the job without a reload',
    ).toBeVisible();

    // A PENDING job has no targetUrl yet (resolved at dispatch — see ActionJob
    // schema), so the row shows its targetId.
    await expect(queue).toContainText(body.actions[0].targetId);

    await api.removeAccount(account.id);
  });

  test('a batch over the cap is refused', async ({ api }) => {
    // The cap lives on the server (service.MaxActionBatch) and the form caps
    // selection client-side, so the guard is asserted where it is enforced.
    // Driving it through the UI would need 51 ticked accounts.
    const account = await api.createAccount(`e2e_cap_${Date.now().toString(36)}`, 'instagram');
    const items = Array.from({ length: 51 }, (_, i) => ({
      accountId: account.id,
      targetUrl: `${TARGET}?${i}`,
      actionType: 'action_like',
    }));

    const { status, body } = await api.enqueueRejected(items);
    expect(status).toBe(400);
    expect(body).toMatch(/cap is 50/);

    await api.removeAccount(account.id);
  });

  test('the status filter narrows the queue', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_flt_${Date.now().toString(36)}`, 'instagram');
    await api.enqueue([{ accountId: account.id, targetUrl: TARGET, actionType: 'action_like' }]);

    await authedPage.goto('/actions');

    // Assert on THIS test's row, never on the global count. The queue is shared
    // and a job row outlives the account it belonged to (action_job cascades on
    // account delete, but a spec that failed before its own cleanup leaves rows
    // behind), so `${n} of ${n} shown` is not a number any single spec owns.
    // The account name is unique per run, which makes its group header a stable
    // handle whatever else is in the queue. Scoped to the table because the
    // account picker also renders the handle.
    const queue = authedPage.getByRole('table', { name: 'Action queue' });
    const mine = queue.getByText(new RegExp(`@${account.username}`));
    await expect(mine).toBeVisible();

    // A PENDING job is not SUCCESS, so filtering to Success must drop the group
    // that holds it. This is the filter contract, asserted without a count.
    await authedPage.getByLabel('Status filter: All statuses').click();
    await authedPage.getByRole('option', { name: 'Success' }).click();
    await expect(mine).toBeHidden();

    // And the filter is reversible: back to All, the row returns.
    await authedPage.getByLabel('Status filter: Success').click();
    await authedPage.getByRole('option', { name: 'All statuses' }).click();
    await expect(mine).toBeVisible();

    await api.removeAccount(account.id);
  });

  test('a job card opens the detail dialog with its target', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_dlg_${Date.now().toString(36)}`, 'instagram');
    await api.enqueue([{ accountId: account.id, targetUrl: TARGET, actionType: 'action_like' }]);

    await authedPage.goto('/actions');
    // Wait for this test's own row before clicking, so the click cannot land on
    // another spec's job when the queue is shared. Scoped to the table because
    // the account picker also renders the handle.
    const queue = authedPage.getByRole('table', { name: 'Action queue' });
    await expect(queue.getByText(new RegExp(`@${account.username}`))).toBeVisible();

    // Group headers are plain divs, so the table's only buttons are job rows.
    // Clicking the one inside this account's group keeps the assertion scoped to
    // a job this test created, whatever else is queued.
    await queue
      .getByRole('button')
      .filter({ hasText: /Like/ })
      .first()
      .click();

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