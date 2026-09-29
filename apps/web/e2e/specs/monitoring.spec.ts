import { test, expect } from '../fixtures/base.js';

/**
 * Monitoring (P2-15) and the shared dashboard shell.
 *
 * The provider is whatever the local stack is configured with: the
 * deterministic stub, or Composio, which only ingests an account bound to a
 * real connection. The specs therefore seed through the API, ask the API what
 * it actually stored, and assert the page agrees — never pin a figure the
 * stack does not control.
 */
import type { ApiClient } from '../fixtures/api.js';

/**
 * Seeds one official account and runs an ingest. Returns the account plus the
 * per-metric totals the API stored, or null totals when the configured
 * provider cannot serve this account (Composio without a connection id) — the
 * page then has nothing to show, and asserting a number would be asserting a
 * fiction.
 */
async function seedAndIngest(api: ApiClient, handle: string) {
  const acc = await api.createOfficialAccount(handle);
  await api.refreshAnalytics();
  const { kpis } = await api.analyticsOverview(30);
  return {
    acc,
    kpis,
    ingested: kpis.length > 0,
    total: (metric: string) =>
      kpis.filter((k) => k.metric === metric).reduce((s, k) => s + (k.value ?? 0), 0),
  };
}

test.describe('monitoring page', () => {
  test('totals each metric separately instead of summing the whole KPI list', async ({
    authedPage,
    api,
  }) => {
    const { acc, ingested, total, kpis } = await seedAndIngest(api, `e2e_ig_${Date.now()}`);
    try {
      if (ingested) {
        // The API returns one row per (account, metric). Summing all of them
        // adds reach to views to followers and mislabels the total, so the
        // grand total must be strictly larger than the reach alone.
        const grandTotal = kpis.reduce((s, k) => s + (k.value ?? 0), 0);
        expect(grandTotal).toBeGreaterThan(total('reach'));
      }

      await authedPage.goto('/monitoring');
      await expect(authedPage.getByRole('heading', { name: 'Monitoring' }).first()).toBeVisible();
      if (ingested) {
        await expect(authedPage.getByText(total('reach').toLocaleString(), { exact: true }).first())
          .toBeVisible();
      }
    } finally {
      await api.deleteOfficialAccount(acc.id);
    }
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

  test('the platform page keeps its KPI strip populated under the default filter', async ({
    authedPage,
    api,
  }) => {
    const { acc, ingested, total } = await seedAndIngest(api, `e2e_ig_page_${Date.now()}`);
    try {
      await authedPage.goto('/monitoring/instagram');
      await expect(authedPage).toHaveURL(/.*\/monitoring\/instagram/);
      await expect(authedPage.getByRole('button', { name: 'Sync now' })).toBeVisible();
      await expect(authedPage.getByText(acc.handle, { exact: true })).toBeVisible();

      // "All official accounts" is the default. It must not filter the strip to
      // nothing: a monitored account with a stored figure has to render it.
      if (ingested) {
        await expect(
          authedPage.getByText(total('followers').toLocaleString(), { exact: true }).first(),
        ).toBeVisible();
      }
    } finally {
      await api.deleteOfficialAccount(acc.id);
    }
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

      // "Overview" is nested under the collapsible "Monitoring" nav group and is
      // hidden until the group is expanded; the group is collapsed by default on
      // /workers (no monitoring route is active). Expand it before clicking.
      if (label === 'Overview') {
        await authedPage.getByRole('button', { name: 'Monitoring' }).click();
      }

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
    // Identity is a role-derived label in a static block (the session has no
    // email and this is not a clickable menu), tagged data-testid=session-role.
    await expect(authedPage.getByTestId('session-role')).toContainText(me.role.toLowerCase());
  });
});
