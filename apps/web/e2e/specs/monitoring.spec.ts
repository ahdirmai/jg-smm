import { test, expect } from '../fixtures/base.js';

/**
 * Monitoring (P2-15) and the shared dashboard shell. The KPI strip and the
 * official-account table come from the analytics provider, so these specs
 * assert on rendering and navigation — not on metric values, which the stack
 * does not control.
 */

test.describe('monitoring page', () => {
  test('renders the KPI strip and its empty state', async ({ authedPage }) => {
    await authedPage.goto('/monitoring');

    await expect(authedPage.getByRole('heading', { name: 'Monitoring' })).toBeVisible();
    for (const label of [
      'Monitored accounts',
      'Platforms',
      'Total followers',
      'Stale accounts',
    ]) {
      await expect(authedPage.getByText(label)).toBeVisible();
    }
    // No official accounts are seeded, so the table says so instead of
    // rendering a hollow shell.
    await expect(authedPage.getByText('No official accounts monitored yet.')).toBeVisible();
  });

  test('the sync button is a real round trip and settles', async ({ authedPage }) => {
    await authedPage.goto('/monitoring');

    await authedPage.getByRole('button', { name: 'Sync now' }).click();

    // The button flips to its busy label and comes back; it never errors into
    // a stuck "Syncing…" — that would hang the page's only refresh affordance.
    await expect(authedPage.getByRole('button', { name: 'Sync now' })).toBeVisible({
      timeout: 60_000,
    });
  });

  test('a platform subroute renders without bouncing to an error', async ({ authedPage }) => {
    await authedPage.goto('/monitoring/instagram');
    await expect(authedPage).toHaveURL(/.*\/monitoring\/instagram/);
    await expect(authedPage.getByRole('button', { name: 'Sync now' })).toBeVisible();
  });
});

test.describe('dashboard shell', () => {
  // Every nav entry the operator can reach; the dead-link spec proves each one
  // resolves. The sidebar groups Monitoring under a collapsible section, so its
  // items are reached by exact label, not by the group heading.
  const NAV: { label: string; url: string }[] = [
    { label: 'Workers', url: '/workers' },
    { label: 'Accounts', url: '/accounts' },
    { label: 'Actions', url: '/actions' },
    { label: 'Templates', url: '/templates' },
    { label: 'Overview', url: '/monitoring' },
    { label: 'Reports', url: '/reports' },
    { label: 'Audit Log', url: '/audit' },
    { label: 'Settings', url: '/settings' },
  ];

  for (const { label, url } of NAV) {
    test(`the ${label} nav entry routes to ${url}`, async ({ authedPage }) => {
      await authedPage.goto('/workers');
      await authedPage.getByRole('link', { name: label, exact: true }).click();

      await expect(authedPage).toHaveURL(new RegExp(`${url.replace(/\//g, '\\/')}\\/?$`));
    });
  }

  test('the shell header carries the page title and subtitle', async ({ authedPage }) => {
    await authedPage.goto('/actions');
    await expect(authedPage.getByRole('heading', { name: 'Actions' })).toBeVisible();
    await expect(authedPage.getByText('Targeted like, comment, and report jobs')).toBeVisible();
  });

  test('the session menu shows the signed-in owner', async ({ authedPage, api }) => {
    const me = await api.me();

    await authedPage.goto('/workers');
    await authedPage.getByRole('button', { name: new RegExp(me.user.email) }).click();
    await expect(authedPage.getByText(me.user.email)).toBeVisible();
  });
});
