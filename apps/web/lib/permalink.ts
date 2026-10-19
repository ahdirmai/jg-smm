import type { ScrapedPost } from '@/lib/api';
import { platformLabel } from '@/lib/platforms';

/**
 * Public URL for a scraped post, or null when the platform cannot be addressed
 * from the id we stored.
 *
 * This lived twice — `permalinkFor` in the actions scrape history and
 * `postPermalink` in the batch detail page — and the two had drifted: the first
 * handled only Threads and fabricated an instagram.com link for everything
 * else, so a YouTube video id was handed over as an Instagram shortcode and 404'd
 * on click. One function, seven cases.
 *
 * Null is a real answer, not a fallback to Instagram. The batch detail page
 * gates its "Open" link, its "Copy link" button and its "Fetch comments" button
 * on this, so returning a wrong-but-non-null URL is worse than returning none.
 */
export function postPermalink(p: ScrapedPost): string | null {
  const code = p.externalId?.trim();
  if (!code) return null;
  const handle = p.authorHandle?.trim();
  const hasHandle = Boolean(handle) && handle !== 'unknown';

  switch (p.platform?.toLowerCase()) {
    case 'instagram':
      return `https://www.instagram.com/p/${code}/`;
    case 'threads':
      return `https://www.threads.com/post/${code}`;
    case 'tiktok':
      return hasHandle ? `https://www.tiktok.com/@${handle}/video/${code}` : null;
    case 'youtube':
      return `https://www.youtube.com/watch?v=${code}`;
    case 'x':
      return hasHandle ? `https://x.com/${handle}/status/${code}` : null;
    case 'facebook':
      return `https://www.facebook.com/${code}`;
    case 'linkedin':
      // LinkedIn activity ids are addressed through the URN, not the bare id.
      return `https://www.linkedin.com/feed/update/urn:li:activity:${code}`;
    default:
      return null;
  }
}

/** Human label for a post's platform, for text next to the link. */
export function postPlatformLabel(p: ScrapedPost): string {
  return platformLabel(p.platform ?? '') ?? p.platform ?? 'unknown';
}

/**
 * Deep-link into /actions with one scraped post preloaded and pre-scraped.
 *
 * The batch page is a discovery surface: the operator finds a post worth acting
 * on there, but every action lives on /actions. Without this they retype or copy
 * a permalink by hand, which is exactly the step that makes them act on the
 * wrong post.
 *
 * Returns null when the post cannot be addressed OR its platform is not
 * scrapable on demand. The second check is not cosmetic: /actions loads the post
 * through the on-demand scrape endpoint, which answers 400 "no actor configured"
 * for the five platforms that have no actor. Handing over a link that always
 * errors is worse than showing no button — the operator learns the feature is
 * broken rather than that it does not apply here.
 */
export function actionDeepLink(p: ScrapedPost): string | null {
  const url = postPermalink(p);
  if (!url) return null;
  const platform = p.platform?.toLowerCase();
  if (platform !== 'instagram' && platform !== 'threads') return null;
  const qs = new URLSearchParams({ post: url, platform });
  return `/actions?${qs.toString()}`;
}
