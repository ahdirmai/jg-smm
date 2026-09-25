'use client';

import { Badge, Card, CardContent, CardDescription, CardHeader, CardTitle } from '@smm/ui';
import { AlertCircle, Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useParams } from 'next/navigation';

import { api, type KeywordBatch, type ScrapedPost } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

function metricsLine(m: Record<string, number> | null): string {
  if (!m) return '';
  const parts: string[] = [];
  for (const key of ['likes', 'likeCount', 'comments', 'commentCount', 'views', 'viewCount', 'playCount']) {
    if (typeof m[key] === 'number') parts.push(`${key}: ${m[key]}`);
  }
  return parts.join(' · ');
}

export default function BatchDetailPage() {
  const { id } = useParams<{ id: string }>();
  const [batch, setBatch] = useState<KeywordBatch | null>(null);
  const [posts, setPosts] = useState<ScrapedPost[]>([]);
  const [err, setErr] = useState<string | null>(null);

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

  if (err) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
        <AlertCircle className="size-4" /> {err}
      </div>
    );
  }
  if (!batch) {
    return (
      <div className="flex items-center gap-2 py-10 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Loading batch…
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            Batch: {batch.keywords.join(', ')}
            <Badge variant={batch.status === 'SUCCEEDED' ? 'default' : batch.status === 'FAILED' ? 'destructive' : 'secondary'}>
              {batch.status}
            </Badge>
          </CardTitle>
          <CardDescription>
            {PLATFORM_LABEL[batch.platform as 'instagram' | 'threads'] ?? batch.platform} · {batch.actorId} · max {batch.maxPosts} ·{' '}
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
          <CardDescription>Hasil batch ini (beda dari post satuan di /scrape/target atau /actions). Klik author untuk detail post satuan.</CardDescription>
        </CardHeader>
        <CardContent>
          {posts.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {batch.status === 'SUCCEEDED' ? 'Tidak ada post untuk keyword/window ini.' : 'Menunggu batch selesai…'}
            </p>
          ) : (
            <div className="max-h-[600px] space-y-2 overflow-y-auto">
              {posts.map((p) => (
                <div key={p.id} className="rounded-lg border px-3 py-2">
                  <div className="flex items-center gap-2 text-xs text-muted-foreground">
                    <span className="font-medium text-foreground">@{p.authorHandle || 'unknown'}</span>
                    <span>{new Date(p.scrapedAt).toLocaleDateString()}</span>
                    {p.metrics ? <span>{metricsLine(p.metrics)}</span> : null}
                  </div>
                  {p.text ? <p className="mt-1 line-clamp-3 text-sm">{p.text}</p> : <p className="mt-1 text-sm italic text-muted-foreground">No text</p>}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
