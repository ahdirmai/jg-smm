/**
 * Composition root. Wires config, logger, heartbeat, transport and the action
 * controller, then owns the process lifecycle (graceful shutdown).
 *
 * The BLPOP loop is strictly sequential per container (concurrency=1 lives
 * here, not in the controller): the next job is popped only after the previous
 * one plus its jitter finishes. Nothing here throws on boot — a missing BE
 * endpoint must not crash the container.
 */
import { loadConfig } from './core/config.js';
import type { BrowserContext, Page } from 'playwright';
import { createLogger } from './core/logger.js';
import { createHeartbeat, type HeartbeatPayload } from './core/heartbeat.js';
import { claimRow } from './core/claim.js';
import { createController } from './core/controller.js';
import { clearAuthContext, runLogin, submitAuthInput } from './core/auth.js';
import { launchBrowser, newAccountContext, type BrowserHandle } from './core/browser.js';
import { createGeolocation } from './core/geolocation.js';
import { readSession, writeSession } from './core/session.js';
import { createCallback } from './transport/callback.js';
import { createAuthCallback } from './transport/auth-callback.js';
import { createSessionCallback } from './transport/session-callback.js';
import { createQueueConsumer } from './transport/queue.js';
import { createControlSubscriber, isControlMessage } from './transport/control.js';
import { configureAdapters } from './platforms/deps.js';
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

// --- claim ---------------------------------------------------------------
// A scaled container knows only its hostname. Claim binds it to the oldest
// free dashboard-created row and returns that row's real id — which is what
// the queue key, the control channel and every heartbeat must address. Until
// this resolves the worker is unassigned; it stays alive on its boot id and
// retries on the next cycle rather than crashing (see claim.ts).
const bootId = config.workerId;
const claimed = await claimRow(
  {
    containerId: bootId,
    controlChannel: process.env.CONTROL_CHANNEL,
    actionQueue: process.env.ACTION_QUEUE,
    sessionPvc: process.env.SESSION_PVC,
    novncUrl: config.novncUrl,
  },
  { apiUrl: config.apiUrl, logger, internalSecret: config.internalSecret },
);
// The resolved id is what the rest of the process addresses; the boot id is
// kept only for the log line above and the fallback in claim.ts.
const workerId = claimed.workerId;
// A claimed row may carry its own frozen GPS point; expose it to the
// geolocation reader so the spoof matches the row, not the boot id.
const claimedLocation =
  typeof claimed.latitude === 'number' && typeof claimed.longitude === 'number'
    ? { latitude: claimed.latitude, longitude: claimed.longitude, location: claimed.location }
    : null;

// --- composition ---------------------------------------------------------
// Adapter boot deps are set once, before any job can run, so the screenshot
// name is deterministic from the first action (§7.3).
configureAdapters({
  workerId,
  screenshotDir: process.env.SCREENSHOT_DIR ?? '/data/screenshots',
});

const callback = createCallback(config, logger);
const queue = createQueueConsumer(config.redisUrl, workerId, logger);
const control = createControlSubscriber(config.redisUrl, workerId, logger);

// The worker's frozen GPS point, read once from the API. A claimed row may
// already carry it; the API lookup is still tried so a row whose point was set
// after the claim is honoured. Best-effort: a worker with no assigned location
// keeps the real device position (see geolocation.ts).
const geolocation = createGeolocation({
  apiUrl: config.apiUrl,
  internalSecret: config.internalSecret,
  workerId,
  logger,
  initial: claimedLocation ?? undefined,
});

