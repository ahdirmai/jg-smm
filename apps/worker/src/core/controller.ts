/**
 * Action orchestration. Owns the sequential-execution contract:
 *   one action per container at a time, single context per account, closed in
 *   `finally`, then a random jitter before the next job (DEVELOPMENT_RULE §7.3).
 *
 * The controller is platform-agnostic: it resolves an adapter from the registry
 * and dispatches on `job.action` (OCP/DIP). Skeleton: dispatch wiring only.
 */
import type { BrowserContext } from 'playwright';

import { getAdapter } from '../platforms/registry.js';
import type { ActionJob, ActionResult } from '../types.js';
import type { Logger } from './logger.js';

export interface ControllerDeps {
  logger: Logger;
  /** Resolve a live context for the job's account (hydrates the session). */
  contextFor: (job: ActionJob) => Promise<BrowserContext>;
  /** Min/max jitter in ms between sequential jobs. Defaults 30s–90s. */
  jitterRangeMs?: readonly [number, number];
  /** Injectable RNG for deterministic tests. */
  random?: () => number;
}

/** Random jitter between sequential jobs so the cadence is not machine-like. */
export function computeJitter(
  range: readonly [number, number],
  random: () => number = Math.random,
): number {
  const [min, max] = range;
  return Math.round(min + random() * (max - min));
}

export function createController(deps: ControllerDeps) {
  const jitterRange = deps.jitterRangeMs ?? [30_000, 90_000];
  const random = deps.random ?? Math.random;

  async function run(job: ActionJob): Promise<ActionResult> {
    const startedAt = Date.now();
    const log = deps.logger.child({ jobId: job.id, accountId: job.accountId, attempt: job.attempt });

    const adapter = getAdapter(job.platform);
    if (!adapter) {
      return {
        jobId: job.id,
        accountId: job.accountId,
        workerId: '',
        attempt: job.attempt,
        status: 'failed',
        error: `no adapter for platform ${job.platform}`,
        durationMs: Date.now() - startedAt,
      };
    }

    let ctx: BrowserContext | undefined;
    try {
      ctx = await deps.contextFor(job);
      const result = await dispatch(adapter, ctx, job);
      return {
        jobId: job.id,
        accountId: job.accountId,
        workerId: '',
        attempt: job.attempt,
        status: result.ok ? 'success' : 'failed',
        ...(result.renderedText !== undefined ? { renderedText: result.renderedText } : {}),
        ...(result.screenshot !== undefined ? { screenshot: result.screenshot } : {}),
        ...(result.error !== undefined ? { error: result.error } : {}),
        durationMs: Date.now() - startedAt,
      };
    } catch (err) {
      log.error('action threw', { error: (err as Error).message });
      return {
        jobId: job.id,
        accountId: job.accountId,
        workerId: '',
        attempt: job.attempt,
        status: 'failed',
        error: (err as Error).message,
        durationMs: Date.now() - startedAt,
      };
    } finally {
      // Isolation: always close the account context before the next job.
      await ctx?.close().catch(() => undefined);
      await sleep(computeJitter(jitterRange, random));
    }
  }

  return { run };
}

async function dispatch(
  adapter: ReturnType<typeof getAdapter>,
  ctx: BrowserContext,
  job: ActionJob,
) {
  if (!adapter) throw new Error('adapter missing');
  switch (job.action) {
    case 'comment':
      return adapter.comment(ctx, job);
    case 'reply_comment':
      return adapter.replyComment(ctx, job);
    case 'like_comment':
      return adapter.likeComment(ctx, job);
    case 'like':
      return adapter.like(ctx, job);
    default:
      // Unknown actions are reported, never silently swallowed.
      return { ok: false, error: `unsupported action ${job.action}` };
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
