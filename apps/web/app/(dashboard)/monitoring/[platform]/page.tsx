'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@smm/ui';
import { RefreshCw } from 'lucide-react';
import Link from 'next/link';
import { use, useMemo, useState } from 'react';

import { ContentMetricsTable } from '@/components/content-metrics-table';
import {
  EngagementBreakdownCard,
  type EngagementBreakdownRow,
} from '@/components/engagement-breakdown-card';
import { FreshnessBadge } from '@/components/freshness-badge';
import { TrendChart } from '@/components/trend-chart';
import {
  useAnalyticsRefresh,
  useContentMetrics,
  useOfficialAccounts,
  usePlatformAnalytics,
} from '@/lib/hooks/use-analytics';
import { PLATFORMS, PLATFORM_LABEL } from '@/lib/platforms';
import { analyticsConfig } from '@/lib/analytics-config';

/** The window the reader is looking at, and the one it is compared against. */
const WINDOW_DAYS = 30;

/**
 * Per-platform analytics (P6-06).
 *
 * All figures come from the analytics ingest for official (monitored) accounts
 * only — worker accounts are never measured here. KPI cards aggregate the
 * metric across the platform's accounts so the strip reads as one number per
 * metric.
 */
export default function PlatformAnalyticsPage({
  params,
}: {
  params: Promise<{ platform: string }>;
}) {
  const { platform } = use(params);
  const cfg = analyticsConfig(platform);
  const label = PLATFORM_LABEL[platform] ?? platform;

  // The metric drives the trend. It is a real control, not a constant: the API
  // serves one series per request, so this is how the reader compares reach
  // against views without a second, differently-scaled line.
  const [metric, setMetric] = useState(cfg?.defaultMetric ?? 'reach');
  const [accountFilter, setAccountFilter] = useState<string>('all');

  // Twice the window in one request: the extra half is what the period-over-
  // period figure is measured against, so the comparison costs no extra call.
  const accounts = useOfficialAccounts(platform);
  const analytics = usePlatformAnalytics(platform, metric, WINDOW_DAYS * 2);
  // The content table follows the same account filter as the KPI strip: a
  // reader who narrows to one account means it everywhere on the page, not
  // just on the strip above.
  const content = useContentMetrics(
    platform,
    accountFilter === 'all' ? undefined : accountFilter,
    WINDOW_DAYS,
    50,
  );
  const refresh = useAnalyticsRefresh();
  const [syncing, setSyncing] = useState(false);

  // The account filter is client-side: the API returns the platform's KPIs in
  // one call, so narrowing to one account is a projection of that payload.
  // "All" must be null, not an empty Set — an empty Set is truthy, so testing it
  // as a filter drops every KPI on the default view.
  const accountIds = useMemo(
    () => (accountFilter === 'all' ? null : new Set([accountFilter])),
    [accountFilter],
  );

  const kpis = useMemo(() => {
    const all = analytics.data?.kpis ?? [];
    const rows = accountIds ? all.filter((k) => accountIds.has(k.officialAccountId)) : all;
    const byMetric = new Map<string, number>();
    for (const k of rows) {
      if (k.value == null) continue;
      byMetric.set(k.metric, (byMetric.get(k.metric) ?? 0) + k.value);
    }
    return byMetric;
  }, [analytics.data?.kpis, accountIds]);

  // The chart shows the recent window; the half before it is the baseline.
  const { points, period } = useMemo(() => {
    const all = analytics.data?.trend ?? [];
    const recent = all.slice(-WINDOW_DAYS);
    const sum = (xs: typeof recent) => xs.reduce((s, p) => s + p.value, 0);
    const current = sum(recent);
    const previous = sum(all.slice(-WINDOW_DAYS * 2, -WINDOW_DAYS));
    return {
      points: recent,
      period: {
        current,
        // No baseline means no honest percentage: reporting "+100%" off a zero
        // previous period would be a fabricated number.
        changePct: previous > 0 ? ((current - previous) / previous) * 100 : null,
      },
    };
  }, [analytics.data?.trend]);

  const metricLabel = cfg?.kpis.find((k) => k.metric === metric)?.label ?? metric;
  const accountRows = accounts.data?.officialAccounts ?? [];

  // The breakdown is filtered client-side for the same reason the KPI strip is:
  // the platform endpoint already returns every account in one call.
  const breakdownRows = useMemo(() => {
    const all = analytics.data?.breakdown ?? [];
    const rows = accountIds ? all.filter((b) => accountIds.has(b.officialAccountId)) : all;
    return rows as EngagementBreakdownRow[];
  }, [analytics.data?.breakdown, accountIds]);

  const accountFilterLabel =
    accountFilter === 'all'
      ? 'All official accounts'
      : (accountRows.find((a) => a.id === accountFilter)?.handle ?? 'this account');

  async function onSync() {
    setSyncing(true);
    try {
      await refresh.refresh();
      analytics.refresh();
      accounts.refresh();
      content.refresh();
    } finally {
      setSyncing(false);
    }
  }

  if (!PLATFORMS.includes(platform as (typeof PLATFORMS)[number])) {
    return (
      <div className="space-y-4">
        <h1 className="text-2xl font-semibold tracking-tight">Unknown platform</h1>
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            <p>&quot;{platform}&quot; is not a supported platform.</p>
            <Button asChild variant="outline" size="sm" className="mt-4">
              <Link href="/monitoring">Back to monitoring</Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Page-level actions. The title/subtitle live in the shell header (h-14),
          so this row carries only the sync controls. */}
      <div className="flex items-center justify-end gap-2">
        <FreshnessBadge freshness={analytics.data?.freshness} />
        <Button variant="outline" size="sm" onClick={onSync} disabled={syncing}>
          <RefreshCw className={syncing ? 'mr-2 size-4 animate-spin' : 'mr-2 size-4'} />
          {syncing ? 'Syncing…' : 'Sync now'}
        </Button>
      </div>

      {/* Platform switcher (design system: pill tab switcher). */}
      <section className="flex flex-wrap items-center gap-2">
        <span className="mr-1 text-xs text-muted-foreground">Per platform:</span>
        {PLATFORMS.map((p) => (
          <Button
            key={p}
            asChild
            variant={p === platform ? 'default' : 'outline'}
            size="sm"
            aria-current={p === platform ? 'page' : undefined}
          >
            <Link href={`/monitoring/${p}`}>{PLATFORM_LABEL[p]}</Link>
          </Button>
        ))}
        <span className="ml-auto">
          <Badge variant="info">3rd-party analytics · read-only</Badge>
        </span>
      </section>

      {analytics.error || accounts.error ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            {analytics.error ?? accounts.error}
          </CardContent>
        </Card>
      ) : null}

      {/* Filters: account narrows the KPI strip, metric drives the trend. */}
      <section className="flex flex-wrap items-center gap-2">
        <Select value={accountFilter} onValueChange={setAccountFilter}>
          <SelectTrigger className="w-56" aria-label="All official accounts">
            <SelectValue placeholder="All official accounts" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All official accounts</SelectItem>
            {accountRows.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                {a.handle}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={metric} onValueChange={setMetric}>
          <SelectTrigger className="w-48" aria-label="Trend metric">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(cfg?.kpis ?? []).map((k) => (
              <SelectItem key={k.metric} value={k.metric}>
                {k.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </section>

      {/* KPI strip: the platform's own metric vocabulary (analyticsConfig). */}
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {(cfg?.kpis ?? []).map((k) => {
          const value = kpis.get(k.metric);
          const Icon = k.icon;
          return (
            <Card key={k.metric}>
              <CardContent className="space-y-3 p-5">
                {/* Principle 6: small monochrome icon in a soft rounded square. */}
                <span className="grid size-9 place-items-center rounded-lg bg-secondary text-muted-foreground">
                  <Icon className="size-4" />
                </span>
                <div className="text-2xl font-semibold tracking-tight tabular-nums">
                  {analytics.loading || value == null ? '—' : value.toLocaleString()}
                </div>
                <div className="text-xs uppercase tracking-wide text-muted-foreground">
                  {k.label}
                </div>
              </CardContent>
            </Card>
          );
        })}
      </section>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between gap-4">
            <div>
              <CardTitle>{metricLabel} · last {WINDOW_DAYS} days</CardTitle>
              <CardDescription>
                {period.changePct == null ? (
                  'Daily series · no earlier period to compare against yet'
                ) : (
                  <>
                    {period.current.toLocaleString()} in the last {WINDOW_DAYS} days ·{' '}
                    <span
                      className={
                        period.changePct >= 0
                          ? 'font-medium text-foreground'
                          : 'font-medium text-destructive'
                      }
                    >
                      {period.changePct >= 0 ? '+' : ''}
                      {period.changePct.toFixed(0)}%
                    </span>{' '}
                    vs the {WINDOW_DAYS} days before
                  </>
                )}
              </CardDescription>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <TrendChart points={points} />
        </CardContent>
      </Card>

      <EngagementBreakdownCard
        rows={breakdownRows}
        platformLabel={label}
        loading={analytics.loading}
      />

      <ContentMetricsTable
        items={content.data?.items ?? []}
        platformLabel={label}
        loading={content.loading}
        accountLabel={accountFilterLabel}
      />

      <Card>
        <CardHeader>
          <CardTitle>Monitored accounts</CardTitle>
          <CardDescription>
            Official accounts on {label}, with the latest synced followers.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {accounts.loading ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Loading accounts…</p>
          ) : accountRows.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-10 text-center">
              <p className="text-sm font-medium">No official {label} accounts monitored yet</p>
              <p className="text-xs text-muted-foreground">
                Add an official account to start receiving 3rd-party analytics for {label}.
              </p>
            </div>
          ) : (
            <ul className="divide-y divide-border/60">
              {accountRows.map((a) => (
                <li key={a.id} className="flex items-center justify-between py-3">
                  <div className="flex flex-col">
                    <span className="font-medium">{a.handle}</span>
                    {a.displayName ? (
                      <span className="text-xs text-muted-foreground">{a.displayName}</span>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-4">
                    <span className="text-xs text-muted-foreground">Followers</span>
                    <span className="tabular-nums">
                      {kpisForAccount(analytics.data?.kpis ?? [], a.id, 'followers')}
                    </span>
                    <Badge variant={a.stale ? 'destructive' : 'success'}>
                      {a.stale ? 'Stale' : 'Fresh'}
                    </Badge>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function kpisForAccount(
  kpis: { officialAccountId: string; metric: string; value: number | null }[],
  id: string,
  metric: string,
): string {
  const k = kpis.find((x) => x.officialAccountId === id && x.metric === metric);
  return k?.value != null ? k.value.toLocaleString() : '—';
}
