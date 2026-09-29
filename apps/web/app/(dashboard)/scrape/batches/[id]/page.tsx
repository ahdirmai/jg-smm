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
  Check,
  Copy,
  ExternalLink,
  Loader2,
  MessageSquare,
  RefreshCw,
} from 'lucide-react';
import Link from 'next/link';
import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';

import { api, type KeywordBatch, type ScrapedComment, type ScrapedPost } from '@/lib/api';
import { metricsLine, PLATFORM_LABEL } from '@/lib/platforms';
import { PostMedia } from '@/components/post-media';

/** The public permalink for a stored post, rebuilt from its platform + shortcode. */
function postPermalink(p: ScrapedPost): string | null {
  const code = p.externalId?.trim();
  if (!code) return null;
  // Platform arrives as the API's domain constant (INSTAGRAM/THREADS/TIKTOK).
  const platform = p.platform?.toLowerCase();
  if (platform === 'threads') return `https://www.threads.com/post/${code}`;
  if (platform === 'instagram') return `https://www.instagram.com/p/${code}/`;
  // TikTok's permalink is derived the same way as the other two. The actor
  // also sends a ready-made `webVideoUrl` per row, but there is no column to
  // store it and both parts it needs — handle and video id — already are.
  if (platform === 'tiktok') {
    const handle = p.authorHandle?.trim();
    if (!handle || handle === 'unknown') return null;
    return `https://www.tiktok.com/@${handle}/video/${code}`;
  }
  return null;
}

/** Copy-to-clipboard button. Falls back to a hidden textarea where the async
 *  Clipboard API is unavailable (non-secure origin), so it never silently no-ops. */
function CopyButton({ value, label }: { value: string; label: string }) {
  const [done, setDone] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      const el = document.createElement('textarea');
      el.value = value;
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      try {
        document.execCommand('copy');
      } catch {
        return;
      } finally {
        document.body.removeChild(el);
      }
    }
    setDone(true);
    setTimeout(() => setDone(false), 1500);
  }
  return (
    <Button variant="outline" size="sm" onClick={copy} aria-label={label}>
      {done ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
      {done ? 'Copied' : 'Copy'}
    </Button>
  );
}

export default function BatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [batch, setBatch] = useState<KeywordBatch | null>(null);
  const [posts, setPosts] = useState<ScrapedPost[]>([]);
  const [err, setErr] = useState<string | null>(null);
  // Post whose comments are open. Null = dialog closed; the comments load on
  // open so the batch page does not pay for 50 comment fetches up front.
  const [detail, setDetail] = useState<ScrapedPost | null>(null);
  const [comments, setComments] = useState<ScrapedComment[] | null>(null);
  const [commentErr, setCommentErr] = useState<string | null>(null);
  // A paid actor run is in flight for this post (the comments button).
  const [fetchingComments, setFetchingComments] = useState(false);

  async function load() {
    try {
      const b = await api.getKeywordBatch(id);
      setBatch(b.batch);
      if (b.batch.status === 'SUCCEEDED') {
        const pr = await api.listKeywordBatchPosts(id, { limit: 100 });
        setPosts(pr.posts);
      }
      setErr(null);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

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
            {PLATFORM_LABEL[batch.platform.toLowerCase()] ?? batch.platform} · {batch.actorId} · max {batch.maxPosts} ·{' '}
            {batch.windowFrom ? new Date(batch.windowFrom).toLocaleDateString() : '—'} →{' '}
            {batch.windowTo ? new Date(batch.windowTo).toLocaleDateString() : '—'} · created {new Date(batch.createdAt).toLocaleString()}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div className="flex flex-wrap gap-4 text-muted-foreground">
            <span>{batch.postsCount} posts</span>
            <span>· {batch.commentsCount} comments</span>
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
            <div className="max-h-[600px] space-y-2 overflow-y-auto">
              {posts.map((p) => {
                const link = postPermalink(p);
                return (
                  <div key={p.id} className="flex gap-3 rounded-lg border px-3 py-2">
                    {/* Thumbnail: the batch is a discovery surface, so the operator
                        picks posts by what they look like. Clicking it opens the
                        same detail dialog as the Detail button. */}
                    {p.mediaUrls?.[0] ? (
                      <button
                        type="button"
                        onClick={() => void openDetail(p)}
                        aria-label={`Open detail for @${p.authorHandle}`}
                        className="h-16 w-16 shrink-0 overflow-hidden rounded-md border border-border/60 bg-secondary/40 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      >
                        {/* eslint-disable-next-line @next/next/no-img-element -- remote user content on rotating CDN hosts */}
                        <img
                          src={p.mediaUrls[0]}
                          alt=""
                          loading="lazy"
                          draggable={false}
                          className="h-full w-full object-cover"
                        />
                      </button>
                    ) : null}
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        <span className="font-medium text-foreground">@{p.authorHandle || 'unknown'}</span>
                        {p.publishedAt ? (
                          <span title="Tanggal publikasi post">{new Date(p.publishedAt).toLocaleDateString()}</span>
                        ) : (
                          <span title="Tanggal publikasi tidak tersedia untuk post ini — menampilkan kapan post di-scrape" className="text-muted-foreground/70">
                           scrape {new Date(p.scrapedAt).toLocaleDateString()}
                          </span>
                        )}
                        {p.metrics ? <span>{metricsLine(p.platform, p.metrics)}</span> : null}
                        <div className="ml-auto flex items-center gap-1">
                          {link ? (
                            <>
                              <CopyButton value={link} label={`Copy link to @${p.authorHandle}`} />
                              <Button variant="outline" size="sm" asChild>
                                <a href={link} target="_blank" rel="noreferrer noopener">
                                  <ExternalLink className="size-3.5" />
                                  Open
                                </a>
                              </Button>
                            </>
                          ) : null}
                          <Button variant="outline" size="sm" onClick={() => void openDetail(p)}>
                            <MessageSquare className="size-3.5" />
                            Detail
                          </Button>
                        </div>
                      </div>
                      {p.text ? <p className="mt-1 line-clamp-3 text-sm">{p.text}</p> : <p className="mt-1 text-sm italic text-muted-foreground">No text</p>}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
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
              ) : null}
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
                  <ul className="space-y-2">
                    {comments.map((c) => (
                      <li key={c.id} className="rounded-lg border border-border/60 px-3 py-2">
                        <div className="text-xs font-medium text-foreground">@{c.authorHandle || 'unknown'}</div>
                        <p className="mt-0.5 whitespace-pre-wrap text-sm">{c.text}</p>
                      </li>
                    ))}
                  </ul>
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
