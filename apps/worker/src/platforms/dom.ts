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

/** How long to wait for a bounced permalink to inject its post link / to route. */
const DEEPLINK_RECOVER_MS = 8_000;
const DEEPLINK_POLL_MS = 400;

/** The post shortcode in a Meta permalink (IG `/p|reel|tv/<code>`, Threads `/post/<code>`). */
export function postShortcode(url: string): string | undefined {
  return /\/(?:p|reel|tv|post|c)\/([A-Za-z0-9_-]+)/.exec(url)?.[1];
}

/**
 * The comment id in a Meta comment permalink (`/p/<code>/c/<commentId>/`).
 * Empty means the URL addresses a post, not a comment — the API rejects a
 * reply job in that case, so this is the worker's half of the same contract.
 */
export function commentId(url: string): string | undefined {
  return /\/c\/([A-Za-z0-9_-]+)/.exec(url)?.[1];
}

/** Bound for the account-chooser interstitial to appear/settle after a cold load. */
const INTERSTITIAL_WAIT_MS = 4_000;

/**
 * Dismiss Meta's "Continue as <user>" account-chooser interstitial if present.
 * A cold context lands on it with a valid session; the "Continue" button (a
 * `<button>`/`<div role=button>`/`<a>` whose own text is exactly "Continue")
 * warms the profile. Returns true when a Continue affordance was clicked, so the
 * caller knows to re-navigate. Best-effort: any miss or error returns false and
 * the normal flow proceeds. Scoped to an exact-text match so it never fires on
 * an unrelated "Continue" elsewhere in the app.
 */
export async function dismissContinueInterstitial(page: Page): Promise<boolean> {
  const btn = page
    .locator('button, div[role="button"], a')
    .filter({ hasText: /^\s*Continue\s*$/ })
    .first();
  const started = Date.now();
  while (Date.now() - started < INTERSTITIAL_WAIT_MS) {
    if ((await btn.count().catch(() => 0)) > 0) {
      const clicked = await btn
        .click({ timeout: 5_000 })
        .then(() => true)
        .catch(() => false);
      if (clicked) {
        await sleep(DEEPLINK_POLL_MS);
        return true;
      }
    }
    await sleep(DEEPLINK_POLL_MS);
  }
  return false;
}

/**
 * Dismiss Meta's periodic "Your Meta Account is here" account-consolidation
 * modal if present.
 *
 * Why this exists (observed live, two separate accounts): Meta shows a
 * full-screen `role=dialog` promotion — "Your Meta Account is here / Your
 * updated account works across Meta apps and devices" with a "See what's new"
 * button — on a normal post load. It carries a modal overlay, so EVERY
 * subsequent click lands on the scrim instead of the page underneath. The reply
 * flow then opens no composer and fails as "no @mention prefilled", blaming the
 * selector for what is really an unclickable page. The same screenshot showed
 * the composer rendered behind the modal, which is the tell.
 *
 * It is dismissed by its own close control, with a text-matched fallback on the
 * confirmation button for rollouts that drop the X. Scoped to the exact
 * copy so it can never fire on an unrelated dialog. Best-effort: a miss
 * returns false and the normal flow proceeds — this must never be the reason a
 * job fails.
 */
