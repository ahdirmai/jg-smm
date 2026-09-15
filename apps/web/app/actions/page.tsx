'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
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
  Textarea,
} from '@smm/ui';
import { AlertCircle, Loader2, Send } from 'lucide-react';
import { useMemo, useState } from 'react';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { PLATFORM_LABEL } from '@/lib/platforms';
import type { ActionItem, JobStatus } from '@/lib/api';

const STATUSES: JobStatus[] = ['PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'CANCELLED'];
const MAX_BATCH = 50;

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
                disabled={busy}
              />
            </div>

            {formError ? (
              <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {formError}
              </div>
            ) : null}

            <div className="flex items-center gap-3">
              <Button type="submit" disabled={busy}>
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

          <div className="overflow-hidden rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Status</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead>Verdict</TableHead>
                  <TableHead>Attempts</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.length === 0 && !loading ? (
                  <TableRow>
                    <TableCell colSpan={5} className="py-10 text-center text-muted-foreground">
                      The queue is empty. Enqueue above.
                    </TableCell>
                  </TableRow>
                ) : (
                  filtered.map((a) => (
                    <TableRow key={a.id}>
                      <TableCell>
                        <Badge variant={statusTone(a.status)}>{a.status}</Badge>
                      </TableCell>
                      <TableCell className="text-sm">
                        {a.actionType === 'action_comment' ? 'Comment' : 'Like'}
                      </TableCell>
                      <TableCell className="max-w-[280px] truncate font-mono text-xs text-muted-foreground">
                        {a.targetUrl ?? a.targetId}
                      </TableCell>
                      <TableCell className="max-w-[280px] truncate text-xs">
                        {a.renderedText ? (
                          <span className="text-foreground">{a.renderedText}</span>
                        ) : a.error ? (
                          <span className="text-destructive" title={a.error}>
                            {a.errorClass ? `${a.errorClass}: ` : ''}
                            {a.error}
                          </span>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-sm tabular-nums">{a.attempts}</TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
