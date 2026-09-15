/**
 * P3-11 callback contract tests. The transport's job is three invariants: the
 * idempotency key is "<jobId>:<attempt>", a 4xx stops retries, and a 5xx
 * retries up to the budget. A stubbed fetch proves all three without a server.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { createCallback } from '../transport/callback.js';
import type { WorkerConfig } from '../types.js';
import type { Logger } from '../core/logger.js';

const cfg: WorkerConfig = {
  workerId: 'worker-1',
  apiUrl: 'http://api:8080',
  redisUrl: 'redis://redis:6379',
  platforms: ['instagram'],
  dryRun: true,
  display: ':99',
  heartbeatIntervalSec: 30,
};

const silentLogger: Logger = {
  child: () => silentLogger,
  debug: () => undefined,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
};

function recordingFetch(calls: { url: string; body: string }[], status: number) {
  return async (url: string, init: RequestInit) => {
    calls.push({ url, body: String(init.body) });
    return new Response(null, { status });
  };
}

test('callback body carries the attemptId idempotency key "<jobId>:<attempt>"', async () => {
  const calls: { url: string; body: string }[] = [];
  const cb = createCallback(cfg, silentLogger, {
    maxAttempts: 1,
    fetchImpl: recordingFetch(calls, 200) as unknown as typeof fetch,
  });

  await cb.post({
    jobId: 'job-42',
    accountId: 'acct-1',
    workerId: '',
    attempt: 3,
    status: 'success',
    renderedText: 'nice post',
    durationMs: 1200,
  });

  assert.equal(calls.length, 1);
  const first = calls[0];
  if (!first) throw new Error('expected one callback call');
  const payload = JSON.parse(first.body);
  assert.equal(payload.attemptId, 'job-42:3', 'the API resolves job+attempt from this key');
  assert.equal(payload.jobId, 'job-42', 'jobId stays for operator logs');
  assert.equal(payload.attempt, 3);
  assert.equal(payload.workerId, 'worker-1');
});

test('a 4xx is permanent: the callback stops and does not retry', async () => {
  const calls: { url: string; body: string }[] = [];
  const cb = createCallback(cfg, silentLogger, {
    maxAttempts: 3,
    fetchImpl: recordingFetch(calls, 400) as unknown as typeof fetch,
  });

  await cb.post({
    jobId: 'job-bad',
    accountId: 'acct-1',
    workerId: '',
    attempt: 1,
    status: 'failed',
    error: 'malformed',
  });

  assert.equal(calls.length, 1, 'a 4xx must not be retried');
});

test('a 5xx retries up to the budget', async () => {
  const calls: { url: string; body: string }[] = [];
  const cb = createCallback(cfg, silentLogger, {
    maxAttempts: 3,
    fetchImpl: recordingFetch(calls, 503) as unknown as typeof fetch,
  });

  await cb.post({
    jobId: 'job-flaky',
    accountId: 'acct-1',
    workerId: '',
    attempt: 1,
    status: 'failed',
    error: 'server down',
  });

  assert.equal(calls.length, 3, 'a 5xx must exhaust the retry budget');
  // Every retry carries the same idempotency key: the API's UNIQUE
  // (action_job_id, attempt) makes replays converge on one verdict row.
  for (const c of calls) {
    assert.equal(JSON.parse(c.body).attemptId, 'job-flaky:1');
  }
});

test('a network failure retries like a 5xx and never throws', async () => {
  let attempts = 0;
  const cb = createCallback(cfg, silentLogger, {
    maxAttempts: 2,
    fetchImpl: (async () => {
      attempts += 1;
      throw new Error('ECONNREFUSED');
    }) as unknown as typeof fetch,
  });

  // Must reject-free: a worker never dies over a callback it could not send.
  await cb.post({
    jobId: 'job-net',
    accountId: 'acct-1',
    workerId: '',
    attempt: 1,
    status: 'failed',
    error: 'boom',
  });

  assert.equal(attempts, 2);
});
