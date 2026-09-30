/**
 * P3-04 / P3-05 / P3-08 tests. Two layers, on purpose:
 *
 *   1. DOM primitives (`likePost` / `commentOnPost` / `waitForCommentText`) are
 *      driven directly with a fake page and an injected clock, because every
 *      timeout-sensitive failure path lives there. The clock advances past any
 *      bound on its second read, so a "never lands" case is one poll, not 15s.
 *   2. The adapters are driven through their public interface with a fake
 *      context, which proves only the `runAction` wiring: page lifecycle, the
 *      AUTH-class login wall, and the empty-text guard. Only instant paths are
 *      exercised here — the slow ones are already covered in layer 1.
 *
 * No test sets a screenshot dir: `adapterDeps()` stays at its dir-less default,
 * so `captureScreenshot` returns the basename and never opens a CDP session
 * (§7.3). The basename is still asserted on failures, because the operator's
 * only evidence of a failure is that file.
 */
import assert from 'node:assert/strict';
import { test } from 'node:test';

import type { BrowserContext, Locator, Page } from 'playwright';

import type { ActionJob } from '../types.js';
import { instagramAdapter } from '../platforms/instagram.js';
import { threadsAdapter } from '../platforms/threads.js';
import {
  commentOnPost,
  likePost,
  replyToComment,
  runAction,
  waitForCommentText,
  type DomDeps,
} from '../platforms/dom.js';
import { selectorsFor, type SelectorSet } from '../sel/index.js';

/** A clock that jumps past any poll bound on its second read. */
function fastClock(): { now: () => number } {
  let t = 0;
  return { now: () => (t += 60_000) };
}

interface Story {
  /** URL after the last navigation. */
  url: string;
  /** The like button's aria-pressed / aria-label pair. */
  likePressed: string | null;
  likeLabel: string | null;
  /** When true, clicking the like button does not flip its state. */
  likeStuck: boolean;
  /** Text the feed currently renders. */
  feedText: string;
  /** Rendered `body` innerText (pageUnavailable reads this). */
  bodyText: string;
  /** A 2FA/checkpoint field is present (needsLogin). */
  verificationField: boolean;
  /** When true, the target URL redirects to a login wall. */
  loginRedirect: boolean;
  /** Ctrl+Enter in the composer lands the comment. */
  pressLands: boolean;
  /** Clicking the Post button lands the comment. */
  submitLands: boolean;
  /** Whether the composer is mounted; false until the comment affordance is clicked. */
  composerOpen: boolean;
  /** Text currently in the composer. Clears on a landed submit, like the real box. */
  composed: string;
  /** Count of Reply-affordance clicks. H-2 asserts the guard blocks the click. */
  replyClicks: number;
  /** Capture the element the last call touched (`fill`/`pressSequentially`/`press`). */
  lastTouched: string;
  /**
   * Story flips to the login route at reply-submit time, AFTER `openPost`'s
   * pre-action guard already passed. Models the session dying mid-flow — the H-3
   * case where the composer clears but the post never landed.
   */
  loginWallOnSubmit: boolean;
  /** As above, for the "page isn't available" screen (H-3, second guard). */
  unavailableOnSubmit: boolean;
  /** Fails every candidate composer lookup (the "reply composer not found" path). */
  composerMissing: boolean;
  /** The reply box pre-fills this "@mention"; its presence proves a threaded box. */
  mentionPrefill: string;
  /** A mention is required for the composer to count as the threaded reply box. */
  requireMention: boolean;
  /** `pressSequentially` types into the box. False models a rollout that rejects it. */
  sequentialTypes: boolean;
  /** A submit has been attempted (flips the URL/body for the H-3 races). */
  submitStarted: boolean;
}

function makeStory(over: Partial<Story> = {}): Story {
  return {
    url: 'https://www.instagram.com/p/abc123/',
    likePressed: 'false',
    likeLabel: 'Like',
    likeStuck: false,
    feedText: '',
    // A normal post renders its caption, never the "page isn't available" copy.
    bodyText: 'a caption\n3 likes',
    verificationField: false,
    loginRedirect: false,
    pressLands: true,
    submitLands: true,
    composerOpen: true,
    composed: '',
    replyClicks: 0,
    lastTouched: '',
    loginWallOnSubmit: false,
    unavailableOnSubmit: false,
    composerMissing: false,
    mentionPrefill: '',
    requireMention: false,
    sequentialTypes: true,
    submitStarted: false,
    ...over,
  };
}

