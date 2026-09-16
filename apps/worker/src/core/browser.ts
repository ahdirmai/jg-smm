/**
 * Browser lifecycle (P1-10). One headful Chromium per container, launched on
 * the Xvfb display (`:99`). Fingerprint (UA/locale/viewport) is fixed per
 * container so identity stays consistent between login and action
 * (DEVELOPMENT_RULE §7.3): the platform sees the same "device" every time.
 *
 * Playwright is imported only here and in auth.ts; the rest of the worker talks
 * to BrowserContext, never to the driver (DIP).
 */
import type { Browser, BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

import type { Geolocation } from './geolocation.js';

export interface BrowserHandle {
  browser: Browser;
  close(): Promise<void>;
}

export const FIXED_LOCALE = 'en-US';
export const FIXED_VIEWPORT = { width: 1280, height: 800 } as const;

export interface LaunchOptions {
  /** Xvfb display, e.g. `:99`. Defaults to the DISPLAY env var. */
  display?: string;
  /** Executable path override (tests inject a stub). */
  executablePath?: string;
  /** Skip the real driver entirely (unit tests). */
  launchImpl?: (opts: Record<string, unknown>) => Promise<Browser>;
}

/**
 * Launch the shared headful browser. The display must exist before this is
 * called — the container entrypoint starts Xvfb first.
 */
export async function launchBrowser(options: LaunchOptions = {}): Promise<BrowserHandle> {
  const display = options.display ?? process.env.DISPLAY ?? ':99';
  const launch = options.launchImpl ?? defaultLaunch;
  const browser = await launch({
    headless: false,
    executablePath: options.executablePath,
    env: { ...process.env, DISPLAY: display },
    args: [
      // noVNC sees a real desktop; the container's entrypoint starts Xvfb.
      '--start-maximized',
      '--disable-blink-features=AutomationControlled',
    ],
  });
  return {
    browser,
    close: async () => {
      await browser.close().catch(() => undefined);
    },
  };
}

/**
 * Create an isolated context for one account, hydrating a stored session. A
 * fresh context per account is the isolation boundary: cookies never cross
 * accounts inside one container.
 *
 * When geolocation is known, the context is pinned to the worker's frozen
 * coordinate (see geolocation.ts): Playwright then reports that position to
 * any page that asks, instead of the datacentre's real one. It is set on the
 * context, not per-page, so every navigation in the session stays put.
 */
export async function newAccountContext(
  browser: Browser,
  platform: Platform,
  storageState?: unknown,
  geo?: Geolocation | null,
): Promise<BrowserContext> {
  const ctx = await browser.newContext({
    locale: FIXED_LOCALE,
    viewport: FIXED_VIEWPORT,
    // Acceptable fingerprints per platform; pinned, not randomised.
    userAgent: agentFor(platform),
    ...(storageState ? { storageState: storageState as never } : {}),
  });
  if (geo) {
    await ctx.setGeolocation({ latitude: geo.latitude, longitude: geo.longitude });
    // Grant the permission so a page reading geolocation is not prompted.
    await ctx.grantPermissions(['geolocation']).catch(() => undefined);
  }
  return ctx;
}

function agentFor(platform: Platform): string {
  switch (platform) {
    case 'instagram':
    case 'threads':
      // Meta platforms expect a recent Chrome UA on a desktop profile.
      return 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36';
    default:
      return 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36';
  }
}

async function defaultLaunch(opts: Record<string, unknown>): Promise<Browser> {
  // Imported lazily so unit tests never pull in the driver or need a display.
  const { chromium } = await import('playwright');
  return chromium.launch(opts) as Promise<Browser>;
}
