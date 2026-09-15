/**
 * Composition root. Wires config, logger, heartbeat, transport and the action
 * controller, then owns the process lifecycle (graceful shutdown).
 *
 * P0-09 skeleton: heartbeat runs for real so the container is observably alive;
 * the BLPOP queue loop and control subscriber are surfaced but not yet looping
 * (their implementations land in the P1 worker tickets). Nothing here throws on
 * boot — a missing BE endpoint must not crash the container.
 */
import { loadConfig } from './core/config.js';
import { createLogger } from './core/logger.js';
import { createHeartbeat, type HeartbeatPayload } from './core/heartbeat.js';
import { createController } from './core/controller.js';
import { createCallback } from './transport/callback.js';
import { createQueueConsumer } from './transport/queue.js';
import { createControlSubscriber, isControlMessage } from './transport/control.js';
import { supportedPlatforms } from './platforms/registry.js';

const config = loadConfig();
const logger = createLogger({ workerId: config.workerId, containerId: process.env.HOSTNAME });

logger.info('worker starting', {
  apiUrl: config.apiUrl,
  redisUrl: config.redisUrl,
  platforms: config.platforms,
  adapters: supportedPlatforms(),
  dryRun: config.dryRun,
  display: config.display,
});

// --- composition ---------------------------------------------------------
const callback = createCallback(config, logger);
const queue = createQueueConsumer(config, logger);
const control = createControlSubscriber(config.workerId, logger);

const controller = createController({
  logger,
  contextFor: async () => {
    throw new Error('context resolution not implemented yet (P0-09 skeleton)');
  },
});

// --- heartbeat -----------------------------------------------------------
const lastActionAt: string | null = null;
const heartbeat = createHeartbeat(
  config,
  logger,
  (): HeartbeatPayload => ({
    workerId: config.workerId,
    browserStatus: 'idle',
    queueDepth: 0,
    lastActionAt,
  }),
);
heartbeat.start();

// --- control channel (dispatch seam only) --------------------------------
void control
  .start(async (message) => {
    if (!isControlMessage(message)) {
      logger.warn('dropped malformed control message');
      return;
    }
    logger.info('control message', { type: message.type, accountId: message.accountId });
    // Auth handlers land with the login tickets.
  })
  .catch((err) => logger.warn('control subscriber unavailable', { error: (err as Error).message }));

// --- action loop (seam; real BLPOP loop lands in P1) ---------------------
logger.info('action loop ready (sequential, batch)', {
  queue: `queue:action:${config.workerId}`,
});
void controller;
void callback;
void queue;

// --- lifecycle -----------------------------------------------------------
let shuttingDown = false;
async function shutdown(signal: string): Promise<void> {
  if (shuttingDown) return;
  shuttingDown = true;
  logger.info('worker shutting down', { signal });
  heartbeat.stop();
  queue.stop();
  await control.stop().catch(() => undefined);
  process.exit(0);
}

process.on('SIGINT', () => void shutdown('SIGINT'));
process.on('SIGTERM', () => void shutdown('SIGTERM'));
