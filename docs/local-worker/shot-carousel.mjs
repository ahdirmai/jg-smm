import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';

const env = Object.fromEntries(
  readFileSync('/Users/ahdirmai/MVP-1-SMM/.env', 'utf8')
    .split('\n').filter(l => l.includes('=') && !l.trim().startsWith('#'))
    .map(l => { const i = l.indexOf('='); return [l.slice(0, i).trim(), l.slice(i + 1).trim().replace(/^["']|["']$/g, '')]; }),
);

const b = await chromium.launch();
for (const theme of ['dark', 'light']) {
  const ctx = await b.newContext({ viewport: { width: 1440, height: 1200 } });
  const page = await ctx.newPage();
  await page.goto('http://localhost:24081/login', { waitUntil: 'domcontentloaded' });
  await page.getByLabel(/email/i).fill(env.SEED_ADMIN_EMAIL);
  await page.getByLabel(/password/i).fill(env.SEED_ADMIN_PASSWORD);
  await page.getByRole('button', { name: /sign in|log in|login/i }).click();
  await page.waitForURL(u => !u.pathname.includes('/login'), { timeout: 30000 });
  await page.emulateMedia({ colorScheme: theme });
  await page.goto('http://localhost:24081/actions', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);

  // Click the first scrape-history card to load the stored post into the form.
  const card = page.locator('button', { hasText: '@ahdirmai.id' }).first();
  if (await card.count()) {
    await card.click();
    await page.waitForTimeout(6000);
  }
  await page.screenshot({ path: `/tmp/actions-${theme}.png`, fullPage: false });
  console.log(`OK ${theme}`);
  await ctx.close();
}
await b.close();
