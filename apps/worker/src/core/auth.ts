/**
 * Login orchestration. Runs headful under Xvfb, polls for a success signal
 * (an auth cookie — never the URL), and parks the live context in `authContexts`
 * when operator input is required (2FA / checkpoint).
 *
 * The `authContexts` Map is the bridge between `auth-login` and the follow-up
 * `auth-input` instruction, so both must address the SAME context.
 *
 * Skeleton only: signatures fixed, behaviour lands with the login tickets.
 */
import type { BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

/** Live login contexts awaiting operator input, keyed by accountId. */
export const authContexts = new Map<string, BrowserContext>();

export type LoginOutcome = 'verified' | 'needs_input' | 'rejected' | 'failed';

export interface LoginResult {
  outcome: LoginOutcome;
  /** Resolved handle when `verified`. */
  handle?: string;
  /** Screenshot basename for the operator when `needs_input`. */
  screenshot?: string;
}

/** Start a login and block until an outcome is reached. */
export async function runLogin(accountId: string, platform: Platform): Promise<LoginResult> {
  void accountId;
  void platform;
  throw new Error('runLogin not implemented yet (P0-09 skeleton)');
}

/** Continue a parked login with operator-supplied input (e.g. a 2FA code). */
export async function submitAuthInput(
  accountId: string,
  value: string,
): Promise<LoginResult> {
  void accountId;
  void value;
  throw new Error('submitAuthInput not implemented yet (P0-09 skeleton)');
}

/** Drop a parked login context (used by `auth-clear`). */
export function clearAuthContext(accountId: string): void {
  authContexts.delete(accountId);
}
