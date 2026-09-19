import { test, expect } from '../fixtures/base.js';

/**
 * Reports (P2-16) — read-only pivots over executed work. No data is seeded, so
 * the assertions cover the filter surface and the empty result; the CSV export
 * is checked for a real 200, not a browser download.
 */

const TARGET = 'https://www.instagram.com/p/DdaJ8Y8gp3u/';

test.describe('reports page', () => {
  test('renders the filters and runs an empty actions report', async ({ authedPage }) => {
    await authedPage.goto('/reports');
    // The shell header and the page h1 both render a "Reports" heading; the
    // first proves the page rendered.
    await expect(authedPage.getByRole('heading', { name: 'Reports' }).first()).toBeVisible();

    await authedPage.getByLabel('Report').click();
    await authedPage.getByRole('option', { name: 'Actions by day' }).click();
    await authedPage.getByRole('button', { name: 'Run report' }).click();

    // An empty window must say so, not render a hollow chart. Two panels render
    // the same empty-state copy, so scope to the first.
    await expect(authedPage.getByText('No rows in this window').first()).toBeVisible({
      timeout: 30_000,
    });
  });

  test('the targets report renders the posts that drew engagement', async ({ authedPage, api }) => {
    const account = await api.createAccount(`e2e_rep_${Date.now().toString(36)}`, 'instagram');
    await api.enqueue([
      { accountId: account.id, targetUrl: TARGET, actionType: 'action_like' },
    ]);

    await authedPage.goto('/reports');
    await authedPage.getByLabel('Report').click();
    await authedPage.getByRole('option', { name: 'Targets (posts)' }).click();
    await authedPage.getByRole('button', { name: 'Run report' }).click();

    await expect(authedPage.getByText('The posts that drew engagement')).toBeVisible({
      timeout: 30_000,
    });

    await api.removeAccount(account.id);
  });

  test('the export endpoint answers with CSV for the owner', async ({ authedPage, apiUrl }) => {
    await authedPage.goto('/reports');
    await expect(authedPage.getByRole('button', { name: 'Export CSV' })).toBeEnabled();

    // Export is wired as a navigation, not a fetch — clicking it would leave the
    // page. Assert the endpoint the button targets instead: owner has `export`,
    // so it must return real CSV. Use the page's request context so it carries
    // the owner's smm_at cookie; the bare `request` fixture is anonymous → 401.
    const res = await authedPage.request.get(`${apiUrl}/api/reports/export`, {
      params: { kind: 'actions', format: 'csv' },
    });
    expect(res.ok(), `export returned ${res.status()}`).toBe(true);
    expect(res.headers()['content-type'] ?? '').toContain('text/csv');
  });

  test('the analytics report surfaces its metric picker', async ({ authedPage }) => {
    await authedPage.goto('/reports');
    await authedPage.getByLabel('Report').click();
    await authedPage.getByRole('option', { name: 'Official account growth' }).click();

    await expect(authedPage.getByLabel('Metric')).toBeVisible();
    await expect(authedPage.getByLabel('Official account ID')).toBeVisible();
  });
});
