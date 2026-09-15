'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { subscribeStream } from '@smm/shared';

import { api, type ActionItem, type ActionJob, type JobStatus } from '../api';

const SSE_URL = process.env.NEXT_PUBLIC_SSE_URL ?? 'http://localhost:8080/api/stream';


export type ActionsState = {
  actions: ActionJob[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  enqueue: (items: ActionItem[]) => Promise<void>;
};

/**
 * The action queue. The list is the operator's read on dispatch health. A
 * worker verdict publishes an `action-updated` frame (P4-03); the frame carries
 * ids + verdict, and the browser refetches the queue — the read is
 * authoritative, so a race between the log and the job row still converges
 * (ADR 0010: frame is a signal, not a patch).
 */
export function useActions(status?: JobStatus): ActionsState {
  const [actions, setActions] = useState<ActionJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const seq = useRef(0);

  const refresh = useCallback(async () => {
    const id = ++seq.current;
    try {
      const list = await api.listActions(status);
      if (id === seq.current) {
        setActions(list.actions ?? []);
        setError(null);
      }
    } catch (err) {
      if (id === seq.current) {
        setError(err instanceof Error ? err.message : 'Failed to load actions');
      }
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, [status]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // EventSource auto-reconnects on drop; on reconnect the queue is refetched
  // (AC "reconnect refetch"), which closes the gap of any frames missed while
  // the socket was down.
  useEffect(() => {
    return subscribeStream(SSE_URL, 'action-updated', {
      onEvent: () => void refresh(),
      onError: () => {},
    });
  }, [refresh]);

  const enqueue = useCallback(
    async (items: ActionItem[]) => {
      await api.enqueueActions(items);
      // The response carries the created jobs; a refresh keeps sorting
      // consistent with the server's queue order.
      await refresh();
    },
    [refresh],
  );

  return { actions, loading, error, refresh, enqueue };
}
