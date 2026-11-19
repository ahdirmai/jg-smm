/**
 * Analytics overview hook.
 *
 * Monitoring's per-platform surfaces were removed from the dashboard; the
 * overview keeps the single read path the home page still uses.
 */

'use client';

import { useCallback, useEffect, useState } from 'react';

import { analyticsOverview, type AnalyticsOverview } from '@/lib/analytics';
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

export function useAnalyticsOverview(windowDays = 30) {
  return useAsync<AnalyticsOverview>(() => analyticsOverview(windowDays), [windowDays]);
}
