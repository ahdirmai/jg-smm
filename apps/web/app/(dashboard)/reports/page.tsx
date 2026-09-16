'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Label,
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
import { AlertCircle, Download, Loader2 } from 'lucide-react';
import { useState } from 'react';

import { TrendChart } from '@/components/trend-chart';
import { api } from '@/lib/api';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useReports, type ReportResult, type ReportKind } from '@/lib/hooks/use-reports';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';
import { PLATFORM_LABEL, PLATFORMS } from '@/lib/platforms';
import type {
  ActionReport,
  AnalyticsReport,
  ReportQuery,
  TargetReport,
  TrendPoint,
} from '@/lib/api';

const KINDS: { value: ReportKind; label: string }[] = [
  { value: 'actions', label: 'Actions by day' },
  { value: 'targets', label: 'Targets (posts)' },
  { value: 'analytics', label: 'Official account growth' },
];

const ANALYTICS_METRICS = [
  'followers',
  'reach',
  'views',
  'engagements',
  'profile_views',
] as const;

export default function ReportsPage() {
  const [kind, setKind] = useState<ReportKind>('actions');
  const { result, loading, error, run } = useReports(kind);
  const { accounts } = useAccounts();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canExport = can(role, 'export');

  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [platform, setPlatform] = useState('all');
  const [accountId, setAccountId] = useState('all');
  const [metric, setMetric] = useState('followers');

  // exactOptionalPropertyTypes: never assign `undefined` to an optional key; the
  // query serializer treats an absent key as "all".
  const filters: ReportQuery = {
    kind,
    ...(from ? { from } : {}),
    ...(to ? { to } : {}),
    ...(platform !== 'all' ? { platform: platform as (typeof PLATFORMS)[number] } : {}),
    ...(accountId !== 'all' ? { accountId } : {}),
    ...(kind === 'analytics' ? { metric } : {}),
  };

  const onExport = () => {
    // A file download is a navigation, not a fetch: the browser owns the stream
    // and the credentials cookie travels with it.
    window.location.href = api.reportExportURL({ ...filters, format: 'csv' });
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">Reports</h1>
        <p className="text-sm text-muted-foreground">
          Pivot the action history and the monitored-account metrics by window, platform and
          account.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Filters</CardTitle>
          <CardDescription>Empty dates cover the whole history.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <div className="space-y-2">
              <Label htmlFor="r-kind">Report</Label>
              <Select value={kind} onValueChange={(v) => setKind(v as ReportKind)}>
                <SelectTrigger id="r-kind">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KINDS.map((k) => (
                    <SelectItem key={k.value} value={k.value}>
                      {k.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="r-from">From</Label>
              <Input id="r-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="r-to">To</Label>
              <Input id="r-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="r-platform">Platform</Label>
              <Select value={platform} onValueChange={setPlatform}>
                <SelectTrigger id="r-platform">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All platforms</SelectItem>
                  {PLATFORMS.map((p) => (
                    <SelectItem key={p} value={p}>
                      {PLATFORM_LABEL[p] ?? p}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          {kind === 'actions' ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div className="space-y-2">
                <Label htmlFor="r-account">Worker account</Label>
                <Select value={accountId} onValueChange={setAccountId}>
                  <SelectTrigger id="r-account">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All accounts</SelectItem>
                    {accounts.map((a) => (
                      <SelectItem key={a.id} value={a.id}>
                        @{a.username} · {PLATFORM_LABEL[a.platform] ?? a.platform}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : null}

          {kind === 'analytics' ? (
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <div className="space-y-2">
                <Label htmlFor="r-official">Official account ID</Label>
                <Input
                  id="r-official"
                  value={accountId === 'all' ? '' : accountId}
                  onChange={(e) => setAccountId(e.target.value)}
                  placeholder="The monitored account's id"
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="r-metric">Metric</Label>
                <Select value={metric} onValueChange={setMetric}>
                  <SelectTrigger id="r-metric">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ANALYTICS_METRICS.map((m) => (
                      <SelectItem key={m} value={m}>
                        {capitalize(m)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          ) : null}

          {error ? (
            <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {error}
            </div>
          ) : null}

          <div className="flex items-center gap-3">
            <Button onClick={() => void run(filters)} disabled={loading}>
              {loading ? <Loader2 className="animate-spin" /> : null}
              Run report
            </Button>
            <Button variant="outline" onClick={onExport} disabled={loading || !canExport}>
              <Download />
              Export CSV
            </Button>
            {!canExport ? (
              <span className="text-sm text-muted-foreground">Your role cannot export data.</span>
            ) : null}
          </div>
        </CardContent>
      </Card>

      {result ? <ReportBody kind={kind} result={result} /> : null}
    </div>
  );
}

function ReportBody({ kind, result }: { kind: ReportKind; result: ReportResult }) {
  if (kind === 'actions') {
    const report = result as ActionReport;
    const series: TrendPoint[] = report.series ?? [];
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Volume per day</CardTitle>
            <CardDescription>Total actions across the selection.</CardDescription>
          </CardHeader>
          <CardContent>
            {series.length ? (
              <TrendChart points={series.map((p) => ({ bucket: p.bucket, value: p.value }))} />
            ) : (
              <Empty />
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Rows</CardTitle>
          </CardHeader>
          <CardContent>
            {report.rows.length ? (
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Day</TableHead>
                      <TableHead>Account</TableHead>
                      <TableHead>Platform</TableHead>
                      <TableHead>Action</TableHead>
                      <TableHead className="text-right">Total</TableHead>
                      <TableHead className="text-right">Succeeded</TableHead>
                      <TableHead className="text-right">Failed</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {report.rows.map((r) => (
                      <TableRow key={`${r.day}-${r.accountId}-${r.actionType}`}>
                        <TableCell className="font-mono text-xs">{r.day}</TableCell>
                        <TableCell>@{r.username}</TableCell>
                        <TableCell>
                          <Badge variant="outline">{PLATFORM_LABEL[r.platform] ?? r.platform}</Badge>
                        </TableCell>
                        <TableCell>{r.actionType === 'action_like' ? 'Like' : 'Comment'}</TableCell>
                        <TableCell className="text-right">{r.total}</TableCell>
                        <TableCell className="text-right text-emerald-600 dark:text-emerald-400">
                          {r.succeeded}
                        </TableCell>
                        <TableCell className="text-right text-destructive">{r.failed}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty />
            )}
          </CardContent>
        </Card>
      </div>
    );
  }

  if (kind === 'targets') {
    const report = result as TargetReport;
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Targets</CardTitle>
          <CardDescription>The posts that drew engagement, busiest first.</CardDescription>
        </CardHeader>
        <CardContent>
          {report.rows.length ? (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>URL</TableHead>
                    <TableHead>Platform</TableHead>
                    <TableHead className="text-right">Total</TableHead>
                    <TableHead className="text-right">Succeeded</TableHead>
                    <TableHead className="text-right">Failed</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {report.rows.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell className="max-w-[420px] truncate font-mono text-xs">
                        <a href={r.url} target="_blank" rel="noreferrer" className="hover:underline">
                          {r.url}
                        </a>
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">{PLATFORM_LABEL[r.platform] ?? r.platform}</Badge>
                      </TableCell>
                      <TableCell className="text-right">{r.total}</TableCell>
                      <TableCell className="text-right text-emerald-600 dark:text-emerald-400">
                        {r.succeeded}
                      </TableCell>
                      <TableCell className="text-right text-destructive">{r.failed}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <Empty />
          )}
        </CardContent>
      </Card>
    );
  }

  const report = result as AnalyticsReport;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{report.metric}</CardTitle>
        <CardDescription>Growth series for the selected account.</CardDescription>
      </CardHeader>
      <CardContent>
        {report.series.length ? (
          <TrendChart points={report.series.map((p) => ({ bucket: p.bucket, value: p.value }))} />
        ) : (
          <Empty />
        )}
      </CardContent>
    </Card>
  );
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function Empty() {
  return (
    <p className="py-8 text-center text-sm text-muted-foreground">
      No rows in this window. Widen the range or clear the filters.
    </p>
  );
}
