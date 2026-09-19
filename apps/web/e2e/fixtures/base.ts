import { test as base, type Page, type APIRequestContext } from '@playwright/test';

import { ApiClient } from './api.js';

/**
 * The suite's fixtures. `api` is a Node-side client seeded from .env, so a spec
 * can create a container via the API and then assert the UI renders it — or the
 * reverse. `apiUrl` is the base the browser-facing requests hit.
 *
 * Login happens once for the whole run, not per test: the API caps login at 10
 * attempts per source IP per minute (service/auth.go), so a fixture that logs
 * in for every spec would rate-limit itself partway through the suite. The
 * cookie is shared by the Node client and the browser contexts.
 */

const API_URL = process.env.E2E_API_URL ?? 'http://localhost:24080';
const OWNER_EMAIL = process.env.E2E_OWNER_EMAIL ?? 'owner@smm.local';
const OWNER_PASSWORD = process.env.E2E_OWNER_PASSWORD ?? 'changeme-changeme';

let cookiePromise: Promise<string> | null = null;

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * One login for the whole run. The result is memoised, but — critically — a
 * FAILED login is not: a single transient 429 must not poison every downstream
 * test with a cached rejection. The API caps login at 10/min per source IP
 * (service/auth.go); when a prior run left the window saturated the first
 * attempt can 429, so back off and retry across the ~60s window rather than
 * failing the whole suite. On final failure the cache is cleared so the next
 * test re-attempts instead of replaying the rejection.
 */
function ownerCookie(): Promise<string> {
  if (!cookiePromise) {
    cookiePromise = (async () => {
      const MAX_ATTEMPTS = 5;
      const BACKOFF_MS = 15_000; // 5 × 15s spans the 1-minute rate-limit window
      let lastStatus = 0;
      for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
        const res = await fetch(`${API_URL}/api/auth/login`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email: OWNER_EMAIL, password: OWNER_PASSWORD }),
        });
        if (res.ok) {
          const setCookie = res.headers.get('set-cookie') ?? '';
          const access = setCookie
            .split(',')
            .map((c) => c.trim())
            .find((c) => c.startsWith('smm_at='));
          if (!access) throw new Error('fixture login: no smm_at cookie returned');
          return access.split('=')[1]?.split(';')[0] ?? '';
        }
        lastStatus = res.status;
        // Only 429 is worth retrying: a 401 means bad creds, retrying won't help.
        if (res.status !== 429 || attempt === MAX_ATTEMPTS) break;
        await sleep(BACKOFF_MS);
      }
      cookiePromise = null; // do not cache the failure — let the next test retry
      throw new Error(`fixture login failed: ${lastStatus}`);
    })();
  }
  return cookiePromise;
}

/**
 * Every dashboard page subscribes to the SSE stream for its lifetime, so the
 * `load` event never fires — the default `waitUntil` hangs until timeout.
 * `domcontentloaded` is the page-ready signal; the specs' own assertions wait
 * for the elements they actually care about.
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
    // Adopt the one shared cookie rather than spending another login attempt.
    const client = new ApiClient(API_URL);
    await client.setCookie(await ownerCookie());
    await use(client);
  },
  authedPage: async ({ browser }, use) => {
    // Plant the shared cookie in a fresh context rather than driving the login
    // form in-band: the cookie is the same identity, and the form costs a page
    // load per spec on top of the rate-limit budget.
    const value = await ownerCookie();
    const ctx = await browser.newContext();
    await ctx.addCookies([
      {
        name: 'smm_at',
        value,
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
