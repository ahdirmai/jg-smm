/**
 * Screenshot capture (P3-14). Uses the CDP `Page.captureScreenshot` command
 * rather than `page.screenshot()`: Playwright's helper waits for fonts to
 * settle, and a Meta feed never settles (continuous re-layout), so it times
 * out. CDP captures the current frame immediately (DEVELOPMENT_RULE §7.3).
 *
 * Only the basename is ever returned; the caller must not learn the on-disk
 * path (the BE rejects paths on callback, §7.4).
 */
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

import type { Page } from 'playwright';

export interface ShotDeps {
  /** Directory screenshots land in. When absent nothing is written. */
  dir?: string;
}

/**
 * Capture `page` to `<dir>/task-<id>-<worker>.jpg`. Never throws: a screenshot
 * is an operator aid, not a correctness signal, so a failure degrades to "no
 * screenshot" rather than failing the action.
 */
export async function captureScreenshot(
  page: Page,
  id: string,
  workerId: string,
  deps: ShotDeps = {},
): Promise<string | undefined> {
  const basename = `task-${id}-${workerId}.jpg`;
  if (!deps.dir) return basename; // no disk configured: shape only

  try {
    const session = await page.context().newCDPSession(page);
    const { data } = await session.send('Page.captureScreenshot', {
      format: 'jpeg',
      quality: 80,
    });
    await session.detach().catch(() => undefined);

    await mkdir(deps.dir, { recursive: true });
    await writeFile(join(deps.dir, basename), Buffer.from(data, 'base64'));
    return basename;
  } catch {
    return undefined; // best effort
  }
}
