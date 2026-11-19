'use client';

import {
  Activity,
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Clock3,
  Flag,
  Heart,
  MessageSquare,
  MonitorSmartphone,
  Plus,
  Reply,
  Search,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { useMemo } from 'react';

import { Badge, Button, Card } from '@smm/ui';
import SpotlightCard from '@/components/reactbits/spotlight-card';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { useContainers } from '@/lib/hooks/use-containers';
import type { ActionJob } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

const ACTION_LABEL: Record<string, string> = {
  action_like: 'Like',
  action_comment: 'Comment',
  action_reply_comment: 'Reply',
  action_report: 'Report',
};

function actionLabel(value: string): string {
  return ACTION_LABEL[value] ?? value.replace(/^action_/, '').replaceAll('_', ' ');
}

function statusTone(status: ActionJob['status']): 'success' | 'destructive' | 'secondary' | 'outline' {
  if (status === 'SUCCESS') return 'success';
  if (status === 'FAILED') return 'destructive';
  if (status === 'RUNNING') return 'secondary';
  return 'outline';
}

/**
 * Overview is a decision page, not an analytics dashboard.
 *
 * It answers three questions in order: what needs attention, is the fleet
 * ready, and what happened recently. The old reach/likes KPI strip and the
 * decorative trend sketch were removed; nothing here is invented, and every
 * row links to the page where the operator can act on it.
 */
export default function HomePage() {
  const actions = useActions();
  const containers = useContainers();
  const accounts = useAccounts();

  const metrics = useMemo(() => {
    const liveWorkers = containers.containers.filter((c) => c.desiredState === 'RUNNING').length;
    const authenticated = accounts.accounts.filter((a) => a.authStatus === 'AUTHENTICATED').length;
    const needsInput = accounts.accounts.filter((a) => a.authStatus === 'NEEDS_INPUT').length;
    const failedAuth = accounts.accounts.filter((a) => a.authStatus === 'FAILED').length;
    const pending = actions.actions.filter((a) => a.status === 'PENDING').length;
    const running = actions.actions.filter((a) => a.status === 'RUNNING').length;
    const failed = actions.actions.filter((a) => a.status === 'FAILED').length;
    const success = actions.actions.filter((a) => a.status === 'SUCCESS').length;
    return {
      liveWorkers,
      totalWorkers: containers.containers.length,
      authenticated,
      totalAccounts: accounts.accounts.length,
      needsInput,
      failedAuth,
      pending,
      running,
      failed,
      success,
      inFlight: pending + running,
    };
  }, [accounts.accounts, actions.actions, containers.containers]);

  const attention = useMemo(() => {
    const out: { title: string; detail: string; href: string; cta: string; tone: 'danger' | 'warning' | 'info' }[] = [];
    if (metrics.failed > 0) {
      out.push({
        title: `${metrics.failed} failed action${metrics.failed === 1 ? '' : 's'}`,
        detail: 'Open the queue to retry, or inspect the failure reason before trying again.',
        href: '/actions',
        cta: 'Review queue',
        tone: 'danger',
      });
    }
    if (metrics.needsInput > 0 || metrics.failedAuth > 0) {
      const count = metrics.needsInput + metrics.failedAuth;
      out.push({
        title: `${count} account${count === 1 ? '' : 's'} need a sign-in`,
        detail: 'A 2FA prompt or a failed session is blocking these accounts from taking work.',
        href: '/accounts',
        cta: 'Open accounts',
        tone: 'warning',
      });
    }
    if (metrics.totalWorkers === 0) {
      out.push({
        title: 'No workers configured',
        detail: 'Add a worker before adding an account or running an action.',
        href: '/workers',
        cta: 'Set up a worker',
        tone: 'info',
      });
    } else if (metrics.liveWorkers < metrics.totalWorkers) {
      out.push({
        title: `${metrics.totalWorkers - metrics.liveWorkers} worker${metrics.totalWorkers - metrics.liveWorkers === 1 ? '' : 's'} offline`,
        detail: 'Start the fleet so every account can be scheduled again.',
        href: '/workers',
        cta: 'Open fleet',
        tone: 'warning',
      });
    }
    if (metrics.totalAccounts === 0 && metrics.totalWorkers > 0) {
      out.push({
        title: 'No accounts yet',
        detail: 'Add the first account to make the action queue useful.',
        href: '/accounts?add=1',
        cta: 'Add account',
        tone: 'info',
      });
    }
    return out;
  }, [metrics]);

  const recent = useMemo(
    () =>
      [...actions.actions]
        .sort((a, b) => Date.parse(b.scheduledAt) - Date.parse(a.scheduledAt))
        .slice(0, 6),
    [actions.actions],
  );

  const authBreakdown = useMemo(() => {
    const buckets = [
      { key: 'AUTHENTICATED', label: 'Ready', tone: 'bg-success' },
      { key: 'NEEDS_INPUT', label: 'Needs input', tone: 'bg-warning' },
      { key: 'FAILED', label: 'Failed', tone: 'bg-destructive' },
      { key: 'PENDING', label: 'Pending', tone: 'bg-muted-foreground' },
    ];
    return buckets.map((bucket) => ({
      ...bucket,
      count: accounts.accounts.filter((a) => a.authStatus === bucket.key).length,
    }));
  }, [accounts.accounts]);

  const platformBreakdown = useMemo(() => {
    const counts = new Map<string, number>();
    for (const account of accounts.accounts) {
      counts.set(account.platform, (counts.get(account.platform) ?? 0) + 1);
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1]);
  }, [accounts.accounts]);

  const loading = actions.loading || containers.loading || accounts.loading;
  const error = actions.error || containers.error || accounts.error;

  return (
    <div className="space-y-5">
      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.5fr)_minmax(300px,0.8fr)]">
        <SpotlightCard className="rounded-[26px] p-6 md:p-7">
          <p className="text-[11px] font-bold uppercase tracking-[0.14em] text-primary">Workspace overview</p>
          <h2 className="mt-2 max-w-2xl text-2xl font-semibold leading-tight tracking-[-0.04em] md:text-[30px]">
            {loading
              ? 'Reading the workspace…'
              : attention.length > 0
                ? `${attention.length} thing${attention.length === 1 ? '' : 's'} need${attention.length === 1 ? 's' : ''} your attention.`
                : 'The workspace is ready for work.'}
          </h2>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">
            {loading
              ? 'Checking workers, accounts, and the action queue.'
              : attention.length > 0
                ? 'Start with the item that blocks the most work. Everything else can wait.'
                : `${metrics.liveWorkers} live worker${metrics.liveWorkers === 1 ? '' : 's'}, ${metrics.authenticated} authenticated account${metrics.authenticated === 1 ? '' : 's'}, and no failed jobs.`}
          </p>

          <div className="mt-6 flex flex-wrap items-center gap-2.5">
            <Button asChild>
              <Link href="/actions">
                <Plus />
                Create an action
              </Link>
            </Button>
            <Button asChild variant="outline">
              <Link href="/scrape">
                <Search />
                Scrape posts
              </Link>
            </Button>
            <Button asChild variant="ghost">
              <Link href="/workers">
                <MonitorSmartphone />
                Manage workers
              </Link>
            </Button>
          </div>

          <div className="mt-7 grid grid-cols-2 gap-3 border-t border-white/50 pt-5 sm:grid-cols-4 dark:border-white/10">
            <MiniStat label="Live workers" value={`${metrics.liveWorkers}/${metrics.totalWorkers}`} loading={containers.loading} />
            <MiniStat label="Accounts ready" value={`${metrics.authenticated}/${metrics.totalAccounts}`} loading={accounts.loading} />
            <MiniStat label="In flight" value={String(metrics.inFlight)} loading={actions.loading} />
            <MiniStat label="Failed" value={String(metrics.failed)} loading={actions.loading} danger={metrics.failed > 0} />
          </div>
        </SpotlightCard>

        <Card className="p-5 md:p-6">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold tracking-tight">Attention</h2>
              <p className="mt-1 text-xs text-muted-foreground">Ranked by what blocks work</p>
            </div>
            <span className="grid size-9 place-items-center rounded-xl bg-primary/10 text-primary">
              <AlertCircle className="size-4" />
            </span>
          </div>
          {loading ? (
            <div className="mt-5 space-y-2">
              <div className="h-14 animate-pulse rounded-xl bg-secondary/60" />
              <div className="h-14 animate-pulse rounded-xl bg-secondary/60" />
            </div>
          ) : attention.length === 0 ? (
            <div className="mt-5 rounded-2xl border border-success/25 bg-success/8 p-4">
              <p className="flex items-center gap-2 text-sm font-semibold text-success">
                <CheckCircle2 className="size-4" />
                All clear
              </p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                No failed actions or account sign-ins are waiting. Use the quiet moment to scrape the next batch.
              </p>
            </div>
          ) : (
            <ul className="mt-4 space-y-2">
              {attention.slice(0, 4).map((item) => (
                <li key={item.title}>
                  <Link
                    href={item.href}
                    className="block rounded-xl border border-border/60 bg-white/30 p-3 transition-colors hover:bg-white/55 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 dark:bg-white/5 dark:hover:bg-white/10"
                  >
                    <span className="flex items-start gap-2">
                      <span
                        className={`mt-1 size-1.5 shrink-0 rounded-full ${item.tone === 'danger' ? 'bg-destructive' : item.tone === 'warning' ? 'bg-warning' : 'bg-primary'}`}
                        aria-hidden="true"
                      />
                      <span className="min-w-0">
                        <span className="block text-xs font-semibold">{item.title}</span>
                        <span className="mt-0.5 block text-[11px] leading-4 text-muted-foreground">{item.detail}</span>
                        <span className="mt-1.5 inline-flex items-center gap-1 text-[11px] font-semibold text-primary">
                          {item.cta} <ArrowRight className="size-3" />
                        </span>
                      </span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
          {error ? (
            <p className="mt-4 text-xs text-destructive" role="alert">
              {error}
            </p>
          ) : null}
        </Card>
      </section>

      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.7fr)]">
        <Card className="overflow-hidden">
          <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border/50 p-5">
            <div>
              <h2 className="text-sm font-semibold tracking-tight">Recent actions</h2>
              <p className="mt-1 text-xs text-muted-foreground">The latest queue entries and their live status</p>
            </div>
            <Button asChild variant="ghost" size="sm">
              <Link href="/actions">
                Open queue <ArrowRight />
              </Link>
            </Button>
          </div>
          {actions.loading ? (
            <div className="space-y-2 p-5">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-12 animate-pulse rounded-xl bg-secondary/60" />
              ))}
            </div>
          ) : actions.error ? (
            <p className="p-5 text-sm text-destructive" role="alert">{actions.error}</p>
          ) : recent.length === 0 ? (
            <div className="p-8 text-center">
              <span className="mx-auto grid size-10 place-items-center rounded-2xl bg-primary/10 text-primary">
                <Activity className="size-5" />
              </span>
              <p className="mt-3 text-sm font-medium">No actions have been queued yet</p>
              <p className="mx-auto mt-1 max-w-sm text-xs leading-5 text-muted-foreground">
                Pick a stored post and create the first action. Its progress will appear here in real time.
              </p>
              <Button asChild variant="outline" size="sm" className="mt-4">
                <Link href="/actions">Create an action</Link>
              </Button>
            </div>
          ) : (
            <ul className="divide-y divide-border/50">
              {recent.map((job) => {
                const account = accounts.accounts.find((a) => a.id === job.accountId);
                return (
                  <li key={job.id} className="flex flex-wrap items-center gap-3 px-5 py-3">
                    <span className="grid size-8 shrink-0 place-items-center rounded-xl bg-secondary text-muted-foreground">
                      {actionIcon(job.actionType)}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-medium">{actionLabel(job.actionType)}</span>
                        <Badge variant={statusTone(job.status)}>{job.status}</Badge>
                      </span>
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                        {account ? `@${account.username}` : 'account'} · {job.targetUrl ?? job.targetId}
                      </span>
                    </span>
                    <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
                      {formatWhen(job.scheduledAt)}
                    </span>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>

        <div className="grid gap-5">
          <Card className="p-5">
            <div className="flex items-start justify-between gap-3">
              <div>
                <h2 className="text-sm font-semibold tracking-tight">Account health</h2>
                <p className="mt-1 text-xs text-muted-foreground">{metrics.totalAccounts} accounts</p>
              </div>
              <Users className="size-4 text-muted-foreground" />
            </div>
            <div className="mt-5 space-y-3">
              {authBreakdown.map((bucket) => (
                <div key={bucket.key}>
                  <div className="flex items-center justify-between text-xs">
                    <span className="text-muted-foreground">{bucket.label}</span>
                    <span className="font-semibold tabular-nums">{bucket.count}</span>
                  </div>
                  <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-secondary">
                    <div
                      className={`h-full rounded-full ${bucket.tone}`}
                      style={{ width: `${metrics.totalAccounts > 0 ? (bucket.count / metrics.totalAccounts) * 100 : 0}%` }}
                    />
                  </div>
                </div>
              ))}
            </div>
            <Button asChild variant="outline" size="sm" className="mt-5 w-full">
              <Link href="/accounts">Review accounts <ArrowRight /></Link>
            </Button>
          </Card>

          <Card className="p-5">
            <h2 className="text-sm font-semibold tracking-tight">By platform</h2>
            {platformBreakdown.length === 0 ? (
              <p className="mt-4 text-xs text-muted-foreground">No accounts added yet.</p>
            ) : (
              <ul className="mt-4 space-y-2.5">
                {platformBreakdown.map(([platform, count]) => (
                  <li key={platform} className="flex items-center justify-between text-xs">
                    <span className="text-muted-foreground">{PLATFORM_LABEL[platform as keyof typeof PLATFORM_LABEL] ?? platform}</span>
                    <span className="font-semibold tabular-nums">{count}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </section>
    </div>
  );
}

function actionIcon(actionType: string) {
  switch (actionType) {
    case 'action_like':
      return <Heart className="size-4" />;
    case 'action_comment':
      return <MessageSquare className="size-4" />;
    case 'action_reply_comment':
      return <Reply className="size-4" />;
    case 'action_report':
      return <Flag className="size-4" />;
    default:
      return <Clock3 className="size-4" />;
  }
}

function formatWhen(value: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return '';
  return date.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function MiniStat({
  label,
  value,
  loading,
  danger = false,
}: {
  label: string;
  value: string;
  loading: boolean;
  danger?: boolean;
}) {
  return (
    <div>
      <span className="block text-[11px] font-medium text-muted-foreground">{label}</span>
      <span className={`mt-1 block text-xl font-semibold tabular-nums tracking-tight ${danger ? 'text-destructive' : ''}`}>
        {loading ? '—' : value}
      </span>
    </div>
  );
}
