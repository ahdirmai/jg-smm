/**
 * Composition root.
 *
 * Two shapes of worker live here, chosen by WORKER_POOL_MODE:
 *
 *  - `stateless` (boxd fleet): the worker LISTENS. Actions, logins and live-view
 *    navigations arrive as authenticated HTTPS requests and the microVM sleeps
 *    the moment the work is done. No heartbeat, no Redis claim, no polling —
 *    anything that holds a socket open also holds the VM awake and billing.
 *  - `legacy` (containers): the worker PULLS. It claims a row, subscribes to its
 *    control channel and BLPOPs its queue.
 *
 * Both paths end in the same controller and auth core; only the transport and
 * the source of account identity differ.
 */
import type { BrowserContext } from 'playwright';

import { loadConfig } from './core/config.js';
import { createLogger } from './core/logger.js';
import { claimRow } from './core/claim.js';
import { createController } from './core/controller.js';
import { authContexts, clearAuthContext, runLogin, submitAuthInput } from './core/auth.js';
import { launchBrowser, newAccountContext, type AccountContextInit, type BrowserHandle } from './core/browser.js';
import { createGeolocation } from './core/geolocation.js';
import { readSession } from './core/session.js';
import { createCallback } from './transport/callback.js';
import { createAuthCallback } from './transport/auth-callback.js';
import { createSessionCallback } from './transport/session-callback.js';
import { createQueueConsumer } from './transport/queue.js';
import { createControlSubscriber, isControlMessage } from './transport/control.js';
import { createHeartbeat, type HeartbeatPayload } from './core/heartbeat.js';
import { writeSession } from './core/session.js';
import { createRpcServer, verifyLiveViewTicket } from './transport/rpc-server.js';
import { configureAdapters } from './platforms/deps.js';
import { supportedPlatforms } from './platforms/registry.js';
import type { AccountRuntimeSpec, ActionJob } from './types.js';

const config = loadConfig();
const logger = createLogger({ workerId: config.workerId, containerId: process.env.HOSTNAME });

logger.info('worker starting', {
  apiUrl: config.apiUrl,
  poolMode: config.poolMode,
  rpc: `${config.rpcHost}:${config.rpcPort}`,
  platforms: config.platforms,
  adapters: supportedPlatforms(),
  dryRun: config.dryRun,
  display: config.display,
});

const callback = createCallback(config, logger);
const screenshotDir = process.env.SCREENSHOT_DIR;

