'use client';

/**
 * Keyword Scrape (dashboard tool). Flow: pick platform → type keywords as
 * chips → optional date window + max posts → Search (runs the platform's Apify
 * search actor asynchronously) → the stored posts come back, freshest first.
 * The rows land in the same post/comment tables as the on-demand scrape, so
 * they are reusable by every scrape-driven feature.
 *
 * Three things this surface has to answer without the operator opening the API
 * logs, because a keyword run is SLOW (40-60s per keyword) and sometimes
 * expensive:
 *   - which batches are mine and what state are they in (live list, polled);
 *   - why a failed batch failed (the actor's own reason, straight from Apify);
 *   - what a batch actually produced (posts vs items read vs comments, which
 *     are NOT the same number and used to be presented as if they were).
 */

import { Badge, Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from '@smm/ui';
import { AlertCircle, CheckCircle2, ExternalLink, Loader2, Search, X, XCircle } from 'lucide-react';
import Link from 'next/link';
import { useEffect, useMemo, useState } from 'react';

import { api, type KeywordBatch } from '@/lib/api';
import { PLATFORMS, PLATFORM_LABEL, platformLabel, type Platform } from '@/lib/platforms';

const MAX_KEYWORDS = 5;
const MAX_POSTS = 200;

type StatusFilter = 'all' | KeywordBatch['status'];

export function KeywordScrapeForm() {
  const [platform, setPlatform] = useState<Platform>('instagram');
  const [draft, setDraft] = useState('');
  const [keywords, setKeywords] = useState<string[]>([]);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [maxPosts, setMaxPosts] = useState('50');
  const [busy, setBusy] = useState(false);
  const [batches, setBatches] = useState<KeywordBatch[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Batch-list filters. Client-side over the 20 most recent batches: the
  // endpoint has no filter parameters and 20 rows is smaller than a round trip.
  const [listPlatform, setListPlatform] = useState<string>('all');
  const [listStatus, setListStatus] = useState<StatusFilter>('all');
  const [listQuery, setListQuery] = useState('');

  /** Commit the typed text as a chip. Enter in the field does the same, and so
   *  does a comma or a paste — an operator pasting "a, b, c" should not have to
   *  retype it to get three chips. */
  function commitDraft(raw = draft) {
    const parts = raw
      .split(',')
      .map((k) => k.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    setKeywords((cur) => {
      // Case-insensitive dedupe: "Batulicin" and "batulicin" are one search on
      // every platform's engine, and paying twice for it is pure waste.
      const seen = new Set(cur.map((k) => k.toLowerCase()));
      const next = [...cur];
      for (const k of parts) {
        const key = k.toLowerCase();
        if (seen.has(key) || next.length >= MAX_KEYWORDS) continue;
        seen.add(key);
        next.push(k);
      }
      return next;
    });
    setDraft('');
  }

  function removeKeyword(k: string) {
    setKeywords((cur) => cur.filter((x) => x !== k));
  }

  async function refreshBatches() {
    try {
      const res = await api.listKeywordBatches({ limit: 20 });
      setBatches(res.batches);
    } catch {
      // silent — polling
    }
  }
  // Poll only while something is actually running. This used to arm a 4s timer
  // on mount and never disarm it, so a tab left open after the last batch
  // settled kept hitting /api/scrape/keyword-batches ~900 times an hour until
  // the tab was closed. The dependency flips the effect once the last in-flight
  // batch reaches a terminal state, which clears the interval.
  const hasActiveBatch = batches.some((b) => b.status === 'PENDING' || b.status === 'RUNNING');
  useEffect(() => {
    refreshBatches();
    if (!hasActiveBatch) return;
    const id = setInterval(refreshBatches, 4000);
    return () => clearInterval(id);
  }, [hasActiveBatch]);

  // A run that just went from RUNNING to failed should explain itself without
  // the operator hunting through the list. Tracked by id so a refresh that
  // re-renders the same rows does not re-announce.
  const failedNow = batches.filter((b) => b.status === 'FAILED' && b.error).slice(0, 1)[0];

  async function runSearch() {
    setError(null);
    setNotice(null);
    if (draft.trim()) commitDraft();
    const all = draft.trim()
      ? Array.from(new Map([...keywords, ...draft.split(',').map((k) => k.trim())].filter(Boolean).map((k) => [k.toLowerCase(), k])).values())
      : keywords;
    if (all.length === 0) {
      setError('Add at least one keyword.');
      return;
    }
    setBusy(true);
    try {
      await api.scrapeKeywords({
        platform,
        keywords: all,
        from: from || undefined,
        to: to || undefined,
        maxPosts: Number(maxPosts) || undefined,
      });
      setKeywords([]);
      await refreshBatches();
      setNotice(
        `Search started for ${all.length} keyword${all.length === 1 ? '' : 's'} on ${
          PLATFORM_LABEL[platform]
        }. It runs in the background — the batch appears below and updates on its own.`,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Keyword scrape failed');
    } finally {
      setBusy(false);
    }
  }

  const visible = useMemo(() => {
    const needle = listQuery.trim().toLowerCase();
    return batches.filter((b) => {
      if (listPlatform !== 'all' && b.platform.toLowerCase() !== listPlatform) return false;
      if (listStatus !== 'all' && b.status !== listStatus) return false;
      if (needle && !b.keywords.join(' ').toLowerCase().includes(needle)) return false;
      return true;
    });
  }, [batches, listPlatform, listStatus, listQuery]);

  const activeCount = batches.filter((b) => b.status === 'PENDING' || b.status === 'RUNNING').length;
  const filtering = listPlatform !== 'all' || listStatus !== 'all' || listQuery !== '';

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Keyword scrape</CardTitle>
          <CardDescription>
            Search {PLATFORM_LABEL[platform] ?? platform} by up to {MAX_KEYWORDS} keywords inside an
            optional date window. Results are stored and reusable by the action queue.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* A real <form>, so Enter in the keyword input searches. As a bare
              div the keystroke did nothing: the operator had to find the button
              with the mouse, on a form whose whole purpose is typing keywords. */}
          <form
            className="space-y-4"
            onSubmit={(e) => {
              e.preventDefault();
              void runSearch();
            }}
          >
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="space-y-1.5">
                <Label htmlFor="ks-platform">Platform</Label>
                <select
                  id="ks-platform"
                  value={platform}
                  onChange={(e) => setPlatform(e.target.value as Platform)}
                  className="h-9 w-full rounded-lg border border-input bg-transparent px-3 text-sm"
                >
                  {PLATFORMS.map((p) => (
                    <option key={p} value={p}>
                      {PLATFORM_LABEL[p]}
                    </option>
                  ))}
                </select>
              </div>
              <div className="space-y-1.5 sm:col-span-2">
                <Label htmlFor="ks-keywords">
                  Keywords{' '}
                  <span className="font-normal text-muted-foreground">
                    ({keywords.length}/{MAX_KEYWORDS} — comma or Enter to add)
                  </span>
                </Label>
                <Input
                  id="ks-keywords"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  onKeyDown={(e) => {
                    // Enter commits a chip instead of submitting the form the
                    // moment one keyword is typed — the old single field made
                    // Enter fire the paid run on a half-typed list.
                    if (e.key === 'Enter' || e.key === ',') {
                      e.preventDefault();
                      commitDraft();
                    } else if (e.key === 'Backspace' && draft === '' && keywords.length > 0) {
                      removeKeyword(keywords[keywords.length - 1]!);
                    }
                  }}
                  onBlur={() => commitDraft()}
                  onPaste={(e) => {
                    const text = e.clipboardData.getData('text');
                    if (text.includes(',')) {
                      e.preventDefault();
                      commitDraft(text);
                    }
                  }}
                  placeholder={
                    keywords.length >= MAX_KEYWORDS ? 'Maximum reached' : 'e.g. batulicin, jhonlin'
                  }
                  disabled={keywords.length >= MAX_KEYWORDS}
                />
                {keywords.length > 0 ? (
                  <div className="flex flex-wrap gap-1.5 pt-0.5">
                    {keywords.map((k) => (
                      <span
                        key={k}
                        className="inline-flex items-center gap-1 rounded-full border border-border bg-secondary/60 py-0.5 pl-2.5 pr-1 text-xs"
                      >
                        {k}
                        <button
                          type="button"
                          onClick={() => removeKeyword(k)}
                          aria-label={`Remove keyword ${k}`}
                          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
                        >
                          <X className="size-3" />
                        </button>
                      </span>
                    ))}
                  </div>
                ) : null}
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
                <Label htmlFor="ks-max">
                  Max posts{' '}
                  <span className="font-normal text-muted-foreground">(per keyword, 1–{MAX_POSTS})</span>
                </Label>
                <Input
                  id="ks-max"
                  type="number"
                  min={1}
                  max={MAX_POSTS}
                  value={maxPosts}
                  onChange={(e) => setMaxPosts(e.target.value)}
                />
              </div>
            </div>

            {/* A window the window-less actors cannot enforce is worth saying
                out loud: TikTok and X have no date input at all, and the
                ingestor drops out-of-window rows AFTER the actor was paid. A
                silently-ignored filter is the shape that makes an operator
                believe a keyword had no recent posts. */}
            {from || to ? (
              <p className="text-xs text-muted-foreground">
                {platform === 'tiktok' || platform === 'x'
                  ? `${PLATFORM_LABEL[platform]}’s actor has no date filter: the window is applied after the run, so you still pay for rows that get dropped.`
                  : platform === 'youtube'
                    ? 'YouTube accepts only relative buckets (hour/day/week/month/year), so the exact window is applied after the run.'
                    : 'The window is sent to the actor where it supports one, and applied to the stored rows either way.'}
              </p>
            ) : null}

            <div className="flex flex-wrap items-center gap-3">
              <Button type="submit" disabled={busy || (keywords.length === 0 && draft.trim() === '')}>
                {busy ? <Loader2 className="size-4 animate-spin" /> : <Search className="size-4" />}
                {busy ? 'Starting…' : 'Search'}
              </Button>
              {keywords.length > 0 ? (
                <span className="text-xs text-muted-foreground">
                  {keywords.length} keyword{keywords.length === 1 ? '' : 's'} · up to{' '}
                  {Number(maxPosts) || 0} post{Number(maxPosts) === 1 ? '' : 's'} each
                </span>
              ) : null}
            </div>

            {error ? (
              <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="mt-0.5 size-4 shrink-0" />
                {error}
              </div>
            ) : null}
            {notice ? (
              <div className="flex items-start gap-2 rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm">
                <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
                {notice}
              </div>
            ) : null}
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-start justify-between space-y-0">
          <div>
            <CardTitle className="flex items-center gap-2">
              Batches
              {activeCount > 0 ? (
                <Badge variant="secondary" className="font-normal">
                  {activeCount} running
                </Badge>
              ) : null}
            </CardTitle>
            <CardDescription>
              Background keyword runs. Click one for its posts and comments.
            </CardDescription>
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-1.5">
            <Chip label="All platforms" active={listPlatform === 'all'} onClick={() => setListPlatform('all')} />
            {PLATFORMS.map((p) => (
              <Chip
                key={p}
                label={PLATFORM_LABEL[p]}
                active={listPlatform === p}
                onClick={() => setListPlatform(listPlatform === p ? 'all' : p)}
              />
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex flex-wrap items-center gap-1.5">
              <Chip label="Any status" active={listStatus === 'all'} onClick={() => setListStatus('all')} />
              {(['RUNNING', 'SUCCEEDED', 'FAILED'] as const).map((s) => (
                <Chip
                  key={s}
                  label={s[0] + s.slice(1).toLowerCase()}
                  active={listStatus === s}
                  tone={s === 'FAILED' ? 'destructive' : s === 'SUCCEEDED' ? 'success' : 'secondary'}
                  onClick={() => setListStatus(listStatus === s ? 'all' : s)}
                />
              ))}
            </div>
            <div className="relative ml-auto min-w-[160px]">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={listQuery}
                onChange={(e) => setListQuery(e.target.value)}
                placeholder="Filter by keyword"
                aria-label="Filter batches by keyword"
                className="h-8 pl-8 text-sm"
              />
            </div>
            <span className="text-xs text-muted-foreground">
              {filtering ? `${visible.length} of ${batches.length}` : `${batches.length}`}
            </span>
          </div>

          {/* The most recent failure, called out with its own reason. A failed
              batch already carries the actor's message; making the operator
              open the row to read it is the difference between "it is broken"
              and "it is blocked on the free plan". */}
          {failedNow ? (
            <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <XCircle className="mt-0.5 size-4 shrink-0" />
              <span>
                <strong>{failedNow.keywords.join(', ')}</strong> failed: {failedNow.error}
              </span>
            </div>
          ) : null}

          {batches.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No batch yet. Run a search above.
            </p>
          ) : visible.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No batch matches these filters.
            </p>
          ) : (
            <div className="divide-y rounded-lg border">
              {visible.map((b) => (
                <BatchRow key={b.id} batch={b} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

/**
 * One batch row. The counts are spelled out because they measure different
 * things and the old one-line "{posts} posts · comment bodies not fetched ·
 * {items} items" merged them into a sentence that read as a contradiction:
 * `itemsRead` counts ACTOR rows, `postsCount` counts rows that survived the
 * window and de-dupe, and comments are always 0 for a keyword run because the
 * search actors return post rows without comment bodies.
 */
function BatchRow({ batch: b }: { batch: KeywordBatch }) {
  const running = b.status === 'PENDING' || b.status === 'RUNNING';
  const dropped = Math.max(0, b.itemsRead - b.postsCount);
  const elapsed = b.finishedAt
    ? (new Date(b.finishedAt).getTime() - new Date(b.createdAt).getTime()) / 1000
    : (Date.now() - new Date(b.createdAt).getTime()) / 1000;

  return (
    <Link
      href={`/scrape/batches/${b.id}`}
      className="block px-3 py-2.5 transition-colors hover:bg-secondary/40"
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusIcon status={b.status} />
        <span className="text-sm font-medium">{b.keywords.join(', ')}</span>
        <Badge variant="outline" className="font-normal">
          {platformLabel(b.platform) ?? b.platform}
        </Badge>
        {running ? (
          <span className="text-xs text-muted-foreground">running {formatDuration(elapsed)}</span>
        ) : null}
        <span className="ml-auto text-xs text-muted-foreground">
          {new Date(b.createdAt).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}
        </span>
        <ExternalLink className="size-4 shrink-0 text-muted-foreground" />
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 pl-6 text-xs text-muted-foreground">
        {b.status === 'SUCCEEDED' ? (
          <>
            <span>{b.postsCount} posts stored</span>
            {dropped > 0 ? <span>{dropped} dropped by window</span> : null}
            <span>
              {b.commentsCount > 0 ? `${b.commentsCount} comments` : 'no comment bodies (search actor)'}
            </span>
            <span>in {formatDuration(elapsed)}</span>
          </>
        ) : b.status === 'FAILED' ? (
          <span className="text-destructive">{b.error ?? 'failed without a reason'}</span>
        ) : (
          <span>
            {b.itemsRead > 0 ? `${b.itemsRead} items so far` : 'waiting for the actor…'}
          </span>
        )}
      </div>
    </Link>
  );
}

function StatusIcon({ status }: { status: KeywordBatch['status'] }) {
  if (status === 'SUCCEEDED') return <CheckCircle2 className="size-4 shrink-0 text-success" />;
  if (status === 'FAILED') return <XCircle className="size-4 shrink-0 text-destructive" />;
  return <Loader2 className="size-4 shrink-0 animate-spin text-muted-foreground" />;
}

/** Wall-clock duration in the unit a person waiting on it thinks in. */
function formatDuration(secs: number): string {
  if (!Number.isFinite(secs) || secs < 0) return '—';
  if (secs < 60) return `${Math.round(secs)}s`;
  const m = Math.floor(secs / 60);
  const s = Math.round(secs % 60);
  if (m < 60) return s ? `${m}m ${s}s` : `${m}m`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
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
        active
          ? 'border-primary bg-primary/10 text-foreground'
          : 'border-border text-muted-foreground hover:bg-secondary/60',
        !active && tone === 'destructive' ? 'text-destructive' : '',
        !active && tone === 'success' ? 'text-success' : '',
      ].join(' ')}
    >
      {label}
    </button>
  );
}
