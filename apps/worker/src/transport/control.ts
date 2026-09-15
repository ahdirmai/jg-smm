/**
 * Control-channel subscriber (P1-11). Private per-container Redis Pub/Sub for
 * auth instructions: `auth-login`, `auth-input`, `auth-clear` (payload:
 * accountId). Uses a DEDICATED connection because a client in subscribe mode
 * cannot issue other commands.
 *
 * Handlers are fire-and-forget: the caller never awaits them, so a slow login
 * does not stall the subscriber and drop the next instruction.
 */
import { createClient, type RedisClientType } from 'redis';

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
  return (
    typeof maybe.type === 'string' &&
    typeof maybe.accountId === 'string' &&
    typeof maybe.platform === 'string'
  );
}

export function createControlSubscriber(
  redisUrl: string,
  workerId: string,
  logger: Logger,
): ControlSubscriber {
  const channel = `control-${workerId}`;
  let client: RedisClientType | undefined;

  return {
    async start(handler: ControlHandler): Promise<void> {
      if (client) return;
      const next = createClient({ url: redisUrl }) as RedisClientType;
      next.on('error', (err) => logger.warn('control client error', { error: err.message }));
      await next.connect();
      client = next;

      await next.subscribe(channel, (raw) => {
        let message: unknown;
        try {
          message = JSON.parse(raw);
        } catch {
          logger.warn('dropped malformed control message (bad json)', { channel });
          return;
        }
        if (!isControlMessage(message)) {
          logger.warn('dropped malformed control message', { channel });
          return;
        }
        // Fire-and-forget: the login flow is slow and must not stall the
        // subscriber, or a subsequent auth-input would be missed.
        void handler(message).catch((err) =>
          logger.warn('control handler failed', { error: (err as Error).message }),
        );
      });
      logger.info('control subscriber ready', { channel });
    },

    async stop(): Promise<void> {
      if (!client) return;
      try {
        await client.unsubscribe(channel);
        await client.quit();
      } catch {
        // Best effort on shutdown; the process is exiting anyway.
      }
      client = undefined;
    },
  };
}
