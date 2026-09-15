/**
 * Analytics data hooks (P2-14 / P2-15).
 *
 * Fetch-only: React Query is deliberately absent (the accounts hook uses plain
 * fetch + EventSource, and analytics is a read path with no SSE yet). A refresh
 * re-fetches rather than mutating local state, so the numbers always come from
 * the server.
 */

'use client';

import { useCallback, useEffect, useState } from 'react';

import {
  analyticsByPlatform,
  analyticsOverview,
  analyticsRefresh,
  listOfficialAccounts,
  type AnalyticsIngestRun,
  type AnalyticsOverview,
  type OfficialAccountList,
  type PlatformAnalytics,
} from '@/lib/analytics';
import { ApiError } from '@/lib/api';

type State<T> = { data: T | null; loading: boolean; error: string | null };

function useAsync<T>(fn: () => Promise<T>, deps: unknown[]): State<T> & { refresh: () => void } {
  const [state, setState] = useState<State<T>>({ data: null, loading: true, error: null });
  const [nonce, setNonce] = useState(0);

  const refresh = useCallback(() => setNonce((n) => n + 1), []);

  useEffect(() => {
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    fn()
      .then((data) => {
        if (!cancelled) setState({ data, loading: false, error: null });
      })
      .catch((err) => {
        if (cancelled) return;
        const message = err instanceof ApiError ? err.message : 'Failed to load analytics data.';
        setState({ data: null, loading: false, error: message });
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce, ...deps]);

  return { ...state, refresh };
}

export function useOfficialAccounts(platform?: string) {
  return useAsync<OfficialAccountList>(() => listOfficialAccounts(platform), [platform ?? 'all']);
}

export function useAnalyticsOverview(windowDays = 30) {
  return useAsync<AnalyticsOverview>(() => analyticsOverview(windowDays), [windowDays]);
}

export function usePlatformAnalytics(platform: string, metric = 'followers', windowDays = 30) {
  return useAsync<PlatformAnalytics>(
    () => analyticsByPlatform(platform, metric, windowDays),
    [platform, metric, windowDays],
  );
}

export function useAnalyticsRefresh() {
  const [run, setRun] = useState<AnalyticsIngestRun | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setRun(await analyticsRefresh());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Refresh failed.');
    } finally {
      setLoading(false);
    }
  }, []);

  return { run, loading, error, refresh };
}
