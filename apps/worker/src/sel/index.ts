/**
 * Centrally-pinned DOM selectors, one object per platform. When a platform
 * changes its markup this is the single file to patch (DEVELOPMENT_RULE §7.3).
 *
 * Selectors are intentionally permissive (role/aria/text based): Meta A/B tests
 * class names, so a structural selector survives a rollout where a class one
 * would silently break. Every entry is a list, tried in order, so a primary
 * selector can carry a fallback without touching the adapter.
 */
import type { Platform } from '@smm/shared';

export interface SelectorSet {
  /** The `[role=feed]` (or equivalent) that owns rendered comments. */
  feed?: string;
  /** A rendered comment article inside the feed. */
  commentItem?: string;
  /** The like button on a post. */
  likeButton?: string;
  /** The like button in its pressed/active state. */
  likeButtonActive?: string;
  /**
   * Container that scopes the like selectors to the POST's action bar. On IG a
   * like heart also renders on every comment, so an unscoped `likeButton`
   * `.first()` targets a comment. The post heart lives in the action bar — the
   * innermost element that holds BOTH the like heart AND the Comment affordance
   * — so `likePost`/`likeState` resolve the like button and its active marker
   * inside `.last()` of this selector (the innermost, i.e. the bar itself).
   * Absent → the whole page (Threads pre-scopes its like selectors to the
   * target post, and a post with no comments has a single like heart anyway).
   */
  likeButtonScope?: string;
  /** The button that opens the composer (the "reply" bubble under a post). */
  commentButton?: string;
  /**
   * The per-comment "Reply" affordance, for replying to a specific comment on a
   * comment permalink (`/p/<post>/c/<id>/`). Absent → replyToComment falls back
   * to `commentButton`.
   */
  replyButton?: string;
  /**
   * True when opening a comment's reply box PRE-FILLS an "@username" mention that
   * anchors the reply to that comment (Instagram). The reply flow then requires
   * and preserves that mention (append, never fill). False/absent when the
   * platform threads a reply by the comment PERMALINK it is posted from, with no
   * mention in the body (Threads) — the flow fills the composer directly.
   */
  replyPrefillsMention?: boolean;
  /** The composer text box a comment is typed into. */
  composerInput?: string;
  /**
   * Composer candidates tried IN ORDER (§7.3): Meta ships the comment box as a
   * `textarea` on some rollouts and a contenteditable on others, so the flow
   * walks this list and fills the first one that is present rather than pinning
   * a single shape that breaks on the next A/B bucket.
   */
  composerInputs?: string[];
  /** The submit button for the composer. */
  submitButton?: string;
}

/**
 * The target-post scope on Threads. A permalink view keeps the entire home feed
 * mounted behind it, so an unscoped action selector matches dozens of feed rows.
 * The post under the permalink is the single pressable container that also holds
 * the reply composer (the feed rows have none), so this anchors every Threads
 * action selector to exactly that post. (Verified live: count 1.)
 */
const THREADS_TARGET =
  'div[data-pressable-container="true"]:has(div[contenteditable="true"][role="textbox"])';

