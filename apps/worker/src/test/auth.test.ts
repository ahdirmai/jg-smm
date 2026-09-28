import assert from 'node:assert/strict';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import type { Browser, BrowserContext } from 'playwright';

import { runLogin, submitAuthInput, clearAuthContext, authContexts } from '../core/auth.js';

/**
 * A browser/context pair that fakes the login timeline. The context hands out
 * cookies on demand and reports whether a verification field is present, so the
 * auth poll loop can be driven deterministically without a real driver.
 */
function fakeBrowser(story: {
  cookies: Array<{ name: string; value: string }>;
  verificationField: boolean;
}): { browser: Browser; ctx: BrowserContext; storageState: unknown } {
  let state: unknown = undefined;
  const page = {
    url: () => 'https://www.instagram.com/',
    goto: async () => undefined,
    locator: () => ({
      count: async () => (story.verificationField ? 1 : 0),
      fill: async () => undefined,
      press: async () => undefined,
    }),
  };
  const ctx = {
    cookies: async () => story.cookies,
    storageState: async () => {
      state ??= { cookies: story.cookies };
      return state;
    },
    pages: () => [page],
    newPage: async () => page,
    close: async () => undefined,
  } as unknown as BrowserContext;

  const browser = {
    newContext: async () => ctx,
    close: async () => undefined,
  } as unknown as Browser;

  return {
    browser,
    ctx,
    get storageState() {
      return state;
    },
  };
}

test('login is verified when the platform session cookie is present', async () => {
  const { browser } = fakeBrowser({
    cookies: [
      { name: 'sessionid', value: 'abc' },
      { name: 'ds_user_id', value: '123' },
    ],
    verificationField: false,
  });

  const result = await runLogin('a1', 'instagram', {
    browser: async () => browser,
    loginUrlFor: () => 'https://example.test/login',
  });

  assert.equal(result.outcome, 'verified');
  assert.equal(result.handle, 'abc');
  // The parked context is released on success.
  assert.equal(authContexts.has('a1'), false);
});

test('login reports needs_input when a verification field appears', async () => {
  const { browser } = fakeBrowser({
    cookies: [],
    verificationField: true,
  });

  const result = await runLogin('a2', 'instagram', {
    browser: async () => browser,
    loginUrlFor: () => 'https://example.test/login',
  });

  assert.equal(result.outcome, 'needs_input');
  assert.ok(result.screenshot, 'a screenshot basename must be reported to the operator');
  // The context stays parked so auth-input can address it.
  assert.equal(authContexts.has('a2'), true);
  clearAuthContext('a2');
});

test('submitAuthInput fails when no context is parked', async () => {
  const result = await submitAuthInput('ghost', '123456', 'instagram', {
    browser: async () => ({}) as Browser,
  });
  assert.equal(result.outcome, 'failed');
});

test('clearAuthContext is a no-op for an unknown account', () => {
  clearAuthContext('never-logged-in');
  assert.equal(authContexts.has('never-logged-in'), false);
});

/**
 * P2-06: a refresh is only autonomous because `runLogin` hydrates the session
 * the worker already has. Drop that and every scheduled refresh opens a bare
 * login page and parks on needs_input for a human — the feature fails silently
 * while looking green. Pin it by asserting the persisted state reaches
 * `newContext` and the context is therefore proven on the first poll.
 */
test('runLogin hydrates the persisted session so a refresh proves itself', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'smm-session-'));
  // Both of Instagram's proof cookies: `proven` requires every one the
  // adapter lists, and a partial set would poll forever.
  const state = {
    cookies: [
      { name: 'sessionid', value: 'persisted', domain: '.instagram.com', expires: 4_000_000_000 },
      { name: 'ds_user_id', value: '42', domain: '.instagram.com', expires: 4_000_000_000 },
    ],
  };
  await writeFile(join(dir, 'session-instagram.json'), JSON.stringify(state), 'utf8');

  const seen: unknown[] = [];
  const page = {
    url: () => 'https://www.instagram.com/',
    goto: async () => undefined,
    locator: () => ({ count: async () => 0, fill: async () => undefined, press: async () => undefined }),
  };
  const ctx = {
    cookies: async () => state.cookies,
    storageState: async () => state,
    pages: () => [page],
    newPage: async () => page,
    close: async () => undefined,
  } as unknown as BrowserContext;
  const browser = {
    newContext: async (opts: { storageState?: unknown }) => {
      seen.push(opts.storageState);
      return ctx;
    },
    close: async () => undefined,
  } as unknown as Browser;

  try {
    const result = await runLogin('a3', 'instagram', {
      browser: async () => browser,
      loginUrlFor: () => 'https://example.test/login',
      sessionDir: dir,
      now: () => 0,
    });
    // No operator, no typing: the cookie on disk is the proof.
    assert.equal(result.outcome, 'verified');
    assert.deepEqual(seen, [state], 'the persisted session must reach newContext');
    // ...and the expiry crosses the wire, which is what the API schedules on.
    assert.equal(result.expiresAt, 4_000_000_000_000);
  } finally {
    clearAuthContext('a3');
  }
});
