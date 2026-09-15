/**
 * Prototype screenshot capture. Renders pages from the running static server
 * and writes downscaled JPEGs into ./_shots for visual review.
 *
 * Usage:
 *   node docs/prototype/shots.mjs dashboard.html:dark actions.html:light
 *   node docs/prototype/shots.mjs            # captures a sensible default set
 *
 * Requires a static server on http://127.0.0.1:24085 serving this directory.
 */
import { chromium } from 'playwright';
import { readdirSync, existsSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const BASE = process.env.PROTO_BASE || 'http://127.0.0.1:24085';
const HERE = dirname(fileURLToPath(import.meta.url));
const OUT = join(HERE, '_shots');

const DEFAULT_TARGETS = [
  'dashboard.html:dark',
  'dashboard.html:light',
  'actions.html:dark',
  'analytics-instagram.html:dark',
  'analytics-threads.html:light',
  'monitoring.html:dark',
  'workers.html:light',
];

function resolveExecutable() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const cache = join(process.env.HOME || '', 'Library/Caches/ms-playwright');
  if (!existsSync(cache)) return undefined;
  const dirs = readdirSync(cache)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort((a, b) => Number(b.split('-')[1]) - Number(a.split('-')[1]));
  const candidates = [
    (d) =>
      join(cache, d, 'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing'),
    (d) => join(cache, d, 'chrome-mac/Chromium.app/Contents/MacOS/Chromium'),
    (d) => join(cache, d, 'chrome-linux/chrome'),
  ];
  for (const d of dirs)
    for (const c of candidates) {
      const p = c(d);
      if (existsSync(p)) return p;
    }
  return undefined;
}

mkdirSync(OUT, { recursive: true });
const targets = process.argv.slice(2).length ? process.argv.slice(2) : DEFAULT_TARGETS;

const browser = await chromium.launch({ executablePath: resolveExecutable(), headless: true });
const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 1 });
const page = await ctx.newPage();

for (const t of targets) {
  const [file, theme = 'dark'] = t.split(':');
  await page.goto(`${BASE}/${file}`, { waitUntil: 'networkidle' });
  await page.evaluate((th) => window.smmTheme && window.smmTheme.set(th), theme);
  await page.waitForTimeout(500);
  const name = `${file.replace('.html', '')}-${theme}.jpg`;
  await page.screenshot({ path: join(OUT, name), fullPage: true, type: 'jpeg', quality: 70 });
  console.log('shot', name);
}

await browser.close();
