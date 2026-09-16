'use client';

import { useState } from 'react';
import { Loader2, ScrollText } from 'lucide-react';

import {
  Badge,
  Button,
  Card,
  CardContent,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@smm/ui';
import { api, type ActionReportRow } from '@/lib/api';

const ACTION_BADGE: Record<ActionReportRow['actionType'], 'info' | 'success'> = {
  action_like: 'info',
  action_comment: 'success',
  action_report: 'info',
  action_reply_comment: 'success',
};

/**
 * Audit page (P6-10, mirrors docs/prototype/audit.html).
 *
 * There is no dedicated audit-log endpoint yet (P6 scope guard: no new
 * backend). The audit trail of executed work is the actions report — every
 * row is an actor + action + outcome, which is the audit question. When a
 * proper AuditLog endpoint lands, this page swaps the source and keeps the
 * filters.
 */
export default function AuditPage() {
  const [rows, setRows] = useState<ActionReportRow[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [q, setQ] = useState('');

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.reportActions({ kind: 'actions' });
      setRows(res.rows ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load audit trail');
    } finally {
      setLoading(false);
    }
  };

  const filtered = rows.filter(
    (r) =>
      !q ||
      r.username.toLowerCase().includes(q.toLowerCase()) ||
      r.platform.toLowerCase().includes(q.toLowerCase()),
  );

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center gap-3 p-4">
          <div className="w-full max-w-xs">
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Filter by username or platform…"
            />
          </div>
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="animate-spin" /> : null}
            Refresh
          </Button>
          <p className="ml-auto text-xs text-muted-foreground">Source: executed actions report</p>
        </CardContent>
      </Card>

      <Card>
        <div className="flex items-center justify-between border-b p-4">
          <div>
            <h2 className="text-sm font-semibold">Audit trail</h2>
            <p className="text-xs text-muted-foreground">
              Executed actions per account, per day — actor, action, outcome.
            </p>
          </div>
        </div>
        {filtered.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-16 text-center">
            <ScrollText className="size-8 text-muted-foreground" />
            <div>
              <p className="text-sm font-medium">No audit entries yet</p>
              <p className="text-xs text-muted-foreground">
                Run an action from the Actions page and entries appear here.
              </p>
            </div>
            <Button variant="outline" onClick={() => void load()} disabled={loading}>
              {loading ? <Loader2 className="animate-spin" /> : null}
              Load
            </Button>
            {error ? (
              <p className="text-sm text-destructive" role="alert">
                {error}
              </p>
            ) : null}
          </div>
        ) : (
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
                {filtered.map((r, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-mono text-xs">{r.day}</TableCell>
                    <TableCell className="font-medium">{r.username}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{r.platform}</Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={ACTION_BADGE[r.actionType]}>
                        {r.actionType.replace('action_', '')}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">{r.total}</TableCell>
                    <TableCell className="text-right font-mono text-xs text-success">
                      {r.succeeded}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs text-destructive">
                      {r.failed}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Card>
    </div>
  );
}
