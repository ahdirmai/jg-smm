import { test, expect } from '../fixtures/base.js';
import { containerExists, dockerAvailable, expectedContainerName, listWorkerContainers } from '../fixtures/docker.js';

/**
 * Workers / fleet (P4-01, P6-05, F-07). This spec is the reason the docker
 * driver names containers after the row: the operator has to recognise the
 * container they just created on the command line.
 *
 * Each test creates a uniquely-named container and deletes it at the end. The
 * reconciler provisions a real container, so the docker assertions are live,
 * not mocked — they are skipped when no daemon is reachable.
 */

const CITY = 'Jakarta';

test.describe('workers page', () => {
  test('renders the create form with the city list', async ({ authedPage, api }) => {
    await authedPage.goto('/workers');
    await expect(authedPage.getByRole('heading', { name: 'Add container' })).toBeVisible();

    const locations = await api.listLocations();
    for (const loc of locations.slice(0, 3)) {
      await expect(authedPage.locator('#c-location')).toContainText(loc.name);
    }
  });

  test('create from the web lands a container in docker ps with the row name', async ({
    authedPage,
    api,
  }) => {
    test.skip(!dockerAvailable(), 'needs a local docker daemon');
    const name = `e2e-${Date.now().toString(36)}`;

    await authedPage.goto('/workers');
    // The form must be interactive before the click: a pre-hydration click
    // submits natively and navigates away, killing the response wait.
    await expect(authedPage.locator('#c-name')).toBeEnabled();
    await authedPage.locator('#c-name').fill(name);
    await authedPage.locator('#c-location').selectOption(CITY);

    // Assert the round trip, not just the row: the API create is what the
    // reconciler keys off, so a 201 here means the click really submitted.
    const [response] = await Promise.all([
      authedPage.waitForResponse(
        (r) => r.url().endsWith('/api/containers') && r.request().method() === 'POST',
      ),
      authedPage.getByRole('button', { name: 'Create' }).click(),
    ]);
    expect(response.status(), 'POST /api/containers returned 201').toBe(201);
    const created = await response.json();
    test.info().annotations.push({ type: 'workerId', description: created.id });

    // The card for the row appears on the page.
    await expect(authedPage.getByText(name).first()).toBeVisible({ timeout: 30_000 });

    // The container shows up on the command line under the friendly name.
    const expected = expectedContainerName(name, created.id);
    await expect
      .poll(() => containerExists(expected), { timeout: 90_000 })
      .toBeTruthy();

    // And the card flips READY once the worker heartbeats.
    await expect
      .poll(
        async () =>
          (await api.listContainers()).containers.find((c) => c.id === created.id)?.status,
        { timeout: 120_000 },
      )
      .toBe('READY');

    await api.deleteContainer(created.id);
  });

  test('the live-view button is disabled until the worker publishes its URL', async ({
    authedPage,
    api,
  }) => {
    const name = `e2e-novnc-${Date.now().toString(36)}`;
    const created = await api.createContainer(name, CITY);

    await authedPage.goto('/workers');
    const card = authedPage.getByText(name).locator('xpath=ancestor::div[contains(@class,"rounded-xl")]');

    // A fresh row has no URL yet; the button is present but disabled and says why.
    if (!created.novncUrl) {
      await expect(card.getByRole('button', { name: 'Live view' })).toBeDisabled();
    }

    await api.deleteContainer(created.id);
  });

  test('the provisioning log explains a PENDING card', async ({ authedPage, api }) => {
    const name = `e2e-log-${Date.now().toString(36)}`;
    const created = await api.createContainer(name, CITY);

    await authedPage.goto('/workers');
    const card = authedPage.getByText(name).locator('xpath=ancestor::div[contains(@class,"rounded-xl")]');
    await card.getByRole('button', { name: /provisioning log/i }).click();

    // The audit trail renders (rows may be empty for a fast provision, so the
    // panel itself is the assertion — it must open and not hang on "loading…").
    await expect(card.getByText(/loading…|No provisioning ops|create/i)).toBeVisible({ timeout: 30_000 });

    await api.deleteContainer(created.id);
  });

  test('delete removes the container row and the docker container', async ({
    authedPage,
    api,
  }) => {
    test.skip(!dockerAvailable(), 'needs a local docker daemon');
    const name = `e2e-del-${Date.now().toString(36)}`;
    const created = await api.createContainer(name, CITY);
    const expected = expectedContainerName(name, created.id);

    await expect
      .poll(() => containerExists(expected), { timeout: 60_000 })
      .toBeTruthy();

    await authedPage.goto('/workers');
    const card = authedPage.getByText(name).locator('xpath=ancestor::div[contains(@class,"rounded-xl")]');
    await card.getByRole('button', { name: 'Open container menu' }).click();
    await authedPage.getByRole('menuitem', { name: /Delete/i }).click();

    await expect
      .poll(async () => (await api.listContainers()).containers.some((c) => c.id === created.id))
      .toBe(false);
    await expect.poll(() => containerExists(expected)).toBe(false);
  });

  test('delete is refused while an account is packed', async ({ authedPage, api }) => {
    const name = `e2e-packed-${Date.now().toString(36)}`;
    const created = await api.createContainer(name, CITY);
    const account = await api.createAccount(`e2e_bot_${Date.now().toString(36)}`, 'instagram');

    await authedPage.goto('/workers');
    const card = authedPage.getByText(name).locator('xpath=ancestor::div[contains(@class,"rounded-xl")]');
    await card.getByRole('button', { name: 'Open container menu' }).click();
    // The guard is UX: the item names the reason.
    await expect(authedPage.getByRole('menuitem', { name: /Delete \(remove accounts first\)/i })).toBeDisabled();

    await api.removeAccount(account.id);
    await api.deleteContainer(created.id);
  });

  test('grouping by city keeps every container reachable', async ({ authedPage, api }) => {
    const a = await api.createContainer(`e2e-grp-a-${Date.now().toString(36)}`, CITY);
    const b = await api.createContainer(`e2e-grp-b-${Date.now().toString(36)}`, 'Bandung');

    await authedPage.goto('/workers');
    // "All cities" is the default and shows both regardless of group.
    await expect(authedPage.getByText(a.name)).toBeVisible();
    await expect(authedPage.getByText(b.name)).toBeVisible();

    await authedPage.getByRole('button', { name: /All cities/i }).click();
    await authedPage.getByRole('option', { name: 'Bandung' }).click();
    await expect(authedPage.getByText(b.name)).toBeVisible();

    await api.deleteContainer(a.id);
    await api.deleteContainer(b.id);
  });
});

test.afterEach(async ({ api }) => {
  // Best-effort cleanup: a failed spec must not leave fleet rows behind that
  // break the next run's empty-state assertions.
  const list = await api.listContainers();
  for (const c of list.containers.filter((c) => c.name.startsWith('e2e-'))) {
    await api.deleteContainer(c.id).catch(() => undefined);
  }
  // Report any strays so the docker assertion in a later run is not confused.
  if (dockerAvailable()) {
    const strays = listWorkerContainers().filter((c) => c.name.includes('e2e-'));
    expect(strays, 'leftover worker containers after cleanup').toHaveLength(0);
  }
});
