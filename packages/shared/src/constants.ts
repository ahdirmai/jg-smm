/**
 * Shared constants (SSOT for FE). Keep in sync with apps/api/internal/domain/constants.go.
 * Values are non-secret configuration only — never put credentials here.
 */

/** Platforms supported by the platform. MVP active = instagram + threads. */
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

/** Platforms enabled in the MVP rollout. */
export const MVP_PLATFORMS: readonly Platform[] = ['instagram', 'threads'];

/** Redis channel/queue prefixes (must match BE transport). */
export const CONTROL_CHANNEL_PREFIX = 'control-';
export const ACTION_QUEUE_PREFIX = 'queue:action:';

export const controlChannel = (workerId: string): string =>
  `${CONTROL_CHANNEL_PREFIX}${workerId}`;

export const actionQueue = (workerId: string): string => `${ACTION_QUEUE_PREFIX}${workerId}`;
