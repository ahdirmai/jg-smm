import type { Platform } from '@smm/shared';

/**
 * Centrally-pinned DOM selectors, one object per platform. When a platform
 * changes its markup this is the single file to patch (DEVELOPMENT_RULE §7.3).
 * Empty in the skeleton — filled in by the per-platform action tickets.
 */
export interface SelectorSet {
  commentButton?: string;
  composerInput?: string;
  submitButton?: string;
  likeButton?: string;
  feedItem?: string;
}

const SELECTORS: Partial<Record<Platform, SelectorSet>> = {
  instagram: {},
  threads: {},
};

export function selectorsFor(platform: Platform): SelectorSet {
  return SELECTORS[platform] ?? {};
}
