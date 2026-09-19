import { defineConfig, devices } from '@playwright/test';

/**
 * E2E config (QA suite). The suite runs against the live compose stack —
 * `make up` first — not a Playwright-spawned server, because most specs assert
 * on real worker provisioning (a container lands in `docker ps`) and that needs
 * the docker driver the API only gets in compose.
 *
 * WEB_URL / API_URL match the compose defaults in .env. Override per run when a
 * different port is published.
 */
const WEB_URL = process.env.E2E_WEB_URL ?? 'http://localhost:24081';
const API_URL = process.env.E2E_API_URL ?? 'http://localhost:24080';

export default defineConfig({
  testDir: './specs',
  timeout: 120_000,
  // A spec that drives a real container create/delete is slow: the worker has
  // to boot, claim its row and heartbeat before the card flips READY.
  expect: { timeout: 15_000 },
  fullyParallel: false, // the fleet is one shared resource; parallel writes race the reconciler
  // The suite runs against a live stack whose SSE propagation and audit-trail
  // writes settle asynchronously; a single retry absorbs that timing jitter
  // without masking real, repeatable failures (which fail both attempts).
  retries: 1,
  workers: 1,
  reporter: [['list'], ['html', { outputFolder: 'report', open: 'never' }]],
  use: {
    baseURL: WEB_URL,
    // Bound a single action (click/fill/etc.). Without this Playwright's default
    // is 0 (no cap): a locator that never resolves — a control the UI renamed or
    // restructured — hangs the whole test until the 120s test timeout instead of
    // failing in seconds. A wrong selector should fail fast, not stall the suite.
    actionTimeout: 10_000,
    // The dashboard holds an open SSE subscription (/api/stream) for its whole
    // lifetime, so the `load` event never fires — every navigation would hang
    // until timeout. `domcontentloaded` is the real page-ready signal here.
    navigationTimeout: 15_000,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    // every spec starts from a logged-in browser context
    storageState: undefined,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // A spec that times out mid-run never reaches its afterEach, so its rows leak
  // into the next run and the next run's empty-state / unique-row assertions
  // flake on someone else's leftovers. Sweep every e2e_* row once, after the
  // whole suite — including after a failure or a Ctrl-C.
  globalTeardown: './fixtures/teardown.ts',
  // No webServer: the stack is already up. A missing API fails loudly in the
  // first spec instead of silently waiting on a boot that never happens.
  metadata: { apiUrl: API_URL },
});
