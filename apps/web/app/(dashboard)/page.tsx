'use client';

import {
  ArrowRight,
  Eye,
  Flag,
  Heart,
  MessageCircle,
  MessageSquare,
  PlayCircle,
  Reply,
} from 'lucide-react';
import Link from 'next/link';

import { Button, Card, CardContent } from '@smm/ui';
import { useAnalyticsOverview } from '@/lib/hooks/use-analytics';
import { useActions } from '@/lib/hooks/use-actions';
import { useContainers } from '@/lib/hooks/use-containers';

/**
 * Dashboard (P6-02, mirrors docs/prototype/dashboard.html).
 *
 * The overview reads official-account analytics (3rd-party ingest) plus the
 * automation summary from the live action queue. With an empty fleet — the
 * default state — the page explains the first step instead of showing zeros.
 */
export default function HomePage() {
  const overview = useAnalyticsOverview(30);
  const actions = useActions();
  const containers = useContainers();

  const fleetEmpty = containers.containers.length === 0;
  const queued = actions.actions.length;

  if (fleetEmpty) {
    return (
      <div className="mx-auto max-w-3xl space-y-6">
        <Card>
          <CardContent className="space-y-4 p-6">
            <div>
              <h2 className="text-lg font-semibold tracking-tight">Getting started</h2>
              <p className="text-sm text-muted-foreground">
                The fleet starts empty. There are no worker containers by default — you create them.
                Three steps to a first action.
              </p>
            </div>
            <ol className="space-y-3 text-sm">
              {(
                [
                  [
                    'Create a worker',
                    '/workers',
                    'Pick a city; the container is anchored to a GPS point inside it.',
                  ],
                  [
                    'Add an account',
                    '/accounts/new',
                    'One credential set per platform per worker.',
                  ],
                  [
                    'Run an action',
                    '/actions',
                    'Target a post; the worker likes/comments/reports it.',
                  ],
                ] as const
              ).map(([title, href, desc]) => (
                <li key={title} className="flex items-start gap-3">
                  <span className="mt-0.5 grid size-6 shrink-0 place-items-center rounded-lg bg-secondary text-xs font-medium text-foreground">
                    {title[0]}
                  </span>
                  <div className="min-w-0">
                    <Link
                      href={href}
                      className="font-medium text-foreground hover:text-primary hover:underline"
                    >
                      {title}
                    </Link>
                    <p className="text-xs text-muted-foreground">{desc}</p>
                  </div>
                </li>
              ))}
            </ol>
            <Button asChild>
              <Link href="/workers">
                Create a worker
                <ArrowRight />
              </Link>
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  const kpis = overview.data?.kpis;
  const pick = (metric: string) => kpis?.find((k) => k.metric === metric)?.value;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end gap-2">
        <Button variant="outline" size="sm">
          Last 30 days
        </Button>
        <Button asChild size="sm">
          <Link href="/reports">Export CSV</Link>
        </Button>
      </div>

      {/* KPI strip: official-account reach, not worker accounts. */}
      <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <KpiCard label="Reach" icon={Eye} value={pick('reach')} loading={overview.loading} />
        <KpiCard label="Likes" icon={Heart} value={pick('likes')} loading={overview.loading} />
        <KpiCard
          label="Comments"
          icon={MessageCircle}
          value={pick('comments')}
          loading={overview.loading}
        />
        <KpiCard
          label="Views (Reels)"
          icon={PlayCircle}
          value={pick('views')}
          loading={overview.loading}
        />
      </section>

      <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardContent className="space-y-3 p-4">
            <div>
              <h2 className="text-sm font-semibold">Engagement trend</h2>
              <p className="text-xs text-muted-foreground">Reach · Likes · Comments · daily</p>
            </div>
            <div className="flex h-56 w-full items-center justify-center rounded-lg border border-border/60 bg-background/40 p-2">
              {overview.loading ? (
                <p className="text-xs text-muted-foreground">Loading trend…</p>
              ) : overview.error ? (
                <p className="text-xs text-muted-foreground">{overview.error}</p>
              ) : (
                <TrendPlaceholder />
              )}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="space-y-4 p-4">
            <div>
              <h2 className="text-sm font-semibold">Automation today</h2>
              <p className="text-xs text-muted-foreground">Actions executed by workers</p>
            </div>
            <div className="space-y-3 text-sm">
              <SummaryRow icon={MessageSquare} label="Comments" value={queued} />
              <SummaryRow icon={Heart} label="Likes" value={0} />
              <SummaryRow icon={Flag} label="Reports" value={0} />
              <SummaryRow icon={Reply} label="Replies" value={0} />
            </div>
            <div className="h-px bg-border" />
            <div className="flex items-center gap-2 text-xs">
              <span className="inline-block size-2 animate-pulse rounded-full bg-success" />
              <span className="text-muted-foreground">
                {containers.containers.length} worker{containers.containers.length === 1 ? '' : 's'}{' '}
                ·{' '}
                {
                  containers.containers.filter((c) => c.status === 'READY' || c.status === 'IDLE')
                    .length
                }{' '}
                live
              </span>
            </div>
          </CardContent>
        </Card>
      </section>

      <Card>
        <div className="flex items-center justify-between border-b p-4">
          <div>
            <h2 className="text-sm font-semibold">Top posts</h2>
            <p className="text-xs text-muted-foreground">Ranked by reach · last 30 days</p>
          </div>
          <Button variant="outline" size="sm">
            Filter
          </Button>
        </div>
        <div className="px-4 py-10 text-center text-sm text-muted-foreground">
          Top posts for official accounts appear here once 3rd-party analytics is synced.
        </div>
      </Card>
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
    <Card>
      <CardContent className="space-y-3 p-5">
        {/* Principle 6: small monochrome icon in a soft rounded square. */}
        <span className="grid size-9 place-items-center rounded-lg bg-secondary text-muted-foreground">
          <Icon className="size-4" />
        </span>
        {/* Principle 4: the number is the hero — bold, large, dark; the label
         * is a small muted line beneath it, not beside it. */}
        <div className="text-3xl font-semibold tracking-tight tabular-nums">
          {loading || value == null ? '—' : value.toLocaleString()}
        </div>
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
      </CardContent>
    </Card>
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
    <div className="flex items-center justify-between text-sm">
      <span className="flex items-center gap-2 text-muted-foreground">
        <Icon className="size-4" />
        {label}
      </span>
      <span className="font-medium tabular-nums">{value.toLocaleString()}</span>
    </div>
  );
}

/**
 * Dependency-free trend sketch matching the prototype's engagement chart. The
 * real series comes from the analytics API; this keeps the page's shape while
 * the ingest is empty and is replaced by TrendChart when data lands.
 */
function TrendPlaceholder() {
  const series = [
    {
      pts: '0,150 40,140 80,120 120,125 160,95 200,100 240,70 280,80 320,55 360,60 400,40 440,50 480,35 520,45 560,30 600,38',
      color: 'hsl(var(--chart-1))',
      w: 2.5,
    },
    {
      pts: '0,175 40,170 80,160 120,165 160,150 200,155 240,140 280,145 320,135 360,140 400,125 440,130 480,118 520,122 560,110 600,115',
      color: 'hsl(var(--chart-6))',
      w: 2,
    },
    {
      pts: '0,190 40,185 80,188 120,180 160,182 200,175 240,178 280,170 320,172 360,165 400,168 440,160 480,162 520,155 560,158 600,150',
      color: 'hsl(var(--chart-3))',
      w: 2,
    },
  ];
  return (
    <svg viewBox="0 0 600 200" preserveAspectRatio="none" className="h-full w-full" aria-hidden>
      <g stroke="hsl(var(--border))" strokeWidth={1}>
        {[40, 80, 120, 160].map((y) => (
          <line key={y} x1={0} y1={y} x2={600} y2={y} />
        ))}
      </g>
      {series.map((s, i) => (
        <polyline
          key={i}
          fill="none"
          stroke={s.color}
          strokeWidth={s.w}
          points={s.pts}
          vectorEffect="non-scaling-stroke"
        />
      ))}
    </svg>
  );
}
