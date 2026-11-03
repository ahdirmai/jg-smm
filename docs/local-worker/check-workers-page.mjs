import { chromium } from 'playwright';

const base = 'http://localhost:24081';
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 2000 } });

// login via API cookie jar then inject
const login = await page.request.post(base + '/api/auth/login', {
  data: { email: 'owner@smm.local', password: 'changeme-changeme' },
});
console.log('login', login.status());

await page.goto(base + '/workers', { waitUntil: 'networkidle' });
await page.waitForTimeout(3000);

const badges = await page.evaluate(() => {
  const badges = [...document.querySelectorAll('.animate-pulse, [class*="badge"], span')];
  const text = document.body.innerText;
  const active = (text.match(/ACTIVE/g) || []).length;
  const paused = (text.match(/PAUSED/g) || []).length;
  const ready = (text.match(/READY/g) || []).length;
  return { active, paused, ready };
});
console.log('badge counts:', badges);
await page.screenshot({ path: '/tmp/workers-page.png', fullPage: false });
await browser.close();
