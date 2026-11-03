'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@smm/ui';
import {
  AlertCircle,
  ArrowLeft,
  ExternalLink,
  Loader2,
  Megaphone,
  MessageSquare,
  RefreshCw,
} from 'lucide-react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';

import { api, type KeywordBatch, type PostInsight, type ScrapedComment, type ScrapedPost } from '@/lib/api';
import { metricsLine, platformLabel } from '@/lib/platforms';
import { actionDeepLink, postPermalink } from '@/lib/permalink';
import { CopyButton } from '@/components/copy-button';
import { PostMedia } from '@/components/post-media';
import { CommentPreview, RankedPostList } from '@/components/ranked-post-list';

/** Posts per page. The fetch asks for one more than this purely to detect a
 *  next page, so the pager never offers one that does not exist. */
const PAGE_SIZE = 50;

export default function BatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [batch, setBatch] = useState<KeywordBatch | null>(null);
  // Posts are paged client-side through the server's limit/offset. The fetch
  // asks for PAGE_SIZE+1 and keeps the extra row only as a "there is more"
  // flag, so there is no count query and no page that renders a phantom next
  // button on a batch of exactly PAGE_SIZE posts.
  const [page, setPage] = useState(0);
  const [posts, setPosts] = useState<ScrapedPost[]>([]);
  // Server-computed ranking for the CURRENT page of posts.
  const [insights, setInsights] = useState<PostInsight[]>([]);
  // Comments already fetched per post id, so a card can show its sentiment
  // distribution without opening the dialog (which is what made the operator
  // click every post to compare them).
  const [commentsByPost, setCommentsByPost] = useState<Record<string, ScrapedComment[]>>({});
  const [hasMore, setHasMore] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // Post whose comments are open. Null = dialog closed; the comments load on
  // open so the batch page does not pay for 50 comment fetches up front.
  const [detail, setDetail] = useState<ScrapedPost | null>(null);
  const [comments, setComments] = useState<ScrapedComment[] | null>(null);
  const [commentErr, setCommentErr] = useState<string | null>(null);
  // A paid actor run is in flight for this post (the comments button).
  const [fetchingComments, setFetchingComments] = useState(false);

  // The batch header and the post page are fetched separately: the header is
  // polled while the batch runs, the posts only when the batch has settled.
  // Folding them together meant a page change could not refetch anything,
  // because the polling effect below deliberately stops once `settled` is
  // true — which is exactly the state a finished batch sits in.
  async function loadBatch(): Promise<KeywordBatch | null> {
    try {
      const b = await api.getKeywordBatch(id);
      setBatch(b.batch);
      setErr(null);
      return b.batch;
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      return null;
    }
  }

  async function loadPosts() {
    try {
      const pr = await api.listKeywordBatchPosts(id, { limit: PAGE_SIZE + 1, offset: page * PAGE_SIZE });
      setHasMore(pr.posts.length > PAGE_SIZE);
      const pagePosts = pr.posts.slice(0, PAGE_SIZE);
      setPosts(pagePosts);

      // Ranking: the SERVER scores, this page only renders the order it gets.
      // Computed over the same page slice, so a card's rank is its rank within
      // what the operator can see — a global rank would number a page that
      // starts at "27" and read as broken.
      try {
        const r = await api.rankedRecent(200);
        const byId = new Map(r.insights.map((i) => [i.postId, i]));
        setInsights(pagePosts.map((p) => byId.get(p.id)).filter((i): i is PostInsight => Boolean(i)));
      } catch {
        // Ranking is an enhancement; without it the list falls back to the raw
        // post order and the cards simply show no rank badge.
        setInsights([]);
      }

      // Comments already stored for this page's posts — one read per post, no
      // Apify spend. They give the cards a sentiment preview before any click.
      const withComments = await Promise.all(
        pagePosts.map(async (p) => {
          try {
            const res = await api.listPostComments(p.id, { limit: 100 });
            return [p.id, res.comments ?? []] as const;
          } catch {
            return [p.id, []] as const;
          }
        }),
      );
      setCommentsByPost(Object.fromEntries(withComments));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  // Reads the status off the response, not off the `batch` state: the closure
  // would still hold the previous render's value, so the first load would skip
  // the posts fetch entirely and leave the table empty.
  async function load() {
    const b = await loadBatch();
    if (b === null || b.status === 'SUCCEEDED') await loadPosts();
  }

  // Stops on a terminal status. The 3s interval previously ran for the life of
  // the tab, so a finished batch — the state this page rests in almost all the
  // time it is open — re-fetched the batch and its posts ~1,200 times an hour
  // to redisplay identical rows.
  const settled = batch !== null && (batch.status === 'SUCCEEDED' || batch.status === 'FAILED');
  useEffect(() => {
    void load();
    if (settled) return;
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, settled]);

  // Page changes must refetch on their own. This cannot hang off the polling
  // effect above, which returns early the moment the batch is settled — and a
  // settled batch is the only state in which the pager is reachable.
  useEffect(() => {
    if (settled) void loadPosts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, settled]);

  async function openDetail(p: ScrapedPost) {
    setDetail(p);
    setComments(null);
    setCommentErr(null);
    try {
      const res = await api.listPostComments(p.id, { limit: 100 });
      setComments(res.comments ?? []);
    } catch (e) {
      setCommentErr(e instanceof Error ? e.message : String(e));
    }
  }

  /**
   * Fetch this post's comments from Apify. The search actor returns post rows
   * with no comment bodies, so the comments only exist if a per-post scrape is
   * run — a paid actor call. That is why it is a button and not automatic: the
   * operator decides which posts are worth the run.
   *
   * force=true bypasses the 6h freshness cache. Without it this would be a
   * no-op for any post a batch ingested within the last 6 hours, and would
   * silently return the same empty list.
   */
  async function fetchComments(p: ScrapedPost) {
    const url = postPermalink(p);
    if (!url) {
      setCommentErr('No permalink for this post, cannot fetch comments.');
      return;
    }
    setFetchingComments(true);
    setCommentErr(null);
    try {
      const res = await api.scrapeTarget(p.platform, url, true);
      setComments(res.comments ?? []);
      // The re-scrape re-upserts the post row, so refresh the page's own copy
      // rather than leaving a stale one behind the dialog.
      setPosts((ps) => ps.map((x) => (x.id === p.id ? res.post : x)));
    } catch (e) {
      setCommentErr(e instanceof Error ? e.message : String(e));
    } finally {
      setFetchingComments(false);
    }
  }

  if (err) {
    return (
      <div className="mx-auto max-w-6xl space-y-4">
        <BackLink />
        <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertCircle className="size-4" /> {err}
        </div>
      </div>
    );
  }
  if (!batch) {
    return (
      <div className="mx-auto max-w-6xl space-y-4">
        <BackLink />
        <div className="flex items-center gap-2 py-10 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> Loading batch…
        </div>
      </div>
    );
  }

  const shareUrl = typeof window !== 'undefined' ? window.location.href : '';

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <BackLink />
        <CopyButton value={shareUrl} label="Copy link to this batch" />
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            Batch: {batch.keywords.join(', ')}
            <Badge variant={batch.status === 'SUCCEEDED' ? 'default' : batch.status === 'FAILED' ? 'destructive' : 'secondary'}>
              {batch.status}
            </Badge>
          </CardTitle>
          <CardDescription>
            {platformLabel(batch.platform) ?? batch.platform} · {batch.actorId} · max {batch.maxPosts} ·{' '}
            {batch.windowFrom ? new Date(batch.windowFrom).toLocaleDateString() : '—'} →{' '}
            {batch.windowTo ? new Date(batch.windowTo).toLocaleDateString() : '—'} · created {new Date(batch.createdAt).toLocaleString()}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div className="flex flex-wrap gap-4 text-muted-foreground">
            <span>{batch.postsCount} posts</span>
            {/* A keyword run stores the post rows but never their comment bodies,
                so commentsCount is 0 for most batches. A bare 0 reads as "this
                post has no comments", which is a different claim; the per-post
                Detail → Fetch comments button is what actually fetches them. */}
            <span>· comment bodies not fetched</span>
            <span>· {batch.itemsRead} items read</span>
            {batch.finishedAt ? <span>· finished {new Date(batch.finishedAt).toLocaleString()}</span> : <span>· running…</span>}
          </div>
          {batch.error ? <p className="text-destructive">{batch.error}</p> : null}
          {batch.status === 'PENDING' || batch.status === 'RUNNING' ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> Background scrape berjalan… halaman auto-refresh.
            </p>
          ) : null}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Posts — dashboard batch</CardTitle>
          <CardDescription>
            Hasil batch ini (beda dari post satuan di /scrape/target atau /actions). Klik satu post untuk
            melihat detail dan komentar yang tersimpan.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {posts.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {batch.status === 'SUCCEEDED' ? 'Tidak ada post untuk keyword/window ini.' : 'Menunggu batch selesai…'}
            </p>
          ) : (
            <div className="max-h-[720px] overflow-y-auto pr-1">
              <RankedPostList
                posts={posts}
                insights={insights}
                commentsByPost={commentsByPost}
                onOpen={(p) => void openDetail(p)}
              />
            </div>
          )}
          {posts.length > 0 ? (
            <div className="mt-3 flex items-center justify-between gap-2 text-xs text-muted-foreground">
              <span>
                Menampilkan {page * PAGE_SIZE + 1}–{page * PAGE_SIZE + posts.length} dari {batch.postsCount}{' '}
                post
              </span>
              <div className="flex gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={page === 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  Sebelumnya
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!hasMore}
                  onClick={() => setPage((p) => p + 1)}
                >
                  Berikutnya
                </Button>
              </div>
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Dialog open={!!detail} onOpenChange={(o) => !o && setDetail(null)}>
        <DialogContent className="sm:max-w-[640px]">
          <DialogHeader>
            <DialogTitle>@{detail?.authorHandle || 'unknown'}</DialogTitle>
            <DialogDescription>
              {detail ? `${detail.platform} · ${detail.externalId} · scraped ${new Date(detail.scrapedAt).toLocaleString()}` : ''}
            </DialogDescription>
          </DialogHeader>
          {detail ? (
            <div className="max-h-[60vh] space-y-4 overflow-y-auto">
              {detail.mediaUrls && detail.mediaUrls.length > 0 ? (
                <PostMedia urls={detail.mediaUrls} />
              ) : (
                // Said out loud on purpose. A silent gap here is how a whole
                // platform's media went missing and looked identical to a post
                // that genuinely has none.
                <p className="rounded-lg border border-dashed border-border bg-secondary/30 p-3 text-sm text-muted-foreground">
                  Tidak ada media untuk post ini. Kalau postnya jelas punya
                  video atau foto, berarti provider tidak mengembalikan URL
                  media — bukan post tanpa media.
                </p>
              )}
              {detail.text ? (
                <p className="whitespace-pre-wrap rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm">
                  {detail.text}
                </p>
              ) : (
                <p className="text-sm italic text-muted-foreground">Post ini tidak punya teks.</p>
              )}
              {detail.metrics ? (
                <p className="text-xs text-muted-foreground">{metricsLine(detail.platform, detail.metrics)}</p>
              ) : null}
              {postPermalink(detail) ? (
                <div className="flex items-center gap-2">
                  <span className="truncate font-mono text-xs text-muted-foreground">{postPermalink(detail)}</span>
                  <CopyButton value={postPermalink(detail)!} label="Copy post link" />
                  <Button variant="outline" size="sm" asChild>
                    <a href={postPermalink(detail)!} target="_blank" rel="noreferrer noopener">
                      <ExternalLink className="size-3.5" />
                      Open
                    </a>
                  </Button>
                </div>
              ) : null}

              {/* The bridge from discovery to action. The batch page is where a
                  post is FOUND; like/comment/reply all live on /actions. Without
                  this the operator retypes or copies a permalink by hand, which
                  is the step that gets the wrong post acted on.
                  Rendered only when the platform is on-demand scrapable, because
                  /actions loads the post through that endpoint and the other five
                  platforms answer 400 — a button that always errors teaches the
                  operator the feature is broken, not that it does not apply. */}
              {actionDeepLink(detail) ? (
                <Button asChild className="w-full">
                  <Link href={actionDeepLink(detail)!}>
                    <Megaphone className="size-4" />
                    Buat Action untuk post ini
                  </Link>
                </Button>
              ) : null}

              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="flex items-center gap-2 text-sm font-medium">
                    <MessageSquare className="size-4" />
                    Komentar
                    {comments !== null && comments.length > 0 ? (
                      <Badge variant="outline" className="font-normal">
                        {comments.length}
                      </Badge>
                    ) : null}
                  </span>
                  {/* The search actor returns no comment bodies, so this is a
                      separate paid run. Disabled while one is already going,
                      and hidden once there is nothing left to fetch. */}
                  {postPermalink(detail) ? (
                    <Button
                      variant="outline"
                      size="sm"
                      className="ml-auto"
                      disabled={fetchingComments}
                      onClick={() => void fetchComments(detail)}
                    >
                      {fetchingComments ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <RefreshCw className="size-3.5" />
                      )}
                      {fetchingComments
                        ? 'Scraping…'
                        : comments && comments.length > 0
                          ? 'Refresh'
                          : 'Fetch comments'}
                    </Button>
                  ) : null}
                </div>
                {commentErr ? (
                  <p className="text-sm text-destructive">{commentErr}</p>
                ) : comments === null ? (
                  <p className="flex items-center gap-2 text-sm text-muted-foreground">
                    <Loader2 className="size-4 animate-spin" /> Memuat komentar…
                  </p>
                ) : comments.length === 0 ? (
                  <p className="text-sm text-muted-foreground">
                    Belum ada komentar. Actor search tidak mengambil isi komentar — pakai “Fetch
                    comments” di atas.
                  </p>
                ) : (
                  // Threaded, with the sentiment distribution above it: the same
                  // preview the ranked card shows, so the dialog adds depth
                  // rather than switching to a different reading model.
                  <CommentPreview
                    comments={comments}
                    sentiment={comments.reduce(
                      (acc, c) => {
                        if (c.sentiment === 'POSITIVE') acc.positive++;
                        else if (c.sentiment === 'NEGATIVE') acc.negative++;
                        else if (c.sentiment === 'NEUTRAL') acc.neutral++;
                        else acc.unanalysed++;
                        return acc;
                      },
                      { positive: 0, neutral: 0, negative: 0, unanalysed: 0 },
                    )}
                    authorHandle={detail.authorHandle}
                  />
                )}
              </div>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
    </div>
  );
}

function BackLink() {
  return (
    <Button variant="ghost" size="sm" asChild>
      <Link href="/scrape">
        <ArrowLeft className="size-4" />
        Back to keyword batches
      </Link>
    </Button>
  );
}
