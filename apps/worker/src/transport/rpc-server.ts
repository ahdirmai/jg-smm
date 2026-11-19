/**
 * Worker RPC server (stateless pool).
 *
 * Why this replaces the Redis pull loop: a boxd microVM only stops billing when
 * it sleeps, and it only sleeps when nothing holds the machine awake. A worker
 * that BLPOPs Redis never idles, and a hibernated worker cannot wake itself.
 * An inbound HTTPS request does both — the API does the waiting, the VM wakes
 * for one call (~85ms from hibernation) and sleeps again afterwards.
 *
 * Security posture: this listens on a machine with a PUBLIC hostname once nginx
 * publishes it, so every route except `/healthz` and the ticket check requires
 * `X-Internal-Secret`. Bodies are never logged: an action carries a session
 * cookie, a login carries a password.
 */
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { createHmac, timingSafeEqual } from 'node:crypto';

import type { Platform } from '@smm/shared';

import type { AccountRuntimeSpec, ActionResult } from '../types.js';
import type { Logger } from '../core/logger.js';

/** Header carrying the shared machine credential (same name the API sends). */
export const SECRET_HEADER = 'x-internal-secret';

/** Body cap. A dispatch carries a storageState; 2 MiB matches the API's cap. */
const MAX_BODY_BYTES = 2 << 20;

export interface RpcDeps {
  logger: Logger;
  /** The machine credential. Empty means every authed route answers 401. */
  internalSecret: string;
  /** This worker's name, bound into live-view tickets (`smm-worker-03`). */
  workerId: string;
  /**
   * True while an action or a login/manual session owns the display. A worker
   * has ONE Xvfb screen, so a second dispatch would interleave two accounts'
   * browsers on it.
   */
  isBusy: () => boolean;
  /**
   * Which account currently owns this display, if any.
   *
   * `isBusy()` alone cannot answer "is the session the hub thinks exists still
   * HERE?" — a worker that is merely healthy replies busy:false, and a hub that
   * trusts liveness alone keeps a dead parked login alive forever.
   */
  busyAccountId?: () => string | undefined;
  /** Run one action and report its verdict; resolves when the callback is sent. */
  runAction: (job: RpcActionJob) => Promise<ActionResult | undefined>;
  /** Open a headful login context for the account. */
  startLogin: (runtime: AccountRuntimeSpec) => Promise<void>;
  /** Deliver a 2FA / checkpoint code to the parked login. */
  submitAuthInput: (accountId: string, platform: Platform, value: string) => Promise<void>;
  /** Drop the parked context for the account. */
  clearAuth: (accountId: string) => Promise<void>;
  /** Navigate the manual live-view context to `runtime`'s profile. */
  openBrowser: (runtime: AccountRuntimeSpec, targetUrl: string) => Promise<void>;
  /** Verify a live-view ticket (nginx auth_request calls this). */
  verifyTicket: (ticket: string) => boolean;
}

/** The action shape the API pushes (port.ExecuteActionRequest). */
export interface RpcActionJob {
  jobId: string;
  action: string;
  targetUrl: string;
  text?: string;
  templateId?: string;
  expectedMention?: string;
  attempt: number;
  runtime: AccountRuntimeSpec;
}

export interface RpcServer {
  listen(host: string, port: number): Promise<void>;
  close(): Promise<void>;
  /** Bound port; useful when the caller asked for 0 (tests). */
  port(): number;
}

/** Constant-time string compare that tolerates different lengths. */
function safeEqual(a: string, b: string): boolean {
  const ab = Buffer.from(a);
  const bb = Buffer.from(b);
  if (ab.length !== bb.length) return false;
  return timingSafeEqual(ab, bb);
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    req.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('body too large'));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    req.on('error', reject);
  });
}

/**
 * `<expUnix>.<workerName>.<hex hmac>` — the signature is over
 * `"<expUnix>:<workerName>"` with the fleet secret, and the worker name is
 * checked against this machine so a ticket for worker-03 cannot open worker-07.
 * Kept in step with the API's service/novnc_ticket.go.
 */
export function verifyLiveViewTicket(secret: string, ticket: string, workerId: string, nowSec: number): boolean {
  if (!secret || !ticket) return false;
  const parts = ticket.split('.');
  if (parts.length !== 3) return false;
  const [exp, name, mac] = parts as [string, string, string];
  if (!exp || !name || !mac) return false;
  if (name !== workerId) return false;
  const expected = signTicket(secret, exp, name);
  if (!safeEqual(mac, expected)) return false;
  const expUnix = Number.parseInt(exp, 10);
  return Number.isFinite(expUnix) && nowSec < expUnix;
}

function signTicket(secret: string, exp: string, workerName: string): string {
  return createHmac('sha256', secret).update(`${exp}:${workerName}`).digest('hex');
}

function send(res: ServerResponse, status: number, body?: unknown): void {
  const payload = body === undefined ? '' : JSON.stringify(body);
  res.writeHead(status, {
    'content-type': 'application/json',
    'content-length': Buffer.byteLength(payload),
  });
  res.end(payload);
}

