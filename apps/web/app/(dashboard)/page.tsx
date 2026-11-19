'use client';

import {
  Activity,
  AlertCircle,
  ArrowRight,
  CheckCircle2,
  Eye,
  Flag,
  Heart,
  MessageCircle,
  MessageSquare,
  PlayCircle,
  Reply,
  Search,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { useMemo } from 'react';

import { Button, Card, CardContent } from '@smm/ui';
import GlareHover from '@/components/reactbits/glare-hover';
import SpotlightCard from '@/components/reactbits/spotlight-card';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useAnalyticsOverview } from '@/lib/hooks/use-analytics';
import { useActions } from '@/lib/hooks/use-actions';
import { useContainers } from '@/lib/hooks/use-containers';

const ACTION_LABEL: Record<string, string> = {
  like: 'Likes',
  comment: 'Comments',
  reply: 'Replies',
  report: 'Reports',
};

export default function HomePage() {
  const overview = useAnalyticsOverview(30);
  const actions = useActions();
  const containers = useContainers();
  const accounts = useAccounts();

  const actionStats = useMemo(() => {
    const counts = new Map<string, number>();
    for (const job of actions.actions) {
      counts.set(job.actionType, (counts.get(job.actionType) ?? 0) + 1);
    }
    return counts;
  }, [actions.actions]);

  const queued = actions.actions.filter(
    (job) => job.status === 'PENDING' || job.status === 'RUNNING',
  ).length;
  const failed = actions.actions.filter((job) => job.status === 'FAILED').length;
  const liveWorkers = containers.containers.filter(
    (container) => container.desiredState === 'RUNNING',
  ).length;
  const authenticated = accounts.accounts.filter(
    (account) => account.authStatus === 'AUTHENTICATED',
  ).length;

  const kpis = overview.data?.kpis;
  const pick = (metric: string) => kpis?.find((k) => k.metric === metric)?.value;

  const needsSetup =
    !accounts.loading &&
    !containers.loading &&
    accounts.accounts.length === 0 &&
    containers.containers.length === 0;

  if (needsSetup) {
    return (
      <div className="mx-auto max-w-3xl">
        <SpotlightCard className="rounded-[26px] p-6 md:p-8">
          <p className="text-[11px] font-bold uppercase tracking-[0.14em] text-primary">First run</p>
          <h2 className="mt-2 text-2xl font-semibold tracking-[-0.035em]">Bring the workspace online</h2>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">
            A worker is the browser that holds an account. Add one, sign in an account, then
            queue the first action. The empty state is the guide.
          </p>
          <ol className="mt-6 space-y-3">
            {(
              [
                ['Create a worker', '/workers', 'Choose a city so the browser has a stable location.'],
                ['Add an account', '/accounts/new', 'Credentials stay encrypted; the session lives on the worker.'],
                ['Run an action', '/actions', 'Pick a post, then like, comment, or report it.'],
              ] as const
            ).map(([title, href, desc], index) => (
              <li key={title} className="flex items-start gap-3 rounded-2xl border border-white/50 bg-white/30 p-3.5 dark:border-white/10 dark:bg-white/5">
                <span className="grid size-7 shrink-0 place-items-center rounded-full bg-primary/12 text-xs font-bold text-primary">
                  {index + 1}
                </span>
                <div className="min-w-0">
                  <Link href={href} className="text-sm font-semibold hover:underline">
                    {title}
                  </Link>
                  <p className="mt-0.5 text-xs leading-5 text-muted-foreground">{desc}</p>
                </div>
              </li>
            ))}
          </ol>
          <Button asChild className="mt-6">
            <Link href="/workers">
              Create a worker
              <ArrowRight />
            </Link>
          </Button>
        </SpotlightCard>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.55fr)_minmax(280px,0.8fr)]">
        <SpotlightCard className="rounded-[26px] p-6 md:p-7">
          <p className="text-[11px] font-bold uppercase tracking-[0.14em] text-primary">
            Workspace pulse
          </p>
          <h2 className="mt-2 max-w-2xl text-2xl font-semibold leading-tight tracking-[-0.04em] md:text-[30px]">
            Keep the queue moving, one account at a time.
          </h2>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">
            {actions.loading
              ? 'Reading the queue…'
              : failed > 0
                ? `${failed} failed action${failed === 1 ? '' : 's'} need${failed === 1 ? 's' : ''} review. Open the queue to retry or cancel.`
                : queued > 0
                  ? `${queued} job${queued === 1 ? '' : 's'} waiting or running across ${liveWorkers} live worker${liveWorkers === 1 ? '' : 's'}.`
                  : 'The queue is clear. Use the quiet moment to review accounts or scrape the next batch.'}
          </p>

          <div className="mt-6 flex flex-wrap items-center gap-2.5">
            <GlareHover
              width="auto"
              height="auto"
              borderRadius="13px"
              glareColor="#ffffff"
              glareOpacity={0.22}
              glareSize={28}
              mode="hover"
              className="rounded-[13px]"
            >
              <Button asChild>
                <Link href="/actions">
                  Create an action
                  <ArrowRight />
                </Link>
              </Button>
            </GlareHover>
            <Button asChild variant="outline">
              <Link href="/scrape">
                <Search />
                Scrape posts
              </Link>
            </Button>
          </div>

          <div className="mt-7 grid grid-cols-3 gap-3 border-t border-white/50 pt-5 dark:border-white/10">
            <PulseMetric label="Queued / running" value={queued} icon={Activity} />
            <PulseMetric label="Failed" value={failed} icon={AlertCircle} tone={failed > 0 ? 'danger' : 'muted'} />
            <PulseMetric label="Authenticated" value={authenticated} icon={CheckCircle2} />
          </div>
        </SpotlightCard>

        <Card className="p-5 md:p-6">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold tracking-tight">Fleet</h2>
              <p className="mt-1 text-xs text-muted-foreground">Workers that can take work now</p>
            </div>
            <span className="grid size-9 place-items-center rounded-xl bg-primary/10 text-primary">
              <Users className="size-4" />
            </span>
          </div>
          <p className="mt-6 text-4xl font-semibold tracking-[-0.05em] tabular-nums">
            {liveWorkers}
            <span className="ml-1.5 text-sm font-medium tracking-normal text-muted-foreground">
              / {containers.containers.length}
            </span>
          </p>
          <p className="mt-1 text-xs text-muted-foreground">running / configured</p>
          <div className="mt-5 space-y-2.5 border-t border-border/60 pt-4 text-xs">
            <FleetRow label="Accounts" value={accounts.accounts.length} />
            <FleetRow label="Authenticated" value={authenticated} />
            <FleetRow label="Needs attention" value={accounts.accounts.length - authenticated} />
          </div>
          <Button asChild variant="outline" size="sm" className="mt-5 w-full">
            <Link href="/workers">
              Open fleet
              <ArrowRight />
            </Link>
          </Button>
        </Card>
      </section>

      <section className="grid grid-cols-2 gap-4 xl:grid-cols-4">
        <KpiCard label="Reach" icon={Eye} value={pick('reach')} loading={overview.loading} />
        <KpiCard label="Likes" icon={Heart} value={pick('likes')} loading={overview.loading} />
        <KpiCard label="Comments" icon={MessageCircle} value={pick('comments')} loading={overview.loading} />
        <KpiCard label="Reel views" icon={PlayCircle} value={pick('views')} loading={overview.loading} />
      </section>

      <section className="grid gap-5 xl:grid-cols-[minmax(0,1.35fr)_minmax(280px,0.65fr)]">
        <Card className="overflow-hidden">
          <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border/50 p-5">
            <div>
              <h2 className="text-sm font-semibold tracking-tight">Engagement trend</h2>
              <p className="mt-1 text-xs text-muted-foreground">Official-account reach, likes, and comments · 30 days</p>
            </div>
            <Button asChild variant="ghost" size="sm">
              <Link href="/reports">
                Open reports
                <ArrowRight />
              </Link>
            </Button>
          </div>
          <CardContent className="p-5">
            <div className="surface-note flex min-h-56 flex-col items-center justify-center gap-2 px-5 text-center">
              <span className="grid size-10 place-items-center rounded-2xl bg-white/55 text-muted-foreground dark:bg-white/5">
                <Activity className="size-5" />
              </span>
              <p className="text-sm font-medium text-foreground">
                {overview.loading ? 'Loading the series…' : 'No trend series yet'}
              </p>
              <p className="max-w-sm text-xs leading-5 text-muted-foreground">
                {overview.error
                  ? overview.error
                  : 'Connect an official account and refresh analytics to see the trend. The KPI values above stay blank until the first snapshot lands.'}
              </p>
            </div>
          </CardContent>
        </Card>

        <Card className="p-5">
          <div>
            <h2 className="text-sm font-semibold tracking-tight">Queue health</h2>
            <p className="mt-1 text-xs text-muted-foreground">The real action mix, not a sample</p>
          </div>
          {actions.loading ? (
            <p className="mt-6 text-sm text-muted-foreground">Loading the queue…</p>
          ) : actions.error ? (
            <p className="mt-6 text-sm text-destructive" role="alert">{actions.error}</p>
          ) : actions.actions.length === 0 ? (
            <div className="mt-6 rounded-2xl border border-dashed border-border/80 p-4 text-sm text-muted-foreground">
              No actions have been queued yet. Create one from a stored post to make this card useful.
            </div>
          ) : (
            <div className="mt-5 space-y-3">
              {[...actionStats.entries()]
                .sort((a, b) => b[1] - a[1])
                .map(([key, value]) => (
                  <SummaryRow
                    key={key}
                    icon={SUMMARY_ICON[key as keyof typeof SUMMARY_ICON] ?? Activity}
                    label={ACTION_LABEL[key] ?? key.replaceAll('_', ' ')}
                    value={value}
                  />
                ))}
            </div>
          )}
          <div className="mt-5 flex items-center justify-between border-t border-border/60 pt-4">
            <span className="text-xs text-muted-foreground">
              {queued} queued · {failed} failed
            </span>
            <Button asChild variant="ghost" size="sm">
              <Link href="/actions">
                Open queue
                <ArrowRight />
              </Link>
            </Button>
          </div>
        </Card>
      </section>
    </div>
  );
}

