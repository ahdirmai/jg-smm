/**
 * Result callback. The ONLY way a worker reports verdicts — it never writes to
 * the database (DEVELOPMENT_RULE §7.4). Fails soft: retries 3× with linear
 * backoff, stops on 4xx, never throws.
 */
import type { WorkerConfig } from '../types.js';
import type { ActionResult } from '../types.js';
import type { Logger } from '../core/logger.js';

export interface CallbackOptions {
  maxAttempts?: number;
  timeoutMs?: number;
  fetchImpl?: typeof fetch;
}

function backoffMs(attempt: number): number {
  return attempt * 500;
}

export function createCallback(
  config: WorkerConfig,
  logger: Logger,
  options: CallbackOptions = {},
) {
  const maxAttempts = options.maxAttempts ?? 3;
  const timeoutMs = options.timeoutMs ?? 8_000;
  const doFetch = options.fetchImpl ?? fetch;
  const url = `${config.apiUrl}/internal/action-callback`;

  async function post(result: ActionResult): Promise<void> {
    // The API's idempotency key is the (job, attempt) pair as
    // "<jobId>:<attempt>" — one attempt is exactly one row, so a replayed
    // callback updates the verdict instead of duplicating it.
    const body = JSON.stringify({
      ...result,
      // The wire enum is UPPERCASE (AttemptStatus in openapi.yaml); the
      // in-process status is lowercase. Translate here so the controller and
      // the DOM layers never see the wire spelling.
      status: result.status.toUpperCase(),
      attemptId: `${result.jobId}:${result.attempt}`,
      workerId: config.workerId,
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
        // A 4xx is a permanent client error — retrying cannot help.
        if (res.status >= 400 && res.status < 500) {
          logger.warn('callback rejected', { jobId: result.jobId, status: res.status });
          return;
        }
        throw new Error(`callback status ${res.status}`);
      } catch (err) {
        logger.warn('callback attempt failed', {
          jobId: result.jobId,
          attempt,
          error: (err as Error).message,
        });
        if (attempt < maxAttempts) await new Promise((r) => setTimeout(r, backoffMs(attempt)));
      }
    }
    logger.error('callback exhausted retries', { jobId: result.jobId });
  }

  return { post };
}
