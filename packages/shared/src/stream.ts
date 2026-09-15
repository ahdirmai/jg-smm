/**
 * SSE client for GET /api/stream (ADR 0010).
 *
 * Native EventSource only — no library. Commands still go over REST; this is
 * the single server→browser channel. Frames carry the full entity, so the
 * consumer reconciles without a second fetch. EventSource auto-reconnects on
 * drop; `onClose`/`onError` are there for logging and refetch-on-resume.
 */

import type { operations } from './generated/api.js';

/** Event kinds the API emits on /api/stream. */
export type StreamKind = 'account-updated' | 'worker-health' | 'provision-updated';

/** One SSE frame: the `event:` line plus the parsed JSON `data:` body. */
export type StreamEvent<T = unknown> = {
  kind: StreamKind;
  data: T;
};

/** The response schema of /api/stream (informational; bodies vary by kind). */
export type StreamEventSchema = NonNullable<
  operations['streamEvents']['responses']['200']['content']['text/event-stream']
>;

export type StreamHandlers<T = unknown> = {
  /** Called for every frame whose `event:` matches `kind`. */
  onEvent: (event: StreamEvent<T>) => void;
  /** Called on any error; EventSource will already have scheduled a reconnect. */
  onError?: (err: Event) => void;
  /** Called when the connection closes (hub overflow or server shutdown). */
  onClose?: () => void;
};

/**
 * subscribe opens the stream. Returns a disposer. Throws only if EventSource is
 * unavailable (SSR / very old browser); callers guard with typeof checks.
 *
 * Note: EventSource has no custom-headers support, so the access cookie must be
 * sent automatically (SameSite + credentials: 'include' is the browser default
 * for same-origin). Keep the stream same-origin or behind a cookie-aware proxy.
 */
export function subscribeStream<T = unknown>(
  url: string,
  kind: StreamKind,
  handlers: StreamHandlers<T>,
): () => void {
  const es = new EventSource(url, { withCredentials: true });

  es.addEventListener(kind, (raw) => {
    try {
      handlers.onEvent({ kind, data: JSON.parse((raw as MessageEvent).data) as T });
    } catch {
      // A malformed frame is dropped, not fatal: the next frame still lands.
    }
  });

  if (handlers.onError) {
    es.addEventListener('error', (e) => handlers.onError?.(e as Event));
  }
  if (handlers.onClose) {
    es.addEventListener('close', () => handlers.onClose?.());
  }

  return () => es.close();
}
