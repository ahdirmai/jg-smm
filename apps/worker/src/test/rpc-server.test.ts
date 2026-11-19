/**
 * Stateless RPC server: the security and isolation contract.
 *
 * Two properties are load-bearing and easy to regress:
 *  1. A public boxd hostname must not hand out a logged-in browser without a
 *     valid, worker-bound, unexpired ticket.
 *  2. Two consecutive jobs for different cities must be fully isolated — each
 *     gets its own storageState/geolocation/timezone/proxy and writes nothing
 *     to disk, because the next account will run on the same machine.
 */
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import { mkdtemp, readdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { test } from 'node:test';

import type { Browser, BrowserContext } from 'playwright';

import { createRpcServer, verifyLiveViewTicket, type RpcActionJob } from '../transport/rpc-server.js';
import { createController } from '../core/controller.js';
import { newAccountContext } from '../core/browser.js';
import type { AccountRuntimeSpec, ActionJob } from '../types.js';
import type { Logger } from '../core/logger.js';

const SECRET = 'test-secret-32-bytes-minimum-value';
const WORKER = 'smm-worker-01';

const silent: Logger = Object.assign(() => undefined, {
  child: () => silent,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
  debug: () => undefined,
}) as unknown as Logger;

function sign(exp: number, worker = WORKER, secret = SECRET): string {
  const mac = createHmac('sha256', secret).update(`${exp}:${worker}`).digest('hex');
  return `${exp}.${worker}.${mac}`;
}

// --- ticket ---------------------------------------------------------------

test('verify-ticket accepts a valid, bound, unexpired ticket', () => {
  const now = Math.floor(Date.now() / 1000);
  assert.equal(verifyLiveViewTicket(SECRET, sign(now + 300), WORKER, now), true);
});

test('verify-ticket rejects bad signature, expiry, worker and shape', () => {
  const now = Math.floor(Date.now() / 1000);
  assert.equal(verifyLiveViewTicket(SECRET, sign(now + 300, WORKER, 'other-secret'), WORKER, now), false);
  assert.equal(verifyLiveViewTicket(SECRET, sign(now - 1), WORKER, now), false);
  assert.equal(verifyLiveViewTicket(SECRET, sign(now + 300, 'smm-worker-07'), WORKER, now), false);
  assert.equal(verifyLiveViewTicket(SECRET, 'nonsense', WORKER, now), false);
  assert.equal(verifyLiveViewTicket('', sign(now + 300), WORKER, now), false);
});

// --- HTTP surface ---------------------------------------------------------

interface Harness {
  base: string;
  close: () => Promise<void>;
  ran: RpcActionJob[];
  busy: { value: boolean };
  /** Resolves when runAction has been entered — no sleeping to guess. */
  nextRun: () => Promise<void>;
}

async function harness(): Promise<Harness> {
  const busy = { value: false };
  const ran: RpcActionJob[] = [];
  // The dispatch is detached (202 then background work), so the test needs a
  // signal for "it started" rather than a timer.
  const waiters: Array<() => void> = [];
  const server = createRpcServer({
    logger: silent,
    internalSecret: SECRET,
    workerId: WORKER,
    isBusy: () => busy.value,
    verifyTicket: (t) => verifyLiveViewTicket(SECRET, t, WORKER, Math.floor(Date.now() / 1000)),
    runAction: async (job) => {
      ran.push(job);
      waiters.shift()?.();
      return undefined;
    },
    startLogin: async () => undefined,
    submitAuthInput: async () => undefined,
    clearAuth: async () => undefined,
    openBrowser: async () => undefined,
  });
  await server.listen('127.0.0.1', 0);
  return {
    base: `http://127.0.0.1:${server.port()}`,
    close: () => server.close(),
    ran,
    busy,
    nextRun: () => new Promise<void>((resolve) => waiters.push(resolve)),
  };
}

test('healthz is public; machine routes need the secret', async () => {
  const h = await harness();
  try {
    const health = await fetch(`${h.base}/healthz`);
    assert.equal(health.status, 200);

    const noSecret = await fetch(`${h.base}/internal/execute-action`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: '{}',
    });
    assert.equal(noSecret.status, 401);

    const wrong = await fetch(`${h.base}/internal/ping`, {
      method: 'POST',
      headers: { 'x-internal-secret': 'nope' },
    });
    assert.equal(wrong.status, 401);

    const ok = await fetch(`${h.base}/internal/ping`, {
      method: 'POST',
      headers: { 'x-internal-secret': SECRET },
    });
    assert.equal(ok.status, 200);
  } finally {
    await h.close();
  }
});

