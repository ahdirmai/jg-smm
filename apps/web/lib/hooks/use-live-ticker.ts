'use client';

import { useEffect, useRef, useState } from 'react';

import { subscribeStream, type StreamEvent, type StreamKind } from '@smm/shared';

const SSE_URL = process.env.NEXT_PUBLIC_SSE_URL ?? 'http://localhost:8080/api/stream';
const TICKER_MAX = 50;

export type TickerItem = {
  id: string;
  kind: StreamKind;
  at: number;
  label: string;
};

export type LiveTickerState = {
  items: TickerItem[];
  connected: boolean;
};

/**
 * Live ticker (P4-08). A rolling tail of the SSE stream shown above the fleet.
 *
 * It subscribes to every kind the dashboard emits and folds them into one
 * ordered list. It is deliberately presentation-only: the fleet/queue pages
 * reconcile their own state from the same frames (ADR 0010); this hook never
 * derives application state, it just shows that the pipe is live.
 */
export function useLiveTicker(): LiveTickerState {
  const [items, setItems] = useState<TickerItem[]>([]);
  const [connected, setConnected] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const kinds: StreamKind[] = [
      'account-updated',
      'worker-health',
      'provision-updated',
      'action-updated',
    ];
    const disposers = kinds.map((kind) =>
      subscribeStream(SSE_URL, kind, {
        onEvent: (ev: StreamEvent) => {
          seq.current += 1;
          const item: TickerItem = {
            id: `${Date.now()}-${seq.current}`,
            kind,
            at: Date.now(),
            label: describe(ev),
          };
          // Cap the list; drop the oldest so the ticker never grows unbounded.
          setItems((prev) => [item, ...prev].slice(0, TICKER_MAX));
        },
        onError: () => setConnected(false),
        onClose: () => setConnected(false),
      }),
    );
    setConnected(true);
    return () => {
      disposers.forEach((off) => off());
      setConnected(false);
    };
  }, []);

  return { items, connected };
}

/** Pull a human label out of a frame without knowing its schema shape. */
function describe(ev: StreamEvent): string {
  const data = (ev.data ?? {}) as Record<string, unknown>;
  const name = typeof data.name === 'string' ? data.name : undefined;
  const username = typeof data.username === 'string' ? data.username : undefined;
  const status = typeof data.status === 'string' ? data.status : undefined;
  const who = username ? `@${username}` : (name ?? 'worker');
  switch (ev.kind) {
    case 'account-updated':
      return `${who} ${status ?? 'updated'}`;
    case 'worker-health':
      return `${who} heartbeat${status ? ` · ${status}` : ''}`;
    case 'provision-updated':
      return `${who} provision${status ? ` · ${status}` : ''}`;
    case 'action-updated':
      return `${who} action${status ? ` · ${status}` : ''}`;
    default:
      return ev.kind;
  }
}
