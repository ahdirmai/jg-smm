/**
 * Browser lifecycle. One headful Chromium per container, launched on the Xvfb
 * display (`:99`). Fingerprint (UA/locale/viewport) is fixed per container so
 * identity stays consistent between login and action (DEVELOPMENT_RULE §7.3).
 *
 * Skeleton only: `launch`/`newContext` throw until the Playwright wiring lands
 * in the P1 worker tickets. Types are intentionally narrow to keep Playwright
 * out of the rest of the codebase (DIP).
 */
import type { Browser, BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

export interface BrowserHandle {
  browser: Browser;
  close(): Promise<void>;
}

export const FIXED_LOCALE = 'en-US';
export const FIXED_VIEWPORT = { width: 1280, height: 800 } as const;

/** Launch the shared headful browser. Not implemented in the P0-09 skeleton. */
export async function launchBrowser(): Promise<BrowserHandle> {
  throw new Error('launchBrowser not implemented yet (P0-09 skeleton)');
}

/** Create an isolated context for one account, hydrating a stored session. */
export async function newAccountContext(
  browser: Browser,
  platform: Platform,
  storageState?: unknown,
): Promise<BrowserContext> {
  void browser;
  void platform;
  void storageState;
  throw new Error('newAccountContext not implemented yet (P0-09 skeleton)');
}
