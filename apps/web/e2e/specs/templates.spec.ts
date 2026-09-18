import { test, expect } from '../fixtures/base.js';

/**
 * Templates (P3-13). The comment pool. Every variant created here is an
 * Instagram one and is removed at the end so the pool never accumulates E2E
 * text — a leftover variant would start appearing in real queue jobs.
 */

const BODY = `e2e variant ${Date.now().toString(36)}`;

test.describe('templates page', () => {
  test('renders the empty state when the pool has nothing to show', async ({ authedPage, api }) => {
    const list = await api.listTemplates();
    test.skip(list.templates.length > 0, 'empty state needs an empty pool');

    await authedPage.goto('/templates');
    await expect(authedPage.getByText('No variants yet')).toBeVisible();
  });

  test('creates a variant from the dialog and it lands in the table', async ({ authedPage, api }) => {
    await authedPage.goto('/templates');
    await authedPage.getByRole('button', { name: 'New variant' }).click();

    await expect(authedPage.getByRole('dialog')).toContainText('New variant');
    await authedPage.getByLabel('Body').fill(BODY);
    await authedPage.getByLabel('Variables').fill('topic');

    const [response] = await Promise.all([
      authedPage.waitForResponse((r) => r.url().endsWith('/api/templates') && r.request().method() === 'POST'),
      authedPage.getByRole('button', { name: 'Create variant' }).click(),
    ]);
    expect(response.ok(), 'the create succeeded').toBe(true);
    const created = (await response.json()) as { id: string };

    // The dialog closes and the table shows the new row.
    await expect(authedPage.getByRole('dialog')).toBeHidden();
    await expect(authedPage.getByText(BODY)).toBeVisible();

    await api.deleteTemplate(created.id);
  });

  test('refuses an empty body inline', async ({ authedPage }) => {
    await authedPage.goto('/templates');
    await authedPage.getByRole('button', { name: 'New variant' }).click();

    await authedPage.getByRole('button', { name: 'Create variant' }).click();
    await expect(authedPage.getByText('A variant body is required.')).toBeVisible();
    await expect(authedPage.getByRole('dialog')).toBeVisible();
  });

  test('edits an existing variant and the table reflects it', async ({ authedPage, api }) => {
    const created = await api.createTemplate('instagram', BODY);
    const next = `${BODY}-edited`;

    await authedPage.goto('/templates');
    await expect(authedPage.getByText(BODY)).toBeVisible();

    await authedPage
      .getByRole('row')
      .filter({ hasText: BODY })
      .getByRole('button', { name: 'Open variant menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Edit' }).click();

    await expect(authedPage.getByRole('dialog')).toContainText('Edit variant');
    await authedPage.getByLabel('Body').fill(next);
    await authedPage.getByRole('button', { name: 'Save changes' }).click();

    await expect(authedPage.getByRole('dialog')).toBeHidden();
    await expect(authedPage.getByText(next)).toBeVisible();
    await expect(authedPage.getByText(BODY)).toBeHidden();

    await api.deleteTemplate(created.id);
  });

  test('deletes a variant from the row menu', async ({ authedPage, api }) => {
    const created = await api.createTemplate('instagram', BODY);

    await authedPage.goto('/templates');
    await authedPage
      .getByRole('row')
      .filter({ hasText: BODY })
      .getByRole('button', { name: 'Open variant menu' })
      .click();
    await authedPage.getByRole('menuitem', { name: 'Delete' }).click();

    await expect(authedPage.getByText(BODY)).toBeHidden({ timeout: 30_000 });
    await expect
      .poll(async () => (await api.listTemplates()).templates.some((t) => t.id === created.id))
      .toBe(false);
  });

  test('the platform filter narrows the variants table', async ({ authedPage, api }) => {
    const ig = await api.createTemplate('instagram', BODY);
    const tt = await api.createTemplate('tiktok', `e2e tt ${Date.now().toString(36)}`);

    await authedPage.goto('/templates');
    await expect(authedPage.getByText(BODY)).toBeVisible();

    await authedPage.getByLabel('All platforms').click();
    await authedPage.getByRole('option', { name: 'TikTok' }).click();

    await expect(authedPage.getByText(tt.text)).toBeVisible();
    await expect(authedPage.getByText(BODY)).toBeHidden();

    await api.deleteTemplate(ig.id);
    await api.deleteTemplate(tt.id);
  });
});

test.afterEach(async ({ api }) => {
  // The pool feeds real queue jobs, so no E2E text may survive the run.
  const list = await api.listTemplates();
  for (const t of list.templates.filter((t) => t.text.startsWith('e2e'))) {
    await api.deleteTemplate(t.id).catch(() => undefined);
  }
});
