/**
 * SSE client for GET /api/stream (ADR 0010).
 *
 * Native EventSource only — no library. Commands still go over REST; this is
 * the single server→browser channel. Frames carry the full entity, so the
 * consumer reconciles without a second fetch. EventSource auto-reconnects on
 * drop; `onClose`/`onError` are there for logging and refetch-on-resume.
 *
 * The dashboard mounts four hooks that each subscribed to their own kind, and
 * EventSource is one connection per kind per hook — six open connections on the
 * workers page alone. That saturates the browser's six-connections-per-origin
 * limit over HTTP/1.1, so the next request (the create POST) sits in the queue
 * and never lands while the SSE responses hold every slot. Every subscriber
 * therefore shares one connection: the first subscriber opens it, the last one
 * out closes it, and the kinds are demuxed by name.
 */

import type { operations } from './generated/api.js';

/** Event kinds the API emits on /api/stream. */
export type StreamKind =
  | 'account-updated'
  | 'worker-health'
  | 'provision-updated'
  | 'action-updated';

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

type AnyHandlers = StreamHandlers<unknown>;

/** One shared connection per URL, demuxed to every subscribed kind. */
type Hub = {
  source: EventSource;
  /** Subscribers per kind; a kind can have several listeners. */
  listeners: Map<StreamKind, Set<AnyHandlers>>;
};

const hubs = new Map<string, Hub>();

const STREAM_KINDS: StreamKind[] = [
  'account-updated',
  'worker-health',
  'provision-updated',
  'action-updated',
];

function openHub(url: string): Hub {
  const existing = hubs.get(url);
  if (existing) return existing;

  const source = new EventSource(url, { withCredentials: true });
  const listeners = new Map<StreamKind, Set<AnyHandlers>>();
  const hub: Hub = { source, listeners };

  const dispatch = (kind: StreamKind, raw: MessageEvent) => {
    for (const h of hub.listeners.get(kind) ?? []) {
      try {
        h.onEvent({ kind, data: JSON.parse(raw.data) });
      } catch {
        // A malformed frame is dropped, not fatal: the next frame still lands.
      }
    }
  };

  for (const kind of STREAM_KINDS) {
    source.addEventListener(kind, (raw) => dispatch(kind, raw as MessageEvent));
  }

  // A dead stream is reported to every subscriber; EventSource already
  // scheduled its own reconnect.
  source.addEventListener('error', (e) => {
    for (const set of hub.listeners.values()) {
      for (const h of set) h.onError?.(e);
    }
  });

  hubs.set(url, hub);
  return hub;
}

/**
 * subscribe attaches to the shared stream for this URL. Returns a disposer;
 * the connection is closed only when the last listener for it unsubscribes.
 */
export function subscribeStream<T = unknown>(
  url: string,
  kind: StreamKind,
  handlers: StreamHandlers<T>,
): () => void {
  if (typeof EventSource === 'undefined') return () => undefined;

  const hub = openHub(url);
  const typed = handlers as AnyHandlers;
  let set = hub.listeners.get(kind);
  if (!set) {
    set = new Set();
    hub.listeners.set(kind, set);
  }
  set.add(typed);

  return () => {
    set?.delete(typed);
    if (set && set.size === 0) hub.listeners.delete(kind);
    if (hub.listeners.size === 0) {
      hub.source.close();
      hubs.delete(url);
    }
  };
}
