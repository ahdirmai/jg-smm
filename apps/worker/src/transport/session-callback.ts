/**
 * Session export callback (P-C). Carries an account's persisted browser session
 * — its `storageState()`, i.e. the platform cookies — back to the API so an
 * operator can re-import it into a fresh container before deleting this one.
 *
 * Cookies are CREDENTIALS. This is the only place a session crosses the wire,
 * it targets the private-network /internal endpoint, and the session value is
 * NEVER logged (only the accountId / requestId are, so a lost callback is
 * traceable without leaking the secret).
 *
 * Fails soft like the other callbacks: 3× linear backoff, stop on 4xx, never
 * throws — a failed export must not crash the control handler.
 */
import type { Logger } from '../core/logger.js';
import type { WorkerConfig } from '../types.js';

export interface SessionExportPayload {
  accountId: string;
  platform: string;
  /** Correlates the dump with the operator request waiting on the API. */
  requestId: string;
  /** The storageState JSON, or null when no session is persisted. */
  session: unknown | null;
}

export interface SessionCallbackOptions {
  maxAttempts?: number;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

function backoffMs(attempt: number): number {
  return attempt * 500;
}

export function createSessionCallback(
  config: WorkerConfig,
  logger: Logger,
  options: SessionCallbackOptions = {},
) {
  const maxAttempts = options.maxAttempts ?? 3;
  const timeoutMs = options.timeoutMs ?? 8_000;
  const doFetch = options.fetchImpl ?? fetch;
  const url = `${config.apiUrl.replace(/\/+$/, '')}/internal/session-export`;

  async function post(payload: SessionExportPayload): Promise<void> {
    const body = JSON.stringify(payload);
    for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
      try {
        const res = await doFetch(url, {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body,
          signal: AbortSignal.timeout(timeoutMs),
        });
        if (res.ok) return;
        if (res.status >= 400 && res.status < 500) {
          // The waiter may be gone (timed out); retrying cannot help.
          logger.warn('session export rejected', {
            accountId: payload.accountId,
            requestId: payload.requestId,
            status: res.status,
          });
          return;
        }
        throw new Error(`session export status ${res.status}`);
      } catch (err) {
        logger.warn('session export attempt failed', {
          accountId: payload.accountId,
          requestId: payload.requestId,
          attempt,
          error: (err as Error).message,
        });
        if (attempt < maxAttempts) {
          await new Promise((r) => setTimeout(r, backoffMs(attempt)));
        }
      }
    }
    // The operator's export request will time out on the API side; say so here
    // without ever printing the session value.
    logger.error('session export exhausted retries', {
      accountId: payload.accountId,
      requestId: payload.requestId,
    });
  }

  return { post };
}
