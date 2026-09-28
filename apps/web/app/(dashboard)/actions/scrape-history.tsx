'use client';

/**
 * Scrape history. Lists stored posts newest-first (GET /api/scrape/recent),
 * grouped by the keyword batch that produced them. Clicking a row hands its
 * permalink + platform up to the page, which re-fills the New Action form and
 * re-scrapes — the 6-hour cache makes that read the stored post instead of
 * spending another actor run.
 *
 * Grouping needs no new endpoint: /api/scrape/recent returns a flat post list
 * and /api/scrape/batches/:id/posts returns that batch's posts, so the batch id
 * per post is just an intersection. Posts that came from a single-URL scrape
 * belong to no batch and fall into their own "single post" group.
 */

import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@smm/ui';
import { ChevronDown, History, Layers } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { api, type KeywordBatch, type ScrapedPost } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

type Group = {
  key: string;
  label: string;
  sublabel: string;
  batchId?: string;
  status?: KeywordBatch['status'];
  posts: ScrapedPost[];
};

const SINGLE_KEY = '__single__';

export function ScrapeHistory({
  onPickPostAction,
  refreshKey,
}: {
  onPickPostAction: (platform: string, url: string) => void;
  /** Bumped by the page after a fresh scrape lands, to re-read the list. */
  refreshKey: number;
}) {
  const [posts, setPosts] = useState<ScrapedPost[] | null>(null);
  const [groups, setGroups] = useState<Group[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  useEffect(() => {
    let stale = false;
    setError(null);
    // Deliberately no `setPosts(null)` here: a refresh keeps the rows already on
    // screen until the new ones arrive, so the panel does not blank and flash
    // "Loading…" every time a scrape lands.
    api
      .listRecentPosts()
      .then((d) => !stale && setPosts(d.posts))
      .catch((e) => !stale && setError(e instanceof Error ? e.message : 'Could not load history'));
    return () => {
      stale = true;
    };
  }, [refreshKey]);

  // Batch membership is a second read, fetched only once the post list has
  // arrived: a batch's posts can be older than the recent-post window, so
  // intersecting the two views finds every group that is still visible.
  useEffect(() => {
    if (posts === null) return;
    let stale = false;
    api
      .listKeywordBatches({ limit: 20 })
      .then(async ({ batches }) => {
        const members = await Promise.all(
          batches.map(async (b) => {
            try {
              const r = await api.listKeywordBatchPosts(b.id, { limit: 100 });
              return { batch: b, postIds: new Set((r.posts ?? []).map((p) => p.id)) };
            } catch {
              // A batch whose posts cannot be read still shows as its own group
              // header, just without members — better than dropping it.
              return { batch: b, postIds: new Set<string>() };
            }
          }),
        );
        if (stale) return;
        setGroups(groupByBatch(posts, members));
      })
      .catch(() => {
        // Batches unreadable: fall back to one flat group rather than an error
        // banner, since the posts themselves loaded fine.
        if (!stale) setGroups(groupByBatch(posts, []));
      });
    return () => {
      stale = true;
    };
  }, [posts]);

  const total = posts?.length ?? 0;
  const shown = useMemo(() => groups.reduce((n, g) => n + g.posts.length, 0), [groups]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <span className="grid size-7 place-items-center rounded-lg bg-secondary text-muted-foreground">
            <History className="size-4" />
          </span>
          Scrape history
          {total > 0 ? (
            <Badge variant="outline" className="ml-1 font-normal">
              {total}
            </Badge>
          ) : null}
        </CardTitle>
        <CardDescription>
          Posts stored from earlier scrapes, newest first, grouped by the batch that produced them.
          Click one to load it into the form above — the 6-hour cache means it opens instantly.
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
          <div className="max-h-[560px] space-y-3 overflow-y-auto pr-1">
            {groups.map((g) => {
              const isCollapsed = collapsed[g.key];
              return (
                <section key={g.key} className="rounded-xl border border-border/60">
                  <button
                    type="button"
                    onClick={() => setCollapsed((c) => ({ ...c, [g.key]: !c[g.key] }))}
                    aria-expanded={!isCollapsed}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left transition-colors hover:bg-secondary/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  >
                    <ChevronDown
                      className={`size-4 shrink-0 text-muted-foreground transition-transform ${isCollapsed ? '-rotate-90' : ''}`}
                    />
                    {g.batchId ? (
                      <span className="grid size-6 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground">
                        <Layers className="size-3.5" />
                      </span>
                    ) : (
                      <span className="grid size-6 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground">
                        <History className="size-3.5" />
                      </span>
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{g.label}</span>
                      <span className="block truncate text-xs text-muted-foreground">{g.sublabel}</span>
                    </span>
                    {g.status ? <Badge variant={statusVariant(g.status)}>{g.status}</Badge> : null}
                    <Badge variant="outline" className="shrink-0 font-normal">
                      {g.posts.length}
                    </Badge>
                  </button>

                  {!isCollapsed ? (
                    g.posts.length === 0 ? (
                      <p className="px-3 pb-3 pl-14 text-xs text-muted-foreground">
                        No posts from this batch in the recent list.
                      </p>
                    ) : (
                      <div className="grid grid-cols-1 gap-2 px-2 pb-2 sm:grid-cols-2">
                        {g.posts.map((p) => (
                          <PostRow key={p.id} post={p} onPick={onPickPostAction} />
                        ))}
                      </div>
                    )
                  ) : null}
                </section>
              );
            })}
            {groups.length > 0 && shown < total ? (
              <p className="px-1 text-xs text-muted-foreground">
                {total - shown} older post{total - shown === 1 ? '' : 's'} not in the recent window.
              </p>
            ) : null}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function PostRow({
  post,
  onPick,
}: {
  post: ScrapedPost;
  onPick: (platform: string, url: string) => void;
}) {
  return (
    <button
      type="button"
      onClick={() => onPick(post.platform, permalinkFor(post))}
      className="flex items-start gap-3 rounded-lg border border-border/60 bg-background p-2.5 text-left transition-colors hover:bg-secondary/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {post.mediaUrls && post.mediaUrls.length > 0 ? (
        <span className="relative size-14 shrink-0 overflow-hidden rounded-lg bg-secondary">
          {/* eslint-disable-next-line @next/next/no-img-element -- remote user content */}
          <img src={post.mediaUrls[0]} alt="" loading="lazy" className="size-full object-cover" />
          {/* Carousel badge: the cover alone would hide the rest. */}
          {post.mediaUrls.length > 1 ? (
            <span className="absolute bottom-0.5 right-0.5 rounded bg-foreground/70 px-1 text-[10px] font-medium tabular-nums text-background">
              {post.mediaUrls.length}
            </span>
          ) : null}
        </span>
      ) : (
        <span className="grid size-14 shrink-0 place-items-center rounded-lg bg-secondary text-muted-foreground">
          <History className="size-4" />
        </span>
      )}

      <span className="min-w-0 flex-1 space-y-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">@{post.authorHandle || 'unknown'}</span>
          <Badge variant="outline" className="shrink-0 font-normal">
            {PLATFORM_LABEL[post.platform] ?? post.platform}
          </Badge>
        </span>
        {/* Two lines of caption is enough to recognise a post; the
            full text is one click away in the form. */}
        <span className="line-clamp-2 text-xs leading-snug text-muted-foreground">
          {post.text || '(no caption)'}
        </span>
        <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span>{metricsShort(post.metrics)}</span>
          <span aria-hidden>·</span>
          <span>{new Date(post.scrapedAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}</span>
        </span>
      </span>
    </button>
  );
}

function statusVariant(s: KeywordBatch['status']): 'default' | 'secondary' | 'destructive' {
  if (s === 'SUCCEEDED') return 'default';
  if (s === 'FAILED') return 'destructive';
  return 'secondary';
}

/**
 * Buckets the recent posts by the batch that produced them. A post can appear
 * in more than one batch (the same target scraped twice), so the FIRST batch
 * that claims it wins — otherwise it would be rendered twice. Batches are
 * walked newest-first, which makes that the most recent claim.
 */
function groupByBatch(
  posts: ScrapedPost[],
  members: { batch: KeywordBatch; postIds: Set<string> }[],
): Group[] {
  const out: Group[] = [];
  const claimed = new Set<string>();

  for (const { batch, postIds } of members) {
    const mine = posts.filter((p) => postIds.has(p.id) && !claimed.has(p.id));
    for (const p of mine) claimed.add(p.id);
    out.push({
      key: batch.id,
      label: batch.keywords.join(', ') || '(no keywords)',
      sublabel: `${PLATFORM_LABEL[batch.platform] ?? batch.platform} · ${new Date(batch.createdAt).toLocaleString()}`,
      batchId: batch.id,
      status: batch.status,
      posts: mine,
    });
  }

  const singles = posts.filter((p) => !claimed.has(p.id));
  if (singles.length > 0) {
    out.push({
      key: SINGLE_KEY,
      label: 'Single-URL scrapes',
      sublabel: `${singles.length} post${singles.length === 1 ? '' : 's'} not from a keyword batch`,
      posts: singles,
    });
  }
  return out;
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
