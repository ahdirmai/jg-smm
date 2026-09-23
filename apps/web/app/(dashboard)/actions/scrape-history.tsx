'use client';

/**
 * Scrape history. Lists stored posts newest-first (GET /api/scrape/recent).
 * Clicking a row hands its permalink + platform up to the page, which
 * re-fills the New Action form and re-scrapes — the 6-hour cache makes that
 * read the stored post instead of spending another actor run.
 */

import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@smm/ui';
import { History } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, type ScrapedPost } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

export function ScrapeHistory({
  onPickPostAction,
  refreshKey,
}: {
  onPickPostAction: (platform: string, url: string) => void;
  /** Bumped by the page after a fresh scrape lands, to re-read the list. */
  refreshKey: number;
}) {
  const [posts, setPosts] = useState<ScrapedPost[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let stale = false;
    setError(null);
    api
      .listRecentPosts()
      .then((d) => !stale && setPosts(d.posts))
      .catch((e) => !stale && setError(e instanceof Error ? e.message : 'Could not load history'));
    return () => {
      stale = true;
    };
  }, [refreshKey]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <span className="grid size-7 place-items-center rounded-lg bg-secondary text-muted-foreground">
            <History className="size-4" />
          </span>
          Scrape history
        </CardTitle>
        <CardDescription>
          Posts stored from earlier scrapes, newest first. Click a row to open it in the form
          above (re-scrape is instant — the 6-hour cache reads what is already stored).
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : posts === null ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : posts.length === 0 ? (
          <div className="flex items-center justify-center rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
            Nothing scraped yet. Paste a link above to add the first post.
          </div>
        ) : (
          <div className="max-h-[420px] overflow-y-auto rounded-lg border border-border/60">
            {posts.map((p) => (
              <button
                key={p.id}
                type="button"
                onClick={() => onPickPostAction(p.platform, permalinkFor(p))}
                className="flex w-full items-center gap-3 border-b border-border/60 px-3 py-2 text-left transition-colors last:border-b-0 hover:bg-secondary/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              >
                {p.mediaUrls && p.mediaUrls.length > 0 ? (
                  <span className="size-11 shrink-0 overflow-hidden rounded-lg bg-secondary">
                    {/* eslint-disable-next-line @next/next/no-img-element -- remote user content */}
                    <img src={p.mediaUrls[0]} alt="" loading="lazy" className="size-full object-cover" />
                  </span>
                ) : (
                  <span className="size-11 shrink-0 rounded-lg bg-secondary" />
                )}
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-2 text-sm">
                    <span className="truncate font-medium">@{p.authorHandle || 'unknown'}</span>
                    <Badge variant="outline">
                      {PLATFORM_LABEL[p.platform] ?? p.platform}
                    </Badge>
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {p.text ? p.text : '(no caption)'}
                  </span>
                </span>
                <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
                  {metricsShort(p.metrics)} · {new Date(p.scrapedAt).toLocaleDateString()}
                </span>
              </button>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// Rebuild a canonical permalink from the stored post. The target table keeps
// the original URL, but the row we render here is the post, and its (platform,
// externalId) pair is the permalink itself.
function permalinkFor(p: ScrapedPost): string {
  if (p.platform === 'threads') {
    return `https://www.threads.com/@${p.authorHandle || 'unknown'}/post/${p.externalId}`;
  }
  return `https://www.instagram.com/p/${p.externalId}/`;
}

function metricsShort(m: Record<string, number> | null): string {
  if (!m) return '';
  const likes = m.likes ?? m.likeCount;
  if (typeof likes === 'number') return `${likes}♥`;
  return '';
}
