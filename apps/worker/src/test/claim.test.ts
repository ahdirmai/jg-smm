/**
 * Claim handshake tests. The contract is three invariants: a 200 binds the
 * worker to the row's real id, a 404 (nothing free) leaves it on its boot id,
 * and a network failure does the same — nothing here is worth killing the
 * container over.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { claimRow } from '../core/claim.js';
import type { Logger } from '../core/logger.js';

const silentLogger: Logger = {
  child: () => silentLogger,
  debug: () => undefined,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
};

function jsonFetch(status: number, body: unknown) {
  return async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    });
}

function failingFetch() {
  return async () => {
    throw new Error('connection refused');
  };
}

test('claim 200 adopts the row id the API returned', async () => {
  const got = await claimRow(
    { containerId: 'worker-smm-worker-1' },
    {
      apiUrl: 'http://api:8080',
      logger: silentLogger,
      fetchImpl: jsonFetch(200, {
        workerId: '6f5c1f2e-2ba0-4b1d-9d34-7f8a9b0c1d2e',
        name: 'test',
        region: 'ID',
        location: 'Jakarta',
        latitude: -6.2,
        longitude: 106.8,
      }) as typeof fetch,
    },
  );
  assert.equal(got.workerId, '6f5c1f2e-2ba0-4b1d-9d34-7f8a9b0c1d2e');
  assert.equal(got.name, 'test');
  assert.equal(got.location, 'Jakarta');
});

test('claim 404 (no free row) falls back to the boot id, not a crash', async () => {
  const got = await claimRow(
    { containerId: 'worker-smm-worker-1' },
    {
      apiUrl: 'http://api:8080',
      logger: silentLogger,
      fetchImpl: jsonFetch(404, { error: { code: 'not_found' } }) as typeof fetch,
    },
  );
  assert.equal(got.workerId, 'worker-smm-worker-1');
  assert.equal(got.name, 'worker-smm-worker-1');
});

test('claim network failure falls back to the boot id', async () => {
  const got = await claimRow(
    { containerId: 'worker-smm-worker-1' },
    {
      apiUrl: 'http://api:8080',
      logger: silentLogger,
      fetchImpl: failingFetch() as typeof fetch,
    },
  );
  assert.equal(got.workerId, 'worker-smm-worker-1');
});

test('claim 200 without a workerId is treated as unassigned', async () => {
  const got = await claimRow(
    { containerId: 'worker-smm-worker-1' },
    {
      apiUrl: 'http://api:8080',
      logger: silentLogger,
      fetchImpl: jsonFetch(200, { ok: true }) as typeof fetch,
    },
  );
  assert.equal(got.workerId, 'worker-smm-worker-1');
});
