import assert from 'node:assert/strict';
import { test } from 'node:test';

import { loadConfig, parseNovncUrl, parsePlatforms, parsePositiveInt } from '../core/config.js';
import { computeJitter } from '../core/controller.js';
import {
  automatedPlatforms,
  getAdapter,
  isFullyAutomated,
  supportedPlatforms,
} from '../platforms/registry.js';

test('parsePlatforms filters unknown values and falls back on empty', () => {
  assert.deepEqual(parsePlatforms('instagram,threads', []), ['instagram', 'threads']);
  assert.deepEqual(parsePlatforms('instagram,nope', []), ['instagram']);
  assert.deepEqual(parsePlatforms('', ['threads']), ['threads']);
  assert.deepEqual(parsePlatforms(undefined, ['threads']), ['threads']);
  assert.deepEqual(parsePlatforms('nope,nada', ['instagram']), ['instagram']);
});

test('parsePositiveInt accepts positives, rejects junk', () => {
  assert.equal(parsePositiveInt('30', 10), 30);
  assert.equal(parsePositiveInt('0', 10), 10);
  assert.equal(parsePositiveInt('-5', 10), 10);
  assert.equal(parsePositiveInt('abc', 10), 10);
  assert.equal(parsePositiveInt(undefined, 10), 10);
});

test('loadConfig applies defaults and trims the API url', () => {
  const cfg = loadConfig({
    WORKER_ID: ' worker-1 ',
    API_URL: 'http://api:8080/',
    ACTION_DRY_RUN: 'false',
  });
  assert.equal(cfg.workerId, 'worker-1');
  assert.equal(cfg.apiUrl, 'http://api:8080');
  assert.equal(cfg.dryRun, false);
  assert.equal(cfg.heartbeatIntervalSec, 30);
  assert.deepEqual(cfg.platforms, ['instagram', 'threads']);
});

test('parseNovncUrl resolves the live view URL or null when unpublished', () => {
  // Nothing configured: the live view is not published, so the value is null
  // and the dashboard hides the modal instead of linking to a dead URL.
  assert.equal(parseNovncUrl({}), null);
  assert.equal(parseNovncUrl({ NOVNC_BASE_URL: '' }), null);

  // A full URL wins and is trailing-slash normalised.
  assert.equal(parseNovncUrl({ NOVNC_URL: 'http://localhost:6080/' }), 'http://localhost:6080');

  // Base + port compose; an invalid port falls back to 6080.
  assert.equal(
    parseNovncUrl({ NOVNC_BASE_URL: 'http://worker-1/', NOVNC_PORT: '16080' }),
    'http://worker-1:16080',
  );
  assert.equal(
    parseNovncUrl({ NOVNC_BASE_URL: 'http://worker-1', NOVNC_PORT: 'junk' }),
    'http://worker-1:6080',
  );
});

test('computeJitter stays within the range and is injectable', () => {
  assert.equal(
    computeJitter([30_000, 90_000], () => 0),
    30_000,
  );
  assert.equal(
    computeJitter([30_000, 90_000], () => 1),
    90_000,
  );
  assert.equal(
    computeJitter([0, 100], () => 0.5),
    50,
  );
});

test('loadConfig applies defaults and trims the API url', () => {
  const cfg = loadConfig({
    WORKER_ID: ' worker-1 ',
    API_URL: 'http://api:8080/',
    ACTION_DRY_RUN: 'false',
  });
  assert.equal(cfg.workerId, 'worker-1');
  assert.equal(cfg.apiUrl, 'http://api:8080');
  assert.equal(cfg.dryRun, false);
  assert.equal(cfg.heartbeatIntervalSec, 30);
  assert.deepEqual(cfg.platforms, ['instagram', 'threads']);
});

test('registry resolves an adapter for all seven platforms', () => {
  assert.equal(getAdapter('instagram')?.platform, 'instagram');
  assert.equal(getAdapter('threads')?.platform, 'threads');
  // The five non-MVP platforms are now registered (OTP-capable stubs), not
  // undefined — so the operator login/2FA flow works uniformly.
  assert.equal(getAdapter('facebook')?.platform, 'facebook');
  assert.equal(getAdapter('linkedin')?.platform, 'linkedin');
  assert.equal(getAdapter('x')?.platform, 'x');
  assert.equal(getAdapter('youtube')?.platform, 'youtube');
  assert.equal(getAdapter('tiktok')?.platform, 'tiktok');
  assert.deepEqual(
    supportedPlatforms().sort(),
    ['facebook', 'instagram', 'linkedin', 'threads', 'tiktok', 'x', 'youtube'],
  );
});

test('only Instagram and Threads are flagged fully automated; the rest are stubs', () => {
  assert.equal(isFullyAutomated('instagram'), true);
  assert.equal(isFullyAutomated('threads'), true);
  assert.equal(isFullyAutomated('tiktok'), false);
  assert.deepEqual(automatedPlatforms().sort(), ['instagram', 'threads']);
});

test('every adapter exposes the OTP-over-web contract', () => {
  for (const platform of supportedPlatforms()) {
    const adapter = getAdapter(platform);
    assert.ok(adapter, `${platform} has an adapter`);
    // A real login URL, at least one proof cookie, a 2FA selector, and the two
    // detect/submit methods — the surface the operator auth flow depends on.
    assert.match(adapter.loginUrl, /^https:\/\//, `${platform} has an https login URL`);
    assert.ok(adapter.sessionCookies.length > 0, `${platform} names proof cookies`);
    assert.ok(adapter.otpFieldSelector.length > 0, `${platform} pins a 2FA selector`);
    assert.equal(typeof adapter.detectAuthInput, 'function');
    assert.equal(typeof adapter.submitAuthInput, 'function');
  }
});

test('the five stub adapters report an honest not-implemented failure', async () => {
  const fakeCtx = {} as never;
  const fakeJob = { text: 'hi' } as never;
  for (const platform of ['facebook', 'linkedin', 'x', 'youtube', 'tiktok'] as const) {
    const adapter = getAdapter(platform);
    assert.ok(adapter);
    const login = await adapter.login(fakeCtx, { username: 'u', password: 'p' });
    assert.equal(login.outcome, 'failed', `${platform} login is honestly failed`);
    const like = await adapter.like(fakeCtx, fakeJob);
    assert.equal(like.ok, false);
    assert.match(String(like.error), /not_implemented/, `${platform} like is labelled not_implemented`);
  }
});