/** 1 when the pinned active selector would currently match (button pressed). */
function activeCount(story: Story): number {
  return story.likePressed === 'true' || /unlike/i.test(String(story.likeLabel)) ? 1 : 0;
}

/**
 * A scripted page. Selectors are compared against the platform's pinned set, so
 * the fake follows the real selector contract rather than a hand-picked string:
 * if `sel/index.ts` repoints a selector, these tests still address the element
 * the adapter will actually touch.
 */
function fakePage(story: Story, sel: SelectorSet): Page {
  const likeButton = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      click: async () => {
        if (story.likeStuck) return;
        story.likePressed = 'true';
        story.likeLabel = 'Unlike';
      },
      getAttribute: async (name: string) =>
        name === 'aria-pressed'
          ? story.likePressed
          : name === 'aria-label'
            ? story.likeLabel
            : null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => story.feedText,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  const composer = (): Locator => {
    // A landed submit does TWO observable things on the real page: the composer
    // CLEARS, and the text renders in the FEED. The comment path verifies via the
    // feed (`waitForCommentText`); the reply path verifies via the clear
    // (`composerCleared`). The fake must produce both — clearing alone silently
    // broke every comment test, because a fake that empties the box without
    // rendering stops modelling a real submit.
    const landed = (): void => {
      story.feedText = story.composed;
      story.composed = '';
    };
    const loc: Locator = {
      count: async () => (story.composerOpen ? 1 : 0),
      click: async () => {
        story.lastTouched = 'composer';
      },
      getAttribute: async () => null,
      // The IG/Threads composer is a contenteditable, not an <input>: real
      // Playwright `inputValue()` REJECTS on it, and `composerStillHas` relies on
      // that to fall through to `innerText`. Modelling it as a throw keeps the
      // fake honest to the contenteditable contract the flow is written against.
      inputValue: async () => {
        throw new Error('Node is not an <input>, <textarea> or <select> element');
      },
      fill: async (text: string) => {
        story.lastTouched = 'fill';
        story.composed = text;
      },
      // The reply's MENTION model types with pressSequentially (fill() would
      // wipe the prefilled "@mention"). Appends, like a real box. Without this
      // method the reply path could not be driven at all.
      pressSequentially: async (text: string) => {
        story.lastTouched = 'pressSequentially';
        if (story.sequentialTypes) story.composed += text;
      },
      press: async (key: string) => {
        story.lastTouched = `press:${key}`;
        // 'End' only moves the caret; a submit key accepts the composer and
        // clears it. A rejected submit key leaves the text in place, which
        // composerStillHas then reads — exactly the real guard's contract.
        if (key === 'End') return;
        story.submitStarted = true;
        if (story.pressLands) landed();
      },
      innerText: async () => story.composed || story.mentionPrefill,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  // Clicking the comment affordance reveals/focuses the composer.
  const commentButton = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      click: async () => {
        story.composerOpen = true;
        // Threads has no separate replyButton, so its reply path falls back to
        // THIS affordance (`sel.replyButton ?? sel.commentButton`); counting the
        // click here is what lets the Threads reply test observe it.
        story.replyClicks += 1;
      },
      getAttribute: async () => null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => story.feedText,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  // The per-comment Reply affordance. Its click is what H-2's guard is meant to
  // block, so it counts every click — a test can then assert the guard held.
  const replyButton = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      click: async () => {
        story.replyClicks += 1;
      },
      getAttribute: async () => null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => story.feedText,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  const submitButton = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      click: async () => {
        story.lastTouched = 'submitButton';
        // Same two effects as the keyboard path: render in the feed and clear
        // the box. submitReply reads the clear; the comment path reads the feed.
        if (story.submitLands) {
          story.feedText = story.composed;
          story.composed = '';
        }
      },
      getAttribute: async () => null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => story.feedText,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  const plain = (count = 1): Locator => {
    const loc: Locator = {
      count: async () => count,
      click: async () => undefined,
      getAttribute: async () => null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => story.feedText,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  // The document body. Its rendered text is the page chrome plus whatever the
  // feed currently shows — the same relationship the real DOM has, so
  // `pageUnavailable` (reads body) and `waitForCommentText` (also reads body)
  // see one consistent surface instead of two divergent fakes.
  const body = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      click: async () => undefined,
      getAttribute: async () => null,
      fill: async () => undefined,
      press: async () => undefined,
      innerText: async () => {
        // unavailableOnSubmit flips the body to Meta's "isn't available" screen
        // at reply-submit time — pageUnavailable reads exactly this.
        if (story.unavailableOnSubmit && story.submitStarted) {
          return `Sorry, this page isn't available.\n${story.bodyText}`;
        }
        return `${story.bodyText}\n${story.feedText}`;
      },
      first: () => loc,
      last: () => loc,
      filter: () => loc,
    } as unknown as Locator;
    return loc;
  };

  const resolveLocator = (selector: string): Locator => {
    if (selector === 'body') return body();
    if (selector === sel.likeButton) return likeButton();
    if (selector === sel.likeButtonActive) return plain(activeCount(story));
    if (selector === sel.replyButton) return replyButton();
    if (selector === sel.commentButton) return commentButton();
    // The composer is a contenteditable on the current Meta rollout; the
    // textarea candidates are absent, so findComposer walks past them (this
    // is exactly the ordered-fallback behaviour the P-A fix adds).
    if (selector.includes('contenteditable')) return composer();
    if (selector.includes('textarea')) return plain(0);
    if (selector === sel.submitButton) return submitButton();
    if (selector === sel.feed) return plain(1);
    if (selector.includes('verificationCode')) return plain(story.verificationField ? 1 : 0);
    return plain(1);
  };

  // A story that fails every candidate composer lookup. `findComposer` skips a
  // zero-count candidate immediately and burns its whole bound on the rest, so
  // this is reached with the fastClock (one poll) rather than a real 8s wait.
  const noComposer = (selector: string): Locator => {
    if (selector === 'body') return body();
    if (selector === sel.replyButton) return replyButton();
    if (selector === sel.commentButton) return commentButton();
    return plain(0);
  };
  const locate = (selector: string): Locator =>
    story.composerMissing ? noComposer(selector) : resolveLocator(selector);

  // The like-button scope (IG action bar). likeRoot takes `.last()` of it and
  // resolves the like selectors within — so the scope delegates `.locator()`
  // straight back to the resolver, making scoping transparent to these tests
  // (the fake has no comment hearts; the scoped active-count path is exercised).
  const scope = (): Locator => {
    const loc: Locator = {
      count: async () => 1,
      first: () => loc,
      last: () => loc,
      filter: () => loc,
      locator: (s: string) => locate(s),
    } as unknown as Locator;
    return loc;
  };

  const page = {
    goto: async (url: string) => {
      // A dead session redirects the permalink to a login wall before any
      // content renders — the URL after navigation is what needsLogin sees.
      story.url = story.loginRedirect ? 'https://www.instagram.com/accounts/login/' : url;
    },
    url: () => {
      // loginWallOnSubmit models the session dying MID-SUBMIT. It must not flip
      // before the Reply click, or H-2's URL guard fires first and the test
      // would exercise the wrong guard. page.goto already ran with the good URL.
      if (story.loginWallOnSubmit && story.submitStarted) {
        return 'https://www.instagram.com/accounts/login/';
      }
      return story.url;
    },
    // In-page scripts (scrollToLoadComposer's scroll-to-bottom, deep-link
    // recovery probes) run against a real DOM in the browser; the fake has none,
    // so evaluate is a no-op. The flow treats it as best-effort (`.catch`), so a
    // no-op faithfully models "nothing to scroll" rather than masking a failure.
    evaluate: async () => undefined,
    waitForTimeout: async () => undefined,
    locator: (selector: string): Locator => {
      if (story.requireMention && selector.includes('contenteditable')) {
        // The MENTION model mounts the threaded reply box only after the Reply
        // affordance was clicked. Modelled as an order-dependent mount, so a
        // refactor that drops the Reply click loses the composer AND the
        // mention and fails the test — it cannot pass by accident.
        return story.replyClicks > 0 ? composer() : plain(0);
      }
      if (sel.likeButtonScope && selector === sel.likeButtonScope) return scope();
      return locate(selector);
    },
    close: async () => undefined,
  } as unknown as Page;

  return page;
}

function fakeContext(story: Story, sel: SelectorSet): BrowserContext {
  const page = fakePage(story, sel);
  return { newPage: async () => page } as unknown as BrowserContext;
}

const IG = selectorsFor('instagram');
const TH = selectorsFor('threads');

function deps(story: Story, job: ActionJob, sel: SelectorSet): DomDeps {
  return { page: fakePage(story, sel), sel, job, ...fastClock() };
}

function job(over: Partial<ActionJob> = {}): ActionJob {
  return {
    id: 'job-1',
    accountId: 'acct-1',
    platform: 'instagram',
    action: 'comment',
    targetUrl: 'https://www.instagram.com/p/abc123/',
    text: 'great post',
    attempt: 1,
    ...over,
  };
}

// --- like (P3-04) --------------------------------------------------------

test('like: state flips to Unlike ⇒ ok', async () => {
  const story = makeStory();
  const result = await likePost(deps(story, job(), IG));
  assert.equal(result.ok, true);
  assert.equal(story.likeLabel, 'Unlike', 'the button was actually clicked');
});

test('like: already liked is idempotent success (no click)', async () => {
  const story = makeStory({ likePressed: 'true', likeLabel: 'Unlike' });
  const result = await likePost(deps(story, job(), IG));
  assert.equal(result.ok, true);
  assert.equal(story.likeLabel, 'Unlike', 'an already-liked post is not re-clicked');
});

test('like: state never changes ⇒ ok:false + screenshot', async () => {
  const story = makeStory({ likeStuck: true });
  const result = await likePost(deps(story, job(), IG));
  assert.equal(result.ok, false);
  assert.match(
    result.error ?? '',
    /did not change/,
    'a like that never verifies is a failure, never optimism (§7.4)',
  );
  assert.ok(result.screenshot, 'a failure carries a screenshot basename');
});

test('like: no pinned selector ⇒ ok:false before any DOM touch', async () => {
  // An empty selector set models a platform whose markup is not pinned yet.
  const result = await likePost(deps(makeStory(), job(), {}));
  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /no like selector/);
});

// --- comment (P3-05) -----------------------------------------------------

test('comment: text lands in the feed ⇒ ok + renderedText', async () => {
  const story = makeStory();
  const result = await commentOnPost(deps(story, job(), IG), 'great post');
  assert.equal(result.ok, true);
  assert.equal(result.renderedText, 'great post');
  // The composer is EMPTY now and the text is in the feed — the two observable
  // effects of a real landed submit. Asserting the composer still held the text
  // was an artefact of the old fake never clearing; it proved the fill, not the
  // submit, and would have passed on a submit that never happened.
  assert.equal(story.composed, '', 'the composer cleared on the landed submit');
  assert.equal(story.feedText, 'great post', 'the text rendered in the feed');
});

test('comment: text never lands ⇒ ok:false + screenshot', async () => {
  const story = makeStory({ pressLands: false, submitLands: false });
  const result = await commentOnPost(deps(story, job(), IG), 'great post');
  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /not visible in feed/);
  assert.ok(result.screenshot);
});

