/**
 * Platform registry (Open/Closed). Maps `Platform → Adapter`. New platforms are
 * registered here without touching `controller.ts`.
 *
 * The MVP ships Instagram + Threads adapters as not-yet-implemented stubs; the
 * registry is still the single lookup the controller uses so wiring is proven.
 */
import type { Platform } from '@smm/shared';

import type { PlatformAdapter } from './adapter.js';
import { instagramAdapter } from './instagram.js';
import { threadsAdapter } from './threads.js';

const ADAPTERS: Partial<Record<Platform, PlatformAdapter>> = {
  instagram: instagramAdapter,
  threads: threadsAdapter,
};

/** Resolve an adapter for a platform, or `undefined` when not yet supported. */
export function getAdapter(platform: Platform): PlatformAdapter | undefined {
  return ADAPTERS[platform];
}

/** Platforms that currently have a live adapter in this build. */
export function supportedPlatforms(): Platform[] {
  return Object.keys(ADAPTERS) as Platform[];
}
