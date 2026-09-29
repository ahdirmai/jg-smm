import { chromium } from 'playwright';
import { readFileSync } from 'node:fs';

const env = Object.fromEntries(
  readFileSync('/Users/ahdirmai/MVP-1-SMM/.env', 'utf8')
    .split('\n').filter(l => l.includes('=') && !l.trim().startsWith('#'))
    .map(l => { const i = l.indexOf('='); return [l.slice(0, i).trim(), l.slice(i + 1).trim().replace(/^["']|["']$/g, '')]; }),
);

const b = await chromium.launch();
const ctx = await b.newContext({ viewport: { width: 1440, height: 1100 } });
const page = await ctx.newPage();

await page.goto('http://localhost:24081/login', { waitUntil: 'domcontentloaded' });
await page.getByLabel(/email/i).fill(env.SEED_ADMIN_EMAIL);
await page.getByLabel(/password/i).fill(env.SEED_ADMIN_PASSWORD);
await page.getByRole('button', { name: /sign in|log in|login/i }).click();
await page.waitForURL(u => !u.pathname.includes('/login'), { timeout: 30000 });

await page.goto('http://localhost:24081/actions', { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(4000);
await page.screenshot({ path: '/tmp/actions-full.png', fullPage: true });
console.log('OK /actions');
await b.close();