test('comment: submit-button fallback lands the text when Ctrl+Enter did not', async () => {
  // Ctrl+Enter fails to submit, but the Post button succeeds: the fallback is
  // what recovers the action, and it must not double-post a working keyboard
  // submit (the "text lands" case above never clicks Post, proving the guard).
  const story = makeStory({ pressLands: false, submitLands: true });
  const result = await commentOnPost(deps(story, job(), IG), 'great post');
  assert.equal(result.ok, true, 'the fallback click landed the comment');
});

test('comment: composer hidden until the affordance is clicked ⇒ ok', async () => {
  // Models the rollout where the composer is not mounted until the comment
  // icon is clicked — the P-A failure mode. The flow must open it, then fill.
  const story = makeStory({ composerOpen: false });
  const result = await commentOnPost(deps(story, job(), IG), 'great post');
  assert.equal(result.ok, true, 'the affordance was clicked and the composer filled');
  assert.equal(story.composerOpen, true, 'the comment affordance was clicked to reveal the composer');
  assert.equal(story.feedText, 'great post', 'the text landed in the feed after the submit');
});

test('comment: composer never appears ⇒ ok:false + screenshot', async () => {
  // Even after clicking the affordance the composer never mounts (a truly stale
  // page). That is a clear failure with evidence, never a 30s hang.
  const story = makeStory({ composerOpen: false });
  const page = fakePage(story, IG);
  // Neuter the affordance so the composer stays hidden.
  story.composerOpen = false;
  const badButton = {
    count: async () => 1,
    click: async () => undefined,
    getAttribute: async () => null,
    fill: async () => undefined,
    press: async () => undefined,
    innerText: async () => '',
    first() {
      return this;
    },
  } as unknown as Locator;
  const origLocator = page.locator.bind(page);
  page.locator = ((selector: string) =>
    selector === IG.commentButton ? badButton : origLocator(selector)) as Page['locator'];
  const result = await commentOnPost({ page, sel: IG, job: job(), ...fastClock() }, 'great post');
  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /composer not found/);
  assert.ok(result.screenshot, 'a failure carries a screenshot basename');
});

