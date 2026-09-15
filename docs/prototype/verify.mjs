/**
 * Prototype verification: renders every page in a real browser and asserts
 * that the Tailwind Play CDN + theme tokens actually resolve (CSS works),
 * the sidebar nav renders, and no console errors occur.
 *
 * Usage:
 *   node docs/prototype/verify.mjs
 * Requires a static server on http://127.0.0.1:24085 serving this directory.
 */
import { chromium } from 'playwright';
import { readdirSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const BASE = process.env.PROTO_BASE || 'http://127.0.0.1:24085';
const HERE = dirname(fileURLToPath(import.meta.url));

const STANDALONE = new Set(['login.html', 'index.html']);
const pages = readdirSync(HERE)
  .filter((f) => f.endsWith('.html'))
  .sort();

const results = [];
let failed = 0;

/**
 * Resolve a usable Chromium binary. The static files under test only need a
 * modern Chromium; we do not care which Playwright revision is installed, so
 * we discover whatever exists in the shared browser cache. Set CHROME_PATH to
 * override (required for CI where nothing is cached).
 */
function resolveExecutable() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const cache = join(process.env.HOME || '', 'Library/Caches/ms-playwright');
  if (!existsSync(cache)) return undefined;
  const dirs = readdirSync(cache)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort((a, b) => Number(b.split('-')[1]) - Number(a.split('-')[1]));
  const candidates = [
    (d) =>
      join(
        cache,
        d,
        'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
      ),
    (d) => join(cache, d, 'chrome-mac/Chromium.app/Contents/MacOS/Chromium'),
    (d) => join(cache, d, 'chrome-linux/chrome'),
  ];
  for (const d of dirs) {
    for (const c of candidates) {
      const p = c(d);
      if (existsSync(p)) return p;
    }
  }
  return undefined;
}

const EXECUTABLE = resolveExecutable();

const browser = await chromium.launch({
  executablePath: EXECUTABLE,
  headless: true,
});
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();

async function probe(htmlFile) {
  const errors = [];
  const badResponses = [];
  page.removeAllListeners('console');
  page.removeAllListeners('pageerror');
  page.removeAllListeners('response');
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text());
  });
  page.on('pageerror', (e) => errors.push(String(e)));
  page.on('response', (r) => {
    if (r.status() >= 400 && !/favicon\.ico/i.test(r.url()))
      badResponses.push(`${r.status()} ${r.url()}`);
  });

  await page.goto(`${BASE}/${htmlFile}`, { waitUntil: 'networkidle', timeout: 30000 });
  // let theme.js + shell.js settle
  await page.waitForTimeout(400);

  const isStandalone = STANDALONE.has(htmlFile);

  const metrics = await page.evaluate(() => {
    const cs = (el) => (el ? getComputedStyle(el) : null);
    const shell = document.querySelector('[data-shell], aside, nav');
    const main = document.querySelector('main, #app, .theme-float');
    const bodyCs = getComputedStyle(document.body);
    return {
      hasShell: !!shell,
      bodyBg: bodyCs.backgroundColor,
      bodyColor: bodyCs.color,
      htmlClass: document.documentElement.className,
      mainWidth: main ? main.getBoundingClientRect().width : 0,
    };
  });

  // Toggle theme and confirm body background actually CHANGES (proves tokens work)
  await page.evaluate(() => window.smmTheme && window.smmTheme.set('light'));
  await page.waitForTimeout(250);
  const lightBg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  await page.evaluate(() => window.smmTheme && window.smmTheme.set('dark'));
  await page.waitForTimeout(250);
  const darkBg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);

  const themeWorks = lightBg !== darkBg && lightBg.startsWith('rgb') && darkBg.startsWith('rgb');
  const shellOk = isStandalone ? true : metrics.hasShell;
  const cssOk = themeWorks && metrics.bodyBg.startsWith('rgb');
  // A 404 on a non-favicon resource is a real failure. A generic "Failed to load
  // resource" console message is only noise when no real response failed.
  const hardErrors = badResponses;
  const softErrors = errors.filter((e) => !/Failed to load resource/i.test(e));
  const realErrors = [...hardErrors, ...softErrors];
  const clean = realErrors.length === 0;

  const ok = shellOk && cssOk && clean;
  if (!ok) failed++;

  results.push({
    page: htmlFile,
    ok,
    shellOk,
    cssOk,
    clean,
    lightBg,
    darkBg,
    errors: realErrors,
  });

  console.log(
    `${ok ? 'PASS' : 'FAIL'}  ${htmlFile.padEnd(30)} ` +
      `shell=${shellOk ? 'y' : 'n'} css=${cssOk ? 'y' : 'n'} console=${clean ? 'clean' : realErrors.length + ' err'} ` +
      `light=${lightBg} dark=${darkBg}`,
  );
  if (!clean) realErrors.slice(0, 3).forEach((e) => console.log(`        ! ${e.slice(0, 160)}`));
}

for (const p of pages) await probe(p);

await browser.close();

console.log(`\n${pages.length - failed}/${pages.length} pages OK`);
process.exit(failed === 0 ? 0 : 1);
