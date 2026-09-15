/**
 * Action queue consumer. Durable Redis List, `BLPOP` one job at a time so the
 * container is strictly sequential. Skeleton: the Redis client is wired in the
 * P1 transport ticket; this fixes the seam the composition root depends on.
 */
import type { WorkerConfig } from '../types.js';
import type { ActionJob } from '../types.js';
import type { Logger } from '../core/logger.js';

export interface QueueConsumer {
  /** Block for the next job, or `null` when the loop is stopped. */
  next(): Promise<ActionJob | null>;
  stop(): void;
}

export function createQueueConsumer(config: WorkerConfig, logger: Logger): QueueConsumer {
  void config;
  void logger;
  return {
    async next(): Promise<ActionJob | null> {
      throw new Error('queue consumer not implemented yet (P0-09 skeleton)');
    },
    stop() {
      // no-op in the skeleton
    },
  };
}
