'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { api } from '@/lib/api';
import type { ActionReport, AnalyticsReport, ReportQuery, TargetReport } from '@/lib/api';

export type ReportKind = 'actions' | 'targets' | 'analytics';

export type ReportResult = ActionReport | TargetReport | AnalyticsReport;

/** The payload is stored with the kind that produced it, so a slow response
 * from the previous picker can never be rendered as the new kind. */
export type TaggedReportResult =
  | { kind: 'actions'; data: ActionReport }
  | { kind: 'targets'; data: TargetReport }
  | { kind: 'analytics'; data: AnalyticsReport };

export type UseReports = {
  result: TaggedReportResult | null;
  loading: boolean;
  error: string | null;
  run: (params: ReportQuery) => Promise<void>;
  refresh: () => void;
};

/**
 * Runs the selected report on demand (P4-04). Not a live subscription: a
 * report is a point-in-time pivot over history, so it reloads when the filters
 * change, not on every SSE frame.
 */
export function useReports(kind: ReportKind): UseReports {
  const [params, setParams] = useState<ReportQuery>({ kind });
  const [result, setResult] = useState<TaggedReportResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Last write wins. Changing a filter calls run() again immediately, so two
  // requests are routinely in flight at once; without this the slower one can
  // land last and repaint the table with the previous filter's numbers while
  // the picker shows the new one — a report that silently reports the wrong
  // window. Same seq guard use-accounts and use-actions already use.
  const seq = useRef(0);
  const run = useCallback(
    async (next: ReportQuery) => {
      const id = ++seq.current;
      const merged = { ...next, kind };
      setParams(merged);
      setLoading(true);
      setError(null);
      try {
        const fetched = await fetchByKind(kind, merged);
        if (id === seq.current) setResult(fetched);
      } catch (err) {
        if (id === seq.current) {
          setError(err instanceof Error ? err.message : 'Report failed');
          setResult(null);
        }
      } finally {
        if (id === seq.current) setLoading(false);
      }
    },
    [kind],
  );

  const refresh = useCallback(() => {
    void run(params);
  }, [params, run]);

  // First paint loads with the default window so the page is never empty.
  useEffect(() => {
    void run({ kind });
  }, [kind, run]);

  return { result, loading, error, run, refresh };
}

async function fetchByKind(kind: ReportKind, params: ReportQuery): Promise<TaggedReportResult> {
  switch (kind) {
    case 'actions':
      return { kind, data: await api.reportActions(params) };
    case 'targets':
      return { kind, data: await api.reportTargets(params) };
    case 'analytics':
      return { kind, data: await api.reportAnalytics(params) };
  }
}
