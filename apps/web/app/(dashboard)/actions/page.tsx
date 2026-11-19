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
} from '@smm/ui';
import { AlertCircle, Check, History, ListChecks, Loader2, Play, Plus, RotateCcw, X, XCircle } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';

import { NewActionForm, type NewActionFormHandle } from './new-action-form';
import { ScrapeHistory } from './scrape-history';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { useContainers } from '@/lib/hooks/use-containers';
import { useVirtualRowWindow } from '@/lib/hooks/use-virtual-window';
import type { ActionJob, JobStatus } from '@/lib/api';
import { isOnDemandPlatform } from '@/lib/platforms';

const STATUSES: JobStatus[] = ['PENDING', 'RUNNING', 'SUCCESS', 'FAILED', 'CANCELLED'];

/** The operator-facing name of a status, and of the unfiltered state. */
const STATUS_LABEL: Record<string, string> = {
  PENDING: 'Queued',
  RUNNING: 'Running',
  SUCCESS: 'Succeeded',
  FAILED: 'Failed',
  CANCELLED: 'Cancelled',
};

function statusLabel(value: string): string {
  return value === 'all' ? 'All statuses' : (STATUS_LABEL[value] ?? value[0] + value.slice(1).toLowerCase());
}
// Uniform row height: group headers and job rows share it so the window math
// stays fixed-height (see useVirtualRowWindow).
const ROW_HEIGHT = 44;

// Sentinel for the header's whole-queue cancel; it shares the arm state with
// per-row cancels so arming one control disarms the other.
const CANCEL_ALL = 'all-pending';