export function createRpcServer(deps: RpcDeps): RpcServer {
  const log = deps.logger;

  async function handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? '/', 'http://worker.invalid');
    const path = url.pathname;

    // Public by design: nginx probes it locally, and it exposes nothing but a
    // boolean.
    if (req.method === 'GET' && path === '/healthz') {
      send(res, 200, {
        status: 'ok',
        busy: deps.isBusy(),
        ...(deps.busyAccountId ? { accountId: deps.busyAccountId() ?? null } : {}),
      });
      return;
    }

    // nginx auth_request: allows the noVNC WebSocket only for a signed ticket.
    if (req.method === 'GET' && path === '/internal/verify-ticket') {
      const ok = deps.verifyTicket(url.searchParams.get('ticket') ?? '');
      send(res, ok ? 204 : 403);
      return;
    }

    // Every remaining route drives a logged-in browser.
    if (!deps.internalSecret || !safeEqual(req.headers[SECRET_HEADER] as string ?? '', deps.internalSecret)) {
      send(res, 401, { error: 'unauthorized' });
      return;
    }

    if (req.method !== 'POST') {
      send(res, 404, { error: 'not found' });
      return;
    }

    let body: unknown;
    try {
      const raw = await readBody(req);
      body = raw ? JSON.parse(raw) : {};
    } catch {
      send(res, 400, { error: 'invalid body' });
      return;
    }

    switch (path) {
      case '/internal/ping':
        // Reaching this handler IS the keepalive: the inbound request already
        // reset the VM's idle timer.
        send(res, 200, { status: 'ok' });
        return;

      case '/internal/execute-action': {
        const job = body as RpcActionJob;
        if (!job?.jobId || !job.runtime?.accountId) {
          send(res, 400, { error: 'jobId and runtime.accountId are required' });
          return;
        }
        // 409, not a queue: the scheduler reschedules, and accepting the job
        // here would run two accounts on one screen.
        if (deps.isBusy()) {
          log.info('execute-action refused: busy', { jobId: job.jobId });
          send(res, 409, { error: 'busy' });
          return;
        }
        send(res, 202, { accepted: true });
        // Detached on purpose: the verdict travels by callback, so holding the
        // API's request open for the length of a Playwright action would make
        // every dispatch pay the action's latency twice.
        void deps
          .runAction(job)
          .catch((err: unknown) =>
            log.error('execute-action failed', { jobId: job.jobId, error: String(err) }),
          );
        return;
      }

      case '/internal/start-login': {
        const runtime = body as AccountRuntimeSpec;
        if (!runtime?.accountId || !runtime.platform) {
          send(res, 400, { error: 'accountId and platform are required' });
          return;
        }
        // A login OWNS the display until it settles: opening it over a running
        // action would put two browsers on one screen.
        if (deps.isBusy()) {
          send(res, 409, { error: 'busy' });
          return;
        }
        send(res, 202, { accepted: true });
        // Log the start, not just the failure: a login that hangs inside
        // Playwright used to produce NO line at all, so "navigation failed:
        // Timeout" arrived at the operator with nothing in the worker log to
        // say when the attempt began or how long it had been running.
        log.info('start-login accepted', { accountId: runtime.accountId, platform: runtime.platform });
        void deps
          .startLogin(runtime)
          .then(() => {
            log.info('start-login finished', { accountId: runtime.accountId });
          })
          .catch((err: unknown) =>
            log.error('start-login failed', { accountId: runtime.accountId, error: String(err) }),
          );
        return;
      }

      case '/internal/auth-input': {
        const p = body as { accountId?: string; platform?: string; value?: string };
        if (!p.accountId || !p.platform || !p.value) {
          send(res, 400, { error: 'accountId, platform and value are required' });
          return;
        }
        await deps.submitAuthInput(p.accountId, p.platform as Platform, p.value);
        send(res, 200, { status: 'ok' });
        return;
      }

      case '/internal/auth-clear': {
        const p = body as { accountId?: string };
        // No accountId = release EVERYTHING this machine holds. A worker serves
        // one account at a time, and the recovery case is exactly "the slot is
        // gone, but this display still believes it is busy" — with no id left
        // to name, that would otherwise need a container restart.
        await deps.clearAuth(p.accountId ?? '');
        send(res, 200, { status: 'ok' });
        return;
      }

      case '/internal/browser-open': {
        const p = body as { runtime?: AccountRuntimeSpec; targetUrl?: string };
        if (!p.runtime?.accountId) {
          send(res, 400, { error: 'runtime.accountId is required' });
          return;
        }
        // The live view is a human's window: busy never blocks it, because the
        // operator opened it ON PURPOSE and superseding is the point.
        await deps.openBrowser(p.runtime, p.targetUrl ?? '');
        send(res, 200, { status: 'ok' });
        return;
      }

      default:
        send(res, 404, { error: 'not found' });
    }
  }

  const server = createServer((req, res) => {
    handle(req, res).catch((err: unknown) => {
      // Never echo the error text: it may quote a payload.
      log.error('rpc handler error', { error: String(err) });
      if (!res.headersSent) send(res, 500, { error: 'internal error' });
      else res.end();
    });
  });

  return {
    listen(host, port) {
      // Lib target is ES2022, so Promise.withResolvers is unavailable.
      return new Promise<void>((resolve, reject) => {
        server.once('error', reject);
        server.listen(port, host, () => {
          server.off('error', reject);
          resolve();
        });
      });
    },
    close() {
      return new Promise<void>((resolve) => server.close(() => resolve()));
    },
    port() {
      const addr = server.address();
      return typeof addr === 'object' && addr ? addr.port : 0;
    },
  };
}
