'use client';

/**
 * Scrape history. Lists stored posts newest-first (GET /api/scrape/recent),
 * grouped by the keyword batch that produced them.
 *
 * Clicking a row opens the post detail panel — the same one the batch page
 * uses. For an on-demand platform the panel also offers Comment/Like; choosing
 * one loads the STORED row into the New Action form, so acting on history never
 * pays for a second scrape. Other platforms stay read-only: the form only has
 * actors for Instagram, Threads and YouTube.
 *
 * Grouping needs no new endpoint: /api/scrape/recent returns a flat post list
 * and /api/scrape/batches/:id/posts returns that batch's posts, so the batch id
 * per post is just an intersection. Posts that came from a single-URL scrape
 * belong to no batch and fall into their own "single post" group.
 */

import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input } from '@smm/ui';
import { ChevronDown, Filter, Heart, History, Layers, MessageSquare, Search, X } from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';

import { PostDetailDialog } from '@/components/post-detail-dialog';
import { api, type KeywordBatch, type ScrapedPost } from '@/lib/api';
import { isOnDemandPlatform, PLATFORMS, PLATFORM_LABEL, metricsLine, platformLabel } from '@/lib/platforms';
import { postPermalink } from '@/lib/permalink';

type Group = {
  key: string;
  label: string;
  sublabel: string;
  batchId?: string;
  status?: KeywordBatch['status'];
  posts: ScrapedPost[];
};

type Sort = 'newest' | 'oldest' | 'engagement';
type StatusFilter = 'all' | KeywordBatch['status'];

const SINGLE_KEY = '__single__';
const PAGE_SIZES = [20, 50, 100] as const;

/** Engagement is the sum of every numeric metric, not one platform's favourite
 *  count: "most engaged" must mean the same thing on a TikTok video and an
 *  Instagram carousel, and views alone would rank a video with no likes above
 *  a post whose comment thread is the point. */
function engagement(p: ScrapedPost): number {
  if (!p.metrics) return 0;
  return Object.values(p.metrics).reduce((n, v) => n + (typeof v === 'number' ? v : 0), 0);
}

