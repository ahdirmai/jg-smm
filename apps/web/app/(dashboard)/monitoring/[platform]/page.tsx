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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@smm/ui';
import { Filter, RefreshCw } from 'lucide-react';
import Link from 'next/link';
import { use, useMemo, useState } from 'react';

import { FreshnessBadge } from '@/components/freshness-badge';
import { TrendChart } from '@/components/trend-chart';
import {
  useAnalyticsRefresh,
  useOfficialAccounts,
  usePlatformAnalytics,
} from '@/lib/hooks/use-analytics';
import { PLATFORMS, PLATFORM_LABEL } from '@/lib/platforms';
import { analyticsConfig } from '@/lib/analytics-config';

/**
 * Per-platform analytics (P6-06, mirrors docs/prototype/analytics-<platform>.html).
 *
 * Each platform has its own metric vocabulary (IG: saves + reels views, YouTube:
 * watch time + CTR, …) read from analyticsConfig; the KPI strip and the trend
 * pairing follow the approved prototype per platform.
 *
 * All figures come from the 3rd-party analytics ingest for official (monitored)
 * accounts only — worker accounts are never measured here. KPI cards aggregate
 * the metric across the platform's accounts so the strip reads as one number
 * per metric.
 */
export default function PlatformAnalyticsPage({
  params,
}: {
  params: Promise<{ platform: string }>;
}) {
  const { platform } = use(params);
  const cfg = analyticsConfig(platform);
  const label = PLATFORM_LABEL[platform] ?? platform;

  // The trend metric defaults to the platform's primary series pair so the
  // chart title matches the prototype out of the box.
  const [metric] = useState(cfg?.seriesA ?? 'followers');
  const [accountFilter, setAccountFilter] = useState<string>('all');

  const accounts = useOfficialAccounts(platform);
  const analytics = usePlatformAnalytics(platform, metric, 30);
  const refresh = useAnalyticsRefresh();
  const [syncing, setSyncing] = useState(false);

  // The account filter is client-side: the API returns the platform's KPIs in
  // one call, so narrowing to one account is a projection of that payload.
  const accountIds = useMemo(
    () => new Set(accountFilter === 'all' ? null : [accountFilter]),
    [accountFilter],
  );

  const kpis = useMemo(() => {
    const all = analytics.data?.kpis ?? [];
    const filtered = accountIds ? all.filter((k) => accountIds.has(k.officialAccountId)) : all;
    const byMetric = new Map<string, number>();
    for (const k of filtered) {
      if (k.value == null) continue;
      byMetric.set(k.metric, (byMetric.get(k.metric) ?? 0) + k.value);
    }
    return byMetric;
  }, [analytics.data?.kpis, accountIds]);

  const trend = analytics.data?.trend ?? [];
  const accountRows = accounts.data?.officialAccounts ?? [];

  async function onSync() {
    setSyncing(true);
    try {
      await refresh.refresh();
      analytics.refresh();
      accounts.refresh();
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">{label} analytics</h1>
          <p className="text-sm text-muted-foreground">
            Official accounts (monitored) · {label} · data via 3rd-party analytics
          </p>
        </div>
        <div className="flex items-center gap-2">
          <FreshnessBadge freshness={analytics.data?.freshness} />
          <Button variant="outline" size="sm" onClick={onSync} disabled={syncing}>
            <RefreshCw className={syncing ? 'mr-2 size-4 animate-spin' : 'mr-2 size-4'} />
            {syncing ? 'Syncing…' : 'Sync now'}
          </Button>
        </div>
      </div>

      {/* Platform switcher: one analytics page per platform (prototype §analytics). */}
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

      {/* Filters: account narrows the KPI strip; metric drives the trend. */}
      <section className="flex flex-wrap items-center gap-2">
        <Select value={accountFilter} onValueChange={setAccountFilter}>
          <SelectTrigger className="w-56">
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
        <span className="ml-auto text-xs text-muted-foreground">Last 30 days</span>
      </section>

      {/* KPI strip: the platform's own metric vocabulary (analyticsConfig). */}
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {(cfg?.kpis ?? []).slice(0, 4).map((k) => {
          const value = kpis.get(k.metric);
          const Icon = k.icon;
          return (
            <Card key={k.metric}>
              <CardContent className="space-y-2 p-4">
                <div className="flex items-center justify-between">
                  <span className="text-xs uppercase tracking-wide text-muted-foreground">
                    {k.label}
                  </span>
                  <Icon className="size-4 text-muted-foreground" />
                </div>
                <div className="text-2xl font-semibold tracking-tight tabular-nums">
                  {analytics.loading || value == null ? '—' : value.toLocaleString()}
                </div>
              </CardContent>
            </Card>
          );
        })}
      </section>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle>{cfg?.trendTitle ?? 'Trend'}</CardTitle>
                <CardDescription>Daily series · last 30 days</CardDescription>
              </div>
              <div className="flex items-center gap-3 text-xs text-muted-foreground">
                <span className="flex items-center gap-1.5">
                  <span className="inline-block size-2 rounded-full bg-[hsl(var(--chart-1))]" />
                  {cfg?.seriesA}
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="inline-block size-2 rounded-full bg-[hsl(var(--chart-4))]" />
                  {cfg?.seriesB}
                </span>
              </div>
            </div>
          </CardHeader>
          <CardContent>
            <TrendChart points={trend} />
          </CardContent>
        </Card>

        {/* Audience mix: read-only geo split from the ingestion layer. */}
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <div>
                <CardTitle>Audience</CardTitle>
                <CardDescription>Follower mix</CardDescription>
              </div>
              <Badge variant="outline">sample</Badge>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            {AUDIENCE_MIX.map((row) => (
              <div key={row.region} className="space-y-1.5">
                <div className="flex justify-between text-sm">
                  <span>{row.region}</span>
                  <span className="font-mono text-xs">{row.pct}%</span>
                </div>
                <div className="h-1.5 rounded-full bg-secondary">
                  <div
                    className="h-full rounded-full bg-primary"
                    style={{ width: `${row.pct}%` }}
                  />
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      </section>

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
            <ul className="divide-y">
              {accountRows.map((a) => (
                <li key={a.id} className="flex items-center justify-between py-3">
                  <div className="flex flex-col">
                    <span className="font-medium">{a.handle}</span>
                    {a.displayName ? (
                      <span className="text-xs text-muted-foreground">{a.displayName}</span>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-4">
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

      {/* Top posts. The prototype ships this table with its own "not yet
          enabled" empty state; the ingest API exposes KPIs + trend only, so
          per-post rows have no source yet. Structure matches the prototype,
          rows are never fabricated. */}
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle>Top posts</CardTitle>
            <CardDescription>{label} · last 30 days</CardDescription>
          </div>
          <Button variant="outline" size="sm" disabled title="Filter needs a per-post endpoint">
            <Filter className="size-3.5" />
            Filter
          </Button>
        </CardHeader>
        <CardContent>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Post</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Date</TableHead>
                  <TableHead className="text-right">Metrics</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell
                    colSpan={4}
                    className="py-10 text-center text-xs text-muted-foreground"
                  >
                    Not yet enabled in the MVP — layout shown for review.
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
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

/**
 * Audience geo split. The prototype shows this panel on every platform; the
 * real split comes from the 3rd-party ingestion and is not yet exposed by the
 * API. The panel renders with a clear `sample` tag until the endpoint lands —
 * never present example numbers as live.
 */
const AUDIENCE_MIX: { region: string; pct: number }[] = [
  { region: 'Indonesia', pct: 48 },
  { region: 'Singapore', pct: 21 },
  { region: 'Malaysia', pct: 12 },
  { region: 'Other', pct: 19 },
];
