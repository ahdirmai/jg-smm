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

import { FreshnessBadge } from '@/components/freshness-badge';
import { TrendChart } from '@/components/trend-chart';
import {
  useAnalyticsRefresh,
  useOfficialAccounts,
  usePlatformAnalytics,
} from '@/lib/hooks/use-analytics';
import { ANALYTICS_METRICS, PLATFORMS, PLATFORM_LABEL } from '@/lib/platforms';

/**
 * Per-platform analytics (P2-15). Mirrors the approved prototype's analytics
 * page: platform switcher, account filter, KPI strip and the trend chart. All
 * figures come from the 3rd-party analytics ingest for official (monitored)
 * accounts only — worker accounts are never measured here.
 *
 * KPI cards aggregate the selected metric set across the platform's accounts so
 * the strip reads as one number per metric, not one per account.
 */
export default function PlatformAnalyticsPage({
  params,
}: {
  params: Promise<{ platform: string }>;
}) {
  const { platform } = use(params);

  const label = PLATFORM_LABEL[platform] ?? platform;
  const [metric, setMetric] = useState('followers');
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
      <div className="space-y-6">
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
    <div className="space-y-6">
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
            <RefreshCw className={syncing ? 'mr-2 h-4 w-4 animate-spin' : 'mr-2 h-4 w-4'} />
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
        <Select value={metric} onValueChange={setMetric}>
          <SelectTrigger className="w-40">
            <SelectValue placeholder="Metric" />
          </SelectTrigger>
          <SelectContent>
            {ANALYTICS_METRICS.map((m) => (
              <SelectItem key={m.key} value={m.key}>
                {m.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="ml-auto text-xs text-muted-foreground">Last 30 days</span>
      </section>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {ANALYTICS_METRICS.slice(0, 4).map((m) => {
          const value = kpis.get(m.key);
          return (
            <Card key={m.key}>
              <CardHeader className="pb-2">
                <CardDescription>{m.label}</CardDescription>
                <CardTitle className="text-2xl tabular-nums">
                  {analytics.loading || value == null ? '—' : value.toLocaleString()}
                </CardTitle>
              </CardHeader>
            </Card>
          );
        })}
      </div>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <div>
              <CardTitle>
                {ANALYTICS_METRICS.find((m) => m.key === metric)?.label ?? 'Trend'}
              </CardTitle>
              <CardDescription>Daily series across the platform · last 30 days</CardDescription>
            </div>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="flex items-center gap-1.5">
                <span className="inline-block size-2 rounded-full bg-[hsl(var(--chart-1))]" />
                {label}
              </span>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <TrendChart points={trend} />
        </CardContent>
      </Card>

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
            <p className="py-6 text-center text-sm text-muted-foreground">
              No official {label} accounts monitored yet.
            </p>
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