// One headful browser per machine, launched lazily: in dry-run (the compose
// default) no page is ever opened, so a dev box without Xvfb still boots clean.
let browserHandle: BrowserHandle | undefined;
async function browser() {
  if (!browserHandle) {
    browserHandle = await launchBrowser({
      display: config.display,
      // Null `proxyUrl` is the direct-dial default. In pool mode the egress is
      // decided per CONTEXT from the account's own proxy group, so the browser
      // itself must not pin one.
      proxy:
        config.poolMode === 'legacy' && config.proxyUrl
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

// Per-account action context. In pool mode the identity comes from the job; in
// legacy mode from the on-disk session and the worker's frozen GPS point.
function accountInit(
  platform: AccountJobPlatform,
  runtime: AccountRuntimeSpec | undefined,
  legacy: { storageState?: unknown; geolocation?: { latitude: number; longitude: number } | null },
): AccountContextInit {
  if (runtime) {
    return {
      platform,
      storageState: runtime.storageState,
      geolocation: runtime.geolocation ?? null,
      timezoneId: runtime.timezone,
      ...(runtime.userAgent ? { userAgent: runtime.userAgent } : {}),
      proxy: runtime.proxy ?? null,
    };
  }
  return {
    platform,
    storageState: legacy.storageState,
    geolocation: legacy.geolocation ?? null,
  };
}

type AccountJobPlatform = ActionJob['platform'];

// The live-view context, kept open across "open profile" calls so repeated
// clicks navigate one window instead of stacking Chrome windows on the display.
let manualContext: BrowserContext | undefined;
let manualAccountId: string | undefined;

const authCallback = createAuthCallback(config, logger);
const sessionCallback = createSessionCallback(config, logger);
// A new login or auth-clear invalidates callbacks from an earlier attempt.
const authAttempts = new Map<string, symbol>();

/**
 * Identity for the legacy action path. Pool mode never reads it: there the job
 * carries its own runtime.
 */
const legacyIdentity: {
  storageState?: unknown;
  geolocation?: { latitude: number; longitude: number } | null;
} = {};

const controller = createController({
  logger,
  // Pool mode: pacing belongs to the control plane's scheduler. Sleeping here
  // only keeps the microVM awake after the work is already finished.
  jitterRangeMs: config.poolMode === 'stateless' ? [0, 0] : [30_000, 90_000],
  ...(screenshotDir ? { screenshotDir } : {}),
  contextFor: async (job) => {
    const runtime = (job as ActionJob & { runtime?: AccountRuntimeSpec }).runtime;
    return newAccountContext(await browser(), accountInit(job.platform, runtime, legacyIdentity));
  },
});

async function runAction(job: ActionJob): Promise<void> {
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
    return;
  }
  const result = await controller.run(job);
  await callback.post(result);
}

/**
 * Run one login and report every outcome. Shared by the RPC server and the
 * control channel so the two transports cannot drift on what counts as success.
 */
async function startLogin(accountId: string, runtime: AccountRuntimeSpec | undefined, legacyPlatform: AccountJobPlatform): Promise<void> {
  const platform = (runtime?.platform ?? legacyPlatform) as AccountJobPlatform;
  const credentials = runtime?.credentials;
  const attempt = Symbol();
  authAttempts.set(accountId, attempt);
  try {
    const result = await runLogin(
      accountId,
      platform,
      {
        browser,
        ...(runtime ? { runtime } : {}),
        geolocation: async () => (runtime ? (runtime.geolocation ?? null) : manualGeolocation()),
        log: (msg: string, data?: Record<string, unknown>) => logger.warn(msg, data),
        workerId: config.workerId,
        ...(screenshotDir ? { screenshotDir } : {}),
        onSession: async (id: string, p: string, state: unknown) => {
          await sessionCallback.post({ accountId: id, platform: p, requestId: 'login-success', session: state });
        },
        onNeedsInput: async (id: string, input) => {
          if (authAttempts.get(id) === attempt) await authCallback.post(id, input);
        },
      },
      credentials,
    );
    if (authAttempts.get(accountId) === attempt) {
      await authCallback.post(accountId, result);
    }
  } finally {
    if (authAttempts.get(accountId) === attempt) authAttempts.delete(accountId);
  }
}

async function shutdown(signal: string): Promise<void> {
  logger.info('worker shutting down', { signal });
  await browserHandle?.close().catch(() => undefined);
  process.exit(0);
}

process.on('SIGINT', () => void shutdown('SIGINT'));
process.on('SIGTERM', () => void shutdown('SIGTERM'));

if (config.poolMode === 'stateless') {
  await startStateless();
} else {
  await startLegacy();
}

// --- legacy: claim + control channel + BLPOP loop --------------------------

async function startLegacy(): Promise<void> {
  const claimed = await claimRow(
    {
      containerId: config.workerId,
      controlChannel: process.env.CONTROL_CHANNEL,
      actionQueue: process.env.ACTION_QUEUE,
      sessionPvc: process.env.SESSION_PVC,
      novncUrl: config.novncUrl,
    },
    { apiUrl: config.apiUrl, logger, internalSecret: config.internalSecret },
  );
  const workerId = claimed.workerId;
  const claimedLocation =
    typeof claimed.latitude === 'number' && typeof claimed.longitude === 'number'
      ? { latitude: claimed.latitude, longitude: claimed.longitude, location: claimed.location }
      : null;

  configureAdapters({ workerId, screenshotDir: screenshotDir ?? '/data/screenshots' });

  const geolocation = createGeolocation({
    apiUrl: config.apiUrl,
    internalSecret: config.internalSecret,
    workerId,
    logger,
    initial: claimedLocation ?? undefined,
  });
  manualGeolocation = async () => geolocation.get();

  const queue = createQueueConsumer(config.redisUrl, workerId, logger);
  const control = createControlSubscriber(config.redisUrl, workerId, logger);

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
  setInterval(async () => {
    state.queueDepth = await queue.depth().catch(() => 0);
  }, 5_000).unref();

  void control
    .start(async (message) => {
      if (!isControlMessage(message)) {
        logger.warn('dropped malformed control message');
        return;
      }
      logger.info('control message', { type: message.type, accountId: message.accountId });
      switch (message.type) {
        case 'auth-login': {
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
                browser,
                geolocation: () => geolocation.get(),
                log: (msg: string, data?: Record<string, unknown>) => logger.warn(msg, data),
                workerId: config.workerId,
                ...(screenshotDir ? { screenshotDir } : {}),
                onSession: async (id: string, p: string, session: unknown) => {
                  await sessionCallback.post({ accountId: id, platform: p, requestId: 'login-success', session });
                },
                onNeedsInput: async (accountId, input) => {
                  if (authAttempts.get(accountId) === attempt) await authCallback.post(accountId, input);
                },
              },
              credentials,
            );
            if (authAttempts.get(message.accountId) === attempt) {
              await authCallback.post(message.accountId, result);
            }
          } finally {
            if (authAttempts.get(message.accountId) === attempt) authAttempts.delete(message.accountId);
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
          const result = await submitAuthInput(message.accountId, code, message.platform, {
            browser,
            geolocation: () => geolocation.get(),
            log: (msg: string, data?: Record<string, unknown>) => logger.warn(msg, data),
            workerId: config.workerId,
          });
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
          const requestId =
            typeof message.payload?.requestId === 'string' ? message.payload.requestId : '';
          const session = await readSession(message.platform).catch(() => undefined);
          await sessionCallback.post({
            accountId: message.accountId,
            platform: message.platform,
            requestId,
            session: session ?? null,
          });
          break;
        }
        case 'auth-import': {
          const session = message.payload?.session;
          if (session === null || typeof session !== 'object' || Array.isArray(session)) {
            logger.warn('auth-import without a valid session payload', { accountId: message.accountId });
            break;
          }
          await writeSession(message.platform, session);
          logger.info('session imported', { accountId: message.accountId, platform: message.platform });
          break;
        }
        case 'browser-open': {
          const url = typeof message.payload?.url === 'string' ? message.payload.url : '';
          const ctx = await openLiveView(
            { accountId: message.accountId, platform: message.platform, timezone: 'Asia/Jakarta' },
            url,
            async () => ({
              storageState: await readSession(message.platform).catch(() => undefined),
              geolocation: (await geolocation.get().catch(() => null)) ?? null,
            }),
          );
          manualContext = ctx;
          break;
        }
        default:
          logger.warn('unknown control type', { type: message.type });
      }
    })
    .catch((err) => logger.warn('control subscriber unavailable', { error: (err as Error).message }));

  for (;;) {
    const job = await queue.next();
    if (!job) break;
    await runAction(job);
    state.lastActionAt = new Date().toISOString();
  }
}

// --- stateless: HTTP RPC, nothing holds the machine awake -------------------

async function startStateless(): Promise<void> {
  // A pool worker is addressed by its configured name (smm-worker-03), because
  // that name is what the hub's worker row and the ticket signature bind to.
  const workerId = config.workerId;
  configureAdapters({ workerId, screenshotDir: screenshotDir ?? '/tmp/smm-screenshots' });

  // One Xvfb display is the whole resource. Anything that draws on it — a job,
  // a parked login, or an operator's live view — must own it exclusively.
  let busy = false;
  let authAccountId: string | undefined;

  /**
   * Close the operator's live-view context before another account can use the
   * display.
   *
   * This is the isolation boundary, not a tidy-up: `manualContext` carries one
   * account's cookies, proxy, GPS and timezone. Reusing it for a second account
   * would show account A's logged-in session to an operator who opened account
   * B — the exact cross-account leak the per-context runtime exists to prevent.
   */
  const closeManualContext = async (): Promise<void> => {
    const ctx = manualContext;
    manualContext = undefined;
    manualAccountId = undefined;
    await ctx?.close().catch(() => undefined);
  };

  const rpc = createRpcServer({
    logger,
    internalSecret: config.internalSecret,
    workerId,
    // A live view holds the display too: a job that ran while an operator was
    // watching would interleave two accounts on one screen.
    isBusy: () => busy || authAccountId !== undefined || manualContext !== undefined,
    // The hub probes this to decide whether a parked login is still real.
    busyAccountId: () => authAccountId ?? manualAccountId ?? undefined,
    verifyTicket: (ticket) =>
      verifyLiveViewTicket(config.internalSecret, ticket, workerId, Math.floor(Date.now() / 1000)),

    runAction: async (job) => {
      busy = true;
      try {
        await runAction({
          id: job.jobId,
          accountId: job.runtime.accountId,
          platform: job.runtime.platform,
          action: job.action,
          targetUrl: job.targetUrl,
          attempt: job.attempt,
          ...(job.text ? { text: job.text } : {}),
          ...(job.templateId ? { templateId: job.templateId } : {}),
          ...(job.expectedMention ? { expectedMention: job.expectedMention } : {}),
          // Carried through the type, which the controller reads for contextFor.
          ...({ runtime: job.runtime } as { runtime: AccountRuntimeSpec }),
        });
      } finally {
        busy = false;
      }
      return undefined;
    },

    startLogin: async (runtime) => {
      // One display, one account. A parked login for a DIFFERENT account keeps
      // its context (its poll owns it); every manual live view is released,
      // this account's included, so the login builds the screen it will park.
      const parked = authContexts.get(runtime.accountId);
      if (manualAccountId === runtime.accountId) {
        const ctx = manualContext;
        manualContext = undefined;
        manualAccountId = undefined;
        if (ctx !== parked) await ctx?.close().catch(() => undefined);
      } else {
        await closeManualContext();
      }
      authAccountId = runtime.accountId;
      try {
        await startLogin(runtime.accountId, runtime, runtime.platform);
      } finally {
        authAccountId = undefined;
        // A login is SERIAL on this machine, so a context left behind blocks
        // every later login — including accounts that never had a lease to
        // release, which is how one abandoned attempt stranded the whole fleet.
        // Whatever the attempt parked (a CAPTCHA nobody answered, a form it
        // abandoned) is closed here.
        clearAuthContext(runtime.accountId);
      }
    },

    submitAuthInput: async (accountId, platform, value) => {
      const result = await submitAuthInput(accountId, value, platform, {
        browser,
        log: (msg: string, data?: Record<string, unknown>) => logger.warn(msg, data),
        workerId: config.workerId,
      });
      // The parked attempt owns the final callback; report only when nobody is
      // polling this context any more.
      if (!authAttempts.has(accountId)) await authCallback.post(accountId, result);
    },

    clearAuth: async (accountId) => {
      if (!accountId) {
        // Recovery sweep: drop the live view and every parked login. Safe
        // because this machine runs one account at a time, and the caller is
        // the hub with no lease left to name.
        authAttempts.clear();
        for (const id of authContexts.keys()) clearAuthContext(id);
        await closeManualContext();
        logger.info('cleared all auth and live-view contexts (recovery)');
        return;
      }
      authAttempts.delete(accountId);
      clearAuthContext(accountId);
      if (manualAccountId === accountId) await closeManualContext();
    },

    openBrowser: async (runtime, targetUrl) => {
      // A parked login owns the display: show ITS context untouched. Setting
      // `manualAccountId` here would let a later `closeManualContext()` (or the
      // account's own `clearAuth`) close the login's challenge out from under
      // the running poll.
      if (authContexts.has(runtime.accountId)) return;
      // Reuse the window only for the SAME account (a second click navigates
      // it). A different account gets a fresh context, always.
      const reusable = manualAccountId === runtime.accountId ? manualContext : undefined;
      if (!reusable) await closeManualContext();
      manualContext = await openLiveView(runtime, targetUrl, async () => ({}), reusable);
      manualAccountId = runtime.accountId;
    },
  });

  await rpc.listen(config.rpcHost, config.rpcPort);
  logger.info('worker rpc listening', { host: config.rpcHost, port: rpc.port() });

  process.on('SIGTERM', () => void rpc.close().finally(() => shutdown('SIGTERM')));
}

// --- shared helpers --------------------------------------------------------

let manualGeolocation: () => Promise<{ latitude: number; longitude: number } | null> = async () => null;

/**
 * Open (or reuse) the live-view context. The context is shared across calls on
 * purpose: a second click navigates the same window instead of stacking Chrome
 * windows on the display.
 *
 * Callers must have checked `authContexts` first: a parked login's context is
 * the live view, and opening the profile in it (or beside it) replaces the
 * OTP/CAPTCHA form the operator came for.
 */
async function openLiveView(
  runtime: AccountRuntimeSpec,
  targetUrl: string,
  legacy: () => Promise<{ storageState?: unknown; geolocation?: { latitude: number; longitude: number } | null }>,
  existing?: BrowserContext,
): Promise<BrowserContext> {
  let ctx = existing;
  if (!ctx) {
    const b = await browser();
    if (config.poolMode === 'stateless') {
      ctx = await newAccountContext(b, accountInit(runtime.platform, runtime, {}));
    } else {
      const identity = await legacy();
      ctx = await newAccountContext(b, accountInit(runtime.platform, undefined, identity));
    }
  }
  const page = ctx.pages()[0] ?? (await ctx.newPage());
  const target = targetUrl || defaultProfileURL(runtime.platform);
  // Fire navigation without blocking the RPC response: the window is already on
  // Xvfb, so the operator sees it load live in noVNC instead of hitting the 30s
  // workerrpc timeout while a residential proxy finishes the first TLS hop.
  void page.goto(target, { waitUntil: 'domcontentloaded', timeout: 60_000 }).catch(() => undefined);
  logger.info('live view opened', { url: target, platform: runtime.platform });
  return ctx;
}

function defaultProfileURL(platform: string): string {
  if (platform === 'instagram') return 'https://www.instagram.com/';
  if (platform === 'threads') return 'https://www.threads.net/';
  return 'about:blank';
}
