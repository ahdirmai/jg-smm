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

export const FIXED_LOCALE = 'id-ID';
export const FIXED_VIEWPORT = { width: 1280, height: 800 } as const;

export interface LaunchOptions {
  /** Xvfb display, e.g. `:99`. Defaults to the DISPLAY env var. */
  display?: string;
  /** Executable path override (tests inject a stub). */
  executablePath?: string;
  /** Skip the real driver entirely (unit tests). */
  launchImpl?: (opts: Record<string, unknown>) => Promise<Browser>;
  /**
   * Egress proxy for the whole browser. Playwright binds the proxy at launch,
   * so this cannot vary per account — see the note at the launch call.
   */
  proxy?: { server: string; username?: string; password?: string } | null;
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
    ignoreDefaultArgs: ['--enable-automation'],
    // The proxy is a LAUNCH option, not a context one: Playwright fixes the
    // egress at browser start, so per-account routing would need one browser
    // per account. That is why the proxy comes from the container's own
    // `proxy_group` binding rather than from whichever account is logging in.
    ...(options.proxy
      ? {
          proxy: {
            server: options.proxy.server,
            ...(options.proxy.username ? { username: options.proxy.username } : {}),
            ...(options.proxy.password ? { password: options.proxy.password } : {}),
          },
        }
      : {}),
    args: [
      // noVNC sees a real desktop; the container's entrypoint starts Xvfb.
      '--incognito',
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
 * Pin a context to the worker's frozen coordinate (see geolocation.ts) so
 * Playwright reports that position to any page that asks, instead of the
 * datacentre's real one. Set on the context, not per-page, so every navigation
 * in the session stays put. A null/absent point is a no-op: the browser then
 * reports its true position (a worker with no assigned location).
 *
 * Exported because every context the worker opens must be pinned, not just the
 * action one — login and the manual live-view browser are read by the operator
 * to confirm the setup, and a login context left unpinned would report a
 * datacentre position that contradicts the action context.
 */
export async function pinGeolocation(
  ctx: BrowserContext,
  geo?: Geolocation | null,
): Promise<void> {
  if (!geo) return;
  await ctx.setGeolocation({ latitude: geo.latitude, longitude: geo.longitude });
  // Grant the permission so a page reading geolocation is not prompted.
  await ctx.grantPermissions(['geolocation']).catch(() => undefined);
}

/**
 * Everything that makes one account look like one device. Passed per context,
 * never cached on the worker: in the stateless pool the machine is shared and
 * the account identity travels with the request.
 */
export interface AccountContextInit {
  platform: Platform;
  storageState?: unknown;
  geolocation?: Geolocation | null;
  /** IANA zone matching the account's city. Defaults to WIB. */
  timezoneId?: string;
  proxy?: { server: string; username?: string; password?: string } | null;
  userAgent?: string;
}

/**
 * Create an isolated context for one account. A fresh context per job is the
 * isolation boundary: cookies never cross accounts on a shared worker.
 *
 * The timezone is the part that is easy to get wrong and expensive to miss: a
 * boxd VM runs in Frankfurt, so without `timezoneId` every page's clock and
 * Intl formatting report CET beside a GPS point in Jakarta — a contradiction
 * no real device produces.
 */
export async function newAccountContext(
  browser: Browser,
  init: AccountContextInit,
): Promise<BrowserContext> {
  // Keep the browser's own native User-Agent when the account row has no
  // explicit override: spoofing a hardcoded macOS Chrome 131 UA on Linux
  // Chromium 140 contradicts Sec-CH-UA / Sec-CH-UA-Platform / navigator.platform
  // and triggers anti-bot login rejections (e.g. Instagram e=1348020 / CAPTCHA
  // loops and Google's "browser not secure").
  const userAgent = init.platform === 'youtube' ? undefined : init.userAgent;
  const ctx = await browser.newContext({
    locale: FIXED_LOCALE,
    timezoneId: init.timezoneId || 'Asia/Jakarta',
    viewport: FIXED_VIEWPORT,
    // Acceptable fingerprints per platform; pinned, not randomised.
    ...(userAgent ? { userAgent } : {}),
    ...(init.storageState ? { storageState: init.storageState as never } : {}),
    ...(init.proxy
      ? {
          proxy: {
            server: init.proxy.server,
            ...(init.proxy.username ? { username: init.proxy.username } : {}),
            ...(init.proxy.password ? { password: init.proxy.password } : {}),
          },
        }
      : {}),
  });
  await pinGeolocation(ctx, init.geolocation);
  return ctx;
}

async function defaultLaunch(opts: Record<string, unknown>): Promise<Browser> {
  // Imported lazily so unit tests never pull in the driver or need a display.
  const { chromium } = await import('playwright');
  return chromium.launch(opts) as Promise<Browser>;
}