const SUMMARY_ICON = {
  like: Heart,
  comment: MessageSquare,
  reply: Reply,
  report: Flag,
} as const;

function PulseMetric({
  label,
  value,
  icon: Icon,
  tone = 'default',
}: {
  label: string;
  value: number;
  icon: typeof Activity;
  tone?: 'default' | 'danger' | 'muted';
}) {
  return (
    <div>
      <span className="flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
        <Icon className="size-3.5" />
        {label}
      </span>
      <span className={`mt-1 block text-xl font-semibold tabular-nums tracking-tight ${tone === 'danger' ? 'text-destructive' : ''}`}>
        {value}
      </span>
    </div>
  );
}

function FleetRow({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-semibold tabular-nums">{value}</span>
    </div>
  );
}

function KpiCard({
  label,
  icon: Icon,
  value,
  loading,
}: {
  label: string;
  icon: typeof Eye;
  value: number | null | undefined;
  loading: boolean;
}) {
  return (
    <SpotlightCard className="rounded-[22px] p-5" intensity={0.09} spotlightSize={260}>
      <div className="flex items-start justify-between gap-2">
        <span className="grid size-9 place-items-center rounded-xl bg-primary/10 text-primary">
          <Icon className="size-4" />
        </span>
        {loading ? <span className="text-[10px] text-muted-foreground">loading</span> : null}
      </div>
      <div className="mt-5 text-3xl font-semibold tracking-[-0.045em] tabular-nums">
        {loading || value == null ? '—' : value.toLocaleString()}
      </div>
      <div className="mt-1 text-xs font-medium text-muted-foreground">{label}</div>
    </SpotlightCard>
  );
}

function SummaryRow({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Eye;
  label: string;
  value: number;
}) {
  return (
    <div className="flex items-center justify-between gap-4 text-sm">
      <span className="flex items-center gap-2 text-muted-foreground">
        <Icon className="size-4" />
        {label}
      </span>
      <span className="font-semibold tabular-nums">{value.toLocaleString()}</span>
    </div>
  );
}
