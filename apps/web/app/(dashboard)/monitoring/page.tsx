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

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Monitoring</h1>
          <p className="text-sm text-muted-foreground">
            Official account reach and engagement across every platform.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <FreshnessBadge freshness={overview.data?.freshness} />
          <Button variant="outline" size="sm" onClick={onSync} disabled={syncing}>
            <RefreshCw className={syncing ? 'mr-2 h-4 w-4 animate-spin' : 'mr-2 h-4 w-4'} />
            {syncing ? 'Syncing…' : 'Sync now'}
          </Button>
        </div>
      </div>

      {overview.error || accounts.error ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            {overview.error ?? accounts.error}
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="Monitored accounts" value={rows.length} loading={accounts.loading} />
        <KpiCard label="Platforms" value={new Set(rows.map((a) => a.platform)).size} loading={accounts.loading} />
        <KpiCard
          label="Total followers"
          value={kpis.reduce((sum, k) => sum + (k.value ?? 0), 0)}
          loading={overview.loading}
        />
        <KpiCard
          label="Stale accounts"
          value={rows.filter((a) => a.stale).length}
          loading={accounts.loading}
          tone={rows.some((a) => a.stale) ? 'warn' : 'ok'}
        />
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
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Account</TableHead>
                  <TableHead>Platform</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Followers</TableHead>
                  <TableHead>Last synced</TableHead>
                  <TableHead>Analytics</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((a) => {
                  const kpi = kpis.find((k) => k.officialAccountId === a.id);
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
                        {kpi?.value != null ? kpi.value.toLocaleString() : '—'}
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
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function KpiCard({
  label,
  value,
  loading,
  tone = 'ok',
}: {
  label: string;
  value: number;
  loading: boolean;
  tone?: 'ok' | 'warn';
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardDescription>{label}</CardDescription>
        <CardTitle className={`text-2xl tabular-nums ${tone === 'warn' && value > 0 ? 'text-destructive' : ''}`}>
          {loading ? '—' : value.toLocaleString()}
        </CardTitle>
      </CardHeader>
    </Card>
  );
}