// --- waitForCommentText (P3-08 ground truth) ----------------------------

test('waitForCommentText: returns the text once visible', async () => {
  const story = makeStory({ feedText: 'already there' });
  const result = await waitForCommentText(deps(story, job(), IG), 'already there', 5_000);
  assert.equal(result, 'already there');
});

test('waitForCommentText: times out when the text never appears', async () => {
  const story = makeStory({ feedText: 'something else' });
  const result = await waitForCommentText(deps(story, job(), IG), 'never appears');
  assert.equal(result, undefined);
});

// --- adapter wiring (P3-04 / P3-05 through the public interface) --------

test('adapter comment: success through runAction (page opened, then closed)', async () => {
  const story = makeStory();
  let closed = false;
  const page = fakePage(story, IG);
  const ctx = {
    newPage: async () => ({ ...page, close: async () => void (closed = true) }),
  } as unknown as BrowserContext;

  const result = await instagramAdapter.comment(ctx, job());

  assert.equal(result.ok, true);
  assert.equal(result.renderedText, 'great post');
  assert.equal(story.url, job().targetUrl, 'the post permalink was opened');
  assert.equal(closed, true, 'the page is closed in finally');
});

test('adapter comment: a login wall is an AUTH failure, never a retryable one', async () => {
  const story = makeStory({ loginRedirect: true });
  const ctx = fakeContext(story, IG);

  const result = await instagramAdapter.comment(ctx, job());

  assert.equal(result.ok, false);
  // The BE classifier keys "login required" + "session expired" to AUTH
  // (P3-12), which is not retryable: re-running a dead session cannot help.
  assert.match(result.error ?? '', /login required/);
  assert.ok(result.screenshot);
});

