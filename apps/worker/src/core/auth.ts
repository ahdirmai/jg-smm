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

import { FIXED_VIEWPORT } from './browser.js';
import { readSession, writeSession } from './session.js';

/** Live login contexts awaiting operator input, keyed by accountId. */
export const authContexts = new Map<string, BrowserContext>();

export type LoginOutcome = 'verified' | 'needs_input' | 'rejected' | 'failed';

export interface LoginResult {
  outcome: LoginOutcome;
  /** Resolved session cookie value when `verified`. */
  handle?: string;
  /** Screenshot basename for the operator when `needs_input`. */
  screenshot?: string;
}

export interface AuthDeps {
  /** Resolve the browser the login runs in. */
  browser: () => Promise<Browser>;
  /** Where screenshots land (basename only in the result). */
  screenshotDir?: string;
  /** Injectable clock for deterministic tests. */
  now?: () => number;
  /** Injectable hook to override the per-platform login URL (tests). */
  loginUrlFor?: (platform: Platform) => string;
}

/**
 * The cookies that prove a session per platform. A login is `verified` only
 * when all of them are present — never when the URL merely looks logged in.
 */
const SESSION_COOKIES: Partial<Record<Platform, string[]>> = {
  instagram: ['sessionid', 'ds_user_id'],
  threads: ['sessionid'],
};

const POLL_INTERVAL_MS = 1_000;
/** Upper bound on a login attempt before it is reported `failed`. */
const LOGIN_TIMEOUT_MS = 180_000;

/**
 * Start a login and block until an outcome is reached. The worker never holds
 * account passwords: the credential is entered by the operator in the headful
 * session the noVNC view exposes.
 */
export async function runLogin(
  accountId: string,
  platform: Platform,
  deps: AuthDeps,
): Promise<LoginResult> {
  const browser = await deps.browser();
  const ctx = await browser.newContext({ viewport: FIXED_VIEWPORT });
  authContexts.set(accountId, ctx);

  const page = await ctx.newPage();
  try {
    await page.goto(loginUrlFor(platform, deps), { waitUntil: 'domcontentloaded' });
    return await waitForOutcome(accountId, platform, ctx, deps);
  } catch {
    return failedResult(accountId);
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
  deps: AuthDeps,
): Promise<LoginResult> {
  const ctx = authContexts.get(accountId);
  if (!ctx) return failedResult(accountId);

  const pages = ctx.pages();
  const page = pages[pages.length - 1];
  if (!page) return failedResult(accountId);

  const platform = platformFromUrl(page.url());
  try {
    const field = page.locator(
      'input[name="verificationCode"], input[autocomplete="one-time-code"]',
    );
    await field.fill(value);
    await field.press('Enter');
    return await waitForOutcome(accountId, platform, ctx, deps);
  } catch {
    return failedResult(accountId);
  }
}

/** Drop a parked login context (used by `auth-clear`). */
export function clearAuthContext(accountId: string): void {
  const ctx = authContexts.get(accountId);
  authContexts.delete(accountId);
  void ctx?.close().catch(() => undefined);
}

async function waitForOutcome(
  accountId: string,
  platform: Platform,
  ctx: BrowserContext,
  deps: AuthDeps,
): Promise<LoginResult> {
  const started = (deps.now ?? Date.now)();
  const wanted = SESSION_COOKIES[platform] ?? [];

  for (;;) {
    if ((deps.now ?? Date.now)() - started > LOGIN_TIMEOUT_MS) {
      return failedResult(accountId);
    }

    const cookies = await ctx.cookies();
    const have = new Set(cookies.map((c) => c.name));
    const proven = wanted.length > 0 && wanted.every((name) => have.has(name));
    if (proven) {
      // Persist so a restart needs no re-login. A disk failure must never
      // downgrade a verified login to failed — the session is an optimisation.
      try {
        await writeSession(platform, await ctx.storageState());
      } catch {
        // Best effort; the login itself already succeeded.
      }
      const handle = cookies.find((c) => c.name === wanted[0])?.value;
      clearAuthContext(accountId);
      return { outcome: 'verified', ...(handle ? { handle } : {}) };
    }

    if (await needsOperatorInput(ctx)) {
      const shot = await takeScreenshot(ctx, accountId, deps);
      return { outcome: 'needs_input', screenshot: shot };
    }

    await sleep(POLL_INTERVAL_MS);
  }
}

async function needsOperatorInput(ctx: BrowserContext): Promise<boolean> {
  for (const page of ctx.pages()) {
    const count = await page
      .locator('input[name="verificationCode"], input[autocomplete="one-time-code"]')
      .count()
      .catch(() => 0);
    if (count > 0) return true;
  }
  return false;
}

async function takeScreenshot(
  ctx: BrowserContext,
  accountId: string,
  deps: AuthDeps,
): Promise<string> {
  const basename = `${accountId}-${(deps.now ?? Date.now)()}.png`;
  const page = ctx.pages()[ctx.pages().length - 1];
  if (page && deps.screenshotDir) {
    const { mkdir, writeFile } = await import('node:fs/promises');
    const { join } = await import('node:path');
    const buf = await page.screenshot().catch(() => Buffer.alloc(0));
    await mkdir(deps.screenshotDir, { recursive: true });
    await writeFile(join(deps.screenshotDir, basename), buf);
  }
  return basename;
}

function loginUrlFor(platform: Platform, deps: AuthDeps): string {
  if (deps.loginUrlFor) return deps.loginUrlFor(platform);
  switch (platform) {
    case 'instagram':
      return 'https://www.instagram.com/accounts/login/';
    case 'threads':
      return 'https://www.threads.net/login';
    default:
      return `https://${platform}.com/login`;
  }
}

/** Best-effort platform guess for a parked context, from the open page host. */
function platformFromUrl(url: string): Platform {
  if (url.includes('instagram')) return 'instagram';
  if (url.includes('threads')) return 'threads';
  return 'instagram';
}

function failedResult(accountId: string): LoginResult {
  clearAuthContext(accountId);
  return { outcome: 'failed' };
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

// Re-exported for the action path, which hydrates a context from the persisted
// state before running a job.
export { readSession };