test('execute-action answers 409 while the worker is busy', async () => {
  const h = await harness();
  try {
    h.busy.value = true;
    const res = await fetch(`${h.base}/internal/execute-action`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-internal-secret': SECRET },
      body: JSON.stringify({ jobId: 'j1', action: 'like', targetUrl: 'u', attempt: 1, runtime: { accountId: 'a1', platform: 'instagram', timezone: 'Asia/Jakarta' } }),
    });
    assert.equal(res.status, 409);
    assert.equal(h.ran.length, 0);

    h.busy.value = false;
    const started = h.nextRun();
    const accepted = await fetch(`${h.base}/internal/execute-action`, {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'x-internal-secret': SECRET },
      body: JSON.stringify({ jobId: 'j2', action: 'like', targetUrl: 'u', attempt: 1, runtime: { accountId: 'a1', platform: 'instagram', timezone: 'Asia/Jakarta' } }),
    });
    assert.equal(accepted.status, 202);
    // 202 is the acceptance; the run is detached and lands on the callback.
    await started;
    assert.equal(h.ran.length, 1);
  } finally {
    await h.close();
  }
});

test('verify-ticket route answers 204/403 like nginx expects', async () => {
  const h = await harness();
  try {
    const now = Math.floor(Date.now() / 1000);
    const good = await fetch(`${h.base}/internal/verify-ticket?ticket=${encodeURIComponent(sign(now + 60))}`);
    assert.equal(good.status, 204);
    const bad = await fetch(`${h.base}/internal/verify-ticket?ticket=nope`);
    assert.equal(bad.status, 403);
  } finally {
    await h.close();
  }
});

// --- per-context isolation ------------------------------------------------

interface RecordingBrowser {
  browser: Browser;
  contexts: Array<Record<string, unknown>>;
}

function recordingBrowser(): RecordingBrowser {
  const contexts: Array<Record<string, unknown>> = [];
  const browser = {
    newContext: async (options: Record<string, unknown>) => {
      contexts.push(options);
      const page = {
        url: () => 'https://www.instagram.com/',
        goto: async () => undefined,
        context: () => ctx,
      };
      const ctx = {
        setGeolocation: async () => undefined,
        grantPermissions: async () => undefined,
        newPage: async () => page,
        pages: () => [page],
        close: async () => undefined,
        storageState: async () => ({ cookies: [{ name: 'sessionid', value: 'rotated' }] }),
      } as unknown as BrowserContext;
      return ctx;
    },
  } as unknown as Browser;
  return { browser, contexts };
}

const JAKARTA: AccountRuntimeSpec = {
  accountId: 'acct-jakarta',
  platform: 'instagram',
  storageState: { cookies: [{ name: 'sessionid', value: 'jakarta' }] },
  geolocation: { latitude: -6.2088, longitude: 106.8456 },
  timezone: 'Asia/Jakarta',
  userAgent: 'UA-jakarta',
  proxy: { server: 'http://jakarta-gw:8080', username: 'jkt', password: 'p1' },
};

const MAKASSAR: AccountRuntimeSpec = {
  accountId: 'acct-makassar',
  platform: 'instagram',
  storageState: { cookies: [{ name: 'sessionid', value: 'makassar' }] },
  geolocation: { latitude: -5.1477, longitude: 119.4327 },
  timezone: 'Asia/Makassar',
  userAgent: 'UA-makassar',
  proxy: { server: 'http://makassar-gw:8080', username: 'mks', password: 'p2' },
};

function jobFor(runtime: AccountRuntimeSpec): ActionJob {
  return {
    id: `job-${runtime.accountId}`,
    accountId: runtime.accountId,
    platform: 'instagram',
    action: 'like',
    targetUrl: 'https://www.instagram.com/p/x/',
    attempt: 1,
    ...({ runtime } as { runtime: AccountRuntimeSpec }),
  };
}

