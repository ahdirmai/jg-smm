'use client';

/**
 * One scraped post, in full: media, caption, every metric the platform
 * reports, its stored comments and sentiment, plus the two things an operator
 * actually wants from a row — open it on the platform, or copy its link.
 *
 * Extracted from the batch detail page, which had this same panel inline. The
 * Scrape History list needs the identical panel, and a second copy would drift
 * exactly the way `permalinkFor` did (see lib/permalink.ts) — two renderers of
 * the same post that disagree about which fields exist.
 *
 * Deliberately NO "create action" button: that is the caller's decision. Only
 * Instagram and Threads can be acted on, and a component that offers the button
 * for a TikTok post would promise something the worker cannot do.
 */

import { Badge, Button, Dialog, DialogContent, DialogHeader, DialogTitle } from '@smm/ui';
import { Clock, ExternalLink, Loader2, MessageSquare, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';

import { CopyButton } from '@/components/copy-button';
import { PostMedia } from '@/components/post-media';
import { SentimentSummary } from '@/components/sentiment';
import { api, type ScrapedComment, type ScrapedPost } from '@/lib/api';
import { metricsLine, platformLabel } from '@/lib/platforms';
import { postPermalink } from '@/lib/permalink';

/** Platforms whose stored comments can be refreshed by a paid per-post run. */
const REFETCHABLE = new Set(['instagram', 'threads']);

export function PostDetailDialog({
  post,
  onClose,
  footer,
}: {
  /** Null closes the dialog. */
  post: ScrapedPost | null;
  onClose: () => void;
  /** Caller-specific actions (e.g. "Buat Action"), rendered above the comments. */
  footer?: React.ReactNode;
}) {
  const [comments, setComments] = useState<ScrapedComment[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [fresh, setFresh] = useState<ScrapedPost | null>(null);

  const current = fresh ?? post;
  const platform = current?.platform?.toLowerCase() ?? '';
  const url = current ? postPermalink(current) : null;

  // Comments load per post, on open — not for every row up front. For the
  // platforms whose search actor stores no comment bodies this is a read of
  // nothing, which is why the empty state below explains WHERE comments come
  // from instead of claiming the post has none.
  // Keyed on the ID, not the object: the same post re-rendered by a parent
  // refresh is the same post, and re-fetching its comments on every render of
  // a new object would be a request per keystroke in the filter box above.
  const postId = post?.id;
  useEffect(() => {
    setComments(null);
    setError(null);
    setFresh(null);
    if (!postId) return;
    let stale = false;
    api
      .listPostComments(postId, { limit: 100 })
      .then((r) => !stale && setComments(r.comments ?? []))
      .catch((e) => !stale && setError(e instanceof Error ? e.message : 'Could not load comments'));
    return () => {
      stale = true;
    };
  }, [postId]);

  async function refetch() {
    if (!url) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.scrapeTarget(current!.platform, url, true);
      setComments(res.comments ?? []);
      setFresh(res.post);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Re-scrape failed');
    } finally {
      setBusy(false);
    }
  }

  if (!current) return null;

  const sentiment = (comments ?? []).reduce(
    (acc, c) => {
      if (c.sentiment === 'POSITIVE') acc.positive++;
      else if (c.sentiment === 'NEGATIVE') acc.negative++;
      else if (c.sentiment === 'NEUTRAL') acc.neutral++;
      else acc.unanalysed++;
      return acc;
    },
    { positive: 0, neutral: 0, negative: 0, unanalysed: 0 },
  );

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-2">
            @{current.authorHandle || 'unknown'}
            <Badge variant="outline" className="font-normal">
              {platformLabel(platform) ?? platform}
            </Badge>
          </DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <PostMedia urls={current.mediaUrls} />

          <p className="whitespace-pre-wrap text-sm leading-relaxed">
            {current.text || <span className="text-muted-foreground">(no caption)</span>}
          </p>

          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>{metricsLine(platform, current.metrics) || 'no metrics reported'}</span>
            <span className="flex items-center gap-1">
              <Clock className="size-3" />
              {(current.publishedAt ? new Date(current.publishedAt) : new Date(current.scrapedAt)).toLocaleString(
                undefined,
                { dateStyle: 'medium', timeStyle: 'short' },
              )}
              {current.publishedAt ? null : ' (scraped)'}
            </span>
          </div>

          {url ? (
            <div className="flex flex-wrap items-center gap-2">
              <CopyButton value={url} label="Copy link" />
              <Button variant="outline" size="sm" asChild>
                <a href={url} target="_blank" rel="noreferrer noopener">
                  <ExternalLink className="size-3.5" />
                  Open on {platformLabel(platform) ?? platform}
                </a>
              </Button>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              This post has no addressable permalink, so it cannot be opened or re-scraped.
            </p>
          )}

          {footer}

          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="flex items-center gap-2 text-sm font-medium">
                <MessageSquare className="size-4" />
                Comments
                {comments && comments.length > 0 ? (
                  <Badge variant="outline" className="font-normal">
                    {comments.length}
                  </Badge>
                ) : null}
              </span>
              {url && REFETCHABLE.has(platform) ? (
                <Button
                  variant="outline"
                  size="sm"
                  className="ml-auto"
                  disabled={busy}
                  onClick={() => void refetch()}
                  title="Runs the per-post actor again (paid). Skips the 6-hour cache."
                >
                  {busy ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <RefreshCw className="size-3.5" />
                  )}
                  {busy ? 'Scraping…' : comments && comments.length > 0 ? 'Refresh' : 'Fetch comments'}
                </Button>
              ) : null}
            </div>

            {error ? (
              <p className="text-sm text-destructive">{error}</p>
            ) : comments === null ? (
              <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Loading comments…
              </p>
            ) : comments.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                {REFETCHABLE.has(platform)
                  ? 'No comments stored yet — keyword search does not fetch comment bodies. Use “Fetch comments” above.'
                  : `The ${platformLabel(platform) ?? platform} scraper returns the post only; it does not fetch comments.`}
              </p>
            ) : (
              <div className="space-y-3">
                <SentimentSummary breakdown={sentiment} />
                <div className="max-h-64 space-y-2 overflow-y-auto pr-1">
                  {comments.map((c) => (
                    <div key={c.id} className="rounded-lg border border-border/60 p-2.5">
                      <div className="flex items-center gap-2">
                        <span className="text-xs font-medium">@{c.authorHandle || 'unknown'}</span>
                        <Badge variant="outline" className="font-normal">
                          {c.sentiment ?? 'not analysed'}
                        </Badge>
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground">{c.text}</p>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
