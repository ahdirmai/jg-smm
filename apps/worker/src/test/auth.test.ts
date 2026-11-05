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
  url?: string;
  bodyText?: string;
  submitButton?: boolean;
  /** Thrown from `fill`, to prove a failed autofill does not kill the login. */
  fillError?: Error;
}): { browser: Browser; ctx: BrowserContext; storageState: unknown; fills: string[] } {
  let state: unknown = undefined;
  // `fills` records every fill/press the page receives, so a test can prove
  // the credential autofill actually reached the form.
  const fills: string[] = [];
  const page = {
    url: () => story.url ?? 'https://www.instagram.com/',
    goto: async () => undefined,
    getByRole: () => ({
      first() {
        return this;
      },
      count: async () => (story.submitButton ? 1 : 0),
      click: async () => {
        fills.push('click:Log in');
      },
    }),
    locator: (query: string) => {
      // The production form resolver requests only visible inputs.
      const selector = query.replace(/ >> visible=true$/, '');
      const loc = {
        innerText: async () => story.bodyText ?? '',
        count: async () => {
          // The login form's own fields are always present on the fake page;
          // only the OTP/verification field is gated by the story. This lets
          // firstPresent resolve the first candidate, matching the real form.
          const formFields = [
            'input[name="username"]',
            'input[name="email"]',
            'input[type="text"]',
            'input[name="password"]',
            'input[name="pass"]',
            'input[type="password"]',
          ];
          if (formFields.includes(selector)) return 1;
          return story.verificationField ? 1 : 0;
        },
        fill: async (value: string) => {
          if (story.fillError) throw story.fillError;
          fills.push(`${selector}=${value}`);
        },
        press: async (key: string) => {
          fills.push(`press:${key}`);
        },
        // firstPresent narrows a candidate selector with .first(); the fake
        // has at most one match, so it is its own first.
        first: () => loc,
      };
      return loc;
    },
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
    fills,
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

test('runLogin fills and submits the credential form when the API delivers the credentials', async () => {
  const { browser, fills } = fakeBrowser({
    cookies: [
      { name: 'sessionid', value: 'abc' },
      { name: 'ds_user_id', value: '123' },
    ],
    verificationField: false,
  });

  const result = await runLogin(
    'a-auto',
    'instagram',
    { browser: async () => browser, loginUrlFor: () => 'https://example.test/login' },
    { username: 'growthco', password: 's3cret' },
  );

  assert.equal(result.outcome, 'verified');
  // The platform adapter's autofill reached the form: username filled,
  // password filled, Enter pressed to submit.
  assert.deepEqual(fills, [
    'input[name="username"]=growthco',
    'input[name="password"]=s3cret',
    'press:Enter',
  ]);
});

test('login clicks the visible submit button instead of relying on Enter', async () => {
  const { browser, fills } = fakeBrowser({
    cookies: [
      { name: 'sessionid', value: 's' },
      { name: 'ds_user_id', value: '1' },
    ],
    verificationField: false,
    submitButton: true,
  });
  await runLogin(
    'explicit-submit',
    'instagram',
    { browser: async () => browser },
    { username: 'fixture', password: 'fixture' },
  );
  assert.ok(fills.includes('click:Log in'));
  assert.ok(!fills.includes('press:Enter'));
});

test('a failed credential autofill does not abort the login — the cookie poll still decides', async () => {
  const { browser } = fakeBrowser({
    cookies: [
      { name: 'sessionid', value: 'abc' },
      { name: 'ds_user_id', value: '123' },
    ],
    verificationField: false,
    fillError: new Error('challenge wall instead of the form'),
  });
  const logs: string[] = [];

  const result = await runLogin(
    'a-fill-fail',
    'instagram',
    {
      browser: async () => browser,
      loginUrlFor: () => 'https://example.test/login',
      log: (msg: string) => logs.push(msg),
    },
    { username: 'growthco', password: 's3cret' },
  );

  assert.equal(result.outcome, 'verified');
  assert.ok(
    logs.some((m) => m.includes('autofill failed')),
    'the fill failure must reach the container log',
  );
});

test('OTP fields fail and close the login browser instead of waiting for input', async () => {
  const { browser } = fakeBrowser({
    cookies: [],
    verificationField: true,
  });

  const result = await runLogin('a2', 'instagram', {
    browser: async () => browser,
    loginUrlFor: () => 'https://example.test/login',
  });

  assert.equal(result.outcome, 'failed');
  assert.equal(result.reason, 'otp_required: perlu OTP');
  assert.equal(authContexts.has('a2'), false);
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

for (const challenge of [
  { name: 'captcha', url: 'https://www.instagram.com/auth_platform/recaptcha/' },
  {
    name: 'checkpoint',
    url: 'https://www.instagram.com/accounts/login/',
    bodyText: 'Help us confirm it is you',
  },
]) {
  test(`${challenge.name} keeps the login browser open for manual completion`, async () => {
    const { browser, ctx } = fakeBrowser({
      cookies: [],
      verificationField: false,
      ...challenge,
    });
    const id = `manual-${challenge.name}`;
    try {
      const result = await runLogin(id, 'instagram', {
        browser: async () => browser,
        loginUrlFor: () => challenge.url,
      });
      assert.equal(result.outcome, 'needs_input');
      assert.equal(
        authContexts.get(id),
        ctx,
        'challenge browser must remain available through noVNC',
      );
    } finally {
      clearAuthContext(id);
    }
  });
}

test('wrong password still fails instead of waiting for manual challenge completion', async () => {
  const { browser } = fakeBrowser({
    cookies: [],
    verificationField: false,
    bodyText: 'The login information you entered is incorrect',
  });
  const result = await runLogin('wrong-pass', 'instagram', { browser: async () => browser });
  assert.equal(result.outcome, 'failed');
  assert.match(result.reason ?? '', /^wrong_password:/);
  assert.equal(authContexts.has('wrong-pass'), false);
});

test('manual challenge completion saves the session and reports a verified login', async () => {
  const story = {
    cookies: [] as Array<{ name: string; value: string }>,
    verificationField: false,
    url: 'https://www.instagram.com/auth_platform/recaptcha/',
  };
  const { browser } = fakeBrowser(story);
  const dir = await mkdtemp(join(tmpdir(), 'smm-manual-session-'));
  const updates: string[] = [];
  let persisted = 0;
  const deps = {
    browser: async () => browser,
    sessionDir: dir,
    onNeedsInput: async (_id: string, result: { outcome: string; reason?: string }) => {
      updates.push(result.outcome);
      assert.match(result.reason ?? '', /^captcha:/);
      assert.ok(authContexts.has('manual-complete'));
      // Simulate the operator solving CAPTCHA directly through noVNC.
      story.cookies = [
        { name: 'sessionid', value: 'manual-session' },
        { name: 'ds_user_id', value: '42' },
      ];
    },
    onSession: async () => {
      persisted++;
    },
  };
  const result = await runLogin('manual-complete', 'instagram', deps);
  assert.deepEqual(updates, ['needs_input']);
  assert.equal(result.outcome, 'verified');
  assert.equal(persisted, 1);
  assert.equal(authContexts.has('manual-complete'), false);
});

test('OTP does not request manual input or persist an unverified session', async () => {
  const story = {
    cookies: [] as Array<{ name: string; value: string }>,
    verificationField: true,
  };
  const { browser } = fakeBrowser(story);
  const dir = await mkdtemp(join(tmpdir(), 'smm-code-session-'));
  let persisted = 0;
  let requested = false;
  const deps = {
    browser: async () => browser,
    sessionDir: dir,
    onNeedsInput: async () => {
      requested = true;
      story.cookies = [
        { name: 'sessionid', value: 'code-session' },
        { name: 'ds_user_id', value: '42' },
      ];
    },
    onSession: async () => {
      persisted++;
    },
  };
  const result = await runLogin('manual-code', 'instagram', deps);
  assert.equal(result.outcome, 'failed');
  assert.equal(result.reason, 'otp_required: perlu OTP');
  assert.equal(requested, false);
  assert.equal(persisted, 0);
});

for (const challenge of [
  {
    url: 'https://www.instagram.com/auth_platform/codeentry/?apc=test',
    bodyText: 'Check your email',
  },
  { url: 'https://www.instagram.com/accounts/login/', bodyText: 'Enter the security code' },
  {
    url: 'https://www.instagram.com/challenge/action/test',
    bodyText: "We Detected An Unusual Login Attempt. To secure your account, we'll send you a security code to verify your identity. Phone: +62 ***-****-**45 Continue",
  },
]) {
  test(`OTP is skipped without a recognised input selector: ${challenge.bodyText}`, async () => {
    const { browser } = fakeBrowser({ cookies: [], verificationField: false, ...challenge });
    let clock = 0;
    const result = await runLogin('otp-no-selector', 'instagram', {
      browser: async () => browser,
      now: () => {
        const value = clock;
        clock += 100_000;
        return value;
      },
    });
    assert.equal(result.outcome, 'failed');
    assert.equal(result.reason, 'otp_required: perlu OTP');
    assert.equal(authContexts.has('otp-no-selector'), false);
  });
}

test('a CAPTCHA that proceeds to email OTP is skipped and closed', async () => {
  const story = {
    cookies: [],
    verificationField: false,
    url: 'https://www.instagram.com/auth_platform/recaptcha/',
  };
  const { browser } = fakeBrowser(story);
  let manual = false;
  let manualTicks = 0;
  const result = await runLogin('captcha-to-otp', 'instagram', {
    browser: async () => browser,
    now: () => (manual && ++manualTicks > 2 ? 600_001 : 0),
    onNeedsInput: async () => {
      story.url = 'https://www.instagram.com/auth_platform/codeentry/';
      manual = true;
    },
  });
  assert.equal(result.outcome, 'failed');
  assert.equal(result.reason, 'otp_required: perlu OTP');
  assert.equal(authContexts.has('captcha-to-otp'), false);
});

test('an interrupted old login does not close the replacement browser', async () => {
  const old = fakeBrowser({ cookies: [], verificationField: false });
  const replacement = fakeBrowser({ cookies: [], verificationField: true });
  old.ctx.pages()[0]!.goto = async () => {
    authContexts.set('restarted-login', replacement.ctx);
    throw new Error('old navigation interrupted');
  };
  try {
    await runLogin('restarted-login', 'instagram', { browser: async () => old.browser });
    assert.equal(authContexts.get('restarted-login'), replacement.ctx);
  } finally {
    clearAuthContext('restarted-login');
  }
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
    locator: () => ({
      count: async () => 0,
      fill: async () => undefined,
      press: async () => undefined,
    }),
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