export async function dismissMetaAccountModal(page: Page): Promise<boolean> {
  const dialog = page.locator('div[role="dialog"]').filter({ hasText: /Meta Account is here/i }).first();
  if ((await dialog.count().catch(() => 0)) === 0) return false;

  // Prefer the X: it leaves the page exactly as it was. The primary button is a
  // fallback for a rollout that ships no close control.
  const closer = dialog
    .locator('button, div[role="button"], [aria-label*="Close" i]')
    .filter({ hasText: /^\s*$/ })
    .or(dialog.locator('[aria-label*="Close" i]'))
    .first();
  const clicked =
    (await closer
      .click({ timeout: 3_000 })
      .then(() => true)
      .catch(() => false)) ||
    (await dialog
      .locator('button, div[role="button"]')
      .filter({ hasText: /See what's new|OK|Got it|Close/i })
      .first()
      .click({ timeout: 3_000 })
      .then(() => true)
      .catch(() => false));

  if (clicked) {
    // Let the overlay fade before anything tries to click through it.
    await page.waitForTimeout(600);
  }
  return clicked;
}


/**
 * Open a post permalink. `domcontentloaded` only: `networkidle` never fires on
 * a Meta page (infinite re-layout), so waiting on it would time out every job.
 *
 * Deep-link recovery (Threads): a COLD permalink load boots the SPA, which
 * bounces to the home feed and normalises the URL to `/` — but it injects the
 * target post's own link into that feed (`injected_media_ids`). A full
 * `page.goto` can only ever cold-boot, so it never lands on the post. When we
 * bounce, click the injected link: that is a client-side route, which the SPA
 * resolves to the standalone post view. IG lands on the permalink directly, so
 * the URL already carries the shortcode and this is a no-op there.
 */
export async function openPost(page: Page, url: string): Promise<void> {
  await page.goto(url, { waitUntil: 'domcontentloaded' });

  const short = postShortcode(url);
  if (!short) return;

  // Already on the post (IG, or a Threads load that didn't bounce): done, and
  // crucially we do NOT wait on anything below — IG mounts the composer only
  // after the comment affordance is clicked, so waiting for it here would hang.
  const onPost = (): boolean => page.url().includes(short);
  if (onPost()) {
    // Still clear any modal: it can cover the post we are already on (observed
    // live), and every later click would land on its scrim.
    await dismissMetaAccountModal(page);
    return;
  }

  // Meta account-chooser interstitial. A COLD browser context (fresh container
  // profile, valid session cookie but no warmed profile) loads a permalink into
  // a "Continue as <user>" screen that bounces the URL to `/` with NO injected
  // post link — so the deeplink recovery below can never find one to click, and
  // every action reads an empty action bar ("like button not found"). Clicking
  // Continue warms the profile; a re-goto then lands on the permalink directly
  // (IG). Verified live in-container: post URL bounced to `/`, one Continue
  // click + re-goto put the like button back on the page. Best-effort and
  // bounded: absent screen → no-op, and Threads still falls through to recovery.
  if (await dismissContinueInterstitial(page)) {
    await page.goto(url, { waitUntil: 'domcontentloaded' });
    if (onPost()) {
      await dismissMetaAccountModal(page);
      return;
    }
  }

  // Bounced (Threads): click the injected post link to client-side route in.
  let recovered = false;
  const started = Date.now();
  while (!onPost() && Date.now() - started < DEEPLINK_RECOVER_MS) {
    const link = page.locator(`a[href*="${short}"]`).first();
    if ((await link.count().catch(() => 0)) > 0) {
      await link.click({ timeout: 5_000 }).catch(() => undefined);
      await sleep(DEEPLINK_POLL_MS);
      if (onPost()) {
        recovered = true;
        break;
      }
    }
    await sleep(DEEPLINK_POLL_MS);
  }

  // The URL flips to the permalink before the post view finishes mounting, so a
  // caller that reads the action bar immediately (likePost takes one look, it
  // does not poll) races the render. After a recovery, wait for the composer —
  // it mounts together with the post's action bar — so the post is interactive
  // before we hand back. Bounded; a miss just means the caller's own wait runs.
  if (recovered) {
    const renderBy = Date.now() + DEEPLINK_RECOVER_MS;
    while (Date.now() < renderBy) {
      const present = await page
        .locator('div[contenteditable="true"][role="textbox"]')
        .count()
        .then((n) => n > 0)
        .catch(() => false);
      if (present) break;
      await sleep(DEEPLINK_POLL_MS);
    }
  }

  // Last step, whichever route we took: clear the consolidation modal. It is
  // shown on ordinary post loads and its scrim swallows every later click.
  await dismissMetaAccountModal(page);
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

/**
 * The failure text for a permalink that resolved to Meta's "page isn't
 * available" screen. Exported so the BE classifier contract is asserted against
 * the same string the worker sends (it matches no classify.go needle: the
 * target is gone, not the session, so the job is UNKNOWN — not retryable).
 */
export const TARGET_UNAVAILABLE_ERROR =
  'target unavailable: the post is deleted, private, or hidden from this account';

/**
 * True when the permalink rendered Meta's "page isn't available" screen — the
 * post is deleted, private, or this account cannot see it (e.g. it is blocked
 * by the author).
 *
 * This is a 200 with a full HTML shell, so nothing else catches it: the URL is
 * still the permalink (needsLogin sees no login route) and there is no login
 * field. Without this gate every action burns its own timeout and then reports
 * a SELECTOR failure ("like button not found on the post"), which names the
 * wrong cause — the account did nothing wrong and no retry can help.
 *
 * Ground truth is RENDERED text (`innerText`, never `textContent`): Meta inlines
 * large JSON payloads carrying these strings, and only a visible occurrence
 * means the screen is actually up. English copy only — verified live. A rollout
 * that localises the screen needs its string added here (the fake-page tests
 * pin the contract).
 */
export async function pageUnavailable(page: Page): Promise<boolean> {
  const body = await page
    .locator('body')
    .innerText()
    .catch(() => '');
  // Normalise the typographic apostrophe so both spellings match.
  const text = body.toLowerCase().replace(/’/g, "'");
  return text.includes("this page isn't available") || text.includes('page not found');
}

/** How long to wait for the post's action bar (the like button) to paint. */
const LIKE_BUTTON_TIMEOUT_MS = 8_000;

/**
 * Root the like selectors resolve against. When `likeButtonScope` is pinned (IG:
 * every comment renders its own like heart, so an unscoped `likeButton` `.first()`
 * in DOM order lands on a COMMENT), the post heart lives in the action bar — the
 * innermost element holding the like heart AND the Comment affordance — so we
 * take `.last()` of the scope (`:has` matches every ancestor of the bar; the
 * innermost is the bar). If the scope resolves to nothing (unexpected markup),
 * fall back to the whole page so a valid post is never misread as "not found".
 * Absent scope (Threads pre-scopes its like selectors) → the whole page.
 */
async function likeRoot(d: DomDeps): Promise<Locator | Page> {
  const { page, sel } = d;
  if (!sel.likeButtonScope) return page;
  const scoped = page.locator(sel.likeButtonScope);
  return (await scoped.count().catch(() => 0)) > 0 ? scoped.last() : page;
}

/** Like the post and confirm the button state actually flipped. */
export async function likePost(d: DomDeps): Promise<AdapterResult> {
  const { sel } = d;
  if (!sel.likeButton) return { ok: false, error: 'no like selector pinned' };

  // The action bar can paint a beat after openPost returns: IG returns as soon
  // as the URL carries the post shortcode (openPost does NOT wait, so the IG
  // composer path is not blocked), which is before the like button mounts. A
  // single state read here would race that paint and misreport "button not
  // found", so wait (bounded) for the button to be present first.
  const present = await pollUntil(
    async () => (await (await likeRoot(d)).locator(sel.likeButton!).first().count().catch(() => 0)) > 0,
    d.pollMs ?? POLL_MS,
    LIKE_BUTTON_TIMEOUT_MS,
    d.now,
  );
  if (!present) return failShot(d, 'like button not found on the post');

  const before = await likeState(d);
  if (before === undefined) return failShot(d, 'like button not found on the post');
  if (before.liked) return { ok: true }; // idempotent: already liked is success

  await (await likeRoot(d)).locator(sel.likeButton).first().click();

  const changed = await pollUntil(
    async () => likeStateChanged(d, before),
    d.pollMs ?? POLL_MS,
    VERIFY_TIMEOUT_MS,
    d.now,
  );
  if (!changed) return failShot(d, 'like state did not change (verification failed)');
  return { ok: true };
}

/** Read a like control's state label, whether it is labelled by aria-label
 * (Instagram: `svg[aria-label="Like"|"Unlike"]`) or by an svg `<title>` (Threads:
 * `<svg><title>Like|Unlike</title>`). Returns the lowercased word, or undefined
 * when the element is gone. `liked` is the "Unlike"/"remove like" reading. */
async function controlLikeLabel(loc: Locator): Promise<string | undefined> {
  if ((await loc.count().catch(() => 0)) === 0) return undefined;
  const aria = await loc.getAttribute('aria-label').catch(() => null);
  if (aria) return aria.trim().toLowerCase();
  // Threads: the label is an svg <title> inside the button.
  const title = await loc
    .locator('svg title')
    .first()
    .textContent()
    .catch(() => null);
  if (title) return title.trim().toLowerCase();
  return '';
}

/** Resolve the first COMMENT like control, excluding the post's own heart. The
 * post heart (when a post scope is pinned) shares the comment heart's selector;
 * it is the one inside likeButtonScope, so we skip it and take the next match.
 * Returns undefined when the only match is the post heart (no comment to like). */
async function firstCommentLike(d: DomDeps, commentSel: string): Promise<Locator | undefined> {
  const { page, sel } = d;
  const all = page.locator(commentSel);
  const n = await all.count().catch(() => 0);
  if (n === 0) return undefined;

  // The post heart's element handle (if a post scope is pinned), so we can skip
  // it by identity rather than by a brittle index.
  let postHandle: import('playwright').ElementHandle | null = null;
  if (sel.likeButtonScope && sel.likeButton) {
    postHandle = await (await likeRoot(d))
      .locator(sel.likeButton)
      .first()
      .elementHandle()
      .catch(() => null);
  }

  for (let i = 0; i < n; i++) {
    const cand = all.nth(i);
    if (postHandle) {
      const h = await cand.elementHandle().catch(() => null);
      const same = h ? await page.evaluate(([a, b]) => a === b, [h, postHandle]).catch(() => false) : false;
      if (h) await h.dispose().catch(() => undefined);
      if (same) continue; // this is the post heart, not a comment
    }
    return cand;
  }
  return undefined;
}

/**
 * Like the top comment on the post and confirm the button state flipped
 * (ACTION_LIKE_COMMENT). Requires `commentLikeButton` to be pinned for the
 * platform: it never falls back to `likeButton`, which is the POST — liking the
 * post when asked to like a comment would be a silent wrong action.
 */
export async function likeComment(d: DomDeps): Promise<AdapterResult> {
  const { page, sel } = d;
  if (!sel.commentLikeButton) {
    return { ok: false, error: 'like-comment is not supported on this platform yet (no comment-like selector)' };
  }

  // Wait (bounded) for at least one comment like heart to paint.
  const present = await pollUntil(
    async () => (await page.locator(sel.commentLikeButton!).count().catch(() => 0)) > 0,
    d.pollMs ?? POLL_MS,
    LIKE_BUTTON_TIMEOUT_MS,
    d.now,
  );
  if (!present) return failShot(d, 'no comment found to like on the post');

  const target = await firstCommentLike(d, sel.commentLikeButton);
  if (!target) return failShot(d, 'no comment like button found (only the post heart is present)');

  const before = await controlLikeLabel(target);
  if (before === undefined) return failShot(d, 'comment like button state unreadable');
  if (/unlike|remove like/i.test(before)) return { ok: true }; // idempotent: already liked

  // Click the BUTTON ancestor, not the svg, and force it. Verified live on a
  // comment heart: a direct svg click and a plain Playwright click both leave
  // the state unchanged (an inner element wins Playwright's actionability hit-
  // test, so the click lands on a non-handling node), while `force: true` — and
  // a raw JS click — do flip it. force bypasses the intercept check but still
  // dispatches a real mouse event at the element, which the like handler takes.
  // `ancestor-or-self` also handles a platform whose commentLikeButton already
  // points at the button (Threads).
  // A native JS click dispatched on the button element is what reliably lands
  // (verified live: a plain Playwright click and even a forced click on the
  // resolved locator can miss when the comment list re-renders between resolve
  // and click, but `el.closest(button).click()` evaluated in-page always fires
  // on the live node). Fall back to a forced Playwright click if evaluate is
  // unavailable (the fake page in unit tests).
  const clicked = await target
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    .evaluate((el: any) => {
      const btn = el.closest('[role="button"],button') ?? el;
      btn.click();
      return true;
    })
    .catch(() => false);
  if (!clicked) {
    const clickable = target.locator('xpath=ancestor-or-self::*[self::button or @role="button"][1]');
    const btnTarget = (await clickable.count().catch(() => 0)) > 0 ? clickable.first() : target;
    await btnTarget.click({ timeout: 6_000, force: true });
  }

  const changed = await pollUntil(
    async () => {
      const now = await controlLikeLabel(target);
      return now !== undefined && now !== before;
    },
    d.pollMs ?? POLL_MS,
    VERIFY_TIMEOUT_MS,
    d.now,
  );
  if (!changed) return failShot(d, 'comment like state did not change (verification failed)');
  return { ok: true };
}

/** How long to hunt for the composer once the affordance has been clicked. */
const COMPOSER_OPEN_TIMEOUT_MS = 8_000;
/** A quick first look: on a permalink the composer is usually already mounted. */
const COMPOSER_PROBE_MS = 1_000;
/** How long to wait for IG to pre-fill the "@mention" after opening a reply. */
const MENTION_TIMEOUT_MS = 5_000;
/**
 * How long to wait for the per-comment "Reply" affordance to mount before
 * clicking it. Measured live: 0 matches until ~3s on a warm load, and ~5s on a
 * load where Meta's consolidation modal has to be dismissed first (the modal
 * delays the feed render). The bound is generous on purpose — the wait is
 * bounded and its expiry is not a failure, so a slow mount costs latency only
 * when the composer never arrives. A tight bound here is what turned a slow
 * render into a failed job.
 */
const AFFORDANCE_WAIT_MS = 12_000;

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
 * Reply to a specific comment. The job's targetUrl is a comment permalink
 * (`/p/<post>/c/<commentId>/` on IG); the check below confirms the browser
 * actually landed on that comment before anything is clicked. Then clicking the
 * focused comment's Reply affordance opens its reply box, which threads the
 * answer under it. Reuses the same fill/submit/verify path as a top-level
 * comment.
 *
 * ponytail: `.first()` still picks the first Reply affordance in DOM order, so
 * a reply whose target is a NESTED comment (a second `/c/` segment) may attach
 * to the parent. Anchor on the comment node's own id when that matters.
 */
export async function replyToComment(d: DomDeps, text: string): Promise<AdapterResult> {
  const { page, sel } = d;
  const candidates = composerCandidates(sel);
  if (candidates.length === 0) {
    return { ok: false, error: 'no comment selectors pinned' };
  }

  // openPost can land somewhere else entirely: a deleted comment resolves to the
  // plain post permalink (200, no "page isn't available" copy), and the cold-load
  // deeplink recovery bounces through the feed. `.first()` below would then
  // click the Reply button of whichever comment is at the top of the DOM — not
  // the target. Refuse rather than reply to the wrong person.
  //
  // The landing check is per threading model: IG keeps the `/c/<id>/` segment in
  // the URL, Threads has no such route (it 404s) — a Threads comment IS a post
  // whose own shortcode is the address, so the opened URL must still carry it.
  const want = commentId(d.job.targetUrl);
  if (want && !page.url().includes(`/c/${want}`)) {
    return failShot(d, `comment ${want} is no longer on the page (landed on ${page.url()})`);
  }
  if (!want) {
    const code = postShortcode(d.job.targetUrl);
    if (code && !page.url().includes(`/post/${code}`)) {
      return failShot(d, `reply target ${code} is no longer on the page (landed on ${page.url()})`);
    }
  }

  // Scroll first so a lazily-hydrated reply composer mounts (ref: jg/automation).
  await scrollToLoadComposer(page);

  // A reply threads under a comment ONLY if we open that comment's reply box.
  // On IG the permalink we opened already focuses that comment's box and
  // pre-fills the "@username" mention, so this click is belt-and-braces: the
  // mention check below is what actually decides threading, and it passes even
  // when this click never lands.
  //
  // That is why the click MUST NOT fail the job. It was made fatal once and
  // every reply died — on a fresh load the affordance does not exist for the
  // first ~3s (see replyButton's note), so the click threw and three "retries"
  // all failed while the composer was already open and correct.
  //
  // Wait (bounded) for the affordance to mount, then click it best-effort. The
  // wait is what makes the click useful; the best-effort is what keeps a miss
  // from failing a reply that would otherwise have worked.
  const replyAffordance = sel.replyButton ?? sel.commentButton;
  if (replyAffordance) {
    const target = page.locator(replyAffordance).first();
    // Poll `count()` rather than `waitFor({state:'visible'})`: presence is the
    // signal (the affordance is in the DOM before it is clickable), and count()
    // is the one locator method every fake in the test suite implements. A
    // waitFor here would couple the action path to a richer fake for no gain.
    const deadline = Date.now() + AFFORDANCE_WAIT_MS;
    for (;;) {
      const present = await target
        .count()
        .then((n) => n > 0)
        .catch(() => false);
      if (present) break;
      if (Date.now() >= deadline) break;
      await page.waitForTimeout(250);
    }
    await target.click({ timeout: 5_000 }).catch(() => undefined);
  }
  const box = await findComposer(d, candidates, COMPOSER_OPEN_TIMEOUT_MS);
  if (!box) return failShot(d, 'reply composer not found (after clicking Reply)');

  await box.click({ timeout: 3_000 }).catch(() => undefined);

  // Two threading models, keyed by the platform selector flag (no platform
  // branch in this DIP-neutral helper):
  //
  //   • MENTION model (IG, replyPrefillsMention): the reply box pre-fills an
  //     "@username" that anchors the thread. Require it (its absence proves we
  //     opened the top-level composer, not the comment's reply box), then APPEND
  //     the text so the mention is preserved (fill() would wipe it → unthreaded).
  //
  //   • PERMALINK model (Threads): the reply is threaded by the comment permalink
  //     it is posted from — the composer holds NO mention (verified live: empty
  //     box, placeholder "Reply to <user>..."). Fill directly; requiring a
  //     mention here would fail every Threads reply.
  if (sel.replyPrefillsMention) {
    // Require the "@username" mention IG pre-fills when a COMMENT's reply box
    // opens. Its presence proves we opened a threaded reply and not the
    // top-level composer — without it a submit posts a plain top-level comment.
    //
    // It must also be the RIGHT handle when we know it. Observed live: a
    // top-level composer pre-fills the POST AUTHOR's handle, so a bare
    // "starts with @" check passed on `@<postAuthor>` and would have posted a
    // reply tagging the wrong person, threaded under the wrong comment — the
    // exact failure this guard exists to catch, defeated by its own leniency.
    // expectedMention is the target comment's author, resolved by the caller.
    const mention = await waitForMention(box, MENTION_TIMEOUT_MS, d.job.expectedMention);
    if (!mention) {
      return failShot(
        d,
        'reply did not open a threaded composer (no @mention prefilled — the reply affordance is not the target comment)',
      );
    }
    // A mention that names someone OTHER than nothing is fine (we cannot know the
    // target's handle from the permalink alone), but a mention is still the proof
    // we opened a COMMENT's reply box rather than the top-level composer. Record
    // it in the result so a mis-threaded reply is diagnosable from the log
    // instead of only from the screenshot.
    await box.press('End').catch(() => undefined);
    const sep = mention.endsWith(' ') ? '' : ' ';
    const full = `${mention}${sep}${text}`;
    // Append after the mention (never fill(): that would wipe the mention).
    await box.pressSequentially(`${sep}${text}`, { timeout: 5_000 }).catch(() => box.fill(full));
    return submitReply(d, box, text, full);
  }

  // PERMALINK model: fill the reply text directly (no mention to preserve).
  await box.fill(text, { timeout: 5_000 });
  return submitReply(d, box, text, text);
}

/**
 * Submit a filled reply composer and confirm it cleared. Composer-clearing is
 * the "reply sent" signal (not feed body-text, which would false-positive on a
 * pre-existing identical comment). Shared by both threading models above.
 */
async function submitReply(
  d: DomDeps,
  box: Locator,
  text: string,
  rendered: string,
): Promise<AdapterResult> {
  const { page, sel } = d;
  await box.press('Enter').catch(() => undefined);
  let sent = await composerCleared(box, text, 3_000);
  if (!sent && sel.submitButton) {
    await page
      .locator(sel.submitButton)
      .first()
      .click({ timeout: 5_000 })
      .catch(() => undefined);
    sent = await composerCleared(box, text, VERIFY_TIMEOUT_MS);
  }
  if (!sent) return failShot(d, 'reply not submitted (composer still holds the text)');
  // A cleared composer is a NEGATIVE signal, not proof. When the session dies
  // mid-submit Meta swaps the SPA for the login route, and a detached
  // contenteditable reads '' — which composerStillHas reports as "sent". So
  // re-run the two guards before claiming success; otherwise the API records
  // Verified: true and the operator gets SUCCESS with a login-page screenshot.
  if (await needsLogin(page)) return failShot(d, 'auth: login required (session expired or login wall)');
  if (await pageUnavailable(page)) return failShot(d, TARGET_UNAVAILABLE_ERROR);
  return { ok: true, renderedText: rendered };
}

/**
 * Poll the composer until it holds an "@mention" prefill, or the bound expires.
 *
 * `expected` (when known) is the handle the mention MUST name. Without it the
 * check accepts any `@…`, which a top-level composer satisfies by pre-filling
 * the POST AUTHOR — so the reply would tag the wrong person. Comparison is
 * case-insensitive (handles are) and tolerates the trailing space IG appends.
 */
async function waitForMention(
  box: Locator,
  timeoutMs: number,
  expected?: string,
): Promise<string | undefined> {
  const want = expected?.replace(/^@/, '').trim().toLowerCase();
  const started = Date.now();
  for (;;) {
    const v = (await composerValue(box)).trim();
    if (v.startsWith('@') && v.length > 1) {
      // No expectation → any mention is the best evidence available (the
      // caller had no handle to compare against, e.g. a comment row with no
      // author recorded).
      if (!want) return v;
      const got = (v.slice(1).split(/\s/)[0] ?? '').toLowerCase();
      if (got === want) return v;
    }
    if (Date.now() - started >= timeoutMs) return undefined;
    await sleep(POLL_MS);
  }
}

/** Current composer contents (textarea value or contenteditable text). */
async function composerValue(box: Locator): Promise<string> {
  const val = await box.inputValue().catch(() => null);
  if (val !== null) return val;
  return await box.innerText().catch(() => '');
}

/** Poll until the composer no longer holds `text` (i.e. the submit landed). */
async function composerCleared(box: Locator, text: string, timeoutMs: number): Promise<boolean> {
  const started = Date.now();
  for (;;) {
    if (!(await composerStillHas(box, text))) return true;
    if (Date.now() - started >= timeoutMs) return false;
    await sleep(POLL_MS);
  }
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
 * exact rendered text appears, or the bound expires. Exported so the inline
 * check inside `comment`/`replyComment` has one definition of "landed" (DRY).
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
 * the page actually showed. A success carries one too — a verdict of "liked"
 * with no picture is unauditable, and the operator has no way to tell a real
 * like from one the page merely claimed. Never throws — the caller decides what
 * a failure means, and an orchestration bug must not masquerade as a platform
 * failure.
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
    // The permalink resolved to Meta's "page isn't available" screen: the target
    // is gone (or hidden from this account). Fail HERE rather than letting the
    // action burn its timeout and report a selector miss — the like/comment
    // flows would each poll their full bound on a page that can never satisfy
    // them, and the operator would be told to fix a selector that is fine.
    if (await pageUnavailable(page)) {
      return await failShot(d, TARGET_UNAVAILABLE_ERROR);
    }
    const result = await fn(d);
    if (!result.ok) return result;
    // Success gets the same evidence as failure. Captured HERE, on the page the
    // action just acted on, and before `finally` closes it — the action
    // functions return as soon as they have verified, so this is the state the
    // verification saw. A missing shot degrades to no screenshot rather than
    // failing a job that already succeeded.
    const deps = adapterDeps();
    const shot = await captureScreenshot(
      page,
      job.id,
      deps.workerId,
      deps.screenshotDir ? { dir: deps.screenshotDir } : {},
    );
    return shot ? { ...result, screenshot: shot } : result;
  } catch (err) {
    return await failShot(d, `action ${job.action} threw: ${(err as Error).message}`);
  } finally {
    await page.close().catch(() => undefined);
  }
}

/**
 * Credential login shared by the Meta adapters. Both surface the same login
 * form (one identifier text input, one password input — Meta re-names the
 * attributes between rollouts, so `fillMetaCredentials` resolves them by
 * candidate selectors), and a platform supplies only its login URL. Verdict
 * comes from `pollLoginOutcome` — the cookie standard, never the URL (DRY with
 * the operator headful flow in core/auth).
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
    await fillMetaCredentials(page, credentials);
    // The page must stay open while the poll runs: closing it would abort the
    // submit navigation. Cookies live on the context, so the verdict reads the
    // context, and the page is dropped only once an outcome is reached.
    return await pollLoginOutcome(platform, ctx);
  } finally {
    await page.close().catch(() => undefined);
  }
}

/**
 * The Meta login form's identifier field, tried in order. Meta re-names the
 * attributes between rollouts (`username` -> `email`), so the first selector
 * that resolves wins; `input[type="text"]` is the type-level fallback, the one
 * attribute the form's single identifier field always has. An empty list
 * means the login form is not on the page at all (a checkpoint/consent wall).
 */
const USERNAME_FIELD_SELECTORS = [
  'input[name="username"]',
  'input[name="email"]',
  'input[type="text"]',
];
const PASSWORD_FIELD_SELECTORS = [
  'input[name="password"]',
  'input[name="pass"]',
  'input[type="password"]',
];

/**
 * Bound for the login form to mount. Threads serves `/login` as a client-
 * rendered SPA: at `domcontentloaded` the page is the "Scan to get the app"
 * shell and the fields appear only after hydration (~3s observed live). The
 * old single-pass check ran in that window and reported
 * `login form field not found` on a form that was about to render.
 */
const LOGIN_FORM_WAIT_MS = 20_000;
const LOGIN_FORM_POLL_MS = 500;

async function firstPresent(page: Page, selectors: string[]): Promise<Locator> {
  const deadline = Date.now() + LOGIN_FORM_WAIT_MS;
  for (;;) {
    for (const sel of selectors) {
      const loc = page.locator(sel);
      if ((await loc.count()) > 0) return loc.first();
    }
    if (Date.now() >= deadline) {
      throw new Error(`login form field not found (tried: ${selectors.join(', ')})`);
    }
    await sleep(LOGIN_FORM_POLL_MS);
  }
}

/**
 * Fill and submit the Meta credential form (IG + Threads share the markup).
 * Used by the adapters' autonomous login and by the operator headful flow's
 * autofill — one definition of the form (DRY). Pressing Enter in the password
 * field submits; the verdict always comes from the cookie poll, never from the
 * URL landing.
 */
export async function fillMetaCredentials(
  page: Page,
  credentials: LoginCredentials,
): Promise<void> {
  const username = await firstPresent(page, USERNAME_FIELD_SELECTORS);
  await username.fill(credentials.username);
  const password = await firstPresent(page, PASSWORD_FIELD_SELECTORS);
  await password.fill(credentials.password);
  await password.press('Enter');
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
  const { sel } = d;
  if (!sel.likeButton) return undefined;
  const root = await likeRoot(d);
  const btn = root.locator(sel.likeButton).first();
  if ((await btn.count()) === 0) return undefined;

  const pressed = await btn.getAttribute('aria-pressed').catch(() => null);
  const label = await btn.getAttribute('aria-label').catch(() => null);
  let active = 0;
  if (sel.likeButtonActive) {
    // Count WITHIN the scope: a liked COMMENT would otherwise flip `active` and
    // read as a liked post (false idempotent success without ever liking it).
    active = await root
      .locator(sel.likeButtonActive)
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