export function ScrapeHistory({
  refreshKey,
  onUseAsTarget,
}: {
  refreshKey: number;
  /**
   * Turn a stored row into a new action target. The caller must NOT re-scrape:
   * this row is already the target, and the form loads it plus its saved
   * comments from the database.
   */
  onUseAsTarget?: (post: ScrapedPost, flow: 'comment' | 'like') => void;
}) {
  const [posts, setPosts] = useState<ScrapedPost[] | null>(null);
  const [groups, setGroups] = useState<Group[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [selected, setSelected] = useState<ScrapedPost | null>(null);

  // Filters. Client-side on purpose: /api/scrape/recent already returns the
  // window the page shows, so a round trip per keystroke would re-fetch the
  // same rows. If the window ever grows past a few hundred rows, move the
  // predicate into the query instead of raising the limit.
  const [platform, setPlatform] = useState<string>('all');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [query, setQuery] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [sort, setSort] = useState<Sort>('newest');
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES[1]);

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

  /** The predicates, in one place so the counts and the list cannot disagree. */
  const matches = useMemo(() => {
    const needle = query.trim().toLowerCase();
    const fromTs = from ? new Date(`${from}T00:00:00`).getTime() : null;
    const toTs = to ? new Date(`${to}T23:59:59.999`).getTime() : null;
    return (p: ScrapedPost) => {
      if (platform !== 'all' && p.platform.toLowerCase() !== platform) return false;
      const ts = new Date(p.scrapedAt).getTime();
      if (fromTs !== null && ts < fromTs) return false;
      if (toTs !== null && ts > toTs) return false;
      if (needle) {
        const hay = `${p.text ?? ''} ${p.authorHandle ?? ''}`.toLowerCase();
        if (!hay.includes(needle)) return false;
      }
      return true;
    };
  }, [platform, query, from, to]);

  const postMatches = useMemo(() => {
    if (!posts) return new Set<string>();
    return new Set(posts.filter(matches).map((p) => p.id));
  }, [posts, matches]);

  const sortPosts = useCallback(
    (list: ScrapedPost[]): ScrapedPost[] => {
      if (sort === 'engagement') return [...list].sort((a, b) => engagement(b) - engagement(a));
      const dir = sort === 'oldest' ? 1 : -1;
      return [...list].sort(
        (a, b) => dir * (new Date(a.scrapedAt).getTime() - new Date(b.scrapedAt).getTime()),
      );
    },
    [sort],
  );

  /** Filtered + sorted groups, with the status filter applied to the BATCH.
   *  The single-post group has no status and survives it only when the filter
   *  is off — a group with no batch is neither SUCCEEDED nor FAILED. */
  const visible = useMemo(() => {
    return groups
      .filter((g) => status === 'all' || g.status === status)
      .map((g) => ({ ...g, posts: sortPosts(g.posts.filter((p) => postMatches.has(p.id))) }))
      .filter((g) => g.posts.length > 0 || (g.batchId && status !== 'all'));
  }, [groups, status, postMatches, sortPosts]);

  const shown = visible.reduce((n, g) => n + g.posts.length, 0);
  const total = posts?.length ?? 0;
  const filtering = platform !== 'all' || status !== 'all' || query !== '' || from !== '' || to !== '';

  function resetFilters() {
    setPlatform('all');
    setStatus('all');
    setQuery('');
    setFrom('');
    setTo('');
  }

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
              {filtering ? `${shown} / ${total}` : total}
            </Badge>
          ) : null}
        </CardTitle>
        <CardDescription>
          Posts stored from earlier scrapes, grouped by the batch that produced them. Click one to
          read it in full.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* Filters. Chips for the two enumerable dimensions (platform, batch
            status) and inputs only for the free-form ones — a dropdown for a
            9-value set hides its own contents behind a click for no gain. */}
        <div className="space-y-2 rounded-lg border border-border/60 p-2.5">
          <div className="flex flex-wrap items-center gap-1.5">
            <Filter className="size-3.5 shrink-0 text-muted-foreground" />
            <Chip label="All platforms" active={platform === 'all'} onClick={() => setPlatform('all')} />
            {PLATFORMS.map((p) => (
              <Chip
                key={p}
                label={PLATFORM_LABEL[p]}
                active={platform === p}
                onClick={() => setPlatform(platform === p ? 'all' : p)}
              />
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            <Chip label="Any status" active={status === 'all'} onClick={() => setStatus('all')} />
            {(['SUCCEEDED', 'FAILED', 'RUNNING'] as const).map((s) => (
              <Chip
                key={s}
                label={s[0] + s.slice(1).toLowerCase()}
                active={status === s}
                tone={s === 'FAILED' ? 'destructive' : s === 'SUCCEEDED' ? 'success' : 'secondary'}
                onClick={() => setStatus(status === s ? 'all' : s)}
              />
            ))}
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <div className="relative min-w-[180px] flex-1">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Search caption or @handle"
                aria-label="Search caption or handle"
                className="h-8 pl-8 text-sm"
              />
            </div>
            <label className="flex items-center gap-1 text-xs text-muted-foreground">
              From
              <Input
                type="date"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
                className="h-8 w-[140px] text-sm"
                aria-label="Scraped from date"
              />
            </label>
            <label className="flex items-center gap-1 text-xs text-muted-foreground">
              To
              <Input
                type="date"
                value={to}
                onChange={(e) => setTo(e.target.value)}
                className="h-8 w-[140px] text-sm"
                aria-label="Scraped to date"
              />
            </label>
            <select
              value={sort}
              onChange={(e) => setSort(e.target.value as Sort)}
              aria-label="Sort order"
              className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm"
            >
              <option value="newest">Newest scraped</option>
              <option value="oldest">Oldest scraped</option>
              <option value="engagement">Most engagement</option>
            </select>
            <select
              value={pageSize}
              onChange={(e) => setPageSize(Number(e.target.value))}
              aria-label="Posts per group"
              className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm"
            >
              {PAGE_SIZES.map((n) => (
                <option key={n} value={n}>
                  {n} per group
                </option>
              ))}
            </select>
            {filtering ? (
              <Button variant="ghost" size="sm" className="h-8" onClick={resetFilters}>
                <X className="size-3.5" />
                Clear
              </Button>
            ) : null}
          </div>
        </div>

        {error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : posts === null ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : total === 0 ? (
          <div className="flex items-center justify-center rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
            Nothing scraped yet. Run a keyword search or paste a link to add the first post.
          </div>
        ) : visible.length === 0 ? (
          <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed py-10 text-sm text-muted-foreground">
            No post matches these filters.
            <Button variant="outline" size="sm" onClick={resetFilters}>
              Clear filters
            </Button>
          </div>
        ) : (
          <div className="max-h-[560px] space-y-3 overflow-y-auto pr-1">
            {visible.map((g) => {
              const isCollapsed = collapsed[g.key];
              const overflow = g.posts.length - pageSize;
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
                    <span className="grid size-6 shrink-0 place-items-center rounded-md bg-secondary text-muted-foreground">
                      {g.batchId ? <Layers className="size-3.5" /> : <History className="size-3.5" />}
                    </span>
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
                        No post from this batch matches the current filters.
                      </p>
                    ) : (
                      <>
                        <div className="grid grid-cols-1 gap-2 px-2 pb-2 sm:grid-cols-2">
                          {g.posts.slice(0, pageSize).map((p) => (
                            <PostRow key={p.id} post={p} onOpen={() => setSelected(p)} />
                          ))}
                        </div>
                        {overflow > 0 ? (
                          <p className="px-3 pb-2 text-xs text-muted-foreground">
                            {overflow} more in this group — raise “per group” above to show them.
                          </p>
                        ) : null}
                      </>
                    )
                  ) : null}
                </section>
              );
            })}
          </div>
        )}
      </CardContent>

      <PostDetailDialog
        post={selected}
        onClose={() => setSelected(null)}
        footer={
          selected && onUseAsTarget && isOnDemandPlatform(selected.platform) ? (
            <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/60 bg-secondary/30 p-2">
              <span className="text-xs font-medium text-muted-foreground">Act on this stored post</span>
              <Button
                size="sm"
                onClick={() => {
                  onUseAsTarget(selected, 'comment');
                  setSelected(null);
                }}
              >
                <MessageSquare className="size-3.5" />
                Comment
              </Button>
              {selected.platform.toLowerCase() !== 'youtube' ? (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    onUseAsTarget(selected, 'like');
                    setSelected(null);
                  }}
                >
                  <Heart className="size-3.5" />
                  Like
                </Button>
              ) : null}
            </div>
          ) : null
        }
      />
    </Card>
  );
}

