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
  /** The button that opens the composer (the "reply" bubble under a post). */
  commentButton?: string;
  /** The composer text box a comment is typed into. */
  composerInput?: string;
  /** The submit button for the composer. */
  submitButton?: string;
}

const SELECTORS: Partial<Record<Platform, SelectorSet>> = {
  instagram: {
    // The main column is the feed landmark on a post permalink.
    feed: 'main [role=feed], main section',
    // A rendered comment is an <article> sibling of the post, or a list item.
    commentItem: 'article, li[role=menuitem], div[role=button] > span',
    likeButton: 'svg[aria-label="Like"], svg[aria-label="Unlike"], section button svg',
    // aria-pressed is the accessibility state Meta flips when a like lands.
    likeButtonActive: 'svg[aria-label="Unlike"], button[aria-pressed="true"] svg',
    commentButton: 'svg[aria-label="Comment"], svg[aria-label="Reply"]',
    // The composer is a contenteditable on IG, not a textarea.
    composerInput: 'div[contenteditable="true"][role="textbox"]',
    submitButton:
      'button[type="button"] > div:has-text("Post"), div[role="button"]:has-text("Post")',
  },
  threads: {
    feed: 'div[role=feed], main',
    commentItem: 'div[role="article"], article',
    likeButton: 'div[role="button"] svg, button[aria-label*="ike"]',
    likeButtonActive: 'div[role="button"][aria-pressed="true"] svg, button[aria-label*="nlike"]',
    commentButton: 'div[role="button"][aria-label*="eply"], button[aria-label*="eply"]',
    composerInput: 'div[contenteditable="true"][role="textbox"], textarea',
    submitButton: 'div[role="button"]:has-text("Post"), button:has-text("Post")',
  },
};

export function selectorsFor(platform: Platform): SelectorSet {
  return SELECTORS[platform] ?? {};
}
