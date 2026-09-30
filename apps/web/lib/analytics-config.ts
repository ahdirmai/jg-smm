/**
 * Per-platform analytics vocabulary (P6-06).
 *
 * The metric KEYS are not free-form: `analytics_snapshot` stores six
 * cross-platform scalars and the API emits a KPI only for the ones a provider
 * actually reported. An earlier version of this file invented a key per
 * platform (reels views, saves, watch hours, CTR), which no provider populates
 * and the trend query cannot even select — every such card rendered "—" forever.
 * So the keys are fixed here, and only the LABEL and the ORDER vary per
 * platform, which is the part that genuinely differs.
 */

import { Eye, Heart, PlayCircle, User, UserPlus, type LucideIcon } from 'lucide-react';

import type { Platform } from '@/lib/platforms';

/** The six scalars the snapshot table stores, with a neutral label. */
const SCALARS: Record<string, { icon: LucideIcon; label: string }> = {
  reach: { icon: Eye, label: 'Reach' },
  views: { icon: PlayCircle, label: 'Views' },
  engagements: { icon: Heart, label: 'Engagements' },
  profile_views: { icon: User, label: 'Profile views' },
  followers: { icon: UserPlus, label: 'Followers' },
  // mentions is a real column but no provider adapter fills it yet (Instagram
  // publishes no mentions tool). Add it to a platform's `order` the day one
  // does — a card that can only ever render "—" is dead UI.
};

type PlatformSpec = {
  /** KPI order, most important first. Every key must exist in SCALARS. */
  order: string[];
  /** The platform's own name for a scalar, where it differs from the neutral one. */
  rename?: Record<string, string>;
  /** The metric the trend chart shows until the reader picks another. */
  defaultMetric: string;
};

const SPECS: Record<Platform, PlatformSpec> = {
  instagram: {
    order: ['reach', 'views', 'engagements', 'profile_views', 'followers'],
    rename: {
      engagements: 'Interactions',
      profile_views: 'Profile visits',
    },
    defaultMetric: 'reach',
  },
  threads: {
    order: ['views', 'engagements', 'profile_views', 'followers'],
    rename: { engagements: 'Interactions' },
    defaultMetric: 'views',
  },
  facebook: {
    order: ['reach', 'views', 'engagements', 'followers'],
    rename: { views: 'Page views', engagements: 'Post engagements', followers: 'Page followers' },
    defaultMetric: 'reach',
  },
  linkedin: {
    order: ['views', 'engagements', 'followers'],
    rename: { views: 'Unique visitors' },
    defaultMetric: 'views',
  },
  x: {
    order: ['reach', 'engagements', 'followers'],
    rename: { reach: 'Impressions' },
    defaultMetric: 'reach',
  },
  youtube: {
    order: ['views', 'engagements', 'followers'],
    rename: { engagements: 'Interactions', followers: 'Subscribers' },
    defaultMetric: 'views',
  },
  tiktok: {
    order: ['views', 'engagements', 'followers'],
    rename: { views: 'Video views', engagements: 'Interactions' },
    defaultMetric: 'views',
  },
};

export type KpiDef = { label: string; icon: LucideIcon; metric: string };
export type PlatformAnalyticsConfig = { kpis: KpiDef[]; defaultMetric: string };

export const ANALYTICS_CONFIG: Record<string, PlatformAnalyticsConfig> = Object.fromEntries(
  Object.entries(SPECS).map(([key, spec]) => [
    key,
    {
      defaultMetric: spec.defaultMetric,
      kpis: spec.order.map((metric) => {
        const scalar = SCALARS[metric];
        if (!scalar) throw new Error(`analytics-config: unknown metric "${metric}" for ${key}`);
        return { metric, label: spec.rename?.[metric] ?? scalar.label, icon: scalar.icon };
      }),
    },
  ]),
);

export function analyticsConfig(platform: string): PlatformAnalyticsConfig | undefined {
  return ANALYTICS_CONFIG[platform.toLowerCase()];
}
