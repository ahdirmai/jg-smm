/**
 * Auth outcome callback contract (P1-11 / P1-12). Three invariants: the outcome
 * maps to the wire AuthStatus enum, a 4xx stops retries, a 5xx retries up to
 * the budget — and none of it throws, so a dead account never stalls the
 * control loop.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { createAuthCallback } from '../transport/auth-callback.js';
import type { LoginResult } from '../core/auth.js';
import type { Logger } from '../core/logger.js';
import type { WorkerConfig } from '../types.js';

const cfg = {
  apiUrl: 'http://api:8080',
  redisUrl: 'redis://redis:6379',
  workerId: 'w1',
} as unknown as WorkerConfig;

const silentLogger: Logger = {
  child: () => silentLogger,
  debug: () => undefined,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
};

function recordingFetch(calls: Array<{ url: string; body: unknown }>, status: number) {
  return async (url: string, init: RequestInit) => {
    calls.push({ url, body: init.body ? JSON.parse(String(init.body)) : null });
    return new Response(null, { status });
  };
}

test('a verified login posts AUTHENTICATED with the session handle', async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  const cb = createAuthCallback(cfg, silentLogger, {
    fetchImpl: recordingFetch(calls, 200) as unknown as typeof fetch,
  });

  const result: LoginResult = { outcome: 'verified', handle: 'sess-123' };
  await cb.post('a1', result);

  assert.equal(calls.length, 1);
  assert.equal(calls[0]?.url, 'http://api:8080/internal/account-callback');
  assert.deepEqual(calls[0]?.body, { accountId: 'a1', authStatus: 'AUTHENTICATED', handle: 'sess-123' });
});

test('needs_input posts NEEDS_INPUT so the dashboard can prompt for a code', async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  const cb = createAuthCallback(cfg, silentLogger, {
    fetchImpl: recordingFetch(calls, 200) as unknown as typeof fetch,
  });

  await cb.post('a1', { outcome: 'needs_input', screenshot: 'a1.png' });

  // The screenshot is not sent: the operator reads it from the worker's live
  // view, not from this callback.
  assert.deepEqual(calls[0]?.body, { accountId: 'a1', authStatus: 'NEEDS_INPUT' });
});

test('a failed login reports FAILED with a reason', async () => {
  const calls: Array<{ url: string; body: unknown }> = [];
  const cb = createAuthCallback(cfg, silentLogger, {
    fetchImpl: recordingFetch(calls, 200) as unknown as typeof fetch,
  });

  await cb.post('a1', { outcome: 'failed' });

  assert.deepEqual(calls[0]?.body, {
    accountId: 'a1',
    authStatus: 'FAILED',
    error: 'login did not complete',
  });
});

test('a 4xx is permanent: one attempt, no throw', async () => {
  let hits = 0;
  const fetch = async () => {
    hits += 1;
    return new Response(null, { status: 404 });
  };
  const cb = createAuthCallback(cfg, silentLogger, {
    maxAttempts: 3,
    fetchImpl: fetch as unknown as typeof fetch,
  });

  await cb.post('gone', { outcome: 'verified' });
  assert.equal(hits, 1);
});

test('a 5xx retries up to maxAttempts, then stops without throwing', async () => {
  let hits = 0;
  const fetch = async () => {
    hits += 1;
    return new Response(null, { status: 503 });
  };
  const cb = createAuthCallback(cfg, silentLogger, {
    maxAttempts: 3,
    fetchImpl: fetch as unknown as typeof fetch,
  });

  await cb.post('a1', { outcome: 'verified' });
  assert.equal(hits, 3);
});
