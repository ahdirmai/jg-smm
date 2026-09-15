/**
 * P3-07 sequential controller tests. The two acceptance criteria are structural
 * invariants, not behaviours, so the test drives the controller with a scripted
 * context factory and asserts the ordering:
 *
 *   AC "Concurrency=1": a job's context is created only after the previous job's
 *      context was closed — no overlap is possible.
 *   AC "isolasi cookie antar task": every job gets its own fresh context, and
 *      each is closed in `finally` even when the action throws.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { computeJitter, createController } from '../core/controller.js';
import type { ActionJob } from '../types.js';
import type { Logger } from '../core/logger.js';

const silentLogger: Logger = {
  child: () => silentLogger,
  debug: () => undefined,
  info: () => undefined,
  warn: () => undefined,
  error: () => undefined,
};

/** A scripted context records create/close order and can block on demand. */
let nextContextId = 0;
const events: string[] = [];
const openContexts: { id: number; closed: boolean }[] = [];

function makeContextFor(blocked: Promise<void> | undefined) {
  return async () => {
    const ctx: { id: number; closed: boolean } = { id: nextContextId++, closed: false };
    events.push(`open:${ctx.id}`);
    openContexts.push(ctx);
    if (blocked) await blocked;
    // Cast: the controller only uses .close(), and the stub adapters never
    // touch the context, so a minimal stand-in is enough to prove the ordering.
    return {
      id: ctx.id,
      close: async () => {
        ctx.closed = true;
        events.push(`close:${ctx.id}`);
        const i = openContexts.indexOf(ctx);
        if (i >= 0) openContexts.splice(i, 1);
      },
      ctx,
    } as unknown as import('playwright').BrowserContext;
  };
}

function job(n: number): ActionJob {
  return {
    id: `job-${n}`,
    accountId: 'acct-1',
    platform: 'instagram',
    action: 'comment',
    targetUrl: 'https://instagram.com/p/x',
    text: 'nice post',
    attempt: 1,
  };
}

test('AC concurrency=1: the next context opens only after the previous closed', async () => {
  nextContextId = 0;
  events.length = 0;
  openContexts.length = 0;
  const controller = createController({
    logger: silentLogger,
    contextFor: makeContextFor(undefined),
    jitterRangeMs: [0, 0], // no waiting in the test
    random: () => 0,
  });

  await controller.run(job(1));
  await controller.run(job(2));

  // Strict alternation: open:0, close:0, open:1, close:1 — never open:1 before
  // close:0, which is the only way two jobs could overlap.
  assert.deepEqual(events, ['open:0', 'close:0', 'open:1', 'close:1']);
});

test('AC cookie isolation: every job gets a fresh context, none reused', async () => {
  nextContextId = 0;
  events.length = 0;
  openContexts.length = 0;
  const controller = createController({
    logger: silentLogger,
    contextFor: makeContextFor(undefined),
    jitterRangeMs: [0, 0],
    random: () => 0,
  });

  await controller.run(job(1));
  await controller.run(job(2));
  await controller.run(job(3));

  // Three distinct contexts, all closed, none left open between jobs.
  assert.equal(events.filter((e) => e.startsWith('open:')).length, 3, 'one fresh context per job');
  assert.equal(openContexts.length, 0, 'no context leaks between jobs');
});

test('the context is closed in finally even when the action throws', async () => {
  nextContextId = 0;
  events.length = 0;
  openContexts.length = 0;
  const controller = createController({
    logger: silentLogger,
    contextFor: makeContextFor(undefined),
    jitterRangeMs: [0, 0],
    random: () => 0,
  });

  // The instagram adapter is a stub that throws inside comment(), i.e. AFTER
  // the context opened — exactly the path the finally must cover.
  const result = await controller.run(job(1));

  assert.equal(result.status, 'failed', 'a throwing adapter is a failed verdict');
  assert.deepEqual(events, ['open:0', 'close:0'], 'context closed despite the throw');
});

test('computeJitter stays within range and is injectable', () => {
  const r = () => 0.5;
  assert.equal(computeJitter([30_000, 90_000], r), 60_000);
  assert.equal(
    computeJitter([30_000, 90_000], () => 0),
    30_000,
  );
  assert.equal(
    computeJitter([30_000, 90_000], () => 0.999),
    89_940,
  );
  // A zero-width range is a fixed delay, not NaN.
  assert.equal(computeJitter([5_000, 5_000], r), 5_000);
});