test('adapter comment: empty text fails before any page is opened', async () => {
  const story = makeStory();
  const ctx = fakeContext(story, IG);

  // An empty text models a job published without a rendered template: the
  // guard must reject it before a browser page is ever opened.
  const result = await instagramAdapter.comment(ctx, { ...job(), text: '' });

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /empty comment text/);
  assert.equal(story.url, makeStory().url, 'no navigation happened');
});

test('adapter like: success through the public interface', async () => {
  const story = makeStory();
  const ctx = fakeContext(story, IG);
  const result = await instagramAdapter.like(ctx, job({ action: 'like' }));
  assert.equal(result.ok, true);
  assert.equal(story.likeLabel, 'Unlike');
});

test('threads adapter shares the same wiring (OCP: one registry entry per platform)', async () => {
  const story = makeStory({ url: 'https://www.threads.net/@user/post/xyz/' });
  const ctx = fakeContext(story, TH);
  const result = await threadsAdapter.comment(ctx, job({ platform: 'threads' }));
  assert.equal(result.ok, true);
  assert.equal(result.renderedText, 'great post');
});

test('runAction: an exception becomes a screenshot-bearing failure, never a throw', async () => {
  const story = makeStory();
  const page = fakePage(story, IG);
  // goto throwing models a navigation failure (TRANSIENT by the BE classifier).
  const ctx = {
    newPage: async () => ({ ...page, goto: async () => Promise.reject(new Error('net timeout')) }),
  } as unknown as BrowserContext;

  const result = await runAction(ctx, 'instagram', job(), async (d) => commentOnPost(d, 'x'));

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /net timeout/, 'the original message is preserved');
  assert.ok(result.screenshot);
});

