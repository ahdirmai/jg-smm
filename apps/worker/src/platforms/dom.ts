/**
 * Shared DOM primitives for the action adapters (P3-04 / P3-05 / P3-08). Both
 * Meta platforms expose the same shape — a `[role=feed]`, a contenteditable
 * composer, and an svg like button whose state flips — so the steps live once
 * here. Per-platform files supply only selectors and a login URL (DIP: the
 * adapters depend on these helpers + sel/, never on platform markup details).
 *
 * Verification is ground truth, never optimism (DEVELOPMENT_RULE §7.4): a
 * comment counts only when its text is visible in the feed, and a like counts
 * only when the button state changed. No verified signal ⇒ no SUCCESS.
 */
import type { BrowserContext, Locator, Page } from 'playwright';

import type { Platform } from '@smm/shared';

import type { ActionJob } from '../types.js';
import type { AdapterResult, LoginCredentials, LoginResultLike } from './adapter.js';
import { adapterDeps } from './deps.js';
import type { SelectorSet } from '../sel/index.js';
import { selectorsFor } from '../sel/index.js';
import { captureScreenshot } from '../core/screenshot.js';
import { pollLoginOutcome } from '../core/auth.js';

/** Ground-truth poll bound (§7.4: comment must appear in feed, poll ≤15s). */
export const VERIFY_TIMEOUT_MS = 15_000;
const POLL_MS = 500;

export interface DomDeps {
  page: Page;
  sel: SelectorSet;
  job: ActionJob;
  /** Injectable for deterministic tests. */
  pollMs?: number;
  now?: () => number;
}

/**
 * Open a post permalink. `domcontentloaded` only: `networkidle` never fires on
 * a Meta page (infinite re-layout), so waiting on it would time out every job.
 */
export async function openPost(page: Page, url: string): Promise<void> {
  await page.goto(url, { waitUntil: 'domcontentloaded' });
}

/**
 * A login/2FA wall means the session is dead. Reported as an AUTH-class failure
 * (P3-12) rather than retried as transient — no amount of retrying fixes an
 * expired session.
 */
export async function needsLogin(page: Page): Promise<boolean> {
  if (/\/(login|accounts\/login|checkpoint|two_factor)/i.test(page.url())) return true;
  const count = await page
    .locator('input[name="verificationCode"], input[autocomplete="one-time-code"]')
    .count();
  return count > 0;
}

/** Like the post and confirm the button state actually flipped. */
export async function likePost(d: DomDeps): Promise<AdapterResult> {
  const { page, sel } = d;
  if (!sel.likeButton) return { ok: false, error: 'no like selector pinned' };

  const before = await likeState(d);
  if (before === undefined) return failShot(d, 'like button not found on the post');
  if (before.liked) return { ok: true }; // idempotent: already liked is success

  await page.locator(sel.likeButton).first().click();

  const changed = await pollUntil(
    async () => likeStateChanged(d, before),
    d.pollMs ?? POLL_MS,
    VERIFY_TIMEOUT_MS,
    d.now,
  );
  if (!changed) return failShot(d, 'like state did not change (verification failed)');
  return { ok: true };
}

/** How long to hunt for the composer once the affordance has been clicked. */
const COMPOSER_OPEN_TIMEOUT_MS = 8_000;
/** A quick first look: on a permalink the composer is usually already mounted. */
const COMPOSER_PROBE_MS = 1_000;

/**
 * Submit a comment and verify it rendered (§7.4). The flow is defensive because
 * Meta reshapes the composer across rollouts:
 *
 *   1. Look for the composer with the ordered candidate list. It is usually
 *      already on a post permalink.
 *   2. If it is not present, click the comment affordance (the reply bubble /
 *      "Comment" icon) to reveal or focus it, then look again.
 *   3. Fill the first candidate that appeared, then submit keyboard-first
 *      (Enter → Ctrl+Enter) and fall back to the "Post" button only when the
 *      text is still absent — that ordering keeps a working keyboard submit
 *      from double-posting.
 *
 * Every step has a short bound and fails with a screenshot rather than throwing,
 * so a stale selector is a clear failure, not a 30s hang (the P-A bug).
 */
