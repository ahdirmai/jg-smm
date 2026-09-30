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

// Keyed by Platform, not string: adding a platform to PLATFORMS without a label
// is a compile error rather than an "undefined" rendered somewhere at runtime.
export const PLATFORM_LABEL: Record<Platform, string> = {
  instagram: 'Instagram',
  threads: 'Threads',
  facebook: 'Facebook',
  linkedin: 'LinkedIn',
  x: 'X',
  youtube: 'YouTube',
  tiktok: 'TikTok',
};

/**
 * Display name for an untrusted platform id — a route segment, an API value, a
 * stored row. Returns undefined for anything this codebase does not know, which
 * is the signal a caller needs to fall back. Going through PLATFORMS rather
 * * than indexing the record directly keeps a bare `string` from silently
 * producing `undefined` under a type that claims it cannot happen.
 */
export function platformLabel(id: string): string | undefined {
  const key = id.trim().toLowerCase();
  return PLATFORMS.find((p) => p === key)
    ? PLATFORM_LABEL[key as Platform]
    : undefined;
}

/** KPI metrics available per platform (PLATFORM_MATRIX §2.3). */
export const ANALYTICS_METRICS = [
  { key: 'followers', label: 'Followers' },
  { key: 'reach', label: 'Reach' },
  { key: 'views', label: 'Views' },
  { key: 'engagements', label: 'Engagements' },
  { key: 'mentions', label: 'Mentions' },
  { key: 'profile_views', label: 'Profile views' },
] as const;

/**
 * How each platform names its engagement metrics, in the order they should be
 * read. The stored `post.metrics` keys are normalised by the API to be
 * platform-neutral (`comments` holds Threads' reply count, `shares` its repost
 * count), so the same row renders natively per platform without the API
 * inventing platform-specific keys.
 *
 * Order is what a reader expects to see first — the metric the platform itself
 * surfaces first — and anything the actor did not report is skipped, so a
 * post with no view count does not render an empty "Views: —".
 */
export const PLATFORM_METRICS: Record<string, { key: string; label: string }[]> = {
  instagram: [
    { key: 'likes', label: 'Likes' },
    { key: 'comments', label: 'Comments' },
    { key: 'views', label: 'Views' },
  ],
  threads: [
    { key: 'likes', label: 'Likes' },
    // Threads calls a comment a reply, and a repost a repost — showing
    // "Comments" on a Threads row mislabels the platform's own vocabulary.
    { key: 'comments', label: 'Replies' },
    { key: 'shares', label: 'Reposts' },
    { key: 'views', label: 'Views' },
  ],
  facebook: [
    { key: 'likes', label: 'Reactions' },
    { key: 'comments', label: 'Comments' },
    { key: 'shares', label: 'Shares' },
  ],
  tiktok: [
    { key: 'views', label: 'Views' },
    { key: 'likes', label: 'Likes' },
    { key: 'comments', label: 'Comments' },
    { key: 'shares', label: 'Shares' },
  ],
  x: [
    { key: 'likes', label: 'Likes' },
    { key: 'comments', label: 'Replies' },
    { key: 'shares', label: 'Reposts' },
    { key: 'views', label: 'Views' },
  ],
  youtube: [
    { key: 'views', label: 'Views' },
    { key: 'likes', label: 'Likes' },
    { key: 'comments', label: 'Comments' },
  ],
  linkedin: [
    { key: 'likes', label: 'Reactions' },
    { key: 'comments', label: 'Comments' },
  ],
};

/**
 * Render one post's metrics in its own platform's vocabulary, e.g.
 * "Likes 73 · Comments 13" on Instagram and "Likes 73 · Replies 13" on Threads.
 * Unknown platforms fall back to a generic pass so a new actor's keys still show
 * up rather than rendering as nothing.
 */
export function metricsLine(
  platform: string,
  metrics: Record<string, number> | null,
): string {
  if (!metrics) return '';
  // The API serialises platform as its domain constant (INSTAGRAM), while this
  // table is keyed by the lowercase route/platform id. Normalise once here so a
  // missed lookup cannot silently degrade every row to the generic fallback.
  const spec = PLATFORM_METRICS[platform.toLowerCase()];
  if (spec) {
    return spec
      .filter((m) => typeof metrics[m.key] === 'number')
      .map((m) => `${m.label} ${metrics[m.key]}`)
      .join(' · ');
  }
  return Object.entries(metrics)
    .filter(([, v]) => typeof v === 'number')
    .map(([k, v]) => `${k} ${v}`)
    .join(' · ');
}