function workerLabel(workerId: string | null | undefined, containers: Array<{ id: string; name: string }>): string {
  if (!workerId) return 'unassigned';
  return containers.find((c) => c.id === workerId)?.name ?? workerId.slice(0, 8);
}

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
  const { containers } = useContainers();
  const { actions, loading, error, retry, retryFailed, runPendingNow, cancel, cancelPendingNow } = useActions();
  const [tab, setTab] = useState<'create' | 'scrape' | 'queue'>('create');

  const formRef = useRef<NewActionFormHandle>(null);
  // Bumped after a fresh scrape so the history card re-reads the stored list.
  const [historyRefresh, setHistoryRefresh] = useState(0);

  // Deep-link from the batch preview: /actions?post=<permalink>&platform=<p>
  // preloads the form and runs the scrape, so "Buat Action" on a discovered post
  // lands the operator on a filled-in form instead of an empty one.
  //
  // A ref, not a state guard: the effect must run at most ONCE per mount. React
  // StrictMode double-invokes effects in development, and running twice would
  // fire two paid scrape calls for one click. `replaceState` then clears the
  // query so a reload doesn't re-trigger it either.
  const deepLinkDone = useRef(false);
  useEffect(() => {
    if (deepLinkDone.current) return;
    deepLinkDone.current = true;

    const params = new URLSearchParams(window.location.search);
    const post = params.get('post');
    const platform = params.get('platform');
    if (!post) return;
    // Only the on-demand platforms with an actor can be preloaded; anything
    // else would 400. YouTube is included: the same actor takes a video URL.
    if (!platform || !isOnDemandPlatform(platform)) return;

    // Wait one frame: `loadPost` re-enters the form's own state, and on the
    // first paint the imperative handle is attached but the scrape effect has
    // not yet read the url it sets.
    setTab('create');
    const t = window.setTimeout(() => formRef.current?.loadPost(platform, post), 0);
    window.history.replaceState({}, '', window.location.pathname);
    return () => window.clearTimeout(t);
  }, []);

  const [status, setStatusFilter] = useState<string>('all');
  // Job id currently being re-queued, so its row shows a spinner and the
  // button cannot be double-pressed into two retries.
  const [retrying, setRetrying] = useState<string | null>(null);
  const [retryError, setRetryError] = useState<string | null>(null);
  // Set while the whole-queue "run now" is in flight, so the button shows a
  // spinner and cannot be pressed twice into two overlapping sweeps.
  const [sweeping, setSweeping] = useState(false);
  // Cancelling is destructive and irreversible (it is not a retry: the row
  // stays terminal), so a cancel click only arms the action; the second click
  // on the same control executes it. One state covers a row id and the
  // whole-queue sentinel.
  const [armedCancel, setArmedCancel] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);
  // Bulk retry posts real comments/likes, so it is armed exactly like cancel.
  const [armedRetryAll, setArmedRetryAll] = useState(false);
  const [retryingAll, setRetryingAll] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  const inFlight = useMemo(
    () => actions.filter((a) => a.status === 'PENDING' || a.status === 'RUNNING').length,
    [actions],
  );

  // Per-status tally for the filter chips: the old "4 of 4 shown" line told an
  // operator nothing about whether the queue was healthy or stuck.
  const tally = useMemo(() => {
    const t: Record<string, number> = { all: actions.length };
    for (const s of STATUSES) t[s] = 0;
    for (const a of actions) t[a.status] = (t[a.status] ?? 0) + 1;
    return t;
  }, [actions]);

  const failed = tally.FAILED ?? 0;
  // Pending jobs carry a future scheduled_at while a backoff runs, so "why is
  // nothing happening" is answerable without opening the API logs.
  const pendingJobs = useMemo(
    () => actions.filter((a) => a.status === 'PENDING'),
    [actions],
  );
  const pendingCount = pendingJobs.length;
  const nextDueLabel = useMemo(() => {
    const waiting = pendingJobs
      .map((a) => new Date(a.scheduledAt).getTime())
      .filter((t) => Number.isFinite(t) && t > Date.now());
    if (waiting.length === 0) return 'nothing waiting on a timer';
    const soonest = Math.min(...waiting);
    const secs = Math.round((soonest - Date.now()) / 1000);
    if (secs < 60) return `next in ${secs}s`;
    const mins = Math.round(secs / 60);
    return `next in ${mins}m`;
  }, [pendingJobs]);

  // One retry at a time: the button disables per-row, and this clears the
  // previous error so a second click does not leave a stale banner up.
  async function doRetry(jobId: string) {
    setRetryError(null);
    setRetrying(jobId);
    try {
      await retry(jobId);
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : 'Retry failed');
    } finally {
      setRetrying(null);
    }
  }

  // Pull every PENDING job forward. Backoff is the queue protecting the
  // platform, not the operator: the per-account velocity gate still applies to
  // what the worker does after the claim, so this ends the wait without
  // removing the protection.
  async function doRunPendingNow() {
    setRetryError(null);
    setNotice(null);
    setSweeping(true);
    try {
      const moved = await runPendingNow();
      setNotice(
        moved === 0
          ? 'Nothing was waiting — the queue is empty or already running.'
          : `Moved ${moved} job${moved === 1 ? '' : 's'} to now — workers pick them up on their next poll.`,
      );
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : 'Could not run the queue');
    } finally {
      setSweeping(false);
    }
  }
  // Bulk retry: every FAILED job from the last 24h. The arm click is the
  // confirmation because a retry can publish a comment; old jobs are skipped
  // by the hook (their per-account body TTL has expired).
  async function doRetryAll() {
    if (!armedRetryAll) {
      setArmedRetryAll(true);
      return;
    }
    setArmedRetryAll(false);
    setRetryError(null);
    setNotice(null);
    setRetryingAll(true);
    try {
      const { requeued, failed: failedToRequeue, skippedOld } = await retryFailed();
      const parts = [
        requeued === 0
          ? 'No failed job was requeued.'
          : `Requeued ${requeued} failed job${requeued === 1 ? '' : 's'}.`,
      ];
      if (skippedOld > 0) {
        parts.push(
          `Skipped ${skippedOld} older than 24h — their per-account text may have expired.`,
        );
      }
      if (failedToRequeue > 0) {
        parts.push(`${failedToRequeue} could not be requeued.`);
      }
      setNotice(parts.join(' '));
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : 'Could not retry failed jobs');
    } finally {
      setRetryingAll(false);
    }
  }

  // Cancel one queued/running job. Armed first: the click that turns the
  // button destructive is the confirmation. Throws on the server's 400 when
  // the job already finished, surfaced as the row error banner.
  async function doCancel(jobId: string) {
    if (armedCancel !== jobId) {
      setArmedCancel(jobId);
      return;
    }
    setArmedCancel(null);
    setRetryError(null);
    setNotice(null);
    setCancelling(jobId);
    try {
      await cancel(jobId);
      // The dialog holds a snapshot of the job, which this cancel just made
      // stale; closing it is honest, and it is a no-op for a row cancel.
      setSelected(null);
      setNotice('Job cancelled. A worker already running it may still finish the action; its report will not overwrite this verdict.');
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : 'Cancel failed');
    } finally {
      setCancelling(null);
    }
  }

  // Cancel the whole PENDING queue. Same two-click arm; RUNNING rows stay
  // untouched (the worker may already be mid-action), which the label says.
  async function doCancelPendingNow() {
    if (armedCancel !== CANCEL_ALL) {
      setArmedCancel(CANCEL_ALL);
      return;
    }
    setArmedCancel(null);
    setRetryError(null);
    setNotice(null);
    setSweeping(true);
    try {
      const cancelled = await cancelPendingNow();
      setNotice(
        cancelled === 0
          ? 'Nothing was queued — no pending job to cancel.'
          : `Cancelled ${cancelled} pending job${cancelled === 1 ? '' : 's'}. Running jobs are untouched.`,
      );
    } catch (err) {
      setRetryError(err instanceof Error ? err.message : 'Could not cancel the queue');
    } finally {
      setSweeping(false);
    }
  }

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
    <div className="mx-auto max-w-6xl space-y-5">
      <div className="grid grid-cols-3 gap-1.5 rounded-2xl border border-border/60 bg-white/40 p-1.5 sm:flex sm:flex-wrap sm:items-center dark:bg-white/5" aria-label="Actions sections">
        <SectionTab active={tab === 'create'} onClick={() => setTab('create')} icon={Plus} label="Create action" />
        <SectionTab active={tab === 'scrape'} onClick={() => setTab('scrape')} icon={History} label="Scraped posts" />
        <SectionTab
          active={tab === 'queue'}
          onClick={() => setTab('queue')}
          icon={ListChecks}
          label="Job queue"
          {...(failed > 0 ? { badge: String(failed), danger: true } : inFlight > 0 ? { badge: String(inFlight) } : {})}
        />
      </div>

      <div hidden={tab !== 'create'}>
        {/* New Action: platform → link → scrape → action → per-account comment → submit. */}
        <NewActionForm
          ref={formRef}
          onScraped={() => setHistoryRefresh((n) => n + 1)}
          onEnqueued={() => setTab('queue')}
        />
      </div>

      <div hidden={tab !== 'scrape'}>
        {/* A stored row opens the post detail panel. "Use as target" loads the
          stored row without a second scrape, switches to Create action, and
          scrolls to the form so the result is visible. */}
        <ScrapeHistory
          refreshKey={historyRefresh}
          onUseAsTarget={(post, flow) => {
            formRef.current?.loadStoredPost(post, flow);
            setTab('create');
            window.scrollTo({ top: 0, behavior: 'smooth' });
          }}
        />
      </div>

      <div hidden={tab !== 'queue'}>
      <Card>
        <CardHeader className="flex-col items-start gap-3 space-y-0 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <CardTitle>Job queue</CardTitle>
            <CardDescription>
              {loading
                ? 'Loading…'
                : failed > 0
                  ? `${failed} failed — retry from the header, a row, or the detail dialog`
                  : inFlight > 0
                    ? `${inFlight} running · ${nextDueLabel}`
                    : `${actions.length} job${actions.length === 1 ? '' : 's'}, all settled`}
              {loading || failed > 0 || inFlight > 0
                ? null
                : pendingCount > 0
                  ? // A settled queue can still hold jobs parked on a backoff
                    // timer; show the wait instead of implying the queue is done.
                    ` · ${nextDueLabel}`
                  : null}
            </CardDescription>
          </div>
          <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
            <span className="flex items-center gap-2 text-xs text-muted-foreground">
              {inFlight > 0 ? <span className="size-2 animate-pulse rounded-full bg-primary" /> : null}
              {inFlight} in flight
            </span>
            {failed > 0 ? (
              <Button
                variant={armedRetryAll ? 'destructive' : 'outline'}
                size="sm"
                disabled={retryingAll}
                onClick={() => void doRetryAll()}
                onBlur={() => setArmedRetryAll(false)}
                title={
                  armedRetryAll
                    ? `Press again to retry ${failed} failed job${failed === 1 ? '' : 's'} (real public actions)`
                    : `Retry failed jobs from the last 24h (${failed} failed; older jobs are skipped)`
                }
              >
                {retryingAll ? <Loader2 className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />}
                {retryingAll ? 'Retrying…' : armedRetryAll ? `Confirm retry ${failed}` : `Retry failed (${failed})`}
              </Button>
            ) : null}
            {pendingCount > 0 ? (
              <Button
                variant="outline"
                size="sm"
                disabled={sweeping}
                onClick={() => void doRunPendingNow()}
                title={`Move ${pendingCount} queued job${pendingCount === 1 ? '' : 's'} to now, skipping the backoff wait`}
              >
                {sweeping ? <Loader2 className="size-3.5 animate-spin" /> : <Play className="size-3.5" />}
                {sweeping ? 'Running…' : 'Run queued now'}
              </Button>
            ) : null}
            {pendingCount > 0 ? (
              <Button
                variant={armedCancel === CANCEL_ALL ? 'destructive' : 'ghost'}
                size="sm"
                disabled={sweeping || cancelling !== null}
                onClick={() => void doCancelPendingNow()}
                onBlur={() => {
                  if (armedCancel === CANCEL_ALL) setArmedCancel(null);
                }}
                title={
                  armedCancel === CANCEL_ALL
                    ? `Press again to cancel ${pendingCount} queued job${pendingCount === 1 ? '' : 's'}`
                    : 'Cancel every queued job. Running jobs are untouched.'
                }
              >
                {armedCancel === CANCEL_ALL ? <XCircle className="size-3.5" /> : <X className="size-3.5" />}
                {armedCancel === CANCEL_ALL ? 'Confirm cancel' : 'Cancel queued'}
              </Button>
            ) : null}
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex flex-wrap items-center gap-2">
            <FilterChip
              label="All"
              count={tally.all ?? 0}
              active={status === 'all'}
              onClick={() => setStatusFilter('all')}
            />
            {STATUSES.map((s) => (
              <FilterChip
                key={s}
                label={statusLabel(s)}
                count={tally[s] ?? 0}
                active={status === s}
                tone={statusTone(s)}
                onClick={() => setStatusFilter(s)}
              />
            ))}
          </section>

          {notice ? (
            <div className="flex items-center gap-2 rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm">
              <Check className="size-4" />
              {notice}
            </div>
          ) : null}
          {retryError ? (
            <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {retryError}
            </div>
          ) : null}

          {error ? (
            <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {error}
            </div>
          ) : null}

          {rows.length === 0 && !loading ? (
            <div className="flex items-center justify-center rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
              The queue is empty. Enqueue above.
            </div>
          ) : (
            // Windowed: only the visible slice (plus overscan) renders, so a
            // 5k-row queue costs the same React work as a 50-row one. The
            // before/after spacers keep the scrollbar proportional.
            <div
              ref={virtual.ref}
              className="max-h-[480px] overflow-y-auto rounded-lg border border-border/60"
              role="table"
              aria-label="Action queue"
            >
              <div style={{ height: win.before + rows.length * ROW_HEIGHT, position: 'relative' }}>
                <div style={{ transform: `translateY(${win.before}px)` }}>
                  {rows.slice(win.start, win.end).map((row) =>
                    row.kind === 'group' ? (
                      <div
                        key={`g-${row.accountId}`}
                        className="flex items-center border-b border-border/60 bg-secondary/50 px-3 text-xs font-medium uppercase tracking-wide text-muted-foreground"
                        style={{ height: ROW_HEIGHT }}
                      >
                        Account {accountLabel(accounts, row.accountId)}
                      </div>
                    ) : (
                      <div
                        key={row.job.id}
                        role="row"
                        tabIndex={0}
                        onClick={() => setSelected(row.job)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            setSelected(row.job);
                          }
                        }}
                        className="flex w-full min-w-0 cursor-pointer items-center gap-3 overflow-hidden border-b border-border/60 px-3 text-left transition-colors hover:bg-secondary/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring md:gap-4"
                        style={{ height: ROW_HEIGHT }}
                      >
                        <Badge variant={statusTone(row.job.status)} className="shrink-0">{STATUS_LABEL[row.job.status] ?? row.job.status}</Badge>
                        <span className="w-16 shrink-0 text-xs font-medium sm:w-20 sm:text-sm">{actionLabel(row.job.actionType)}</span>
                        <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">
                          {row.job.targetUrl ?? row.job.targetId}
                          {row.job.workerId ? (
                            <span className="ml-2 font-sans text-[10px] text-muted-foreground">
                              via {workerLabel(row.job.workerId, containers)}
                            </span>
                          ) : null}
                        </span>
                        <span className="hidden md:block">
                          <Stepper
                            reached={stageReached(row.job.status)}
                            failed={row.job.status === 'FAILED'}
                          />
                        </span>
                        <span className="hidden max-w-[200px] flex-1 truncate text-xs md:block">
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
                        <span
                          className="hidden w-10 shrink-0 text-right text-sm tabular-nums text-muted-foreground sm:inline"
                          title={`${row.job.attempts} attempt${row.job.attempts === 1 ? '' : 's'}`}
                          aria-label={`${row.job.attempts} attempt${row.job.attempts === 1 ? '' : 's'}`}
                        >
                          {row.job.attempts}
                        </span>
                        {/* Only meaningful while queued: a finished job's
                            scheduled_at is history. Absolute time on hover so
                            "in 42s" stays readable without losing the instant. */}
                        {row.job.status === 'PENDING' ? (
                          <span
                            className="hidden w-24 shrink-0 text-right text-xs tabular-nums text-muted-foreground sm:inline"
                            title={`Scheduled for ${new Date(row.job.scheduledAt).toLocaleString()}`}
                          >
                            {countdown(row.job.scheduledAt)}
                          </span>
                        ) : null}
                        {row.job.status === 'FAILED' ? (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-9 shrink-0 sm:h-7"
                            disabled={retrying === row.job.id}
                            onClick={(e) => {
                              // The row is clickable; without this the click
                              // opens the detail dialog behind the retry.
                              e.stopPropagation();
                              void doRetry(row.job.id);
                            }}
                          >
                            {retrying === row.job.id ? (
                              <Loader2 className="size-3 animate-spin" />
                            ) : (
                              <RotateCcw className="size-3" />
                            )}
                            Retry
                          </Button>
                        ) : null}
                        {row.job.status === 'PENDING' || row.job.status === 'RUNNING' ? (
                          <Button
                            variant={armedCancel === row.job.id ? 'destructive' : 'outline'}
                            size="sm"
                            className="h-9 shrink-0 sm:h-7"
                            disabled={cancelling === row.job.id}
                            title={
                              row.job.status === 'RUNNING'
                                ? 'Stop this job. A worker already running it may still finish the action; its report will not overwrite the cancel.'
                                : 'Cancel this queued job'
                            }
                            onClick={(e) => {
                              e.stopPropagation();
                              void doCancel(row.job.id);
                            }}
                            onBlur={() => {
                              if (armedCancel === row.job.id) setArmedCancel(null);
                            }}
                          >
                            {cancelling === row.job.id ? (
                              <Loader2 className="size-3 animate-spin" />
                            ) : armedCancel === row.job.id ? (
                              <XCircle className="size-3" />
                            ) : (
                              <X className="size-3" />
                            )}
                            {armedCancel === row.job.id ? 'Confirm' : 'Cancel'}
                          </Button>
                        ) : null}
                      </div>
                    ),
                  )}
                </div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>
      </div>

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
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1">
                  <div className="text-xs text-muted-foreground">Target</div>
                  <div className="break-all font-mono text-xs">
                    {selected.targetUrl ?? selected.targetId}
                  </div>
                </div>
                <div className="space-y-1">
                  <div className="text-xs text-muted-foreground">Executed via</div>
                  <div className="text-sm font-medium">
                    {selected.workerId ? workerLabel(selected.workerId, containers) : 'Not recorded for this older job'}
                  </div>
                </div>
              </div>
              {selected.renderedText ? (
                <div className="space-y-1">
                  <div className="text-xs text-muted-foreground">Composed comment</div>
                  <div className="rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm">
                    {selected.renderedText}
                  </div>
                </div>
              ) : null}
              {selected.error ? (
                <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 size-4" />
                  <span className="whitespace-pre-wrap">{selected.error}</span>
                </div>
              ) : null}
            </div>
          ) : null}
          <DialogFooter>
            {selected?.status === 'PENDING' || selected?.status === 'RUNNING' ? (
              <Button
                variant={armedCancel === selected.id ? 'destructive' : 'outline'}
                disabled={cancelling === selected.id}
                onClick={() => void doCancel(selected.id)}
              >
                {cancelling === selected.id ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : armedCancel === selected.id ? (
                  <XCircle className="size-4" />
                ) : (
                  <X className="size-4" />
                )}
                {armedCancel === selected.id ? 'Confirm cancel' : 'Cancel this action'}
              </Button>
            ) : null}
            {selected?.status === 'FAILED' ? (
              <Button
                variant="outline"
                disabled={retrying === selected.id}
                onClick={() => {
                  void doRetry(selected.id).then(() => setSelected(null));
                }}
              >
                {retrying === selected.id ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <RotateCcw className="size-4" />
                )}
                Retry this action
              </Button>
            ) : null}
            <Button variant="outline" onClick={() => setSelected(null)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/**
 * Status filter as a toggle chip with its live count. Replaces a Select: a
 * dropdown hid the counts, so an operator could not see "6 failed" without
 * opening it and switching to that value to find out.
 */
/**
 * How long until a queued job becomes claimable, in the unit a person waiting
 * on it actually thinks in. "now" past the due time rather than a negative
 * count — the row is a few seconds stale at worst, and "-3s" reads as broken.
 */
function countdown(scheduledAt: string): string {
  const ms = new Date(scheduledAt).getTime() - Date.now();
  if (!Number.isFinite(ms)) return '—';
  if (ms <= 0) return 'now';
  const secs = Math.round(ms / 1000);
  if (secs < 60) return `in ${secs}s`;
  const mins = Math.round(secs / 60);
  if (mins < 60) return `in ${mins}m`;
  const hrs = Math.round(mins / 60);
  return hrs < 48 ? `in ${hrs}h` : `in ${Math.round(hrs / 24)}d`;
}

function SectionTab({
  active,
  onClick,
  icon: Icon,
  label,
  badge,
  danger = false,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof Plus;
  label: string;
  badge?: string;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={[
        'inline-flex min-h-11 flex-col items-center justify-center gap-1 rounded-xl px-2 py-1 text-[11px] font-medium transition-colors sm:flex-row sm:gap-2 sm:px-3.5 sm:text-sm',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60',
        active
          ? 'bg-primary text-primary-foreground shadow-[inset_0_1px_0_rgb(255_255_255/0.18)]'
          : 'text-muted-foreground hover:bg-white/55 hover:text-foreground dark:hover:bg-white/10',
      ].join(' ')}
    >
      <Icon className="size-4" />
      {label}
      {badge ? (
        <span
          className={[
            'inline-flex min-w-5 items-center justify-center rounded-full px-1.5 text-[10px] font-bold tabular-nums',
            active
              ? 'bg-white/20 text-primary-foreground'
              : danger
                ? 'bg-destructive/12 text-destructive'
                : 'bg-secondary text-muted-foreground',
          ].join(' ')}
        >
          {badge}
        </span>
      ) : null}
    </button>
  );
}

function FilterChip({
  label,
  count,
  active,
  tone,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  tone?: 'success' | 'outline' | 'secondary' | 'destructive';
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={[
        'inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
        active
          ? 'border-primary bg-primary/10 text-foreground'
          : 'border-border text-muted-foreground hover:bg-secondary/60',
        // Only paint the tone when it is a live problem or a good outcome;
        // a muted SUCCESS badge on every row is noise.
        !active && count > 0 && tone === 'destructive'
          ? 'border-destructive/50 text-destructive'
          : '',
        !active && count > 0 && tone === 'success'
          ? 'text-success'
          : '',
      ].join(' ')}
    >
      {label}
      <span className="tabular-nums opacity-70">{count}</span>
    </button>
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
