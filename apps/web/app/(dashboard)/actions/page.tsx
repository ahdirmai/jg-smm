'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@smm/ui';
import { AlertCircle } from 'lucide-react';
import { useMemo, useState } from 'react';

import { NewActionForm } from './new-action-form';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { useVirtualRowWindow } from '@/lib/hooks/use-virtual-window';
import type { ActionJob, JobStatus } from '@/lib/api';

const STATUSES: JobStatus[] = ['PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'CANCELLED'];
// Uniform row height: group headers and job rows share it so the window math
// stays fixed-height (see useVirtualRowWindow).
const ROW_HEIGHT = 44;

// Pipeline the worker actually runs (ADR: publish → dispatch → verify). The
// stepper below maps a real JobStatus onto it; no stage is invented for show.
const PIPELINE = ['Queued', 'Dispatched', 'Running', 'Verifying'] as const;

function statusTone(status: JobStatus): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'SUCCESS':
      return 'success';
    case 'FAILED':
      return 'destructive';
    case 'RUNNING':
      return 'secondary';
    default:
      return 'outline';
  }
}

/** How many pipeline stages a real status has cleared. */
function stageReached(status: JobStatus): number {
  switch (status) {
    case 'PENDING':
      return 1;
    case 'RUNNING':
      return 3;
    case 'SUCCESS':
    case 'FAILED':
      return 4;
    default:
      return 1;
  }
}

