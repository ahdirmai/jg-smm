'use client';

/**
 * Keyword Scrape (dashboard tool). Flow: pick platform → type up to 5 keywords
 * → optional date window + max posts → Search (runs the platform's Apify search
 * actor synchronously) → the stored posts come back, freshest first. The rows
 * land in the same post/comment tables as the on-demand scrape, so they are
 * reusable by every scrape-driven feature.
 */

import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label, Badge } from '@smm/ui';
import { AlertCircle, Loader2, Search, ExternalLink } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useState } from 'react';

import { api, type KeywordBatch } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

const MAX_KEYWORDS = 5;

export function KeywordScrapeForm() {
  const [platform, setPlatform] = useState<'instagram' | 'threads'>('instagram');
  const [keywords, setKeywords] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [maxPosts, setMaxPosts] = useState('50');
  const [busy, setBusy] = useState(false);
  const [batches, setBatches] = useState<KeywordBatch[]>([]);
  const [error, setError] = useState<string | null>(null);

  const parsed = keywords.split(',').map((k) => k.trim()).filter(Boolean);
  const deduped = Array.from(new Map(parsed.map((k) => [k.toLowerCase(), k])).values());
  const tooMany = deduped.length > MAX_KEYWORDS;

  async function refreshBatches() {
    try {
      const res = await api.listKeywordBatches({ limit: 20 });
      setBatches(res.batches);
    } catch {
      // silent — polling
    }
  }
  useEffect(() => {
    refreshBatches();
    const id = setInterval(refreshBatches, 4000);
    return () => clearInterval(id);
  }, []);

  async function runSearch() {
    setError(null);
    if (deduped.length === 0) {
      setError('Type at least one keyword.');
      return;
    }
    if (tooMany) return;
    setBusy(true);
    try {
      await api.scrapeKeywords({
        platform,
        keywords: deduped,
        from: from || undefined,
        to: to || undefined,
        maxPosts: Number(maxPosts) || undefined,
      });
      await refreshBatches();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Keyword scrape failed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Keyword scrape</CardTitle>
        <CardDescription>
          Search {PLATFORM_LABEL[platform] ?? platform} by up to {MAX_KEYWORDS} keywords inside an
          optional date window. Results are stored and reusable by the action queue.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="space-y-1.5">
            <Label htmlFor="ks-platform">Platform</Label>
            <select
              id="ks-platform"
              value={platform}
              onChange={(e) => setPlatform(e.target.value as 'instagram' | 'threads')}
              className="h-9 w-full rounded-lg border border-input bg-transparent px-3 text-sm"
            >
              <option value="instagram">Instagram</option>
              <option value="threads">Threads</option>
            </select>
          </div>
          <div className="space-y-1.5 sm:col-span-2">
            <Label htmlFor="ks-keywords">Keywords (comma-separated, max {MAX_KEYWORDS})</Label>
            <Input
              id="ks-keywords"
              value={keywords}
              onChange={(e) => setKeywords(e.target.value)}
              placeholder="kamu, gacor, review"
            />
          </div>
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="space-y-1.5">
            <Label htmlFor="ks-from">From (optional)</Label>
            <Input id="ks-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ks-to">To (optional)</Label>
            <Input id="ks-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="ks-max">Max posts</Label>
            <Input
              id="ks-max"
              type="number"
              min={1}
              max={200}
              value={maxPosts}
              onChange={(e) => setMaxPosts(e.target.value)}
            />
          </div>
        </div>

        <div className="flex items-center gap-3">
          <Button onClick={runSearch} disabled={busy || deduped.length === 0 || tooMany}>
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Search className="size-4" />}
            {busy ? 'Searching…' : 'Search'}
          </Button>
          {deduped.length > 0 ? (
            <span className="text-xs text-muted-foreground">
              {deduped.length} keyword{deduped.length === 1 ? '' : 's'}
            </span>
          ) : null}
          {tooMany ? (
            <span className="text-xs text-destructive">Max {MAX_KEYWORDS} keywords</span>
          ) : null}
        </div>

        {error ? (
          <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertCircle className="size-4" />
            {error}
          </div>
        ) : null}

        {/* Batch list — background scrape. Klik batch → dashboard batch (beda dari satuan post). */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium">Batches (background)</h3>
            <span className="text-xs text-muted-foreground">{batches.length} batch</span>
          </div>
          {batches.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">Belum ada batch. Jalankan search di atas.</p>
          ) : (
            <div className="divide-y rounded-lg border">
              {batches.map((b) => (
                <Link key={b.id} href={`/scrape/batches/${b.id}`} className="flex items-center justify-between px-3 py-2 hover:bg-muted/50">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={b.status === 'SUCCEEDED' ? 'default' : b.status === 'FAILED' ? 'destructive' : 'secondary'}>{b.status}</Badge>
                      <span className="text-sm font-medium">{b.keywords.join(', ')}</span>
                      <Badge variant="outline">{PLATFORM_LABEL[b.platform as 'instagram' | 'threads'] ?? b.platform}</Badge>
                    </div>
                    <div className="mt-1 flex flex-wrap gap-2 text-xs text-muted-foreground">
                      <span>{b.postsCount} posts · {b.commentsCount} comments · {b.itemsRead} items</span>
                      <span>· {new Date(b.createdAt).toLocaleString()}</span>
                      {b.error ? <span className="text-destructive">{b.error}</span> : null}
                    </div>
                  </div>
                  <ExternalLink className="ml-2 size-4 shrink-0 text-muted-foreground" />
                </Link>
              ))}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
