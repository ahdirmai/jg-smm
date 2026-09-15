/**
 * Platform display metadata shared by every monitoring page. One source so a
 * new platform lands in the sidebar and the table at the same time.
 */

export const PLATFORMS = [
  'instagram',
  'threads',
  'facebook',
  'linkedin',
  'x',
  'youtube',
  'tiktok',
] as const;

export type Platform = (typeof PLATFORMS)[number];

export const PLATFORM_LABEL: Record<string, string> = {
  instagram: 'Instagram',
  threads: 'Threads',
  facebook: 'Facebook',
  linkedin: 'LinkedIn',
  x: 'X',
  youtube: 'YouTube',
  tiktok: 'TikTok',
};

/** KPI metrics available per platform (PLATFORM_MATRIX §2.3). */
export const ANALYTICS_METRICS = [
  { key: 'followers', label: 'Followers' },
  { key: 'reach', label: 'Reach' },
  { key: 'views', label: 'Views' },
  { key: 'engagements', label: 'Engagements' },
  { key: 'mentions', label: 'Mentions' },
  { key: 'profile_views', label: 'Profile views' },
] as const;
