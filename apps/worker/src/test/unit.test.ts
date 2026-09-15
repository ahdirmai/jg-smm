import assert from 'node:assert/strict';
import { test } from 'node:test';

import { loadConfig, parsePlatforms, parsePositiveInt } from '../core/config.js';
import { computeJitter } from '../core/controller.js';
import { getAdapter, supportedPlatforms } from '../platforms/registry.js';

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

test('registry resolves MVP adapters and reports support', () => {
  assert.equal(getAdapter('instagram')?.platform, 'instagram');
  assert.equal(getAdapter('threads')?.platform, 'threads');
  assert.equal(getAdapter('facebook'), undefined);
  assert.deepEqual(supportedPlatforms().sort(), ['instagram', 'threads']);
});
