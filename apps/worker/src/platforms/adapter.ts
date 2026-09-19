/**
 * The single adapter contract every platform implements (LSP). The controller
 * only ever talks to this interface, never to a platform's DOM details (DIP),
 * and adding a platform means adding a file + one registry entry (OCP).
 */
import type { BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

import type { ActionJob } from '../types.js';

export interface AdapterResult {
  ok: boolean;
  /** Text observed on the feed when verification succeeds. */
  renderedText?: string;
  screenshot?: string;
  error?: string;
}

export interface PlatformAdapter {
  readonly platform: Platform;
  /**
   * The platform's credential login URL. Owned by the adapter so the operator
   * headful flow (`core/auth`) and the adapter's own `login` open the same page
   * — one definition per platform (DRY), no `https://${platform}.com/login`
   * guesswork in the controller.
   */
  readonly loginUrl: string;
  /**
   * The cookies that prove a *verified* login on this platform. A login is
   * `verified` only when all of them are present — never when the URL merely
   * looks logged in. `core/auth` reads this as the single cookie standard.
   */
  readonly sessionCookies: string[];
  /**
   * CSS for the platform's 2FA / checkpoint code field. Exposed for callers
   * that only need the selector; the methods below are what the flow uses.
   */
  readonly otpFieldSelector: string;
  /** Perform a full credential login and return the resolved handle. */
  login(ctx: BrowserContext, credentials: LoginCredentials): Promise<LoginResultLike>;
  /** Like the target post. */
  like(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
  /** Comment on the target post with `job.text`. */
  comment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
  /** Confirm the effect actually landed (ground truth). */
  verify(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
  /**
   * True when a 2FA / checkpoint field is showing and the operator must supply a
   * code. The Meta adapters use the shared default (`otpHandlers`); a platform
   * with bespoke markup overrides it.
   */
  detectAuthInput(ctx: BrowserContext): Promise<boolean>;
  /**
   * Fill and submit the operator-supplied code into the platform's field. The
   * default fills a single input and presses Enter; a multi-box SMS challenge
   * supplies its own implementation.
   */
  submitAuthInput(ctx: BrowserContext, value: string): Promise<void>;
}

export interface LoginCredentials {
  username: string;
  password: string;
}

export interface LoginResultLike {
  outcome: 'verified' | 'needs_input' | 'rejected' | 'failed';
  handle?: string;
  screenshot?: string;
}
