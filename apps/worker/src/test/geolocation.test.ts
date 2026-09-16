/**
 * geolocation.spec — the worker's frozen-GPS lookup.
 *
 * The module is best-effort by contract: a worker with no location must keep
 * running (it then reports the real device position), and an unreachable API
 * must not crash the action loop. Both are exercised here against a stubbed
 * fetch so no server is needed.
 */
import { strict as assert } from 'node:assert';
import { test } from 'node:test';

import { createGeolocation } from '../core/geolocation.js';

const silentLogger = {
  debug: () => undefined,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
  child: () => silentLogger,
};

function fetchReturning(status: number, body: unknown) {
  return async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { 'content-type': 'application/json' },
    }) as unknown as Response;
}

test('caches the frozen coordinate from the API', async () => {
  const geo = createGeolocation({
    apiUrl: 'http://api:8080/',
    workerId: 'w-1',
    logger: silentLogger,
    fetchImpl: fetchReturning(200, { latitude: -6.14, longitude: 106.79, location: 'Jakarta' }),
  });
  try {
    const point = await geo.get();
    assert.ok(point, 'expected a coordinate');
    assert.equal(point?.latitude, -6.14);
    assert.equal(point?.longitude, 106.79);
    assert.equal(point?.location, 'Jakarta');
  } finally {
    await geo[Symbol.asyncDispose]();
  }
});

test('a worker with no location resolves to null, not an error', async () => {
  const geo = createGeolocation({
    apiUrl: 'http://api:8080',
    workerId: 'ghost',
    logger: silentLogger,
    fetchImpl: fetchReturning(404, { error: 'not found' }),
  });
  try {
    assert.equal(await geo.get(), null);
  } finally {
    await geo[Symbol.asyncDispose]();
  }
});

test('an unreachable API degrades to null instead of throwing', async () => {
  const failing = async () => {
    throw new Error('ECONNREFUSED');
  };
  const geo = createGeolocation({
    apiUrl: 'http://api:8080',
    workerId: 'w-1',
    logger: silentLogger,
    fetchImpl: failing as unknown as typeof fetch,
  });
  try {
    assert.equal(await geo.get(), null);
  } finally {
    await geo[Symbol.asyncDispose]();
  }
});

test('a body missing coordinates degrades to null', async () => {
  const geo = createGeolocation({
    apiUrl: 'http://api:8080',
    workerId: 'w-1',
    logger: silentLogger,
    fetchImpl: fetchReturning(200, { latitude: 'nope' }),
  });
  try {
    assert.equal(await geo.get(), null);
  } finally {
    await geo[Symbol.asyncDispose]();
  }
});