test('runAction: a "page isn\'t available" permalink fails fast, naming the target not a selector', async () => {
  // The blocked/deleted-post screen: a 200 whose body carries Meta's copy. No
  // login route, no login field — only the rendered text distinguishes it, so
  // this is the one gate that catches it before the action burns its timeout.
  const story = makeStory({
    bodyText: "Sorry, this page isn't available.\nThe link you followed may be broken.",
  });
  const ctx = fakeContext(story, IG);
  let actionRan = false;
  const result = await runAction(ctx, 'instagram', job({ action: 'like' }), async () => {
    actionRan = true;
    return { ok: true };
  });

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /target unavailable/, 'names the target, not a selector');
  assert.equal(actionRan, false, 'the action never runs against a dead target');
  assert.ok(result.screenshot);
  // The BE classifier has no needle for this class on purpose: a deleted/private
  // post is not retryable, and the message must not be mistaken for AUTH.
  assert.doesNotMatch(result.error ?? '', /login|session/i);
});

test('runAction: the typographic apostrophe in Meta copy still matches (both spellings)', async () => {
  const story = makeStory({ bodyText: 'Sorry, this page isn’t available.' });
  const result = await runAction(fakeContext(story, IG), 'instagram', job({ action: 'like' }), async () => ({
    ok: true,
  }));
  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /target unavailable/);
});

test('runAction: a post whose body merely contains similar words is NOT gated', async () => {
  // A caption quoting the copy must not fail the job — only the screen does.
  // "page not found" is the second needle, so the guard is a real substring
  // test on rendered text, not a loose guess at the page's purpose.
  const story = makeStory({ bodyText: 'caption: "this page is not available for sponsorship"' });
  const ctx = fakeContext(story, IG);
  const result = await runAction(ctx, 'instagram', job({ action: 'like' }), async () => ({ ok: true }));
  assert.equal(result.ok, true, 'a normal post still runs the action');
});

// --- reply: replyToComment + submitReply (L-6) ---------------------------
//
// These drive `replyToComment` directly rather than through `runAction`. That
// is deliberate, not a shortcut: `runAction` runs needsLogin/pageUnavailable
// BEFORE the action, so routing these through it would swallow the H-3 case —
// the page dying *during* submit — and the test would pass on the pre-action
// guard alone, never reaching the guard under test. The `reply_comment`
// dispatch through the public interface is covered separately below.

/** A reply job whose targetUrl carries the `/c/<id>/` comment permalink. */
function replyJob(over: Partial<ActionJob> = {}): ActionJob {
  return job({
    action: 'reply_comment',
    targetUrl: 'https://www.instagram.com/p/abc123/c/1789/',
    text: 'nice one',
    ...over,
  });
}

test('replyToComment: refuses to click Reply when the URL no longer carries /c/<id> (H-2)', async () => {
  // The comment vanished (deleted, or the permalink degraded to the post), so
  // the page is on the bare post URL. Clicking `.first()` Reply here would
  // answer whichever comment sits at the top of the DOM — a different person.
  const story = makeStory({ url: 'https://www.instagram.com/p/abc123/' });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /no longer on the page/, 'names the comment it could not find');
  assert.match(result.error ?? '', /1789/, 'and the id it was looking for');
  assert.equal(story.replyClicks, 0, 'the Reply affordance was NEVER clicked');
  assert.equal(story.composed, '', 'nothing was typed');
});

test('replyToComment: clicks Reply when the URL does carry /c/<id> (the guard is not a blanket refusal)', async () => {
  // The positive case. Without it, a guard that refused EVERYTHING would still
  // pass the test above — and a permanently broken reply path looks green.
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    mentionPrefill: '@someone',
    requireMention: true,
  });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(story.replyClicks, 1, 'the target comment Reply affordance was clicked');
  assert.equal(story.composed, '', 'the submit cleared the composer');
  assert.equal(result.ok, true, 'the reply path completes against the right comment');
});

