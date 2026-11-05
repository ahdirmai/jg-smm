/**
 * Login orchestration (P1-10 / P1-12). Runs headful under Xvfb, polls for a
 * success signal — an auth cookie, never the URL — and parks the live context
 * in `authContexts` when operator input is required (2FA / checkpoint).
 *
 * `authContexts` is the bridge between an `auth-login` instruction and the
 * follow-up `auth-input`: both must address the SAME context, so the parked
 * context is keyed by accountId and kept alive across the gap.
 */
import type { Browser, BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

import type { LoginCredentials, PlatformAdapter } from '../platforms/adapter.js';
import { META_OTP_SELECTOR, detectAuthInputWith, submitAuthInputWith } from '../platforms/otp.js';
import { newAccountContext } from './browser.js';
import type { Geolocation } from './geolocation.js';
import { captureScreenshot } from './screenshot.js';
import { readSession, writeSession } from './session.js';
import { blockReason, detectLoginBlock } from './login-block.js';

/** Live login contexts awaiting operator input, keyed by accountId. */
export const authContexts = new Map<string, BrowserContext>();
// Code submission joins the existing poll instead of racing it to close and
// persist the same context twice.
const authOutcomes = new WeakMap<BrowserContext, Promise<LoginResult>>();

export type LoginOutcome = 'verified' | 'needs_input' | 'rejected' | 'failed';

export interface LoginResult {
  outcome: LoginOutcome;
  /** Resolved session cookie value when `verified`. */
  handle?: string;
  /** Screenshot basename for the operator when `needs_input`. */
  screenshot?: string;
  /**
   * Epoch ms the session cookie expires, when the platform gave one (P2-06).
   * Drives the refresh schedule: the API stores it and dispatches the next
   * refresh a week ahead. Absent for a session cookie (`expires: -1`), which
   * cannot be scheduled against — such an account is refreshed only by hand.
   */
  expiresAt?: number;
  /**
   * Failure diagnosis or manual challenge instructions. Crosses the wire as
   * the callback's `error` so the dashboard can show the next operator action.
   */
  reason?: string;
}

export interface AuthDeps {
  /** Resolve the browser the login runs in. */
  browser: () => Promise<Browser>;
  /** The worker's frozen GPS point, so the login context reports the same
   * position the action contexts will. Null/absent leaves the real one. */
  geolocation?: (() => Promise<Geolocation | null>) | undefined;
  /** Where screenshots land (basename only in the result). */
  screenshotDir?: string;
  /** Where persisted sessions live. Defaults to SESSION_DIR / /data/sessions. */
  sessionDir?: string;
  /** Worker id, for the deterministic screenshot name (§7.3). */
  workerId?: string;
  /** Injectable clock for deterministic tests. */
  now?: () => number;
  /** Injectable hook to override the per-platform login URL (tests). */
  loginUrlFor?: (platform: Platform) => string;
  /** Diagnostics sink: the operator only sees the final verdict, so the
   * failure reason must at least reach the container log. */
  log?: (msg: string, data?: Record<string, unknown>) => void;
  /** Called with the freshly persisted storageState after a VERIFIED login.
   * The worker cannot store it durably itself: the volume lives with the
   * container, and the container is recreated whenever the fleet rebalances.
   * Absent in tests and in any caller that has no API to report to. */
  onSession?: (accountId: string, platform: Platform, state: unknown) => Promise<void> | void;
  /** Publish the parked challenge while this login keeps watching for proof. */
  onNeedsInput?: (accountId: string, result: LoginResult) => Promise<void> | void;
}

/**
 * Resolve a platform's adapter lazily. `core/auth` is imported by the adapters'
 * DOM layer (`dom.ts` → `pollLoginOutcome`), so a *static* registry import would
 * close an eval-time cycle: the registry builds its map from the very adapter
 * consts that are still initialising, and touching it too early throws a TDZ
 * `ReferenceError`. A dynamic import defers the lookup to call time — when the
 * registry is fully built — and Node caches the module after the first call, so
 * the poll loop pays nothing after warm-up. The adapter is the single source of
 * truth for a platform's proof cookies, 2FA selector and login URL.
 */
async function adapterFor(platform: Platform): Promise<PlatformAdapter | undefined> {
  const { getAdapter } = await import('../platforms/registry.js');
  return getAdapter(platform);
}

const POLL_INTERVAL_MS = 1_000;
const OTP_REQUIRED_REASON = 'otp_required: perlu OTP';
/** Upper bound on a login attempt before it is reported `failed`. */
export const LOGIN_TIMEOUT_MS = 180_000;
/** Time allowed for an operator to finish a challenge directly in noVNC. */
export const MANUAL_LOGIN_TIMEOUT_MS = 10 * 60_000;

export interface SessionVerdict {
  proven: boolean;
  /** Session cookie value when `proven`. */
  handle?: string;
  /** A 2FA/checkpoint field is showing: operator input needed. */
  needsInput: boolean;
  /** Epoch ms the first-required cookie expires (P2-06); see `LoginResult`. */
  expiresAt?: number;
}

/**
 * The soonest a *required* cookie dies, in epoch ms, or undefined when none of
 * them carries a date. Session cookies (`expires: -1`) never constrain the
 * schedule — Playwright reports them as -1, which is not a timestamp.
 *
 * The minimum, not the first: a session stays proof only while EVERY required
 * cookie is present, so the earliest expiry is when proof actually breaks.
 */
function soonestExpiry(
  cookies: readonly { name: string; expires: number }[],
  wanted: readonly string[],
): number | undefined {
  const dated = cookies
    .filter((c) => wanted.includes(c.name) && c.expires > 0)
    .map((c) => c.expires * 1000);
  return dated.length > 0 ? Math.min(...dated) : undefined;
}

/**
 * Probe the live context for session proof. Exported so the adapter login path
 * (P3-04/P3-05) verifies a login by the same cookie standard as the operator
 * headful flow — one definition of "logged in" (DRY).
 */
export async function probeSession(
  platform: Platform,
  ctx: BrowserContext,
): Promise<SessionVerdict> {
  const adapter = await adapterFor(platform);
  const wanted = adapter?.sessionCookies ?? [];
  const cookies = await ctx.cookies();
  const have = new Set(cookies.map((c) => c.name));
  const proven = wanted.length > 0 && wanted.every((name) => have.has(name));
  const handle = cookies.find((c) => c.name === wanted[0])?.value;
  const expiresAt = soonestExpiry(cookies, wanted);
  return {
    proven,
    needsInput: await detectAuthInput(ctx, adapter),
    ...(handle ? { handle } : {}),
    ...(expiresAt !== undefined ? { expiresAt } : {}),
  };
}

/**
 * Poll the session until it is proven, input is requested, or the bound runs
 * out. Pure: it never parks or closes a context, so callers own that policy
 * (the operator flow parks; the adapter flow does not).
 */
export async function pollLoginOutcome(
  platform: Platform,
  ctx: BrowserContext,
  deps: { now?: () => number } = {},
): Promise<LoginResult> {
  const adapter = await adapterFor(platform);
  const wanted = adapter?.sessionCookies ?? [];
  const started = (deps.now ?? Date.now)();
  // The last page state observed while polling. A timeout verdict without this
  // is the "login did not complete" mystery the operator could not act on: the
  // reason a form is still unproven is nearly always visible in the URL or the
  // missing cookies, and both are free to capture on the final pass.
  let lastURL = '';
  for (;;) {
    if ((deps.now ?? Date.now)() - started > LOGIN_TIMEOUT_MS) {
      return {
        outcome: 'failed',
        reason: await describeFailedLogin(platform, ctx, lastURL, wanted),
      };
    }
    const verdict = await probeSession(platform, ctx);
    if (verdict.proven) {
      return {
        outcome: 'verified',
        ...(verdict.handle ? { handle: verdict.handle } : {}),
        ...(verdict.expiresAt !== undefined ? { expiresAt: verdict.expiresAt } : {}),
      };
    }
    if (verdict.needsInput) return { outcome: 'failed', reason: OTP_REQUIRED_REASON };
    // Credentials and rate limits fail promptly. Human challenges keep the
    // browser parked so the operator can complete them through noVNC.
    const block = await detectLoginBlock(ctx);
    if (block) {
      if (block === 'captcha' || block === 'checkpoint') {
        return {
          outcome: 'needs_input',
          reason: blockReason(block, 'complete the challenge in the live browser'),
        };
      }
      return {
        outcome: 'failed',
        reason:
          block === 'otp_required'
            ? OTP_REQUIRED_REASON
            : blockReason(block, 'stopped at login wall'),
      };
    }
    // Cheap and non-throwing: the page may be mid-navigation, which is itself
    // a state worth reporting if the poll then times out.
    const pages = ctx.pages();
    const page = pages[pages.length - 1];
    if (page) {
      try {
        lastURL = page.url();
      } catch {
        // A closed page between the take and the read: keep the last good URL.
      }
    }
    await sleep(POLL_INTERVAL_MS);
  }
}

/**
 * Turn a timed-out login into an actionable sentence. Every branch here is a
 * distinct operator action, so the string is not a formatted error — it is the
 * diagnosis:
 *
 *   - still on the login URL  -> the submit never landed (bad credential, a
 *     challenge that never rendered an OTP field, or a dead form)
 *   - past login, cookies short -> the platform accepted the submit but did
 *     not grant every required cookie, i.e. an incomplete/2FA interstitial
 *   - every cookie present but unproven is unreachable (proven would have
 *     returned), so that case means the context lost them mid-poll
 */
async function describeFailedLogin(
  platform: Platform,
  ctx: BrowserContext,
  lastURL: string,
  wanted: readonly string[],
): Promise<string> {
  const cookies = await ctx.cookies().catch(() => []);
  const have = new Set(cookies.map((c) => c.name));
  const missing = wanted.filter((name) => !have.has(name));
  const onLoginPage = lastURL.includes('/accounts/login') || lastURL.endsWith('/login');
  if (onLoginPage) {
    return missing.length > 0
      ? `submit did not complete: still on ${lastURL || 'the login page'}, missing cookie(s) ${missing.join(', ')}`
      : `submit did not complete: still on ${lastURL || 'the login page'}`;
  }
  if (missing.length > 0) {
    return `session incomplete: left ${lastURL || 'the login page'} but missing cookie(s) ${missing.join(', ')} (platform handed a partial session, likely a verification interstitial)`;
  }
  return `login timed out on ${platform} after ${Math.round(LOGIN_TIMEOUT_MS / 1000)}s without session proof (url ${lastURL || 'unknown'})`;
}
/**
 * Start a login and block until an outcome is reached. When the API delivers
 * the operator's credentials on the dispatch they are filled into the login
 * form (the platform adapter owns the markup); otherwise the operator types
 * them in the headful session the noVNC view exposes. Either way the verdict
 * is the cookie poll — never the URL.
 */
export async function runLogin(
  accountId: string,
  platform: Platform,
  deps: AuthDeps,
  credentials?: LoginCredentials,
): Promise<LoginResult> {
  clearAuthContext(accountId);
  const browser = await deps.browser();
  // Hydrate the persisted session before the first poll. This is what makes a
  // refresh autonomous: `pollLoginOutcome` proves a session by the presence of
  // the platform's auth cookies, so a context hydrated from disk is proven on
  // the first pass and never reaches the operator. Without it every invocation
  // — including a scheduled P2-06 refresh of a perfectly healthy account —
  // would open a bare login page and park on `needs_input` for a human.
  //
  // Same context shape the action path uses (locale, UA, geolocation), so the
  // platform sees one consistent "device" across login and action.
  const stored = await readSession(platform, deps.sessionDir).catch(() => undefined);
  const ctx = await newAccountContext(
    browser,
    platform,
    stored,
    await deps.geolocation?.().catch(() => null),
  );
  authContexts.set(accountId, ctx);

  const page = await ctx.newPage();
  try {
    await page.goto(await loginUrlFor(platform, deps), { waitUntil: 'domcontentloaded' });
    // A still-valid hydrated session is redirected away from the login page,
    // so there is no form to fill: waiting out the mount bound here only
    // stalls every scheduled refresh (and logs a misleading "field not
    // found"). A stale session — or a first login, where nothing was stored —
    // leaves the form in place and the fill runs normally.
    const hydratedProven = stored ? (await probeSession(platform, ctx)).proven : false;
    if (credentials?.username && credentials.password && !hydratedProven) {
      // Best effort: a fill failure (a challenge wall instead of the form, a
      // markup change) must not kill the login — the operator can still type
      // in the live view, and the poll below is the only verdict authority.
      //
      // The mount wait inside the adapter matters: Threads serves /login as a
      // client-rendered SPA, so at `domcontentloaded` the page is still the
      // "Scan to get the app" shell and the fields appear ~3-10s later.
      // Filling in that window was the "login form field not found" failure.
      try {
        const adapter = await adapterFor(platform);
        await adapter?.autofill?.(page, credentials);
      } catch (err) {
        deps.log?.('auth-login: credential autofill failed, operator can type in the live view', {
          accountId,
          error: err instanceof Error ? err.message : String(err),
        });
      }
    }
    return await waitForOutcome(accountId, platform, ctx, deps);
  } catch (err) {
    // Swallowing this was the "login did not complete" mystery: the operator
    // sees the verdict, the container log must carry the reason.
    const detail = err instanceof Error ? err.message : String(err);
    deps.log?.('auth-login: navigation or session probe failed', {
      accountId,
      error: detail,
    });
    // Carry the reason out with the verdict, not just into the log. The log is
    // only reachable by someone with shell access to the container; the row in
    // the dashboard is what the operator actually looks at, and a bare
    // "failed" there is unactionable.
    return {
      ...failedResult(accountId, ctx),
      reason: `navigation failed: ${detail.split('\n')[0]}`,
    };
  }
}

/**
 * Continue a parked login with operator-supplied input (a 2FA code or
 * checkpoint answer). The context must still be parked; a missing one is a
 * `failed`, never a crash.
 */
export async function submitAuthInput(
  accountId: string,
  value: string,
  platform: Platform,
  deps: AuthDeps,
): Promise<LoginResult> {
  const ctx = authContexts.get(accountId);
  if (!ctx) return failedResult(accountId);

  const pages = ctx.pages();
  const page = pages[pages.length - 1];
  if (!page) return failedResult(accountId);

  // The platform is carried by the `auth-input` control message — the worker
  // never guesses it from the open URL. Delegate the fill/submit to the
  // platform's adapter so a bespoke (multi-box) challenge is handled per
  // platform; fall back to the Meta default only for an unregistered platform.
  try {
    const adapter = await adapterFor(platform);
    if (adapter) await adapter.submitAuthInput(ctx, value);
    else await submitAuthInputWith(ctx, value, META_OTP_SELECTOR);
    return await waitForOutcome(accountId, platform, ctx, deps);
  } catch {
    return failedResult(accountId, ctx);
  }
}

/** Drop a parked login context (used by `auth-clear`). */
export function clearAuthContext(accountId: string): void {
  const ctx = authContexts.get(accountId);
  authContexts.delete(accountId);
  void ctx?.close().catch(() => undefined);
}

function waitForOutcome(
  accountId: string,
  platform: Platform,
  ctx: BrowserContext,
  deps: AuthDeps,
): Promise<LoginResult> {
  const existing = authOutcomes.get(ctx);
  if (existing) return existing;
  const pending = resolveOutcome(accountId, platform, ctx, deps).finally(() => {
    authOutcomes.delete(ctx);
  });
  authOutcomes.set(ctx, pending);
  return pending;
}

async function resolveOutcome(
  accountId: string,
  platform: Platform,
  ctx: BrowserContext,
  deps: AuthDeps,
): Promise<LoginResult> {
  let outcome = await pollLoginOutcome(platform, ctx, deps.now ? { now: deps.now } : {});
  if (outcome.outcome === 'needs_input') {
    const shot = await takeScreenshot(ctx, accountId, deps);
    const input: LoginResult = {
      ...outcome,
      reason: outcome.reason ?? 'verification: complete verification in the live browser',
      ...(shot ? { screenshot: shot } : {}),
    };
    // Standalone callers keep the parked-context contract. The live worker
    // publishes NEEDS_INPUT immediately and then watches manual completion.
    if (!deps.onNeedsInput) return input;
    await deps.onNeedsInput(accountId, input);
    outcome = await waitForManualCompletion(accountId, platform, ctx, deps);
  }
  // A restart/clear supersedes this attempt. Its caller suppresses stale
  // callbacks; it must never close a newer attempt's browser.
  if (authContexts.get(accountId) !== ctx) {
    return { outcome: 'failed', reason: 'login attempt was cancelled' };
  }
  if (outcome.outcome === 'verified') {
    // Persist so a restart needs no re-login. A disk failure must never
    // downgrade a verified login to failed — the session is an optimisation.
    try {
      const state = await ctx.storageState();
      await writeSession(platform, state, deps.sessionDir);
      // Report it for durable storage. The volume copy serves this container;
      // this copy is what survives the container being recreated, which the
      // PAUSED/RUNNING fleet does routinely.
      if (deps.onSession) {
        try {
          await deps.onSession(accountId, platform, state);
        } catch (err) {
          deps.log?.('login session report failed', { err: String(err) });
        }
      }
    } catch {
      // Best effort; the login itself already succeeded.
    }
  }
  if (authContexts.get(accountId) === ctx) clearAuthContext(accountId);
  return outcome;
}

async function waitForManualCompletion(
  accountId: string,
  platform: Platform,
  ctx: BrowserContext,
  deps: AuthDeps,
): Promise<LoginResult> {
  const now = deps.now ?? Date.now;
  const started = now();
  for (;;) {
    if (authContexts.get(accountId) !== ctx) {
      return { outcome: 'failed', reason: 'login attempt was cancelled' };
    }
    if (now() - started > MANUAL_LOGIN_TIMEOUT_MS) {
      return { outcome: 'failed', reason: 'manual verification timed out: start the login again' };
    }
    const verdict = await probeSession(platform, ctx);
    if (verdict.proven) {
      return {
        outcome: 'verified',
        ...(verdict.handle ? { handle: verdict.handle } : {}),
        ...(verdict.expiresAt !== undefined ? { expiresAt: verdict.expiresAt } : {}),
      };
    }
    if (verdict.needsInput) return { outcome: 'failed', reason: OTP_REQUIRED_REASON };
    const block = await detectLoginBlock(ctx);
    if (block && block !== 'captcha' && block !== 'checkpoint') {
      return {
        outcome: 'failed',
        reason:
          block === 'otp_required'
            ? OTP_REQUIRED_REASON
            : blockReason(block, 'stopped at login wall'),
      };
    }
    await sleep(POLL_INTERVAL_MS);
  }
}

/**
 * Whether a 2FA/checkpoint field is showing. Delegates to the platform's
 * adapter so each platform owns its own selector (DRY with the action path);
 * an unregistered platform falls back to the Meta default rather than crashing.
 */
function detectAuthInput(ctx: BrowserContext, adapter?: PlatformAdapter): Promise<boolean> {
  if (adapter) return adapter.detectAuthInput(ctx);
  return detectAuthInputWith(ctx, META_OTP_SELECTOR);
}

async function takeScreenshot(
  ctx: BrowserContext,
  accountId: string,
  deps: AuthDeps,
): Promise<string | undefined> {
  const page = ctx.pages()[ctx.pages().length - 1];
  if (!page) return undefined;
  // CDP capture (§7.3): page.screenshot() waits on font settle and times out
  // on a Meta page. Never throws — the login verdict does not depend on it.
  return await captureScreenshot(
    page,
    accountId,
    deps.workerId ?? 'login',
    deps.screenshotDir ? { dir: deps.screenshotDir } : {},
  );
}

/**
 * The platform's login URL, owned by its adapter (DRY). The test hook still
 * wins so the poll loop can be driven against a fixture; the `https://…` guess
 * is a last resort for a platform with no registered adapter.
 */
async function loginUrlFor(platform: Platform, deps: AuthDeps): Promise<string> {
  if (deps.loginUrlFor) return deps.loginUrlFor(platform);
  return (await adapterFor(platform))?.loginUrl ?? `https://www.${platform}.com/login`;
}

function failedResult(accountId: string, expected?: BrowserContext): LoginResult {
  if (!expected || authContexts.get(accountId) === expected) clearAuthContext(accountId);
  return { outcome: 'failed' };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Re-exported for the action path, which hydrates a context from the persisted
// state before running a job.
export { readSession };
