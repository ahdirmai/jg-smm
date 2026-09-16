/**
 * Per-platform analytics configuration (P6-06).
 *
 * Each platform has its own metric vocabulary (IG has saves and reels views,
 * YouTube has watch time and CTR, TikTok has watch time, …) and its own trend
 * pairing. These mirror the `window.ANALYTICS` blocks in the approved
 * prototype (docs/prototype/analytics-<platform>.html) so a page renders the
 * same KPI set the prototype shows.
 *
 * Data still comes from the API (`usePlatformAnalytics`); these are labels and
 * defaults only — the 3rd-party ingestion is out of P6 scope.
 */

import {
  Activity,
  Bookmark,
  Clock,
  Eye,
  Heart,
  MessageCircle,
  MousePointerClick,
  PlayCircle,
  Quote,
  Repeat,
  Reply,
  Share2,
  ThumbsUp,
  Timer,
  User,
  UserPlus,
  Users,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

export type KpiDef = { label: string; icon: LucideIcon; metric: string };
export type PlatformAnalyticsConfig = {
  label: string;
  kpis: KpiDef[];
  trendTitle: string;
  /** Two series for the trend chart, by metric key. */
  seriesA: string;
  seriesB: string;
  postKindLabel: string;
};

const FOLLOWERS: KpiDef = { label: 'Follower growth', icon: UserPlus, metric: 'followers' };

export const ANALYTICS_CONFIG: Record<string, PlatformAnalyticsConfig> = {
  instagram: {
    label: 'Instagram',
    kpis: [
      { label: 'Reach', icon: Eye, metric: 'reach' },
      { label: 'Reels views', icon: PlayCircle, metric: 'reels_views' },
      { label: 'Likes', icon: Heart, metric: 'likes' },
      { label: 'Saves', icon: Bookmark, metric: 'saves' },
      { label: 'Comments', icon: MessageCircle, metric: 'comments' },
      { label: 'Profile visits', icon: User, metric: 'profile_visits' },
    ],
    trendTitle: 'Reach vs Reels views',
    seriesA: 'reach',
    seriesB: 'reels_views',
    postKindLabel: 'Post',
  },
  threads: {
    label: 'Threads',
    kpis: [
      { label: 'Views', icon: Eye, metric: 'views' },
      { label: 'Likes', icon: Heart, metric: 'likes' },
      { label: 'Replies', icon: Reply, metric: 'replies' },
      { label: 'Reposts', icon: Repeat, metric: 'reposts' },
      { label: 'Quotes', icon: Quote, metric: 'quotes' },
      { label: 'Follower growth', icon: UserPlus, metric: 'followers' },
    ],
    trendTitle: 'Views vs Likes',
    seriesA: 'views',
    seriesB: 'likes',
    postKindLabel: 'Thread',
  },
  facebook: {
    label: 'Facebook',
    kpis: [
      { label: 'Reach', icon: Eye, metric: 'reach' },
      { label: 'Page views', icon: MousePointerClick, metric: 'page_views' },
      { label: 'Reactions', icon: ThumbsUp, metric: 'reactions' },
      { label: 'Comments', icon: MessageCircle, metric: 'comments' },
      { label: 'Shares', icon: Share2, metric: 'shares' },
      { label: 'Follower growth', icon: UserPlus, metric: 'followers' },
    ],
    trendTitle: 'Reach vs Page views',
    seriesA: 'reach',
    seriesB: 'page_views',
    postKindLabel: 'Post',
  },
  linkedin: {
    label: 'LinkedIn',
    kpis: [
      { label: 'Impressions', icon: Eye, metric: 'impressions' },
      { label: 'Unique visitors', icon: Users, metric: 'unique_visitors' },
      { label: 'Reactions', icon: ThumbsUp, metric: 'reactions' },
      { label: 'Comments', icon: MessageCircle, metric: 'comments' },
      { label: 'Reshares', icon: Repeat, metric: 'reshares' },
      { label: 'Follower growth', icon: UserPlus, metric: 'followers' },
    ],
    trendTitle: 'Impressions vs Unique visitors',
    seriesA: 'impressions',
    seriesB: 'unique_visitors',
    postKindLabel: 'Post',
  },
  x: {
    label: 'X',
    kpis: [
      { label: 'Impressions', icon: Eye, metric: 'impressions' },
      { label: 'Engagements', icon: Activity, metric: 'engagements' },
      { label: 'Likes', icon: Heart, metric: 'likes' },
      { label: 'Reposts', icon: Repeat, metric: 'reposts' },
      { label: 'Replies', icon: Reply, metric: 'replies' },
      { label: 'Bookmarks', icon: Bookmark, metric: 'bookmarks' },
    ],
    trendTitle: 'Impressions vs Engagements',
    seriesA: 'impressions',
    seriesB: 'engagements',
    postKindLabel: 'Post',
  },
  youtube: {
    label: 'YouTube',
    kpis: [
      { label: 'Views', icon: PlayCircle, metric: 'views' },
      { label: 'Watch time (h)', icon: Clock, metric: 'watch_hours' },
      { label: 'Subscribers', icon: UserPlus, metric: 'subscribers' },
      { label: 'CTR (%)', icon: MousePointerClick, metric: 'ctr' },
      { label: 'Impressions', icon: Eye, metric: 'impressions' },
      { label: 'Avg view (min)', icon: Timer, metric: 'avg_view_min' },
    ],
    trendTitle: 'Views vs Watch time',
    seriesA: 'views',
    seriesB: 'watch_hours',
    postKindLabel: 'Video',
  },
  tiktok: {
    label: 'TikTok',
    kpis: [
      { label: 'Views', icon: PlayCircle, metric: 'views' },
      { label: 'Likes', icon: Heart, metric: 'likes' },
      { label: 'Comments', icon: MessageCircle, metric: 'comments' },
      { label: 'Shares', icon: Share2, metric: 'shares' },
      { label: 'Followers', icon: UserPlus, metric: 'followers' },
      { label: 'Watch time (h)', icon: Clock, metric: 'watch_hours' },
    ],
    trendTitle: 'Views vs Likes',
    seriesA: 'views',
    seriesB: 'likes',
    postKindLabel: 'Video',
  },
};

export function analyticsConfig(platform: string): PlatformAnalyticsConfig | undefined {
  return ANALYTICS_CONFIG[platform.toLowerCase()];
}

// Kept for the audience-mix panel: every platform's prototype shows a follower
// geo split. Read-only, sourced from the ingestion layer.
export { FOLLOWERS };
