/**
 * Auth outcome callback (P1-11 / P1-12). Reports the result of an operator
 * headful login to /internal/account-callback. The API never holds the
 * credential — only the outcome — so this is the only place a login verdict
 * crosses the wire.
 *
 * Fails soft, like the action callback: 3× linear backoff, stop on 4xx, never
 * throws. A login that succeeded but could not be reported is still logged, so
 * the operator is not falsely told the login failed.
 */
import type { Logger } from '../core/logger.js';
import type { LoginResult, LoginOutcome } from '../core/auth.js';
import type { WorkerConfig } from '../types.js';

export interface AuthCallbackOptions {
  maxAttempts?: number;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

/** The wire enum the API expects (AuthStatus in openapi.yaml). */
const OUTCOME_TO_STATUS: Record<LoginOutcome, string> = {
  verified: 'AUTHENTICATED',
  needs_input: 'NEEDS_INPUT',
  rejected: 'FAILED',
  failed: 'FAILED',
};

function backoffMs(attempt: number): number {
  return attempt * 500;
}

export function createAuthCallback(
  config: WorkerConfig,
  logger: Logger,
  options: AuthCallbackOptions = {},
) {
  const maxAttempts = options.maxAttempts ?? 3;
  const timeoutMs = options.timeoutMs ?? 8_000;
  const doFetch = options.fetchImpl ?? fetch;
  const url = `${config.apiUrl.replace(/\/+$/, '')}/internal/account-callback`;

  async function post(accountId: string, result: LoginResult): Promise<void> {
    const authStatus = OUTCOME_TO_STATUS[result.outcome];
    const body = JSON.stringify({
      accountId,
      authStatus,
      ...(result.handle ? { handle: result.handle } : {}),
      ...(result.outcome === 'failed' ? { error: 'login did not complete' } : {}),
    });
    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
      try {
        const res = await doFetch(url, {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body,
          signal: AbortSignal.timeout(timeoutMs),
        });
        if (res.ok) return;
        // A 4xx is permanent: the account may be gone, for instance. Retrying
        // cannot fix it, and a rejected login is not worth a tight loop.
        if (res.status >= 400 && res.status < 500) {
          logger.warn('auth callback rejected', { accountId, authStatus, status: res.status });
          return;
        }
        throw new Error(`auth callback status ${res.status}`);
      } catch (err) {
        logger.warn('auth callback attempt failed', {
          accountId,
          authStatus,
          attempt,
          error: (err as Error).message,
        });
        if (attempt < maxAttempts) {
          await new Promise((r) => setTimeout(r, backoffMs(attempt)));
        }
      }
    }
    // The login verdict is lost, not the login. Say so explicitly: the
    // dashboard will keep showing AUTHENTICATING, which is wrong but fixable.
    logger.error('auth callback exhausted retries', { accountId, authStatus });
  }

  return { post };
}
