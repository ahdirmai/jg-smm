import assert from 'node:assert/strict';
import { after, test } from 'node:test';

import type { Browser, BrowserContext, Page } from 'playwright';

import { authContexts, runLogin } from '../core/auth.js';

// An OTP field used to be reported as `failed`, so the API released the lease
// and cleared the browser — the noVNC view died on the screen showing the code.
// A parked login now owns the display: it survives, and a second login refuses
// rather than closing it.

function fakeContext(): { ctx: BrowserContext; closes: () => number } {
  let closes = 0;
  const page = {
    url: () => 'https://www.instagram.com/accounts/login/',
    goto: async () => undefined,
  } as unknown as Page;
  const ctx = {
    pages: () => [page],
    newPage: async () => page,
    close: async () => {
      closes += 1;
    },
    cookies: async () => [],
  } as unknown as BrowserContext;
  return { ctx, closes: () => closes };
}

after(() => authContexts.clear());

test('a second login for a PARKED account never closes or replaces its context', async () => {
  const parked = fakeContext();
  authContexts.set('acc-parked', parked.ctx);
  let built = 0;

  const result = await runLogin('acc-parked', 'instagram', {
    browser: async () => {
      built += 1;
      return {} as Browser;
    },
  });

  assert.equal(result.outcome, 'needs_input');
  assert.match(result.reason ?? '', /already running/);
  assert.equal(built, 0, 'login built a browser despite a parked context');
  assert.equal(parked.closes(), 0, 'login closed the parked challenge');
  assert.equal(authContexts.get('acc-parked'), parked.ctx);
});
