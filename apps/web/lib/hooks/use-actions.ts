'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { subscribeStream } from '@smm/shared';

import { SSE_URL, api, type ActionItem, type ActionJob, type JobStatus } from '../api';


export type ActionsState = {
  actions: ActionJob[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  enqueue: (items: ActionItem[]) => Promise<void>;
  retry: (jobId: string) => Promise<void>;
  retryFailed: () => Promise<{ requeued: number; failed: number; skippedOld: number }>;
  runPendingNow: () => Promise<number>;
  cancel: (jobId: string) => Promise<void>;
  cancelPendingNow: () => Promise<number>;
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

  // Manual retry. Throws on the server's 400 (job is not FAILED) so the caller
  // can surface it; the refetch afterwards is what actually moves the row.
  const retry = useCallback(
    async (jobId: string) => {
      await api.retryAction(jobId);
      await refresh();
    },
    [refresh],
  );

  /**
   * Retry every FAILED job finished in the last 24 hours. Older jobs are
   * skipped because the per-account body has a 24h TTL: re-queueing one would
   * silently fall back to the template pool, which is not the text the
   * operator saw. The caller arms this action before invoking it.
   */
  const retryFailed = useCallback(async () => {
    const cutoff = Date.now() - 24 * 60 * 60 * 1000;
    const failedJobs = actions.filter((a) => a.status === 'FAILED');
    // The queue DTO exposes scheduledAt but not finishedAt. A failed job's
    // last schedule is stamped at its final attempt, so it is the closest
    // available age signal; older jobs are left to the per-row retry.
    const recent = failedJobs.filter(
      (a) => new Date(a.scheduledAt).getTime() >= cutoff,
    );
    let requeued = 0;
    let failed = 0;
    for (const job of recent) {
      try {
        await api.retryAction(job.id);
        requeued++;
      } catch {
        failed++;
      }
    }
    await refresh();
    return { requeued, failed, skippedOld: failedJobs.length - recent.length };
  }, [actions, refresh]);

  // Pull the PENDING queue forward so a backoff wait ends now. Resolves to the
  // number moved, so the caller can report "0 waiting" rather than guessing.
  const runPendingNow = useCallback(async () => {
    const { moved } = await api.runPendingNow();
    await refresh();
    return moved;
  }, [refresh]);

  // Cancel one queued/running job. Throws on the server's 400 (job is already
  // terminal) so the caller can surface it; the refetch moves the row.
  const cancel = useCallback(
    async (jobId: string) => {
      await api.cancelAction(jobId);
      await refresh();
    },
    [refresh],
  );

  // Cancel the whole PENDING queue; resolves to the row count so the caller
  // can report "nothing was queued" instead of guessing.
  const cancelPendingNow = useCallback(async () => {
    const { cancelled } = await api.cancelPendingNow();
    await refresh();
    return cancelled;
  }, [refresh]);

  return {
    actions,
    loading,
    error,
    refresh,
    enqueue,
    retry,
    retryFailed,
    runPendingNow,
    cancel,
    cancelPendingNow,
  };
}
