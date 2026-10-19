'use client';

import { useMemo, useState } from 'react';
import { Button, Card, CardContent } from '@smm/ui';
import {
  ArrowUpDown,
  Copy,
  ExternalLink,
  Flame,
  Heart,
  MessageSquare,
  Repeat2,
  TrendingUp,
} from 'lucide-react';
import type { PostInsight, ScrapedComment, ScrapedPost } from '@/lib/api';
import { metricsLine } from '@/lib/platforms';
import { postPermalink } from '@/lib/permalink';
import { SentimentSummary, SentimentBadge } from '@/components/sentiment';
import { CommentThread } from '@/components/comment-thread';

/**
 * Scraped-post preview, ranked by insight.
 *
 * Why this replaces the old thin row: a scrape returns posts the operator has
 * never seen, and the only decision the page supports is "which one do I act
 * on". A row showing just @handle + a metrics line forces a linear read of
 * every post to compare them. This card states the ranking reason up front
 * (rank badge, score, the three engagement numbers) so the list can be scanned
 * top-down and the answer is the first card.
 *
 * Ordering is CLIENT-side from `rank` when the API supplies insights, because
 * the server already computed the score and re-deriving it here would let the
 * two drift. `sortBy` only re-orders what the server ranked — it never
 * recomputes a score.
 */


type SortKey = 'insight' | 'engagement' | 'recent';

/** Rank badge colours: the top three are the actionable picks, so they get the
 *  heat; everything below is neutral. Colour is decoration here — the number is
 *  always shown, so the ranking survives without it. */
function rankStyle(rank: number): string {
  if (rank === 1) return 'bg-amber-500/15 text-amber-700 dark:text-amber-400 ring-amber-500/30';
  if (rank === 2) return 'bg-slate-400/15 text-slate-600 dark:text-slate-300 ring-slate-400/30';
  if (rank === 3) return 'bg-orange-600/15 text-orange-700 dark:text-orange-400 ring-orange-600/30';
  return 'bg-secondary text-muted-foreground ring-transparent';
}

function ago(hours: number): string {
  if (hours < 1) return 'baru saja';
  if (hours < 24) return `${Math.round(hours)} jam lalu`;
  const d = Math.round(hours / 24);
  return `${d} hari lalu`;
}

