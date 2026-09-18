import { test as base, type Page, type APIRequestContext } from '@playwright/test';

import { ApiClient } from './api.js';

/**
 * The suite's fixtures. `api` is a Node-side client seeded from .env, so a spec
 * can create a container via the API and then assert the UI renders it — or the
 * reverse. `apiUrl` is the base the browser-facing requests hit.
 *
 * Login is a fixture, not a setup project: the whole suite shares one owner
 * identity and every spec needs it, so doing it once per worker is both simpler
 * and immune to a stale storageState file.
 */

const API_URL = process.env.E2E_API_URL ?? 'http://localhost:24080';
const OWNER_EMAIL = process.env.E2E_OWNER_EMAIL ?? 'owner@smm.local';
const OWNER_PASSWORD = process.env.E2E_OWNER_PASSWORD ?? 'changeme-changeme';

/**
 * Every dashboard page subscribes to the SSE stream for its lifetime, so the
 * `load` event never fires and the default `waitUntil` hangs until timeout.
 * `domcontentloaded` is the page-ready signal here; the specs' own assertions
 * wait for the elements they actually care about.
 */
/**
 * Wrap a page so navigations use domcontentloaded without each spec having to
 * remember it. Only goto needs patching: the dashboard reaches every state
 * through client routing, and the SSE stream is what stalls the load event.
 */
function withSseSafeNavigation(page: Page): Page {
  const goto = page.goto.bind(page);
  Object.assign(page, {
    goto: (url: string, opts?: object) =>
      goto(url, { waitUntil: 'domcontentloaded', ...opts }),
  });
  return page;
}

export type SuiteFixtures = {
  apiUrl: string;
  api: ApiClient;
  /** A browser page already logged in as the owner. */
  authedPage: Page;
};

export const test = base.extend<SuiteFixtures>({
  apiUrl: async ({}, use) => {
    await use(API_URL);
  },
  api: async ({}, use) => {
    const client = new ApiClient(API_URL);
    await client.login(OWNER_EMAIL, OWNER_PASSWORD);
    await use(client);
  },
  page: async ({ page }, use) => {
    await use(withSseSafeNavigation(page));
  },
  authedPage: async ({ browser, request }, use) => {
    // Log in over HTTP once and plant the resulting cookie in the browser
    // context. Driving the login form in-band costs a page load per spec and
    // races the redirect guard; the cookie is the same identity either way.
    const res = await request.post(`${API_URL}/api/auth/login`, {
      data: { email: OWNER_EMAIL, password: OWNER_PASSWORD },
    });
    if (!res.ok()) throw new Error(`fixture login failed: ${res.status()}`);

    const setCookie = res.headers()['set-cookie'] ?? '';
    const access = setCookie
      .split(',')
      .map((c) => c.trim())
      .find((c) => c.startsWith('smm_at='));
    if (!access) throw new Error('fixture login: no smm_at cookie returned');

    const ctx = await browser.newContext();
    await ctx.addCookies([
      {
        name: 'smm_at',
        value: access.split('=')[1]?.split(';')[0] ?? '',
        domain: new URL(API_URL).hostname,
        path: '/',
        httpOnly: true,
        sameSite: 'Lax',
      },
    ]);
    const page = withSseSafeNavigation(await ctx.newPage());
    await use(page);
    await ctx.close().catch(() => undefined);
  },
});

export { expect } from '@playwright/test';
export type { APIRequestContext };