// One headful browser per container, launched lazily: in dry-run (the compose
// default) no page is ever opened, so a dev box without Xvfb still boots clean.
let browserHandle: BrowserHandle | undefined;
async function browser() {
  if (!browserHandle) {
    browserHandle = await launchBrowser({
      display: config.display,
      // Null `proxyUrl` is the direct-dial default (a local stack has none).
      // When set it is the container's assigned residential pool: the fix for
      // a fleet of workers all presenting one datacentre IP to Instagram.
      proxy: config.proxyUrl
        ? {
            server: config.proxyUrl,
            ...(config.proxyAuth ? { username: config.proxyAuth.username } : {}),
            ...(config.proxyAuth ? { password: config.proxyAuth.password } : {}),
          }
        : null,
    });
  }
  return browserHandle.browser;
}
// The live-view context, kept open across "open profile" calls so repeated
// clicks navigate one window instead of stacking new Chrome windows.
let manualContext: BrowserContext | undefined;

const controller = createController({
  logger,
  jitterRangeMs: [30_000, 90_000],
  contextFor: async (job) =>
    // A fresh context per job is the isolation boundary (P3-07); the session
    // persisted by login is hydrated from the PVC so a restart needs no
    // re-login (§7.1). The context is pinned to the worker's frozen GPS point
    // so every page in the session reports the same position.
    newAccountContext(
      await browser(),
      job.platform,
      await readSession(job.platform),
      await geolocation.get(),
    ),
});

// --- heartbeat -----------------------------------------------------------
const state = { lastActionAt: null as string | null, queueDepth: 0 };
const heartbeat = createHeartbeat(
  { ...config, workerId },
  logger,
  (): HeartbeatPayload => ({
    workerId,
    browserStatus: 'idle',
    queueDepth: state.queueDepth,
    lastActionAt: state.lastActionAt,
    ...(config.novncUrl ? { novncUrl: config.novncUrl } : {}),
  }),
);
heartbeat.start();

// Refresh the backlog lazily so the heartbeat reports truth, not a stale 0.
setInterval(async () => {
  state.queueDepth = await queue.depth().catch(() => 0);
}, 5_000).unref();

// --- control channel (P1-11) --------------------------------------------
// Shared by auth-login and auth-input: one browser, one screenshot dir.
const authDeps = {
  browser,
  geolocation: () => geolocation.get(),
  log: (msg: string, data?: Record<string, unknown>) => logger.warn(msg, data),
  workerId: config.workerId,
  ...(process.env.SCREENSHOT_DIR ? { screenshotDir: process.env.SCREENSHOT_DIR } : {}),
  // A verified login reports its session so it outlives this container. The
  // well-known requestId marks it as a store, not an operator export.
  onSession: async (accountId: string, platform: string, state: unknown) => {
    await sessionCallback.post({
      accountId,
      platform,
      requestId: 'login-success',
      session: state,
    });
  },
};

// The login outcome is reported back so the dashboard's auth badge moves on
// its own; without this the row sits on AUTHENTICATING forever.
const authCallback = createAuthCallback({ ...config, workerId }, logger);

// Session export/import (P-C): the operator can rescue an account's session
// (cookies) off a container before deleting it. The session never appears in a
// log line — only the accountId does.
const sessionCallback = createSessionCallback({ ...config, workerId }, logger);
// A new login or auth-clear invalidates callbacks from an earlier attempt.
const authAttempts = new Map<string, symbol>();

