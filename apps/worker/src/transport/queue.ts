/**
 * Action queue consumer (P1-09). Durable Redis List, consumed with BLPOP one
 * job at a time so the container is strictly sequential (DEVELOPMENT_RULE §7.2:
 * batch sequential — one action per container, never parallel inside it).
 *
 * BLPOP is the blocking pop with a timeout; a timeout is not an error, it just
 * means "queue empty, keep waiting". Two Redis clients are used because a
 * connection in subscribe mode cannot run other commands (see control.ts).
 */
import { createClient, type RedisClientType } from 'redis';

import type { ActionJob } from '../types.js';
import type { Logger } from '../core/logger.js';

export interface QueueConsumer {
  /** Block for the next job, or `null` when the loop is stopping. */
  next(): Promise<ActionJob | null>;
  /** Current backlog, reported to the heartbeat. */
  depth(): Promise<number>;
  stop(): void;
}

export interface QueueOptions {
  /** BLPOP timeout in seconds; 0 = block forever. */
  blpopTimeoutSec?: number;
}

export function createQueueConsumer(
  redisUrl: string,
  workerId: string,
  logger: Logger,
  options: QueueOptions = {},
): QueueConsumer {
  const timeout = options.blpopTimeoutSec ?? 5;
  const key = `queue:action:${workerId}`;
  let client: RedisClientType | undefined;
  let stopping = false;

  async function connect(): Promise<RedisClientType> {
    if (client) return client;
    const next = createClient({ url: redisUrl }) as RedisClientType;
    next.on('error', (err) => logger.warn('queue client error', { error: err.message }));
    await next.connect();
    client = next;
    return next;
  }

  return {
    async next(): Promise<ActionJob | null> {
      const conn = await connect();
      while (!stopping) {
        try {
          // BLPOP returns [key, payload] or null on timeout.
          const entry = (await conn.blPop(key, timeout)) as { key: string; element: string } | null;
          if (!entry) continue; // timeout: keep waiting
          const job = parseJob(entry.element);
          if (!job) continue; // malformed: dropped + logged, the loop survives
          return job;
        } catch (err) {
          logger.warn('blpop failed, retrying', { error: (err as Error).message });
          await sleep(1_000);
        }
      }
      return null;
    },

    async depth(): Promise<number> {
      try {
        const conn = await connect();
        return (await conn.lLen(key)) ?? 0;
      } catch {
        return 0;
      }
    },

    stop(): void {
      stopping = true;
      void client?.quit().catch(() => undefined);
    },
  };
}

function parseJob(raw: string): ActionJob | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (typeof parsed !== 'object' || parsed === null) return null;
  const job = parsed as Record<string, unknown>;
  if (
    typeof job.id !== 'string' ||
    typeof job.accountId !== 'string' ||
    typeof job.action !== 'string' ||
    typeof job.targetUrl !== 'string'
  ) {
    return null;
  }
  return {
    id: job.id,
    accountId: job.accountId,
    platform: job.platform as ActionJob['platform'],
    action: job.action,
    targetUrl: job.targetUrl,
    ...(typeof job.text === 'string' ? { text: job.text } : {}),
    attempt: typeof job.attempt === 'number' ? job.attempt : 1,
  };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
