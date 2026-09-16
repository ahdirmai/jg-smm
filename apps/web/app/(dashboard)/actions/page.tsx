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
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} from '@smm/ui';
import { AlertCircle, Loader2, Send } from 'lucide-react';
import { useMemo, useState } from 'react';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { useVirtualRowWindow } from '@/lib/hooks/use-virtual-window';
import { PLATFORM_LABEL } from '@/lib/platforms';
import type { ActionItem, ActionJob, JobStatus } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

const STATUSES: JobStatus[] = ['PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'CANCELLED'];
const MAX_BATCH = 50;
// Uniform row height: group headers and job rows share it so the window math
// stays fixed-height (see useVirtualRowWindow).
const ROW_HEIGHT = 44;

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

export default function ActionsPage() {
  const { accounts } = useAccounts();
  const { actions, loading, error, enqueue } = useActions();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');

  const [accountId, setAccountId] = useState<string>('');
  const [actionType, setActionType] = useState<'action_like' | 'action_comment'>('action_like');
  // One permalink per line; a paste of N URLs is the common operator flow.
  const [urls, setUrls] = useState<string>('');
  const [status, setStatusFilter] = useState<string>('all');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

  // Only accounts that can actually be dispatched: the enqueue rejects anything
  // not packable, so the dropdown must not offer them.
  const usable = useMemo(
    () => accounts.filter((a) => a.status === 'ACTIVE' || a.status === 'PAUSED'),
    [accounts],
  );

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

  const onEnqueue = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);

    if (!accountId) {
      setFormError('Pick an account first.');
      return;
    }
    const list = urls
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean);
    if (list.length === 0) {
      setFormError('Paste at least one post URL.');
      return;
    }
    if (list.length > MAX_BATCH) {
      setFormError(`A batch is capped at ${MAX_BATCH} actions; you pasted ${list.length}.`);
      return;
    }

    const items: ActionItem[] = list.map((targetUrl) => ({ accountId, targetUrl, actionType }));

    setBusy(true);
    try {
      await enqueue(items);
      setUrls('');
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Enqueue failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">Actions</h1>
        <p className="text-sm text-muted-foreground">
          Enqueue likes and comments. Comment text is composed from templates and screened before
          dispatch — never typed here.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Enqueue</CardTitle>
          <CardDescription>One URL per line, up to {MAX_BATCH} per batch.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onEnqueue} className="space-y-4">
            <fieldset disabled={!canAct} className="space-y-4" aria-label="Enqueue actions">
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="account">Account</Label>
                  <Select value={accountId} onValueChange={setAccountId}>
                    <SelectTrigger id="account">
                      <SelectValue placeholder="Select an account" />
                    </SelectTrigger>
                    <SelectContent>
                      {usable.map((a) => (
                        <SelectItem key={a.id} value={a.id}>
                          @{a.username} · {PLATFORM_LABEL[a.platform] ?? a.platform}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="type">Action</Label>
                  <Select
                    value={actionType}
                    onValueChange={(v) => setActionType(v as 'action_like' | 'action_comment')}
                  >
                    <SelectTrigger id="type">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="action_like">Like</SelectItem>
                      <SelectItem value="action_comment">Comment</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>

              <div className="space-y-2">
                <Label htmlFor="urls">Target URLs</Label>
                <Textarea
                  id="urls"
                  value={urls}
                  onChange={(e) => setUrls(e.target.value)}
                  placeholder={'https://www.instagram.com/p/…\nhttps://www.threads.net/p/…'}
                  rows={5}
                  className="font-mono text-xs"
                  disabled={busy || !canAct}
                />
              </div>
            </fieldset>

            {formError ? (
              <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {formError}
              </div>
            ) : null}

            <div className="flex items-center gap-3">
              <Button type="submit" disabled={busy || !canAct}>
                {busy ? <Loader2 className="animate-spin" /> : <Send />}
                Enqueue
              </Button>
              {inFlight > 0 ? (
                <span className="text-sm text-muted-foreground">{inFlight} in flight</span>
              ) : null}
            </div>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Queue</CardTitle>
          <CardDescription>
            {loading ? 'Loading…' : `${filtered.length} of ${actions.length} shown`}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex items-center gap-2">
            <Select value={status} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-40">
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
                        <span className="w-20 text-sm">
                          {row.job.actionType === 'action_comment' ? 'Comment' : 'Like'}
                        </span>
                        <span className="max-w-[260px] flex-1 truncate font-mono text-xs text-muted-foreground">
                          {row.job.targetUrl ?? row.job.targetId}
                        </span>
                        <span className="max-w-[240px] flex-1 truncate text-xs">
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

      <Dialog open={!!selected} onOpenChange={(o) => !o && setSelected(null)}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>
              {selected?.actionType === 'action_comment' ? 'Comment attempt' : 'Like attempt'}
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

/** Show the @handle when the account is already loaded; fall back to the id. */
function accountLabel(accounts: { id: string; username: string }[], id: string): string {
  const acc = accounts.find((a) => a.id === id);
  return acc ? `@${acc.username} (${id.slice(0, 8)})` : id.slice(0, 8);
}
