/**
 * Control-channel subscriber. Private per-container Pub/Sub for auth
 * instructions (`auth-login`, `auth-input`, `auth-clear`). Requires a dedicated
 * Redis connection because a subscriber cannot issue other commands.
 *
 * Skeleton: fixes the dispatch seam. Control handlers are NOT awaited by the
 * caller (fire-and-forget) so the worker stays responsive while idle.
 */
import type { ControlMessage } from '../types.js';
import type { Logger } from '../core/logger.js';

export type ControlHandler = (message: ControlMessage) => Promise<void>;

export interface ControlSubscriber {
  start(handler: ControlHandler): Promise<void>;
  stop(): Promise<void>;
}

/** Minimal structural validation — malformed messages are dropped, never thrown. */
export function isControlMessage(value: unknown): value is ControlMessage {
  if (typeof value !== 'object' || value === null) return false;
  const maybe = value as Record<string, unknown>;
  return typeof maybe.type === 'string' && typeof maybe.accountId === 'string' && typeof maybe.platform === 'string';
}

export function createControlSubscriber(
  workerId: string,
  logger: Logger,
): ControlSubscriber {
  void workerId;
  void logger;
  return {
    async start(): Promise<void> {
      throw new Error('control subscriber not implemented yet (P0-09 skeleton)');
    },
    async stop(): Promise<void> {
      // no-op in the skeleton
    },
  };
}