export async function commentOnPost(d: DomDeps, text: string): Promise<AdapterResult> {
  const { page, sel } = d;
  const candidates = composerCandidates(sel);
  if (candidates.length === 0) {
    return { ok: false, error: 'no comment selectors pinned' };
  }

  // Threads hydrates the reply composer only after a scroll to the bottom of the
  // thread, and IG lazy-loads late comments the same way; harmless where the
  // composer is already mounted. (ref: jg/automation worker.)
  await scrollToLoadComposer(page);

  // 1. The composer is usually already mounted on a permalink.
  let box = await findComposer(d, candidates, COMPOSER_PROBE_MS);
  // 2. If not, click the comment affordance to reveal/focus it, then re-hunt.
  if (!box && sel.commentButton) {
    await page
      .locator(sel.commentButton)
      .first()
      .click({ timeout: 5_000 })
      .catch(() => undefined);
    box = await findComposer(d, candidates, COMPOSER_OPEN_TIMEOUT_MS);
  }
  if (!box) return failShot(d, 'comment composer not found (even after opening it)');

  // 3. Fill + submit + verify (shared with replyToComment).
  return fillAndSubmit(d, box, text);
}

/**
 * Fill a located composer, submit it, and verify the text landed. Shared by
 * commentOnPost and replyToComment so "how a comment is sent and confirmed" has
 * one definition. Submit retries are guarded by `composerStillHas`: once the
 * composer has cleared the submit landed, so re-pressing would post a DUPLICATE.
 */
async function fillAndSubmit(d: DomDeps, box: Locator, text: string): Promise<AdapterResult> {
  const { page, sel } = d;
  // A focus click first: some composers only accept input once focused.
  await box.click({ timeout: 3_000 }).catch(() => undefined);
  await box.fill(text, { timeout: 5_000 });

  await box.press('Enter').catch(() => undefined);
  let rendered = await waitForCommentText(d, text, 3_000);
  if (!rendered && (await composerStillHas(box, text))) {
    await box.press('Control+Enter').catch(() => undefined);
    rendered = await waitForCommentText(d, text, 3_000);
  }
  if (!rendered && sel.submitButton && (await composerStillHas(box, text))) {
    await page
      .locator(sel.submitButton)
      .first()
      .click({ timeout: 5_000 })
      .catch(() => undefined);
    rendered = await waitForCommentText(d, text);
  }
  // The composer cleared but the feed had not rendered yet within the short
  // per-attempt windows: give it the full verify budget before giving up, so a
  // slow render is not misreported as a failure (which would trigger a retry
  // and a duplicate comment).
  if (!rendered && !(await composerStillHas(box, text))) {
    rendered = await waitForCommentText(d, text);
  }
  if (!rendered) return failShot(d, 'comment not visible in feed (verification failed)');
  return { ok: true, renderedText: rendered };
}

/**
 * Reply to a specific comment. The target is a comment permalink
 * (`/p/<post>/c/<commentId>/` on IG) already open in the page, so the target
 * comment is focused and the first Reply affordance threads to it. Reuses the
 * same fill/submit/verify path as a top-level comment.
 */
export async function replyToComment(d: DomDeps, text: string): Promise<AdapterResult> {
  const { page, sel } = d;
  const candidates = composerCandidates(sel);
  if (candidates.length === 0) {
    return { ok: false, error: 'no comment selectors pinned' };
  }

  // Scroll first so a lazily-hydrated reply composer mounts (ref: jg/automation).
  await scrollToLoadComposer(page);
  // The composer is sometimes already open on a comment permalink; if not, click
  // the reply affordance (reply-specific when pinned, else the generic one).
  let box = await findComposer(d, candidates, COMPOSER_PROBE_MS);
  const replyAffordance = sel.replyButton ?? sel.commentButton;
  if (!box && replyAffordance) {
    await page
      .locator(replyAffordance)
      .first()
      .click({ timeout: 5_000 })
      .catch(() => undefined);
    box = await findComposer(d, candidates, COMPOSER_OPEN_TIMEOUT_MS);
  }
  if (!box) return failShot(d, 'reply composer not found (even after opening it)');
  return fillAndSubmit(d, box, text);
}