const SELECTORS: Partial<Record<Platform, SelectorSet>> = {
  instagram: {
    // The main column is the feed landmark on a post permalink.
    feed: 'main [role=feed], main section',
    // A rendered comment is an <article> sibling of the post, or a list item.
    commentItem: 'article, li[role=menuitem], div[role=button] > span',
    // MUST match BOTH Like+Unlike (state flips on like) so likeState can read
    // the flip. The old `section button svg` fallback was a trap: Playwright
    // returns comma-separated matches in DOM ORDER, not selector order, and the
    // comments section's `<svg aria-label="Load more comments">` sits in a
    // `section button` that PRECEDES the action-bar like svg — so `.first()`
    // resolved to "Load more comments" and every state read + click hit the
    // wrong element (click then timed out, a comment div intercepting the
    // pointer). Verified live: the aria-labelled svg is always present on the
    // post, so the fallback only ever polluted `.first()`. Pinned to the two
    // aria-label states only.
    likeButton: 'svg[aria-label="Like"], svg[aria-label="Unlike"]',
    // aria-pressed is the accessibility state Meta flips when a like lands.
    likeButtonActive: 'svg[aria-label="Unlike"], button[aria-pressed="true"] svg',
    // Scope the like selectors to the POST action bar. Every comment carries its
    // own like heart (w=16, no Comment svg in its ancestry), so an unscoped
    // `.first()` in DOM order resolves to a COMMENT heart, not the post's (the
    // post heart, w=24, renders AFTER the comments in the right column). The
    // action bar is the tight container holding the like heart AND the Comment
    // affordance; `:has(Like):has(Comment)` matches every ancestor of it, so
    // dom.ts takes `.last()` (the innermost = the bar). Verified live: `.last()`
    // resolves the w=24 post heart and its click flips Like↔Unlike; scoping the
    // active marker here also stops a liked COMMENT reading as a liked post.
    likeButtonScope:
      'div:has(svg[aria-label="Like"]):has(svg[aria-label="Comment"]), div:has(svg[aria-label="Unlike"]):has(svg[aria-label="Comment"])',
    // The affordance that reveals/focuses the composer when it is not already
    // mounted. The button ancestor of the svg is what actually takes the click.
    commentButton:
      'svg[aria-label="Comment"], svg[aria-label="Reply"], button:has(svg[aria-label="Comment"]), [aria-label="Comment"]',
    // Per-comment Reply affordance. On a /c/<id>/ permalink the target comment
    // is first, so the first "Reply" match threads to it. IG ships this as a
    // bare `<button class="_a9ze"><span>Reply</span></button>` — NO role
    // attribute. Two traps, both verified live:
    //   • a `[role=button]`/`span[role=button]`-led selector matched ZERO (the
    //     leaf has no role), so the flow never opened the threaded box; and
    //   • `button:has(span:text-is("Reply"))` ALSO matches the ANCESTOR comment
    //     -row button (it too contains the Reply span), and `.first()` in DOM
    //     order is that ancestor — clicking it opens nothing and the mention
    //     never prefills.
    // Match only the LEAF: `:text-is("Reply")` on the element's OWN text excludes
    // every ancestor (their text is the whole comment, not "Reply"). Lead with
    // the bare button, then role/span shapes for other rollouts.
    replyButton:
      'button:text-is("Reply"), [role="button"]:text-is("Reply"), span:text-is("Reply")',
    // IG pre-fills "@username" when a comment's reply box opens; that mention is
    // what threads the reply, so the flow requires and preserves it.
    replyPrefillsMention: true,
    // The composer is a contenteditable on IG, not a textarea.
    composerInput: 'div[contenteditable="true"][role="textbox"]',
    // Tried in order: aria-labelled textarea, the contenteditable box, then a
    // placeholder-labelled textarea. The stale single selector (the middle one)
    // was the P-A comment bug: it timed out on any rollout that shipped a
    // textarea instead.
    composerInputs: [
      'textarea[aria-label*="comment" i]',
      'div[contenteditable="true"][role="textbox"]',
      'textarea[placeholder*="comment" i]',
    ],
    submitButton:
      'button[type="button"] > div:has-text("Post"), div[role="button"]:has-text("Post")',
  },
  threads: {
    // The permalink post view keeps the whole home feed mounted behind it, so
    // every "first" match on a bare action selector hits a hidden FEED row, not
    // the target post (26+ like buttons on one page). The target post is the one
    // pressable container that also holds the reply composer — anchor every
    // action selector to `TARGET` so `.first()` resolves to the post under the
    // permalink, never a feed row. (Verified against live threads.com markup.)
    feed: 'div[role=feed], main',
    commentItem: 'div[role="article"], article',
    // Threads labels action icons with `<svg><title>`, NOT aria-label. Match
    // BOTH states in one selector so the button stays findable AFTER a like
    // flips Like→Unlike — otherwise the post-click verification poll can never
    // re-locate it and a landed like reads as a failure.
    likeButton: `${THREADS_TARGET} div[role="button"]:has(svg title:text-is("Like")), ${THREADS_TARGET} div[role="button"]:has(svg title:text-is("Unlike"))`,
    // Pressed state is the `Unlike` title (Meta ships no aria-pressed here).
    likeButtonActive: `${THREADS_TARGET} div[role="button"]:has(svg title:text-is("Unlike"))`,
    commentButton: `${THREADS_TARGET} div[role="button"]:has(svg title:text-is("Reply"))`,
    composerInput: 'div[contenteditable="true"][role="textbox"]',
    // The composer is a Lexical contenteditable whose aria-label is a generic
    // "Empty text field…", so the reply box is identified by role/placeholder,
    // not an aria-label containing "reply". It is unique on the post view
    // (count 1), so it needs no target scope.
    composerInputs: [
      '[contenteditable="true"][aria-placeholder*="Repl" i]',
      'div[contenteditable="true"][role="textbox"]',
      'textarea',
    ],
    // Scope submit to the composer's container so a feed row's "Post" affordance
    // is never clicked; keyboard submit (Enter) is tried first in dom.ts anyway.
    submitButton: `${THREADS_TARGET} div[role="button"]:has-text("Post"), div[role="dialog"] div[role="button"]:has-text("Post")`,
  },
};

export function selectorsFor(platform: Platform): SelectorSet {
  return SELECTORS[platform] ?? {};
}