test('replyToComment: submitReply fails when the session dies at submit time (H-3)', async () => {
  // The URL is correct when the reply STARTS, so the H-2 guard passes and the
  // Reply affordance is clicked. The session then dies mid-submit: the page
  // flips to the login route and the composer clears — which, before H-3, was
  // the ONLY success signal, so this reported ok:true on a reply that never
  // posted. This asserts the login re-check now catches it.
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    mentionPrefill: '@someone',
    requireMention: true,
    loginWallOnSubmit: true,
  });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(result.ok, false, 'a cleared composer on a login wall is NOT success');
  assert.match(result.error ?? '', /login required/, 'reports the real cause (auth), not a submit failure');
  assert.ok(result.screenshot, 'a failure carries a screenshot of the login page');
  assert.equal(story.replyClicks, 1, 'the reply did start before the session died');
});

test('replyToComment: submitReply fails when the target goes unavailable at submit time (H-3)', async () => {
  // Same shape as above via the second H-3 guard: the composer clears but the
  // page is Meta's "isn't available" screen, so the text was never posted.
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    mentionPrefill: '@someone',
    requireMention: true,
    unavailableOnSubmit: true,
  });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(result.ok, false, 'a cleared composer on a dead page is NOT success');
  assert.match(result.error ?? '', /target unavailable/, 'names the target, not the composer');
  assert.ok(result.screenshot);
});

test('replyToComment: a reply that never clears the composer is a failure, not a success', async () => {
  // The inverse of H-3: guards are healthy, but the submit simply did not land.
  // A fake whose composer kept the text would let this pass — this proves the
  // "cleared → sent" signal is what submitReply actually reads.
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    mentionPrefill: '@someone',
    requireMention: true,
    pressLands: false,
    submitLands: false,
  });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /reply not submitted/, 'reports the composer never cleared');
});

test('replyToComment: the reply composer never mounting is a failure, never a phantom success', async () => {
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    composerMissing: true,
  });
  const result = await replyToComment(deps(story, replyJob(), IG), 'nice one');

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /reply composer not found/);
  assert.equal(story.composed, '', 'nothing was typed');
});

test('adapter replyComment dispatches to the reply path through the public interface (L-6)', async () => {
  // The dispatch half: `action: 'reply_comment'` must reach replyComment, which
  // is what `dispatch` in controller.ts switches on. Before this, NO test
  // exercised the reply action end to end, so a broken wiring was invisible.
  const story = makeStory({
    url: 'https://www.instagram.com/p/abc123/c/1789/',
    mentionPrefill: '@someone',
    requireMention: true,
  });
  const result = await instagramAdapter.replyComment(fakeContext(story, IG), replyJob());

  assert.equal(result.ok, true, 'the reply posted through the public adapter');
  assert.equal(story.replyClicks, 1, 'the target comment was the one replied to');
});

test('adapter replyComment: a deleted comment that lands on the post fails without clicking (H-2)', async () => {
  // The refusal must survive the whole stack. Via openPost the browser REQUESTED
  // the comment permalink, but IG served the bare post (deleted comment → 200 on
  // the post URL, no "isn't available" copy). The guard reads page.url() after
  // navigation, sees no /c/<id>, and refuses — so `.first()` Reply never fires.
  const story = makeStory({ url: 'https://www.instagram.com/p/abc123/' });
  const landedOnPost = {
    ...story,
  };
  const page = fakePage(landedOnPost, IG);
  // openPost would normally set the URL to what it requested; here the page
  // stays on the post permalink, which is exactly the deleted-comment behaviour.
  page.goto = (async () => null) as unknown as typeof page.goto;
  const ctx = { newPage: async () => page } as unknown as BrowserContext;

  const result = await instagramAdapter.replyComment(ctx, replyJob());

  assert.equal(result.ok, false);
  assert.match(result.error ?? '', /no longer on the page/, 'names the missing comment');
  assert.equal(landedOnPost.replyClicks, 0, 'no Reply affordance was clicked');
});

test('threads replyComment: takes the PERMALINK model, no mention required (OCP)', async () => {
  // Threads threads by the comment permalink and ships NO mention prefill, so
  // the mention requirement must be IG-only. A shared helper that demanded a
  // mention on both platforms would fail every Threads reply.
  const story = makeStory({ url: 'https://www.threads.net/@user/post/xyz/c/42/' });
  const result = await threadsAdapter.replyComment(
    fakeContext(story, TH),
    replyJob({ platform: 'threads', targetUrl: 'https://www.threads.net/@user/post/xyz/c/42/' }),
  );

  assert.equal(result.ok, true, 'a Threads reply needs no @mention');
  assert.equal(story.replyClicks, 1);
});
