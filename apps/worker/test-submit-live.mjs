/**
 * Live check: the credential form must actually SUBMIT.
 *
 * Unit fakes cannot prove this — the control Instagram serves is a
 * <div role="button">Login</div> whose <input type="submit"> is 0x0, and a
 * hand-built fake models what its author expected rather than what the page
 * renders. Run against the real site:
 *
 *   node test-submit-live.mjs
 */
import { chromium } from 'playwright';
import { fillMetaCredentials } from './dist/platforms/dom.js';

const b = await chromium.launch({ headless: false, args: ['--no-sandbox'] });
const ctx = await b.newContext({ locale: 'id-ID', timezoneId: 'Asia/Jakarta', viewport: { width: 1280, height: 800 } });
const page = await ctx.newPage();
await page.goto('https://www.instagram.com/accounts/login/', { waitUntil: 'domcontentloaded', timeout: 60_000 });
await page.waitForTimeout(3500);

// "It submitted" is observable as a CHANGE the form causes: a self-posting
// endpoint (React Flight / ajax) or, failing that, an error/challenge message.
// A bare URL match missed the real endpoint and reported a false failure.
let posted = false;
page.on('request', (r) => {
  if (r.method() === 'POST' && /instagram\.com/.test(r.url()) && !/graphql|ajax\/bz|ajax\/qm/.test(r.url())) {
    posted = true;
  }
});

await fillMetaCredentials(page, { username: 'probe_not_a_real_user', password: 'probe_not_a_real_pw' });

// Meta signals a submit either with a request or by leaving the login form
// (a challenge or an error state both mean the click landed).
let feedback = '';
for (let i = 0; i < 24; i++) {
  await page.waitForTimeout(500);
  feedback = await page
    .evaluate(() => document.body.innerText.match(/informasi login yang anda masukkan|incorrect|couldn.t find|wrong password|please wait|challenge/i)?.[0] ?? '')
    .catch(() => '');
  if (posted || feedback) break;
}
await b.close();
const submitted = posted || feedback !== '';
console.log(JSON.stringify({ posted, feedback }));
if (!submitted) {
  console.error('FAIL: the form produced no request and no feedback');
  process.exit(1);
}
console.log('OK: the form submitted');