export default function ActionsPage() {
  const { accounts } = useAccounts();
  const { actions, loading, error } = useActions();

  const [status, setStatusFilter] = useState<string>('all');

  const inFlight = useMemo(
    () => actions.filter((a) => a.status === 'PENDING' || a.status === 'RUNNING').length,
    [actions],
  );

  const filtered = useMemo(
    () => (status === 'all' ? actions : actions.filter((a) => a.status === status)),
    [actions, status],
  );

  // Group per account (P4-02): an operator reads a queue by who is working it.
  // Flatten to one row array so the window can be a fixed-height slice.
  const rows = useMemo(() => {
    type Row = { kind: 'group'; accountId: string } | { kind: 'job'; job: ActionJob };
    const byAccount = new Map<string, ActionJob[]>();
    for (const a of filtered) {
      const bucket = byAccount.get(a.accountId);
      if (bucket) bucket.push(a);
      else byAccount.set(a.accountId, [a]);
    }
    const out: Row[] = [];
    for (const [acc, jobs] of byAccount) {
      out.push({ kind: 'group', accountId: acc });
      for (const job of jobs) out.push({ kind: 'job', job });
    }
    return out;
  }, [filtered]);

  const virtual = useVirtualRowWindow(ROW_HEIGHT);
  const win = virtual.slice(rows.length);
  const [selected, setSelected] = useState<ActionJob | null>(null);

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      {/* New Action: platform → link → scrape → action → per-account comment → submit. */}
      <NewActionForm />

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle>Job queue</CardTitle>
            <CardDescription>
              {loading
                ? 'Loading…'
                : `${filtered.length} of ${actions.length} shown · latest actions advance automatically`}
            </CardDescription>
          </div>
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            {inFlight > 0 ? (
              <span className="size-2 animate-pulse rounded-full bg-primary" />
            ) : null}
            {inFlight} in flight
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex items-center gap-2">
            <Select value={status} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-40" aria-label="All statuses">
                <SelectValue placeholder="All statuses" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All statuses</SelectItem>
                {STATUSES.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s[0] + s.slice(1).toLowerCase()}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </section>

          {error ? (
            <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {error}
            </div>
          ) : null}

          {rows.length === 0 && !loading ? (
            <div className="flex items-center justify-center rounded-md border py-10 text-sm text-muted-foreground">
              The queue is empty. Enqueue above.
            </div>
          ) : (
            // Windowed: only the visible slice (plus overscan) renders, so a
            // 5k-row queue costs the same React work as a 50-row one. The
            // before/after spacers keep the scrollbar proportional.
            <div
              ref={virtual.ref}
              className="max-h-[480px] overflow-y-auto rounded-md border"
              role="table"
              aria-label="Action queue"
            >
              <div style={{ height: win.before + rows.length * ROW_HEIGHT, position: 'relative' }}>
                <div style={{ transform: `translateY(${win.before}px)` }}>
                  {rows.slice(win.start, win.end).map((row) =>
                    row.kind === 'group' ? (
                      <div
                        key={`g-${row.accountId}`}
                        className="flex items-center border-b bg-muted/40 px-3 text-xs font-medium text-muted-foreground"
                        style={{ height: ROW_HEIGHT }}
                      >
                        Account {accountLabel(accounts, row.accountId)}
                      </div>
                    ) : (
                      <button
                        key={row.job.id}
                        type="button"
                        onClick={() => setSelected(row.job)}
                        className="flex w-full items-center gap-4 border-b px-3 text-left transition-colors hover:bg-accent/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                        style={{ height: ROW_HEIGHT }}
                      >
                        <Badge variant={statusTone(row.job.status)}>{row.job.status}</Badge>
                        <span className="w-20 text-sm">{actionLabel(row.job.actionType)}</span>
                        <span className="max-w-[200px] flex-1 truncate font-mono text-xs text-muted-foreground">
                          {row.job.targetUrl ?? row.job.targetId}
                        </span>
                        <Stepper
                          reached={stageReached(row.job.status)}
                          failed={row.job.status === 'FAILED'}
                        />
                        <span className="max-w-[200px] flex-1 truncate text-xs">
                          {row.job.renderedText ? (
                            <span className="text-foreground">{row.job.renderedText}</span>
                          ) : row.job.error ? (
                            <span className="text-destructive" title={row.job.error}>
                              {row.job.errorClass ? `${row.job.errorClass}: ` : ''}
                              {row.job.error}
                            </span>
                          ) : (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </span>
                        <span className="w-10 text-right text-sm tabular-nums text-muted-foreground">
                          {row.job.attempts}
                        </span>
                      </button>
                    ),
                  )}
                </div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">
        Pipeline: <span className="font-mono">{[...PIPELINE, 'Success | Failed'].join(' → ')}</span>
        . Failure branches mirror the real worker: cooldown gate and verification failure are
        reported, never silent.
      </p>

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>
              {selected ? `${actionLabel(selected.actionType)} attempt` : ''}
            </DialogTitle>
            <DialogDescription>
              {selected
                ? `Job ${selected.id} · ${selected.attempts} attempt${selected.attempts === 1 ? '' : 's'}`
                : ''}
            </DialogDescription>
          </DialogHeader>
          {selected ? (
            <div className="space-y-4 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant={statusTone(selected.status)}>{selected.status}</Badge>
                {selected.errorClass ? (
                  <Badge variant="destructive">{selected.errorClass}</Badge>
                ) : null}
              </div>
              <div className="space-y-1">
                <div className="text-xs text-muted-foreground">Target</div>
                <div className="break-all font-mono text-xs">
                  {selected.targetUrl ?? selected.targetId}
                </div>
              </div>
              {selected.renderedText ? (
                <div className="space-y-1">
                  <div className="text-xs text-muted-foreground">Composed comment</div>
                  <div className="rounded-md border bg-muted/40 p-3 text-sm">
                    {selected.renderedText}
                  </div>
                </div>
              ) : null}
              {selected.error ? (
                <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 size-4" />
                  <span className="whitespace-pre-wrap">{selected.error}</span>
                </div>
              ) : null}
            </div>
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setSelected(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** 4-dot pipeline; filled by how far the real status has progressed. */
function Stepper({ reached, failed }: { reached: number; failed: boolean }) {
  return (
    <div className="flex items-center gap-1" aria-hidden>
      {PIPELINE.map((_, i) => {
        const idx = i + 1;
        const done = idx < reached;
        const active = idx === reached && !failed;
        return (
          <span
            key={idx}
            className={[
              'size-1.5 rounded-full',
              done
                ? failed
                  ? 'bg-destructive'
                  : 'bg-primary'
                : active
                  ? 'animate-pulse bg-primary'
                  : 'bg-muted-foreground/30',
            ].join(' ')}
          />
        );
      })}
    </div>
  );
}

/** Human label for a queue job type. */
function actionLabel(t: string): string {
  switch (t) {
    case 'action_comment':
      return 'Comment';
    case 'action_report':
      return 'Report';
    case 'action_reply_comment':
      return 'Reply';
    default:
      return 'Like';
  }
}

/** Show the @handle when the account is already loaded; fall back to the id. */
function accountLabel(accounts: { id: string; username: string }[], id: string): string {
  const acc = accounts.find((a) => a.id === id);
  return acc ? `@${acc.username} (${id.slice(0, 8)})` : id.slice(0, 8);
}
