/**
 * YouTube adapter (authenticates through the Google account flow). OTP-over-web
 * and the comment action are real; like/replyComment/likeComment are still the
 * honest stub (see `stub.ts`).
 *
 * Login is the operator's headful Google sign-in, so the session that matters is
 * the one YouTube mints, not the one Google mints. `continue` is load-bearing:
 * Google sets its cookies on `.google.com`, but YouTube only writes its own
 * cluster (`LOGIN_INFO`, `SAPISID`, `SID`, `HSID`, `SSID`) on `.youtube.com`
 * once the browser lands there. Save a state taken from accounts.google.com and
 * the next run opens youtube.com logged out.
 *
 * Proof is `SAPISID` (auth-critical: YouTube derives its SAPISIDHASH requests
 * from it) plus `LOGIN_INFO` (only set for a signed-in YouTube session).
 * `__Secure-1PSID` alone is NOT proof: Google sets it on `.google.com` before a
 * YouTube session exists. `LOGIN_INFO` is listed first so the value reported as
 * `handle` is never a credential.
 *
 * `verifyLogin` asks the product itself (`ytcfg.LOGGED_IN`) before the worker
 * persists anything — the same check the local harness uses.
 *
 * `autofill` types the two-step Google form (email → Next → password → Next).
 * Best effort by design: Google can answer with a challenge instead of the
 * password field, in which case the operator finishes it in the live view.
 *
 * The comment action works on both shapes of a YouTube target:
 *   - `/shorts/<id>`: the comments live in a right-hand engagement panel that
 *     is collapsed until the "View comments" button is clicked;
 *   - `/watch?v=<id>` (and a canonicalised `youtu.be/<id>`): the comments are
 *     inline below the player and lazy-load when scrolled to.
 * Both surfaces use the same `ytd-comment-simplebox-renderer` composer and the
 * same `#content-text` renderer, so only the "reveal the composer" step differs.
 *
 * `youtu.be` is CANONICALISED before navigation: on networks behind a media
 * filter (FortiGuard in this environment) the short domain is blocked at TLS,
 * while `www.youtube.com/watch?v=` loads normally. A worker that navigated the
 * short link would time out on a page it was never allowed to reach.
 */
import type { BrowserContext, Locator, Page } from 'playwright';

import type { ActionJob } from '../types.js';
import type { AdapterResult, LoginCredentials, PlatformAdapter } from './adapter.js';
import {
  failShot,
  missingText,
  runAction,
  TARGET_UNAVAILABLE_ERROR,
  type DomDeps,
} from './dom.js';
import { makeStubAdapter } from './stub.js';

export const YOUTUBE_LOGIN_URL =
  'https://accounts.google.com/ServiceLogin?service=youtube&continue=https%3A%2F%2Fwww.youtube.com%2F';

const YOUTUBE_HOME = 'https://www.youtube.com/';

/** Google renders the form client-side; poll instead of a single look. */
const FORM_WAIT_MS = 20_000;
/** YouTube lazy-loads the comment section after a scroll (watch pages) or a
 * panel click (Shorts); this is the bound for the composer to appear. */
const COMMENTS_WAIT_MS = 25_000;
/** Ground truth: the posted text must render inside a comment, not the editor. */
const COMMENT_VERIFY_MS = 15_000;
const COMMENT_POLL_MS = 500;
/**
 * The password step can be delayed by Google's own challenge (reCAPTCHA /
 * "verify it's you"), which the operator resolves in the live view. This bound
 * matches the worker's parked-login window: the fill runs alongside the poll,
 * so the password lands as soon as the challenge clears.
 */
const PASSWORD_WAIT_MS = 10 * 60_000;

async function visibleFirst(page: Page, selectors: string[], timeoutMs = FORM_WAIT_MS): Promise<Locator> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    for (const selector of selectors) {
      // `visible=true`: Google keeps hidden clones of the form in the DOM, and
      // filling one of those leaves the real field empty.
      const locator = page.locator(`${selector} >> visible=true`);
      if ((await locator.count().catch(() => 0)) > 0) return locator.first();
    }
    if (Date.now() >= deadline) {
      throw new Error(`Google sign-in field not found (tried: ${selectors.join(', ')})`);
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}

/**
 * Password step after the email submit. A challenge may sit in between, so wait
 * for the operator to clear it — but stop early when Google has already refused
 * the account, rather than burning the whole window.
 */
