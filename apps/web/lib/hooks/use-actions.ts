'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { api, type ActionItem, type ActionJob, type JobStatus } from '../api';

const POLL_MS = 5_000;

export type ActionsState = {
  actions: ActionJob[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  enqueue: (items: ActionItem[]) => Promise<void>;
};

/**
 * The action queue. The list is the operator's read on dispatch health. There
 * is no action SSE frame yet, so a short poll reconciles verdicts; swap to a
 * stream subscription once the backend emits one.
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

  // A queue row changes when a worker reports, not when the operator acts, so
  // a poll is the honest read. Slow enough to be idle-cost, fast enough to see
  // a batch land.
  useEffect(() => {
    const t = setInterval(() => void refresh(), POLL_MS);
    return () => clearInterval(t);
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