/**
 * True while the composer still holds the unsent text. A textarea exposes it as
 * the input value; a contenteditable as its text content. Used to gate submit
 * retries so a landed comment is never posted twice.
 */
async function composerStillHas(box: Locator, text: string): Promise<boolean> {
  const val = await box.inputValue().catch(() => null);
  if (val !== null) return val.includes(text);
  const inner = await box.innerText().catch(() => '');
  return inner.includes(text);
}

/** The ordered composer candidates, preferring the explicit list over the single. */
function composerCandidates(sel: SelectorSet): string[] {
  if (sel.composerInputs && sel.composerInputs.length > 0) return sel.composerInputs;
  return sel.composerInput ? [sel.composerInput] : [];
}

/**
 * Scroll to the bottom of the thread so a lazily-hydrated reply composer mounts.
 * Threads renders the composer only after this; IG lazy-loads trailing comments
 * the same way. Best-effort and harmless when the composer is already present.
 */
async function scrollToLoadComposer(page: Page): Promise<void> {
  // String form so tsc doesn't need the DOM lib for window/document (this runs
  // in the page, not in the worker's node context).
  await page.evaluate('window.scrollTo(0, document.body.scrollHeight)').catch(() => undefined);
  await sleep(600);
}

/**
 * Poll the candidate selectors IN ORDER until one is present, or the bound
 * expires. Presence (count > 0) is used rather than `waitFor` so a candidate
 * that never mounts on this rollout is skipped immediately instead of eating
 * the whole timeout.
 */
async function findComposer(
  d: DomDeps,
  selectors: string[],
  timeoutMs: number,
): Promise<Locator | undefined> {
  const started = (d.now ?? Date.now)();
  for (;;) {
    for (const s of selectors) {
      const loc = d.page.locator(s).first();
      const present = await loc
        .count()
        .then((n) => n > 0)
        .catch(() => false);
      if (present) return loc;
    }
    if ((d.now ?? Date.now)() - started >= timeoutMs) return undefined;
    await sleep(d.pollMs ?? POLL_MS);
  }
}

/**
 * Ground-truth check for an already-submitted comment: poll the feed until the
 * exact rendered text appears, or the bound expires. Exported so `verify` is
 * the same code path `comment` used (DRY — one definition of "landed").
 */
export async function waitForCommentText(
  d: DomDeps,
  text: string,
  timeoutMs: number = VERIFY_TIMEOUT_MS,
): Promise<string | undefined> {
  // Scan the whole document, not just the first feed match. On current IG the
  // feed selector's first match is often the media/header section, whose text
  // never contains a freshly-posted comment — reading only that turned a landed
  // comment into a false "not visible" and drove retries. Body innerText always
  // includes a rendered comment; the pinned feed is a narrowing hint, not a gate.
  const started = (d.now ?? Date.now)();
  for (;;) {
    let body = await d.page.locator('body').innerText().catch(() => '');
    if (!body && d.sel.feed) {
      body = await d.page.locator(d.sel.feed).first().innerText().catch(() => '');
    }
    if (body.includes(text)) return text;
    if ((d.now ?? Date.now)() - started >= timeoutMs) return undefined;
    await sleep(d.pollMs ?? POLL_MS);
  }
}

/**
 * A comment/reply job must carry rendered text. An empty one is a config bug
 * on the BE side, and it is not retryable: re-running it cannot fix a missing
 * template render.
 */
export function missingText(): AdapterResult {
  return { ok: false, error: 'empty comment text: the BE must compose a template before publish' };
}

// --- page lifecycle ------------------------------------------------------

/**
 * The shared action wrapper every Meta adapter runs through (P3-04/P3-05).
 * Owns the page lifecycle and the two invariants that hold for every platform:
 * the post is opened before anything is touched, and a dead session is an
 * AUTH failure (never retried — the BE classifier keys the "login required"
 * string to AUTH, P3-12) rather than a transient one.
 *
 * A failure always carries a screenshot: the operator's only evidence of what
 * the page actually showed. Never throws — the caller decides what a failure
 * means, and an orchestration bug must not masquerade as a platform failure.
 */
