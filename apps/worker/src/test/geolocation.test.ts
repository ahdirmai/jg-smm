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

import type { Browser, BrowserContext } from 'playwright';

import { newAccountContext, pinGeolocation } from '../core/browser.js';
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

/**
 * Every context the worker opens must be pinned, not just the action one. The
 * bug this guards: login and the manual live-view browser each called
 * newContext directly, so the operator checking "where is this container?" in
 * noVNC saw the datacentre's position while the actions ran from Jakarta.
 */
function recordingContext() {
  const calls: Array<{ lat: number; lng: number }> = [];
  let granted = 0;
  const ctx = {
    setGeolocation: async (p: { latitude: number; longitude: number }) => {
      calls.push({ lat: p.latitude, lng: p.longitude });
    },
    grantPermissions: async () => {
      granted += 1;
    },
  } as unknown as BrowserContext;
  return { ctx, calls, granted: () => granted };
}

test('pinGeolocation sets the frozen point and grants the permission', async () => {
  const { ctx, calls, granted } = recordingContext();
  await pinGeolocation(ctx, { latitude: -6.05949, longitude: 106.7524 });
  assert.deepEqual(calls, [{ lat: -6.05949, lng: 106.7524 }]);
  assert.equal(granted(), 1, 'without the grant a geolocation read stays blocked');
});

test('pinGeolocation is a no-op when the worker has no location', async () => {
  const { ctx, calls, granted } = recordingContext();
  await pinGeolocation(ctx, null);
  await pinGeolocation(ctx, undefined);
  assert.deepEqual(calls, [], 'an unpinned context must keep the real position');
  assert.equal(granted(), 0);
});

test('newAccountContext pins the action context too', async () => {
  const { ctx, calls } = recordingContext();
  const browser = {
    newContext: async () => ctx,
  } as unknown as Browser;
  await newAccountContext(browser, 'instagram', undefined, {
    latitude: -6.2,
    longitude: 106.8,
  });
  assert.deepEqual(calls, [{ lat: -6.2, lng: 106.8 }]);
});
