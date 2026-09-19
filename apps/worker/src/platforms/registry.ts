/**
 * Platform registry (Open/Closed). Maps `Platform → Adapter`. New platforms are
 * registered here without touching `controller.ts`.
 *
 * All seven platforms have an adapter so the OTP-over-web surface (login URL,
 * proof cookies, 2FA detect/submit) is uniform across the fleet. Only Instagram
 * and Threads carry *real* like/comment/verify automation, though — the other
 * five are honest stubs (see `stub.ts`). `automatedPlatforms()` is the single
 * source of that distinction, so callers never imply the stubs work.
 */
import { MVP_PLATFORMS, type Platform } from '@smm/shared';

import type { PlatformAdapter } from './adapter.js';
import { facebookAdapter } from './facebook.js';
import { instagramAdapter } from './instagram.js';
import { linkedinAdapter } from './linkedin.js';
import { threadsAdapter } from './threads.js';
import { tiktokAdapter } from './tiktok.js';
import { xAdapter } from './x.js';
import { youtubeAdapter } from './youtube.js';

const ADAPTERS: Partial<Record<Platform, PlatformAdapter>> = {
  instagram: instagramAdapter,
  threads: threadsAdapter,
  facebook: facebookAdapter,
  linkedin: linkedinAdapter,
  x: xAdapter,
  youtube: youtubeAdapter,
  tiktok: tiktokAdapter,
};

/**
 * Platforms whose like/comment/verify automation is real (the MVP rollout).
 * The others are registered and OTP-capable but their actions are stubs, so a
 * caller wanting to promise "this will execute" must gate on this set.
 */
const AUTOMATED = new Set<Platform>(MVP_PLATFORMS);

/** Resolve an adapter for a platform, or `undefined` when not registered. */
export function getAdapter(platform: Platform): PlatformAdapter | undefined {
  return ADAPTERS[platform];
}

/** Platforms that have a registered adapter (OTP-capable) in this build. */
export function supportedPlatforms(): Platform[] {
  return Object.keys(ADAPTERS) as Platform[];
}

/** True when the platform's like/comment/verify automation is real, not a stub. */
export function isFullyAutomated(platform: Platform): boolean {
  return AUTOMATED.has(platform);
}

/** Platforms whose actions actually execute (real automation, not stubs). */
export function automatedPlatforms(): Platform[] {
  return supportedPlatforms().filter(isFullyAutomated);
}