function compact(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(1)}M`;
}

/** One ranked post. `onOpen` opens the comment dialog; the comment count is the
 *  affordance that leads there, since it is the number an operator wants to dig
 *  into when deciding whether a post is worth a reply. */
function InsightCard({
  post,
  insight,
  onOpen,
  sentiment,
  commentCount,
}: {
  post: ScrapedPost;
  insight: PostInsight | null;
  onOpen: () => void;
  // `| undefined` is explicit: the repo compiles with exactOptionalPropertyTypes,
  // so an optional prop does not accept a passed-in undefined.
  sentiment?: { positive: number; neutral: number; negative: number; unanalysed: number } | undefined;
  commentCount?: number | undefined;
}) {
  const link = postPermalink(post);
  return (
    <Card className="overflow-hidden">
      <CardContent className="flex gap-3 p-3">
        {post.mediaUrls?.[0] ? (
          <button
            type="button"
            onClick={onOpen}
            aria-label={`Buka detail @${post.authorHandle}`}
            className="h-24 w-24 shrink-0 overflow-hidden rounded-lg border border-border/60 bg-secondary/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            {/* eslint-disable-next-line @next/next/no-img-element -- remote user content on rotating CDN hosts */}
            <img
              src={post.mediaUrls[0]}
              alt=""
              loading="lazy"
              draggable={false}
              className="h-full w-full object-cover"
            />
          </button>
        ) : (
          <div className="grid h-24 w-24 shrink-0 place-items-center rounded-lg border border-border/60 bg-secondary/40 text-xs text-muted-foreground">
            no media
          </div>
        )}

        <div className="min-w-0 flex-1 space-y-1.5">
          <div className="flex items-start gap-2">
            {insight ? (
              <span
                className={`grid size-6 shrink-0 place-items-center rounded-full text-[11px] font-bold ring-1 ${rankStyle(insight.rank)}`}
                title={`Peringkat ${insight.rank} · skor ${insight.score}`}
              >
                {insight.rank}
              </span>
            ) : null}
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                <span className="font-medium text-foreground">@{post.authorHandle || 'unknown'}</span>
                {post.publishedAt ? (
                  <span className="text-muted-foreground" title="Tanggal publikasi">
                    {new Date(post.publishedAt).toLocaleDateString()}
                  </span>
                ) : null}
                {insight ? (
                  <span className="text-muted-foreground/80" title="Umur post saat ranking dihitung">
                    {ago(insight.ageHours)}
                  </span>
                ) : null}
              </div>
              {post.text ? (
                <p className="mt-0.5 line-clamp-2 text-sm">{post.text}</p>
              ) : (
                <p className="mt-0.5 text-xs text-muted-foreground">(tanpa caption)</p>
              )}
            </div>
          </div>

          {/* The three numbers an operator compares, as heroes. Engagement is
              highlighted because it is the ranking's primary term. */}
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
            <span className="flex items-center gap-1" title="Likes">
              <Heart className="size-3.5 text-muted-foreground" aria-hidden />
              <span className="font-semibold tabular-nums">{compact(insight?.likes ?? 0)}</span>
            </span>
            <span className="flex items-center gap-1" title="Komentar">
              <MessageSquare className="size-3.5 text-muted-foreground" aria-hidden />
              <span className="font-semibold tabular-nums">{compact(insight?.comments ?? 0)}</span>
            </span>
            <span className="flex items-center gap-1" title="Shares / reposts">
              <Repeat2 className="size-3.5 text-muted-foreground" aria-hidden />
              <span className="font-semibold tabular-nums">{compact(insight?.shares ?? 0)}</span>
            </span>
            {insight ? (
              <span
                className="flex items-center gap-1 text-muted-foreground"
                title="Skor ranking: interaksi (log) × efisiensi per-follower × kesegaran"
              >
                <Flame className="size-3.5" aria-hidden />
                skor <span className="font-semibold tabular-nums">{insight.score}</span>
              </span>
            ) : null}
            {insight?.engagementPerFollowerPct != null ? (
              <span className="flex items-center gap-1 text-muted-foreground" title="Interaksi per follower">
                <TrendingUp className="size-3.5" aria-hidden />
                <span className="font-semibold tabular-nums">
                  {insight.engagementPerFollowerPct.toFixed(2)}%
                </span>
                <span>per follower</span>
              </span>
            ) : null}
            {!insight && post.metrics ? (
              <span className="text-muted-foreground">{metricsLine(post.platform, post.metrics)}</span>
            ) : null}
          </div>

          {sentiment && sentiment.positive + sentiment.neutral + sentiment.negative > 0 ? (
            <SentimentSummary breakdown={sentiment} />
          ) : null}

          <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
            <Button variant="outline" size="sm" onClick={onOpen}>
              <MessageSquare className="size-3.5" />
              {commentCount != null && commentCount > 0
                ? `${commentCount} komentar`
                : 'Komentar'}
            </Button>
            {link ? (
              <>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => navigator.clipboard.writeText(link).catch(() => undefined)}
                  title="Copy link"
                >
                  <Copy className="size-3.5" />
                </Button>
                <Button variant="ghost" size="sm" asChild>
                  <a href={link} target="_blank" rel="noreferrer noopener" title="Buka di Instagram">
                    <ExternalLink className="size-3.5" />
                  </a>
                </Button>
              </>
            ) : null}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

export function RankedPostList({
  posts,
  insights,
  commentsByPost,
  onOpen,
}: {
  posts: ScrapedPost[];
  /** Server-computed ranking. Empty falls back to the raw post order. */
  insights: PostInsight[];
  /** Comments already loaded per post id, for the sentiment preview. */
  commentsByPost: Record<string, ScrapedComment[]>;
  onOpen: (p: ScrapedPost) => void;
}) {
  const [sortBy, setSortBy] = useState<SortKey>('insight');

  const byId = useMemo(() => new Map(insights.map((i) => [i.postId, i])), [insights]);

  const ordered = useMemo(() => {
    const withInsight = posts.map((p) => ({ post: p, insight: byId.get(p.id) ?? null }));
    switch (sortBy) {
      case 'engagement':
        return [...withInsight].sort(
          (a, b) => (b.insight?.engagement ?? 0) - (a.insight?.engagement ?? 0),
        );
      case 'recent':
        return [...withInsight].sort((a, b) => {
          const at = a.post.publishedAt ?? a.post.scrapedAt;
          const bt = b.post.publishedAt ?? b.post.scrapedAt;
          return new Date(bt).getTime() - new Date(at).getTime();
        });
      default:
        // Server order IS the ranking; keep it stable.
        return withInsight;
    }
  }, [posts, byId, sortBy]);

  if (posts.length === 0) return null;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">
          {posts.length} post{posts.length === 1 ? '' : 's'}
          {insights.length > 0 ? ' · diperingkat berdasarkan insight' : ''}
        </p>
        <div className="flex items-center gap-1">
          <ArrowUpDown className="size-3.5 text-muted-foreground" aria-hidden />
          {(
            [
              ['insight', 'Insight'],
              ['engagement', 'Interaksi'],
              ['recent', 'Terbaru'],
            ] as const
          ).map(([k, label]) => (
            <Button
              key={k}
              variant={sortBy === k ? 'secondary' : 'ghost'}
              size="sm"
              className="h-7 px-2 text-xs"
              onClick={() => setSortBy(k)}
            >
              {label}
            </Button>
          ))}
        </div>
      </div>

      {ordered.map(({ post, insight }) => {
        const comments = commentsByPost[post.id];
        const sentiment = comments
          ? comments.reduce(
              (acc, c) => {
                if (c.sentiment === 'POSITIVE') acc.positive++;
                else if (c.sentiment === 'NEGATIVE') acc.negative++;
                else if (c.sentiment === 'NEUTRAL') acc.neutral++;
                else acc.unanalysed++;
                return acc;
              },
              { positive: 0, neutral: 0, negative: 0, unanalysed: 0 },
            )
          : undefined;
        return (
          <InsightCard
            key={post.id}
            post={post}
            insight={insight}
            sentiment={sentiment}
            commentCount={comments?.length}
            onOpen={() => onOpen(post)}
          />
        );
      })}
    </div>
  );
}

/** The comment dialog body: sentiment distribution + threaded comments. */
export function CommentPreview({
  comments,
  sentiment,
  authorHandle,
}: {
  comments: ScrapedComment[];
  sentiment?: { positive: number; neutral: number; negative: number; unanalysed: number };
  authorHandle: string;
}) {
  if (comments.length === 0) return null;
  return (
    <div className="space-y-3">
      {sentiment ? <SentimentSummary breakdown={sentiment} /> : null}
      <div className="max-h-[50vh] overflow-y-auto rounded-lg border border-border/60 p-2">
        <CommentThread comments={comments} />
      </div>
      <p className="text-xs text-muted-foreground">
        {comments.length} komentar untuk @{authorHandle}
        {comments.some((c) => c.sentiment === null)
          ? ` · ${comments.filter((c) => c.sentiment === null).length} belum dianalisa`
          : ' · semua sudah dianalisa'}
      </p>
    </div>
  );
}

export { SentimentBadge };
