/**
 * Session persistence. One `storageState()` JSON per platform lives on the
 * container's PVC at `/data/sessions/session-<platform>.json`, so a restart
 * re-hydrates the context without re-login (DEVELOPMENT_RULE §7.1).
 *
 * This is the skeleton: the real read/write lands with the login flow.
 */
import { readFile, writeFile, mkdir, rm } from 'node:fs/promises';
import { dirname, join } from 'node:path';

import type { Platform } from '@smm/shared';

const DEFAULT_SESSION_DIR = process.env.SESSION_DIR ?? '/data/sessions';

export function sessionPath(platform: Platform, dir: string = DEFAULT_SESSION_DIR): string {
  return join(dir, `session-${platform}.json`);
}

/** Read a persisted storage state, or `undefined` when absent/unreadable. */
export async function readSession(
  platform: Platform,
  dir: string = DEFAULT_SESSION_DIR,
): Promise<unknown | undefined> {
  try {
    return JSON.parse(await readFile(sessionPath(platform, dir), 'utf8'));
  } catch {
    return undefined;
  }
}

/** Persist a storage state atomically-ish (write then leave to fsync by caller). */
export async function writeSession(
  platform: Platform,
  state: unknown,
  dir: string = DEFAULT_SESSION_DIR,
): Promise<void> {
  const path = sessionPath(platform, dir);
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, JSON.stringify(state), 'utf8');
}

/** Drop a persisted session (used by `auth-clear`). */
export async function clearSession(
  platform: Platform,
  dir: string = DEFAULT_SESSION_DIR,
): Promise<void> {
  await rm(sessionPath(platform, dir), { force: true });
}
