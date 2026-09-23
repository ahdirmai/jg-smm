'use client';

import { useCallback, useEffect, useState } from 'react';
import { ChevronLeft, ChevronRight, Loader2, ScrollText } from 'lucide-react';

import {
  Badge,
  Button,
  Card,
  CardContent,
  Input,
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
import { api, type AuditLog, type AuditQuery } from '@/lib/api';

const PAGE_SIZE = 25;

/**
 * Audit page (P6-10, mirrors docs/prototype/audit.html).
 *
 * The audit trail is written by the API as middleware on every state-changing
 * /api call, so this table is the authoritative "who did what, when": each row
 * is an actor, an action, a target, and an outcome, with the request IP. System
 * rows (scheduler / reconciler) appear with the actor "system".
 */
export default function AuditPage() {
  const [rows, setRows] = useState<AuditLog[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [actorId, setActorId] = useState<string>('all');
  const [action, setAction] = useState<string>('all');
  const [q, setQ] = useState('');
  const [page, setPage] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const query: AuditQuery = { limit: PAGE_SIZE, offset: page * PAGE_SIZE };
      if (actorId !== 'all') query.actorId = actorId;
      if (action !== 'all') query.action = action;
      const res = await api.listAudit(query);
      setRows(res.rows ?? []);
      setTotal(res.total ?? 0);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load audit trail');
    } finally {
      setLoading(false);
    }
  }, [actorId, action, page]);

  useEffect(() => {
    void load();
  }, [load]);

  // The actors list drives the filter dropdown; the API exposes users, and the
  // synthetic "system" bucket covers rows no person authored.
  const [actors, setActors] = useState<{ id: string; label: string }[]>([]);
  useEffect(() => {
    api
      .listUsers()
      .then((res) => setActors((res.users ?? []).map((u) => ({ id: u.id, label: u.email }))))
      .catch(() => setActors([]));
  }, []);

  const actions = useDistinctActions(rows);

  const filtered = rows.filter((r) => {
    if (!q) return true;
    const needle = q.toLowerCase();
    return (
      (r.actor ?? 'system').toLowerCase().includes(needle) ||
      r.action.toLowerCase().includes(needle) ||
      r.entityId.toLowerCase().includes(needle) ||
      (r.ip ?? '').toLowerCase().includes(needle)
    );
  });

  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center gap-3 p-4">
          <div className="w-48">
            <Select value={actorId} onValueChange={setActorId}>
              <SelectTrigger aria-label="Actor">
                <SelectValue placeholder="All actors" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All actors</SelectItem>
                {actors.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="w-48">
            <Select value={action} onValueChange={setAction}>
              <SelectTrigger aria-label="Action">
                <SelectValue placeholder="All actions" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All actions</SelectItem>
                {actions.map((a) => (
                  <SelectItem key={a} value={a}>
                    {a}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="w-full max-w-xs">
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="Filter by actor, action, target or IP…"
            />
          </div>
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="animate-spin" /> : null}
            Refresh
          </Button>
          <p className="ml-auto text-xs text-muted-foreground">Source: server audit log</p>
        </CardContent>
      </Card>

      <Card>
        <div className="flex items-center justify-between border-b p-4">
          <div>
            <h2 className="text-sm font-semibold">Audit trail</h2>
            <p className="text-xs text-muted-foreground">
              Who did what, when — every state-changing call, with the outcome.
            </p>
          </div>
        </div>
        {filtered.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-16 text-center">
            {/* Principle 6: icon in a soft rounded square. */}
            <span className="grid size-12 place-items-center rounded-xl bg-secondary text-muted-foreground">
              <ScrollText className="size-6" />
            </span>
            <div>
              <p className="text-sm font-medium">No audit entries yet</p>
              <p className="text-xs text-muted-foreground">
                Change something — create a container, add an account, queue an action — and it
                appears here.
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
          <>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Time</TableHead>
                    <TableHead>Actor</TableHead>
                    <TableHead>Action</TableHead>
                    <TableHead>Target</TableHead>
                    <TableHead>Result</TableHead>
                    <TableHead>IP</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.map((r) => (
                    <TableRow key={r.id}>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {formatTime(r.ts)}
                      </TableCell>
                      <TableCell className="font-medium">{r.actor ?? 'system'}</TableCell>
                      <TableCell>
                        <Badge variant={actionBadge(r)}>{r.action}</Badge>
                      </TableCell>
                      <TableCell className="max-w-[220px] truncate font-mono text-xs">
                        {r.entityId || '—'}
                      </TableCell>
                      <TableCell>
                        <Badge variant={resultBadge(r.result)}>{r.result}</Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{r.ip ?? '—'}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="flex items-center justify-between border-t p-3 text-xs text-muted-foreground">
              <span>
                Showing {filtered.length} of {total} entries
              </span>
              <div className="flex gap-1">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page === 0 || loading}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  <ChevronLeft className="size-4" />
                  Prev
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={page >= pages - 1 || loading}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Next
                  <ChevronRight className="size-4" />
                </Button>
              </div>
            </div>
          </>
        )}
      </Card>
    </div>
  );
}

/** Distinct action values from the loaded page, for the filter dropdown. */
function useDistinctActions(rows: AuditLog[]): string[] {
  const seen = new Set<string>();
  for (const r of rows) seen.add(r.action);
  return Array.from(seen).sort();
}

function actionBadge(r: AuditLog): 'info' | 'destructive' | 'success' {
  if (r.action.endsWith('.remove')) return 'destructive';
  if (r.action.endsWith('.create')) return 'success';
  return 'info';
}

function resultBadge(result: string): 'success' | 'destructive' {
  return result === 'ok' ? 'success' : 'destructive';
}

function formatTime(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  return d.toLocaleString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    day: '2-digit',
    month: 'short',
  });
}