async function passwordField(page: Page): Promise<Locator> {
  const selectors = ['input[name="Passwd"]', 'input[type="password"]'];
  const deadline = Date.now() + PASSWORD_WAIT_MS;
  for (;;) {
    for (const selector of selectors) {
      const locator = page.locator(`${selector} >> visible=true`);
      if ((await locator.count().catch(() => 0)) > 0) return locator.first();
    }
    const text = await page
      .locator('body')
      .innerText({ timeout: 5_000 })
      .catch(() => '');
    if (/sandi salah|kata sandi salah|wrong password|tidak dapat menemukan akun|couldn.t find your google account/i.test(text)) {
      throw new Error('Google rejected the account at the identifier step');
    }
    if (Date.now() >= deadline) {
      throw new Error('Google password field not found (challenge not completed in time)');
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
  }
}

/**
 * Type like a person. Google's form is driven by keystroke handlers; a bare
 * `fill()` has been reported to leave the transport's own state empty.
 */
async function typeInto(field: Locator, value: string): Promise<void> {
  try {
    await field.click({ timeout: 5_000 });
    await field.fill('');
    await field.pressSequentially(value, { delay: 25 });
  } catch {
    await field.fill(value);
  }
}

/** Press the step's Next control, whatever markup Google served this time. */
async function clickNext(page: Page): Promise<void> {
  const candidates = [
    page.locator('#identifierNext, #passwordNext').first(),
    page.getByRole('button', { name: /^(next|berikutnya)$/i }).first(),
    page
      .locator(
        'button:has-text("Next"), button:has-text("Berikutnya"), div[role="button"]:has-text("Next"), div[role="button"]:has-text("Berikutnya")',
      )
      .first(),
  ];
  for (const candidate of candidates) {
    if ((await candidate.count().catch(() => 0)) === 0) continue;
    const box = await candidate.boundingBox().catch(() => null);
    // A 0x0 control cannot be clicked; waiting on it burns the login window.
    if (box && (box.width === 0 || box.height === 0)) continue;
    await candidate.click({ timeout: 5_000 });
    return;
  }
  await page.keyboard.press('Enter');
}


function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Canonicalise a `youtu.be/<id>` share link to `www.youtube.com/watch?v=<id>`.
 * The short domain is blocked by the media filter on some worker networks
 * (FortiGuard answers with an untrusted cert, so Chromium never loads a page),
 * while the canonical watch URL is allowed. Shorts/watch URLs pass through.
 */
export function canonicalYouTubeUrl(raw: string): string {
  try {
    const url = new URL(raw);
    if (url.hostname === 'youtu.be' || url.hostname === 'www.youtu.be') {
      const id = url.pathname.split('/').filter(Boolean)[0];
      if (id) {
        const params = new URLSearchParams(url.search);
        params.set('v', id);
        return `https://www.youtube.com/watch?${params.toString()}`;
      }
    }
  } catch {
    // Not a URL: navigation reports the real problem with the target's copy.
  }
  return raw;
}

async function visible(locator: Locator): Promise<boolean> {
  return locator.isVisible().catch(() => false);
}

/**
 * Wait for the page's own `ytcfg` boot. `true`/`false` are YouTube's verdict;
 * `undefined` means the page never booted (a filter/consent/error page), which
 * is a different failure from a dead session and must not be labelled AUTH.
 */
async function youtubeLoggedIn(page: Page): Promise<boolean | undefined> {
  try {
    await page.waitForFunction(
      () => {
        const w = globalThis as unknown as { ytcfg?: { get?: (k: string) => unknown } };
        return typeof w.ytcfg?.get === 'function';
      },
      undefined,
      { timeout: 20_000, polling: 500 },
    );
    return await page.evaluate(() => {
      const w = globalThis as unknown as { ytcfg?: { get?: (k: string) => unknown } };
      return w.ytcfg?.get?.('LOGGED_IN') === true;
    });
  } catch {
    return undefined;
  }
}

/**
 * Reveal the top-level composer. Shorts keeps it in a collapsed engagement
 * panel (click "View comments"); a watch page keeps it inline and lazy-loads it
 * once scrolled near. Returns undefined when neither surface produced one.
 */
async function revealComposer(page: Page): Promise<Locator | undefined> {
  const box = page.locator('ytd-comment-simplebox-renderer').first();
  const shorts = /\/shorts\//.test(page.url());
  let openedPanel = false;
  const deadline = Date.now() + COMMENTS_WAIT_MS;
  for (;;) {
    if (await visible(box)) return box;
    if (Date.now() >= deadline) break;

    if (shorts && !openedPanel) {
      // The rail label carries the count when the Short has comments
      // ("View 174 comments"); the old exact "View comments" missed every
      // popular Short and the job died with "composer not found".
      const view = page.locator('button[aria-label*="comment" i]').first();
      if (await visible(view)) {
        await view.click({ timeout: 5_000 }).catch(() => undefined);
        openedPanel = true;
        await sleep(600);
        continue;
      }
    }

    // Watch page: the comments section mounts below the fold. `ytd-comments`
    // can be in the DOM while still hidden (lazy hydration), and
    // scrollIntoViewIfNeeded on a hidden host is a no-op — that is how a Short
    // opened through its /watch?v= URL timed out before the composer mounted.
    // ALWAYS wheel the window; the host scroll is an additional nudge.
    await page
      .evaluate(() => {
        const w = globalThis as unknown as { scrollBy?: (x: number, y: number) => void };
        w.scrollBy?.(0, 1200);
      })
      .catch(() => undefined);
    await sleep(600);
  }
  return (await visible(box)) ? box : undefined;
}

/** Click the placeholder, then return the contenteditable it reveals. */
async function activateComposer(box: Locator): Promise<Locator> {
  const placeholder = box.locator('#simplebox-placeholder').first();
  if (await visible(placeholder)) await placeholder.click({ timeout: 5_000 });
  else await box.click({ timeout: 5_000 });
  const editor = box.locator('#contenteditable-root').first();
  await editor.waitFor({ state: 'visible', timeout: 10_000 });
  return editor;
}

/**
 * Type into YouTube's Polymer editor and report whether it took the text.
 *
 * `pressSequentially` was enough on a clean panel but lost focus on a busy
 * Shorts panel (174 comments) — the editor existed, the keystrokes went to the
 * page, and the job failed with "editor did not accept the text". Click the
 * editor with `force` and retry once via `insertText`, which does not depend on
 * per-key focus. Raw `fill` is the last resort.
 */
async function typeComment(page: Page, editor: Locator, text: string): Promise<boolean> {
  const wanted = text.replace(/\s+/g, ' ').trim();
  const read = async (): Promise<string> =>
    (await editor.innerText().catch(() => '')).replace(/\s+/g, ' ').trim();

  const clear = async (): Promise<void> => {
    await editor.click({ force: true, timeout: 5_000 }).catch(() => undefined);
    await page.keyboard.press('Control+A').catch(() => undefined);
    await page.keyboard.press('Backspace').catch(() => undefined);
    await editor.fill('', { timeout: 3_000 }).catch(() => undefined);
  };

  for (const mode of ['type', 'insert'] as const) {
    // A partial first pass must not survive into the retry: the observed
    // failure was "Ko" left by the keystroke pass and the full text inserted
    // after it, producing "KoKontroversi…" while `includes` still passed.
    await clear();
    await sleep(150);
    if (mode === 'type') {
      const lines = text.split(/\r?\n/);
      for (let i = 0; i < lines.length; i++) {
        if (i > 0) await page.keyboard.press('Shift+Enter');
        const line = lines[i];
        if (line) await page.keyboard.type(line, { delay: 10 });
      }
    } else {
      await page.keyboard.insertText(text);
    }
    await sleep(300);
    // Top-level YouTube comments have no prefilled mention; exact equality
    // catches a partial prefix that `includes` would accept.
    if ((await read()) === wanted) return true;
  }
  await editor.fill(text, { timeout: 5_000 }).catch(() => undefined);
  return (await read()) === wanted;
}

function normalise(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

/**
 * Post the comment and prove it landed. The editor is NOT evidence: `#content-text`
 * only exists inside a rendered comment, so its presence (with our text) means
 * YouTube accepted and displayed the post.
 */
async function renderedComment(page: Page, text: string): Promise<string | undefined> {
  const want = normalise(text);
  const deadline = Date.now() + COMMENT_VERIFY_MS;
  for (;;) {
    const seen = await page
      .locator('#content-text')
      .evaluateAll((els) => els.map((e) => (e as { innerText?: string }).innerText ?? '').filter(Boolean))
      .catch(() => [] as string[]);
    const hit = seen.find((t) => normalise(t).includes(want));
    if (hit) return hit.trim();
    if (Date.now() >= deadline) return undefined;
    await sleep(COMMENT_POLL_MS);
  }
}

async function commentOnVideo(d: DomDeps, text: string): Promise<AdapterResult> {
  const { page } = d;

  const loggedIn = await youtubeLoggedIn(page);
  if (loggedIn === false) return failShot(d, 'auth: login required (session expired or login wall)');
  if (loggedIn === undefined) {
    // Google's login redirect does not boot YouTube's config, so it lands here
    // too; distinguish it from a network/filter page with the URL.
    if (/accounts\.google\.com|\/(?:signin|servicelogin)/i.test(page.url())) {
      return failShot(d, 'auth: login required (session expired or login wall)');
    }
    return failShot(d, 'video page did not load (YouTube config missing)');
  }

  const body = await page
    .locator('body')
    .innerText({ timeout: 5_000 })
    .catch(() => '');
  if (/this video isn.t available anymore|this video is unavailable|video unavailable|private video/i.test(body)) {
    return failShot(d, TARGET_UNAVAILABLE_ERROR);
  }

  const box = await revealComposer(page);
  if (!box) {
    if (/comments are turned off|komentar.*(dimatikan|dinonaktifkan)/i.test(body)) {
      return failShot(d, 'comments are turned off on this video');
    }
    return failShot(d, 'comment composer not found (comments section did not load)');
  }

  const editor = await activateComposer(box).catch(() => undefined);
  if (!editor) return failShot(d, 'comment editor did not open');

  const editable = await editor.isEditable().catch(() => false);
  if (!editable) {
    return failShot(d, 'comment editor is not editable (this account may be restricted from commenting)');
  }
  if (!(await typeComment(page, editor, text))) {
    return failShot(d, 'comment editor did not accept the text');
  }

  const submit = page
    .locator('#submit-button button, button[aria-label="Comment"]')
    .filter({ visible: true })
    .first();
  if ((await submit.count().catch(() => 0)) === 0) {
    return failShot(d, 'comment submit button not found');
  }
  await submit.click({ timeout: 5_000 });

  const rendered = await renderedComment(page, text);
  if (!rendered) return failShot(d, 'comment not visible in feed (verification failed)');
  return { ok: true, renderedText: rendered };
}

export const youtubeAdapter: PlatformAdapter = {
  ...makeStubAdapter({
    platform: 'youtube',
    loginUrl: YOUTUBE_LOGIN_URL,
    sessionCookies: ['LOGIN_INFO', 'SAPISID'],
    // Google's 2-step code field is `idvPin`; the generic one-time-code hint
    // covers the newer prompt variants.
    otpFieldSelector: 'input[name="idvPin"], input[autocomplete="one-time-code"]',
  }),
  async autofill(page: Page, credentials: LoginCredentials): Promise<void> {
    const email = await visibleFirst(page, [
      'input[type="email"]',
      '#identifierId',
      'input[name="identifier"]',
    ]);
    await typeInto(email, credentials.username);
    await clickNext(page);

    const password = await passwordField(page);
    await typeInto(password, credentials.password);
    await clickNext(page);
  },
  comment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    const targetUrl = canonicalYouTubeUrl(job.targetUrl);
    return runAction(ctx, 'youtube', { ...job, targetUrl }, (d) => commentOnVideo(d, job.text as string), {
      // The worker fleet runs ten Chromiums; a cold YouTube player bundle can
      // exceed the default 30s before DOMContentLoaded.
      openTimeoutMs: 90_000,
    });
  },
  async verifyLogin(ctx: BrowserContext): Promise<boolean> {
    const page = ctx.pages()[ctx.pages().length - 1];
    if (!page) return false;
    if (!page.url().startsWith(YOUTUBE_HOME)) {
      await page.goto(YOUTUBE_HOME, { waitUntil: 'commit', timeout: 60_000 }).catch(() => undefined);
    }
    return page
      .waitForFunction(
        () => {
          const w = globalThis as unknown as { ytcfg?: { get?: (k: string) => unknown } };
          return w.ytcfg?.get?.('LOGGED_IN') === true;
        },
        undefined,
        // Timer polling, not rAF: a backgrounded tab (noVNC window behind
        // another, or an Xvfb session with no focus) throttles animation
        // frames, and the rAF default then times out on a page that is already
        // logged in.
        { timeout: 30_000, polling: 500 },
      )
      .then(() => true)
      .catch(() => false);
  },
};