void control
  .start(async (message) => {
    if (!isControlMessage(message)) {
      logger.warn('dropped malformed control message');
      return;
    }
    logger.info('control message', { type: message.type, accountId: message.accountId });
    switch (message.type) {
      case 'auth-login': {
        // The API delivers the operator's credentials (unsealed at dispatch)
        // so the form can be filled; absent, the operator types in the noVNC
        // view as before. The payload is never logged (line above logs type +
        // accountId only).
        const username =
          typeof message.payload?.username === 'string' ? message.payload.username : '';
        const password =
          typeof message.payload?.password === 'string' ? message.payload.password : '';
        const credentials = username && password ? { username, password } : undefined;
        const attempt = Symbol();
        authAttempts.set(message.accountId, attempt);
        try {
          const result = await runLogin(
            message.accountId,
            message.platform,
            {
              ...authDeps,
              onNeedsInput: async (accountId, input) => {
                if (authAttempts.get(accountId) === attempt)
                  await authCallback.post(accountId, input);
              },
            },
            credentials,
          );
          if (authAttempts.get(message.accountId) === attempt) {
            await authCallback.post(message.accountId, result);
          }
        } finally {
          if (authAttempts.get(message.accountId) === attempt)
            authAttempts.delete(message.accountId);
        }
        break;
      }
      case 'auth-input': {
        const code = typeof message.payload?.value === 'string' ? message.payload.value : '';
        if (!code) {
          logger.warn('auth-input without a value', { accountId: message.accountId });
          break;
        }
        const attempt = authAttempts.get(message.accountId);
        const result = await submitAuthInput(message.accountId, code, message.platform, authDeps);
        // The active login owns the final callback when code submission joins
        // its poll. A legacy parked context still needs its own callback.
        if (!attempt && !authAttempts.has(message.accountId)) {
          await authCallback.post(message.accountId, result);
        }
        break;
      }
      case 'auth-clear':
        authAttempts.delete(message.accountId);
        clearAuthContext(message.accountId);
        break;
      case 'auth-export': {
        // Dump the account's persisted session (cookies) back to the API so an
        // operator can re-import it into a fresh container. requestId correlates
        // the dump with the waiting export request.
        const requestId =
          typeof message.payload?.requestId === 'string' ? message.payload.requestId : '';
        if (!requestId) {
          logger.warn('auth-export without a requestId', { accountId: message.accountId });
          break;
        }
        const session = await readSession(message.platform).catch(() => undefined);
        await sessionCallback.post({
          accountId: message.accountId,
          platform: message.platform,
          requestId,
          // null tells the API there is no session on this container to rescue.
          session: session ?? null,
        });
        break;
      }
      case 'auth-import': {
        // Adopt a session handed over from another container. Validate
        // defensively: a non-object payload is dropped, never written or thrown.
        const session = message.payload?.session;
        if (session === null || typeof session !== 'object' || Array.isArray(session)) {
          logger.warn('auth-import without a valid session payload', {
            accountId: message.accountId,
          });
          break;
        }
        await writeSession(message.platform, session);
        // Log the fact, never the value.
        logger.info('session imported', {
          accountId: message.accountId,
          platform: message.platform,
        });
        break;
      }
      case 'browser-open': {
        // Open the target in the account's LIVE, session-hydrated browser on
        // the Xvfb display, so the noVNC live view shows the profile already
        // logged in. The context is kept across calls: a second "open
        // profile" navigates the same window instead of stacking a second
        // Chrome window on the display.
        const url = typeof message.payload?.url === 'string' ? message.payload.url : '';
        const target = url || 'about:blank';
        try {
          const b = await browser();
          if (!manualContext) {
            // Hydrate the persisted session when the message names a platform
            // this worker serves — the operator then sees the logged-in
            // profile, not a login wall. A bare "Live view" (no platform)
            // keeps the old behaviour: a fresh, session-less context.
            // newAccountContext pins the context to the worker's frozen GPS
            // point — the same position the actions report.
            if (config.platforms.includes(message.platform)) {
              manualContext = await newAccountContext(
                b,
                message.platform,
                await readSession(message.platform).catch(() => undefined),
                await geolocation.get(),
              );
            } else {
              manualContext = await b.newContext({ viewport: { width: 1280, height: 800 } });
            }
          }
          const page: Page = manualContext.pages()[0] ?? (await manualContext.newPage());
          await page.goto(target, { waitUntil: 'domcontentloaded' }).catch(() => undefined);
          logger.info('browser-open: opened in live view', {
            url: target,
            platform: message.platform,
          });
        } catch (err) {
          logger.warn('browser-open failed', { error: (err as Error).message, url: target });
        }
        break;
      }
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
  await browserHandle?.close().catch(() => undefined);
  process.exit(0);
}

process.on('SIGINT', () => void shutdown('SIGINT'));
process.on('SIGTERM', () => void shutdown('SIGTERM'));