function Chip({
  label,
  active,
  tone,
  onClick,
}: {
  label: string;
  active: boolean;
  tone?: 'success' | 'destructive' | 'secondary';
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={[
        'rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring',
        active ? 'border-primary bg-primary/10 text-foreground' : 'border-border text-muted-foreground hover:bg-secondary/60',
        !active && tone === 'destructive' ? 'text-destructive' : '',
        !active && tone === 'success' ? 'text-success' : '',
      ].join(' ')}
    >
      {label}
    </button>
  );
}

function PostRow({ post, onOpen }: { post: ScrapedPost; onOpen: () => void }) {
  // Every row is clickable now: opening the detail panel works for all seven
  // platforms because it reads only stored data. The permalink gate moved into
  // the dialog, where the two actions that NEED a URL are the ones disabled.
  const hasLink = postPermalink(post) !== null;
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex items-start gap-3 rounded-lg border border-border/60 bg-background p-2.5 text-left transition-colors hover:bg-secondary/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    >
      {post.mediaUrls && post.mediaUrls.length > 0 ? (
        <span className="relative size-14 shrink-0 overflow-hidden rounded-lg bg-secondary">
          {/* eslint-disable-next-line @next/next/no-img-element -- remote user content */}
          <img src={post.mediaUrls[0]} alt="" loading="lazy" className="size-full object-cover" />
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
            {platformLabel(post.platform) ?? post.platform}
          </Badge>
        </span>
        <span className="line-clamp-2 text-xs leading-snug text-muted-foreground">
          {post.text || '(no caption)'}
        </span>
        <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span>{metricsLine(post.platform, post.metrics)}</span>
          <span aria-hidden>·</span>
          <span>
            {post.publishedAt
              ? new Date(post.publishedAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
              : `scraped ${new Date(post.scrapedAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}`}
          </span>
          {hasLink ? null : <span className="italic">no permalink</span>}
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
      sublabel: `${platformLabel(batch.platform) ?? batch.platform} · ${new Date(batch.createdAt).toLocaleString()}`,
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
