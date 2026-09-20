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
  composed: string;
}

function makeStory(over: Partial<Story> = {}): Story {
  return {
    url: 'https://www.instagram.com/p/abc123/',
    likePressed: 'false',
    likeLabel: 'Like',
    likeStuck: false,
    feedText: '',
    verificationField: false,
    loginRedirect: false,
    pressLands: true,
    submitLands: true,
    composerOpen: true,
    composed: '',
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
    const loc: Locator = {
      count: async () => (story.composerOpen ? 1 : 0),
      click: async () => undefined,
      getAttribute: async () => null,
      // The IG/Threads composer is a contenteditable, not an <input>: real
      // Playwright `inputValue()` REJECTS on it, and `composerStillHas` relies on
      // that to fall through to `innerText`. Modelling it as a throw keeps the
      // fake honest to the contenteditable contract the flow is written against.
      inputValue: async () => {
        throw new Error('Node is not an <input>, <textarea> or <select> element');
      },
      fill: async (text: string) => {
        story.composed = text;
      },
      press: async () => {
        if (story.pressLands) story.feedText = story.composed;
      },
      innerText: async () => story.composed,
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
        if (story.submitLands) story.feedText = story.composed;
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

  const resolveLocator = (selector: string): Locator => {
    if (selector === sel.likeButton) return likeButton();
    if (selector === sel.likeButtonActive) return plain(activeCount(story));
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
      locator: (s: string) => resolveLocator(s),
    } as unknown as Locator;
    return loc;
  };

  const page = {
    goto: async (url: string) => {
      // A dead session redirects the permalink to a login wall before any
      // content renders — the URL after navigation is what needsLogin sees.
      story.url = story.loginRedirect ? 'https://www.instagram.com/accounts/login/' : url;
    },
    url: () => story.url,
    // In-page scripts (scrollToLoadComposer's scroll-to-bottom, deep-link
    // recovery probes) run against a real DOM in the browser; the fake has none,
    // so evaluate is a no-op. The flow treats it as best-effort (`.catch`), so a
    // no-op faithfully models "nothing to scroll" rather than masking a failure.
    evaluate: async () => undefined,
    waitForTimeout: async () => undefined,
    locator: (selector: string): Locator => {
      if (sel.likeButtonScope && selector === sel.likeButtonScope) return scope();
      return resolveLocator(selector);
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
  assert.equal(story.composed, 'great post', 'the composer was filled');
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
  assert.equal(story.composed, 'great post');
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
