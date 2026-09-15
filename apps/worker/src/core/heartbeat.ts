/**
 * Heartbeat loop. Posts liveness + queue depth to the BE every N seconds.
 *
 * Fails soft: the BE endpoint is introduced in a later ticket, so a missing or
 * failing endpoint must NOT crash the container. After `maxFailures` consecutive
 * failures we self-exit so the orchestrator can restart us (DEVELOPMENT_RULE §7.4).
 */
import type { WorkerConfig } from '../types.js';
import type { Logger } from './logger.js';

export interface HeartbeatPayload {
  workerId: string;
  browserStatus: 'starting' | 'idle' | 'busy';
  queueDepth: number;
  lastActionAt: string | null;
}

export interface HeartbeatOptions {
  /** Consecutive failures tolerated before self-exit. */
  maxFailures?: number;
  /** Override for tests / DI. Defaults to global fetch. */
  fetchImpl?: typeof fetch;
  /** Called when the failure budget is exhausted. Defaults to process.exit(1). */
  onFatal?: () => void;
}

export interface Heartbeat {
  start(): void;
  stop(): void;
  /** Exposed for tests: send a single beat. */
  beat(): Promise<boolean>;
}

export function createHeartbeat(
  config: WorkerConfig,
  logger: Logger,
  snapshot: () => HeartbeatPayload,
  options: HeartbeatOptions = {},
): Heartbeat {
  const maxFailures = options.maxFailures ?? 3;
  const doFetch = options.fetchImpl ?? fetch;
  const onFatal = options.onFatal ?? (() => process.exit(1));
  const url = `${config.apiUrl}/internal/heartbeat`;

  let timer: NodeJS.Timeout | undefined;
  let failures = 0;

  async function beat(): Promise<boolean> {
    try {
      const res = await doFetch(url, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify(snapshot()),
        signal: AbortSignal.timeout(8_000),
      });
      if (!res.ok) throw new Error(`heartbeat status ${res.status}`);
      failures = 0;
      return true;
    } catch (err) {
      failures += 1;
      logger.warn('heartbeat failed', { attempt: failures, error: (err as Error).message });
      if (failures >= maxFailures) {
        logger.error('heartbeat budget exhausted, exiting', { failures });
        if (timer) clearInterval(timer);
        timer = undefined;
        onFatal();
      }
      return false;
    }
  }

  return {
    start() {
      if (timer) return;
      const intervalMs = config.heartbeatIntervalSec * 1_000;
      timer = setInterval(() => void beat(), intervalMs);
      // Send an immediate beat so the BE sees us without waiting a full interval.
      void beat();
    },
    stop() {
      if (timer) clearInterval(timer);
      timer = undefined;
    },
    beat,
  };
}
