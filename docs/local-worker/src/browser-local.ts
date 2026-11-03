/**
 * Local-only browser launcher for the harness.
 *
 * Why this exists: the harness pins `playwright@1.48.2`, which resolves
 * chromium-1140 — and that build's mac bundle fails to spawn on this machine
 * (`browserType.launch: spawn Unknown system error -88`, arm64, unsigned).
 * chromium-1228 (already in the cache) launches fine.
 *
 * This does NOT change what runs: it passes an `executablePath` into the
 * worker's own `launchBrowser`, so the worker's real options (headful, the
 * container's automation-hiding args) still apply. Only the binary differs.
 *
 * Override with DISPLAY_BROWSER=... to point at another build.
 */
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

import { launchBrowser as workerLaunch } from '../../../apps/worker/src/core/browser.js';
import type { BrowserHandle, LaunchOptions } from '../../../apps/worker/src/core/browser.js';

/** Candidate browser binaries, in preference order. */
const CANDIDATES = [
  process.env.DISPLAY_BROWSER,
  join(
    homedir(),
    'Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
  ),
].filter((p): p is string => Boolean(p));

function resolveBinary(): string | undefined {
  for (const p of CANDIDATES) {
    if (existsSync(p)) return p;
  }
  return undefined;
}

/**
 * Launch the worker's browser with a local executablePath. Falls back to the
 * worker's default resolution when no candidate exists (so a machine whose
 * pinned chromium works is unaffected).
 */
export function launchBrowser(options: LaunchOptions = {}): Promise<BrowserHandle> {
  const exe = options.executablePath ?? resolveBinary();
  // DISPLAY is a Linux/Xvfb concern; force it off on darwin so the worker's
  // `env: {DISPLAY: ':99'}` default cannot leak into a mac spawn.
  if (process.platform === 'darwin') {
    process.env.DISPLAY = '';
  }
  return workerLaunch(exe ? { ...options, executablePath: exe } : options);
}

export { newAccountContext } from '../../../apps/worker/src/core/browser.js';
