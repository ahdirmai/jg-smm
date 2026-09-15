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
  /** Perform a full credential login and return the resolved handle. */
  login(ctx: BrowserContext, credentials: LoginCredentials): Promise<LoginResultLike>;
  /** Like the target post. */
  like(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
  /** Comment on the target post with `job.text`. */
  comment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
  /** Confirm the effect actually landed (ground truth). */
  verify(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult>;
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
