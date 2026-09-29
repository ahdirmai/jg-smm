'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@smm/ui';
import { RefreshCw, Users } from 'lucide-react';
import Link from 'next/link';
import { useState } from 'react';

import { FreshnessBadge } from '@/components/freshness-badge';
import { useAnalyticsOverview, useAnalyticsRefresh, useOfficialAccounts } from '@/lib/hooks/use-analytics';
import { PLATFORM_LABEL } from '@/lib/platforms';

/**
 * Monitoring overview (P2-15): KPI strip + the monitored-account table with the
 * freshness badge. The monitored accounts are official (read-only) subjects —
 * distinct from worker accounts, which never appear here.
 */
export default function MonitoringPage() {
  const overview = useAnalyticsOverview(30);
  const accounts = useOfficialAccounts();
  const refresh = useAnalyticsRefresh();
  const [syncing, setSyncing] = useState(false);

  async function onSync() {
    setSyncing(true);
    try {
      await refresh.refresh();
      overview.refresh();
      accounts.refresh();
    } finally {
      setSyncing(false);
    }
  }

  const kpis = overview.data?.kpis ?? [];
  const rows = accounts.data?.officialAccounts ?? [];
  // Aggregates are per metric: the API returns one KPI row per (account,
  // metric), so summing the whole list would add reach to views to followers
  // and label the result "followers".
  const totalByMetric = (metric: string) =>
    kpis.reduce((sum, k) => (k.metric === metric ? sum + (k.value ?? 0) : sum), 0);
  const metricFor = (id: string, metric: string) =>
    kpis.find((k) => k.officialAccountId === id && k.metric === metric)?.value;

  return (
    <div className="space-y-6">
      {/* Page-level actions. The title/subtitle live in the shell header (h-14),
          so this row carries only the sync controls. */}
      <div className="flex items-center justify-end gap-2">
        <FreshnessBadge freshness={overview.data?.freshness} />
        <Button variant="outline" size="sm" onClick={onSync} disabled={syncing}>
          <RefreshCw className={syncing ? 'mr-2 h-4 w-4 animate-spin' : 'mr-2 h-4 w-4'} />
          {syncing ? 'Syncing…' : 'Sync now'}
        </Button>
      </div>

      {overview.error || accounts.error ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            {overview.error ?? accounts.error}
          </CardContent>
        </Card>
      ) : null}

      {/* Four cards: the reach the team is here to watch, plus the two other
          scalars the provider reports and the fleet size. Staleness is not a
          card — the freshness badge above already states it, and each account
          carries its own status in the table. */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="Total reach" value={totalByMetric('reach')} loading={overview.loading} />
        <KpiCard label="Total views" value={totalByMetric('views')} loading={overview.loading} />
        <KpiCard
          label="Total interactions"
          value={totalByMetric('engagements')}
          loading={overview.loading}
        />
        <KpiCard label="Monitored accounts" value={rows.length} loading={accounts.loading} />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Official accounts</CardTitle>
          <CardDescription>Monitored read-only accounts fed by the analytics provider.</CardDescription>
        </CardHeader>
        <CardContent>
          {accounts.loading ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Loading accounts…</p>
          ) : rows.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-10 text-center">
              <Users className="h-8 w-8 text-muted-foreground" />
              <p className="text-sm text-muted-foreground">No official accounts monitored yet.</p>
              <p className="text-xs text-muted-foreground">
                Add one via the API; the first sync populates its metrics.
              </p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border/60">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Account</TableHead>
                  <TableHead>Platform</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Reach</TableHead>
                  <TableHead>Views</TableHead>
                  <TableHead>Followers</TableHead>
                  <TableHead>Last synced</TableHead>
                  <TableHead>Analytics</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((a) => {
                  return (
                    <TableRow key={a.id}>
                      <TableCell className="font-medium">
                        <div className="flex flex-col">
                          <span>{a.handle}</span>
                          {a.displayName ? (
                            <span className="text-xs text-muted-foreground">{a.displayName}</span>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell>{PLATFORM_LABEL[a.platform] ?? a.platform}</TableCell>
                      <TableCell>
                        <Badge variant={a.stale ? 'destructive' : 'success'}>{a.stale ? 'Stale' : 'Fresh'}</Badge>
                      </TableCell>
                      <TableCell className="tabular-nums">
                        {metricFor(a.id, 'reach')?.toLocaleString() ?? '—'}
                      </TableCell>
                      <TableCell className="tabular-nums">
                        {metricFor(a.id, 'views')?.toLocaleString() ?? '—'}
                      </TableCell>
                      <TableCell className="tabular-nums">
                        {metricFor(a.id, 'followers')?.toLocaleString() ?? '—'}
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {a.lastFetchedAt ? new Date(a.lastFetchedAt).toLocaleString() : 'Never'}
                      </TableCell>
                      <TableCell>
                        <Button asChild variant="ghost" size="sm">
                          <Link href={`/monitoring/${a.platform}`}>View</Link>
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function KpiCard({ label, value, loading }: { label: string; value: number; loading: boolean }) {
  return (
    <Card>
      <CardContent className="space-y-2 p-5">
        {/* Principle 4: hero number, small muted label beneath. */}
        <div className="text-2xl font-semibold tracking-tight tabular-nums">
          {loading ? '—' : value.toLocaleString()}
        </div>
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      </CardContent>
    </Card>
  );
}
