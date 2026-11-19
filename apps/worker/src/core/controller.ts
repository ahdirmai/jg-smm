/**
 * Action orchestration. Owns the sequential-execution contract:
 *   one action per container at a time, single context per account, closed in
 *   `finally`, then a random jitter before the next job (DEVELOPMENT_RULE §7.3).
 *
 * The controller is platform-agnostic: it resolves an adapter from the registry
 * and dispatches on `job.action` (OCP/DIP). Skeleton: dispatch wiring only.
 */
import { readFile, rm } from 'node:fs/promises';
import { join } from 'node:path';

import type { BrowserContext } from 'playwright';

import { getAdapter } from '../platforms/registry.js';
import type { ActionJob, ActionResult } from '../types.js';
import type { Logger } from './logger.js';

export interface ControllerDeps {
  logger: Logger;
  /** Resolve a live context for the job's account (hydrates the session). */
  contextFor: (job: ActionJob) => Promise<BrowserContext>;
  /**
   * Min/max jitter in ms between sequential jobs. Defaults 30s–90s, which is
   * right for a long-lived container. A stateless worker passes [0, 0]: pacing
   * is the control plane's job, and sleeping here only keeps a microVM billing.
   */
  jitterRangeMs?: readonly [number, number];
  /**
   * Directory holding screenshot evidence, so the controller can read it back
   * and delete it. Absent means the adapter wrote no file (the BE stores the
   * bare name) — nothing to collect.
   */
  screenshotDir?: string;
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
      // Read the evidence off disk BEFORE the context closes: the screenshot is
      // the only record that the action reached the page, and a boxd worker's
      // disk disappears with the VM.
      const evidence = await collectEvidence(result.screenshot, deps.screenshotDir);
      // A successful action usually rotates the platform's session token, so
      // this state is newer than the one the dispatch carried.
      const updatedSession =
        result.ok && ctx ? await ctx.storageState().catch(() => undefined) : undefined;
      return {
        jobId: job.id,
        accountId: job.accountId,
        attempt: job.attempt,
        status: result.ok ? 'success' : 'failed',
        ...(updatedSession !== undefined ? { updatedSession } : {}),
        ...(evidence !== undefined ? { screenshotJpegB64: evidence } : {}),
        ...(result.renderedText !== undefined ? { renderedText: result.renderedText } : {}),
        // Echo the variant the BE composed from, so the BE can record it on the
        // attempt. The worker never decides this; it is a pure passthrough of
        // what it was dispatched, which is why it survives a dry run too.
        ...(job.templateId !== undefined ? { templateId: job.templateId } : {}),
        ...(result.screenshot !== undefined ? { screenshotPath: result.screenshot } : {}),
        ...(result.error !== undefined ? { error: result.error } : {}),
        durationMs: Date.now() - startedAt,
      };
    } catch (err) {
      log.error('action threw', { error: (err as Error).message });
      return {
        jobId: job.id,
        accountId: job.accountId,
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

/**
 * Read the JPEG an adapter wrote and delete it. Base64 for the callback; the
 * local copy is removed so a microVM never accumulates evidence files.
 * Best-effort: a missing file means "no evidence", not a failed action.
 */
async function collectEvidence(
  basename: string | undefined,
  dir: string | undefined,
): Promise<string | undefined> {
  if (!basename || !dir) return undefined;
  const path = join(dir, basename);
  try {
    const bytes = await readFile(path);
    return bytes.toString('base64');
  } catch {
    return undefined;
  } finally {
    await rm(path, { force: true }).catch(() => undefined);
  }
}