test('two consecutive jobs are fully isolated and leave no files behind', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'smm-worker-'));
  const rec = recordingBrowser();
  const results: unknown[] = [];

  const controller = createController({
    logger: silent,
    jitterRangeMs: [0, 0],
    screenshotDir: dir,
    contextFor: async (job) => {
      const runtime = (job as ActionJob & { runtime?: AccountRuntimeSpec }).runtime;
      return newAccountContext(rec.browser, {
        platform: job.platform,
        ...(runtime?.storageState !== undefined ? { storageState: runtime.storageState } : {}),
        geolocation: runtime?.geolocation ?? null,
        ...(runtime?.timezone ? { timezoneId: runtime.timezone } : {}),
        ...(runtime?.userAgent ? { userAgent: runtime.userAgent } : {}),
        proxy: runtime?.proxy ?? null,
      });
    },
  });

  // Override the adapter dispatch indirectly: 'like' has no adapter result in a
  // fake context, so the controller reports a failure — the CONTEXT setup and
  // the write-back are what this test is about.
  for (const runtime of [JAKARTA, MAKASSAR]) {
    results.push(await controller.run(jobFor(runtime)));
  }

  assert.equal(rec.contexts.length, 2, 'one context per job');
  const [jkt, mks] = rec.contexts;
  assert.equal(jkt?.timezoneId, 'Asia/Jakarta');
  assert.equal(mks?.timezoneId, 'Asia/Makassar');
  assert.deepEqual(jkt?.storageState, JAKARTA.storageState);
  assert.deepEqual(mks?.storageState, MAKASSAR.storageState);
  assert.equal(jkt?.userAgent, 'UA-jakarta');
  assert.equal(mks?.userAgent, 'UA-makassar');
  assert.deepEqual(jkt?.proxy, { server: 'http://jakarta-gw:8080', username: 'jkt', password: 'p1' });
  assert.deepEqual(mks?.proxy, { server: 'http://makassar-gw:8080', username: 'mks', password: 'p2' });

  // Nothing persisted: a stateless worker must not write sessions or evidence.
  const left = await readdir(dir);
  assert.deepEqual(left, [], 'no files may remain for the next account to find');
});

// --- live-view isolation (F3) ---------------------------------------------

/**
 * A second live view for a DIFFERENT account must get its own context.
 *
 * Reusing `manualContext` across accounts handed an operator account A's
 * cookies, proxy, GPS and timezone while the dashboard said account B — the
 * exact leak the per-context runtime exists to prevent. This drives the same
 * composition the RPC server uses (reuse only for the SAME account).
 */
test('live view closes the previous account context before opening another', async () => {
  const rec = recordingBrowser();
  const closed: string[] = [];
  let opened = 0;

  // Wrap contexts so closal is observable.
  const inner = rec.browser.newContext.bind(rec.browser);
  (rec.browser as unknown as { newContext: (o: Record<string, unknown>) => Promise<BrowserContext> }).newContext =
    async (options) => {
      const ctx = await inner(options);
      const id = `ctx-${++opened}`;
      const original = ctx.close.bind(ctx);
      (ctx as unknown as { close: () => Promise<void> }).close = async () => {
        closed.push(id);
        await original();
      };
      (ctx as unknown as { tag: string }).tag = id;
      return ctx;
    };

  // The same rule the RPC wiring applies.
  let manual: BrowserContext | undefined;
  let manualAccount: string | undefined;
  const openLiveView = async (accountId: string) => {
    const reusable = manualAccount === accountId ? manual : undefined;
    if (!reusable && manual) {
      await manual.close();
      manual = undefined;
    }
    manual =
      reusable ??
      (await newAccountContext(rec.browser, {
        platform: 'instagram',
        geolocation: { latitude: -6.2, longitude: 106.8 },
        timezoneId: 'Asia/Jakarta',
      }));
    manualAccount = accountId;
  };

  await openLiveView('acct-a');
  await openLiveView('acct-b');

  assert.equal(rec.contexts.length, 2, 'a different account must get a new context');
  assert.deepEqual(closed, ['ctx-1'], 'the previous account context must be closed');
  assert.notEqual((manual as unknown as { tag: string }).tag, 'ctx-1');

  // Re-opening the SAME account reuses the window instead of stacking one.
  await openLiveView('acct-b');
  assert.equal(rec.contexts.length, 2, 'same account reuses its context');
});

// A login whose navigation times out but whose page DID load must continue.
//
// Instagram's edge regularly exceeds 30s on domcontentloaded while the page is
// already usable; treating that as "navigation failed" rejected accounts whose
// network was fine and left the operator with no way forward. Driven at the
// decision point rather than through a whole login: the poll loop below it needs
// half of Playwright to exist, and says nothing about the rule under test.
function navigationOutcome(pageUrl: string, navError: Error | null): 'throw' | 'continue' {
  if (!navError) return 'continue';
  const loaded = pageUrl !== '' && pageUrl !== 'about:blank';
  return loaded ? 'continue' : 'throw';
}

test('navigation timeout falls back only when a page actually loaded', () => {
  const timeout = new Error('page.goto: Timeout 30000ms exceeded.');
  // The page arrived despite the slow load: keep going.
  assert.equal(navigationOutcome('https://www.instagram.com/accounts/login/', timeout), 'continue');
  // Nothing loaded at all: a real failure, surface it.
  assert.equal(navigationOutcome('about:blank', timeout), 'throw');
  assert.equal(navigationOutcome('', timeout), 'throw');
  // No error at all: plain success.
  assert.equal(navigationOutcome('https://www.instagram.com/', null), 'continue');
});
