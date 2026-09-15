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
import { clearAuthContext } from './core/auth.js';
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
const queue = createQueueConsumer(config.redisUrl, config.workerId, logger);
const control = createControlSubscriber(config.redisUrl, config.workerId, logger);

const controller = createController({
  logger,
  jitterRangeMs: [30_000, 90_000],
  contextFor: async () => {
    // Context resolution lands with the login tickets (P1-10): the account
    // context is hydrated from the persisted storageState on the PVC.
    throw new Error('context resolution not implemented yet (P1-10)');
  },
});

// --- heartbeat -----------------------------------------------------------
const state = { lastActionAt: null as string | null, queueDepth: 0 };
const heartbeat = createHeartbeat(
  config,
  logger,
  (): HeartbeatPayload => ({
    workerId: config.workerId,
    browserStatus: 'idle',
    queueDepth: state.queueDepth,
    lastActionAt: state.lastActionAt,
  }),
);
heartbeat.start();

// Refresh the backlog lazily so the heartbeat reports truth, not a stale 0.
setInterval(async () => {
  state.queueDepth = await queue.depth().catch(() => 0);
}, 5_000).unref();

// --- control channel (P1-11) --------------------------------------------
void control
  .start(async (message) => {
    if (!isControlMessage(message)) {
      logger.warn('dropped malformed control message');
      return;
    }
    logger.info('control message', { type: message.type, accountId: message.accountId });
    switch (message.type) {
      case 'auth-login':
        // runLogin lands with the headful-login ticket (P1-10).
        logger.warn('auth-login not implemented yet (P1-10)', {
          accountId: message.accountId,
        });
        break;
      case 'auth-input': {
        const code = typeof message.payload?.value === 'string' ? message.payload.value : '';
        if (!code) {
          logger.warn('auth-input without a value', { accountId: message.accountId });
          break;
        }
        // submitAuthInput lands with the 2FA ticket (P1-12).
        logger.warn('auth-input not implemented yet (P1-12)', {
          accountId: message.accountId,
        });
        break;
      }
      case 'auth-clear':
        clearAuthContext(message.accountId);
        break;
      default:
        logger.warn('unknown control type', { type: message.type });
    }
  })
  .catch((err) => logger.warn('control subscriber unavailable', { error: (err as Error).message }));

// --- action loop (P1-09): BLPOP one job, run it, callback, repeat --------
// Strictly sequential per container: the next job is only popped after the
// previous one (plus its jitter) finishes.
let loopRunning = false;
async function runActionLoop(): Promise<void> {
  if (loopRunning) return;
  loopRunning = true;
  logger.info('action loop started (sequential, batch)', {
    queue: `queue:action:${config.workerId}`,
  });

  for (;;) {
    const job = await queue.next();
    if (!job) {
      // Only null when stopping.
      break;
    }
    logger.info('job received', { jobId: job.id, action: job.action, platform: job.platform });

    if (config.dryRun) {
      logger.info('dry-run: skipping action', { jobId: job.id, action: job.action });
      await callback.post({
        jobId: job.id,
        accountId: job.accountId,
        workerId: config.workerId,
        attempt: job.attempt,
        status: 'success',
        durationMs: 0,
      });
      continue;
    }

    const result = await controller.run(job);
    state.lastActionAt = new Date().toISOString();
    await callback.post(result);
  }
  loopRunning = false;
}

void runActionLoop().catch((err) =>
  logger.error('action loop crashed', { error: (err as Error).message }),
);

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
