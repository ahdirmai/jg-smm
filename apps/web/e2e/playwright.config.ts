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
  retries: 0,
  workers: 1,
  reporter: [['list'], ['html', { outputFolder: 'report', open: 'never' }]],
  use: {
    baseURL: WEB_URL,
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
  // No webServer: the stack is already up. A missing API fails loudly in the
  // first spec instead of silently waiting on a boot that never happens.
  metadata: { apiUrl: API_URL },
});