export async function runAction(
  ctx: BrowserContext,
  platform: Platform,
  job: ActionJob,
  fn: (d: DomDeps) => Promise<AdapterResult>,
): Promise<AdapterResult> {
  const page = await ctx.newPage();
  const d: DomDeps = { page, sel: selectorsFor(platform), job };
  try {
    await openPost(page, job.targetUrl);
    if (await needsLogin(page)) {
      // "login required" + "session expired" are the AUTH needles (P3-12), so
      // this lands as non-retryable no matter which the classifier hits first.
      return await failShot(d, 'auth: login required (session expired or login wall)');
    }
    return await fn(d);
  } catch (err) {
    return await failShot(d, `action ${job.action} threw: ${(err as Error).message}`);
  } finally {
    await page.close().catch(() => undefined);
  }
}

/**
 * Credential login shared by the Meta adapters. Both surface the same form
 * (`input[name=username]` / `input[name=password]`), so the step lives once
 * here; a platform supplies only its login URL. Verdict comes from
 * `pollLoginOutcome` — the cookie standard, never the URL (DRY with the
 * operator headful flow in core/auth).
 */
export async function credentialLogin(
  ctx: BrowserContext,
  platform: Platform,
  credentials: LoginCredentials,
  loginUrl: string,
): Promise<LoginResultLike> {
  const page = await ctx.newPage();
  try {
    await page.goto(loginUrl, { waitUntil: 'domcontentloaded' });
    await page.locator('input[name="username"]').fill(credentials.username);
    const password = page.locator('input[name="password"]');
    await password.fill(credentials.password);
    await password.press('Enter');
    // The page must stay open while the poll runs: closing it would abort the
    // submit navigation. Cookies live on the context, so the verdict reads the
    // context, and the page is dropped only once an outcome is reached.
    return await pollLoginOutcome(platform, ctx);
  } finally {
    await page.close().catch(() => undefined);
  }
}

// --- internals ------------------------------------------------------------

interface LikeState {
  token: string;
  liked: boolean;
}

/**
 * Read the like button's state as a comparable token. Three signals are tried
 * because Meta surfaces the state inconsistently across rollouts: aria-pressed,
 * the svg aria-label (Like↔Unlike), and the pinned active selector.
 */
async function likeState(d: DomDeps): Promise<LikeState | undefined> {
  const { page, sel } = d;
  if (!sel.likeButton) return undefined;
  const btn = page.locator(sel.likeButton).first();
  if ((await btn.count()) === 0) return undefined;

  const pressed = await btn.getAttribute('aria-pressed').catch(() => null);
  const label = await btn.getAttribute('aria-label').catch(() => null);
  let active = 0;
  if (sel.likeButtonActive) {
    active = await page
      .locator(sel.likeButtonActive)
      .first()
      .count()
      .catch(() => 0);
  }
  const liked = pressed === 'true' || /unlike|remove like/i.test(String(label)) || active > 0;
  return { token: `${pressed}|${label}|${active}`, liked };
}

async function likeStateChanged(d: DomDeps, before: LikeState): Promise<boolean> {
  const after = await likeState(d);
  return after !== undefined && after.token !== before.token;
}

/** Attach a screenshot to a failure. The name is deterministic (§7.3). */
export async function failShot(d: DomDeps, message: string): Promise<AdapterResult> {
  const deps = adapterDeps();
  const shot = await captureScreenshot(
    d.page,
    d.job.id,
    deps.workerId,
    deps.screenshotDir ? { dir: deps.screenshotDir } : {},
  );
  return { ok: false, error: message, ...(shot ? { screenshot: shot } : {}) };
}

async function pollUntil(
  cond: () => Promise<boolean>,
  pollMs: number,
  timeoutMs: number,
  now?: () => number,
): Promise<boolean> {
  const started = (now ?? Date.now)();
  for (;;) {
    if (await cond()) return true;
    if ((now ?? Date.now)() - started >= timeoutMs) return false;
    await sleep(pollMs);
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
