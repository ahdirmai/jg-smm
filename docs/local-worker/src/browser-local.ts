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
import { existsSync, readdirSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

import { launchBrowser as workerLaunch } from '../../../apps/worker/src/core/browser.js';
import type { BrowserHandle, LaunchOptions } from '../../../apps/worker/src/core/browser.js';

const CACHE = join(homedir(), 'Library/Caches/ms-playwright');
const BIN = 'Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';

/**
 * Playwright builds present in the cache, newest first. The pinned driver is
 * older than the cache (its own chromium-1140 does not spawn here), so any
 * newer build is a better bet than none.
 */
function cachedBinaries(): string[] {
  try {
    return readdirSync(CACHE)
      .filter((d) => d.startsWith('chromium-'))
      .sort((a, b) => Number(b.split('-')[1]) - Number(a.split('-')[1]))
      .map((d) => join(CACHE, d, 'chrome-mac-arm64', BIN))
      .filter((p) => existsSync(p));
  } catch {
    return [];
  }
}

/** Candidate browser binaries, in preference order. */
const CANDIDATES = [
  process.env.DISPLAY_BROWSER,
  ...cachedBinaries(),
  join(CACHE, 'chromium-1228/chrome-mac-arm64', BIN),
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
