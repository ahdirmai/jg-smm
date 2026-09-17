import type { Meta, StoryObj } from '@storybook/nextjs';

import { FreshnessBadge } from '../components/freshness-badge';
import { TrendChart } from '../components/trend-chart';
import { SessionContext } from '../lib/auth/session-context';
import type { SessionContextValue } from '../lib/auth/session-context';

const meta = {
  title: 'App/Components',
  parameters: { layout: 'padded' },
} satisfies Meta;

export default meta;

function synced(offsetMinutes: number) {
  return {
    stale: false,
    lastRunAt: new Date(Date.now() - offsetMinutes * 60_000).toISOString(),
    lastRunStatus: 'SUCCESS',
    thresholdSeconds: 3600,
  };
}

export const FreshnessInSync: StoryObj = {
  render: () => <FreshnessBadge freshness={synced(5)} />,
};

export const FreshnessStale: StoryObj = {
  render: () => <FreshnessBadge freshness={{ ...synced(95), stale: true }} />,
};

export const FreshnessNever: StoryObj = {
  render: () => <FreshnessBadge freshness={{ stale: true, thresholdSeconds: 3600 }} />,
};

const SERIES = [
  { bucket: '2026-09-01T00:00:00Z', value: 12 },
  { bucket: '2026-09-02T00:00:00Z', value: 19 },
  { bucket: '2026-09-03T00:00:00Z', value: 7 },
  { bucket: '2026-09-04T00:00:00Z', value: 24 },
  { bucket: '2026-09-05T00:00:00Z', value: 31 },
  { bucket: '2026-09-06T00:00:00Z', value: 28 },
];

export const TrendChartSeries: StoryObj = {
  render: () => <TrendChart points={SERIES} />,
};

export const TrendChartCompare: StoryObj = {
  render: () => (
    <TrendChart
      points={SERIES}
      compare={SERIES.map((p, i) => ({ bucket: p.bucket, value: 8 + i * 3 }))}
    />
  ),
};

export const TrendChartEmpty: StoryObj = {
  render: () => <TrendChart points={[]} />,
};

// A read-only role still renders; the write controls are the gated surface.
export const SessionReadOnly: StoryObj = {
  render: () => {
    const session: SessionContextValue = {
      status: 'authenticated',
      userId: 'u-1',
      role: 'STRATEGIST',
      refresh: async () => {},
    };
    return (
      <SessionContext.Provider value={session}>
        <div className="text-sm text-muted-foreground">Signed in as Strategist (read-only).</div>
      </SessionContext.Provider>
    );
  },
};

export const SessionLoading: StoryObj = {
  render: () => (
    <SessionContext.Provider value={{ status: 'loading', refresh: async () => {} }}>
      <div className="text-sm text-muted-foreground">Resolving session…</div>
    </SessionContext.Provider>
  ),
};
