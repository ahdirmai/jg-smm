'use client';

/**
 * New Action (single form). Flow: pick platform → paste post link → Scrape
 * (runs the Apify actor synchronously; the post + its existing comments are
 * stored server-side and shown here) → pick the action → for a comment, write
 * one per selected account (or draft one with AI in support/counter mode) →
 * submit. Replaces the old "Action to target" card and the region-select
 * wizard: one linear flow, one submit.
 */

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Textarea,
} from '@smm/ui';
import { AlertCircle, AtSign, Heart, Loader2, MessageSquare, Search, Sparkles } from 'lucide-react';
import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useState } from 'react';

import { api, type AccountsByRegionResponse, type ScrapeTargetResponse } from '@/lib/api';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { PLATFORM_LABEL, platformLabel } from '@/lib/platforms';
import type { ActionItem } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';
import { PostMedia } from '@/components/post-media';

const MAX_BATCH = 50;
// Mirrors MaxCommentTextLen in apps/api/internal/service/action.go — the API
// rejects anything longer, so cap it here rather than letting the operator type
// a paste that is guaranteed to fail at enqueue.
const MAX_COMMENT_LEN = 2200;

type Flow = 'comment' | 'like';
// How hard the AI draft may push: a 1-5 slider, 3 = balanced (server default).
const INTENSITY_MIN = 1;
const INTENSITY_MAX = 5;
const INTENSITY_DEFAULT = 3;
const INTENSITY_LABELS: Record<number, string> = {
  1: 'Gentle',
  2: 'Soft',
  3: 'Balanced',
  4: 'Pointed',
  5: 'Sharp',
};
// How long each generated comment is; empty on the wire = medium.
type CommentLength = 'short' | 'medium' | 'long';

/**
 * Metrics arrive as raw Apify keys (`likeCount`, `playCount`, `views` …) with
 * per-platform naming, so a naive render shows `likeCount: 42` to the operator.
 * Fold the synonyms onto one label each, then format the count — these are the
 * page's hero numbers (§3.4), and `128394` is not a number a human reads.
 */
const METRIC_LABELS: Record<string, string> = {
  likes: 'likes',
  likeCount: 'likes',
  comments: 'comments',
  commentCount: 'comments',
  views: 'views',
  viewCount: 'views',
  playCount: 'plays',
  shares: 'shares',
  shareCount: 'shares',
};

function compact(n: number): string {
  if (n < 1_000) return String(n);
  if (n < 1_000_000) return `${(n / 1_000).toFixed(n < 10_000 ? 1 : 0).replace(/\.0$/, '')}K`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`;
}

/** Folded metric pairs, first spelling of a synonym winning, cap 4. */
function metrics(m: Record<string, number> | null): { label: string; value: string }[] {
  if (!m) return [];
  const seen = new Set<string>();
  const out: { label: string; value: string }[] = [];
  for (const [key, label] of Object.entries(METRIC_LABELS)) {
    if (typeof m[key] !== 'number' || seen.has(label)) continue;
    seen.add(label);
    out.push({ label, value: compact(m[key]) });
  }
  return out.slice(0, 4);
}

/** Relative age of the scrape, so a stale 6h-cached post is visible as such. */
function scrapedAgo(iso: string): string {
  const mins = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60_000));
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  return `${Math.round(mins / 60)}h ago`;
}

/**
 * On-demand post scrape is wired to exactly two platforms, and that is a
 * backend fact rather than a UI choice: `actorForPlatform` in
 * cmd/server/main.go returns an actor for Instagram and Threads and "" for the
 * other five, and ScrapeTarget turns an empty actor into a 400 "no actor
 * configured". Listing the other five here would be five options that always
 * fail. Keyword scrape is the surface that covers all seven.
 *
 * Named once because it was spelled out in four signatures and two more
 * callbacks, and actions/page.tsx cast into it unchecked.
 */
const ON_DEMAND_PLATFORMS = ['instagram', 'threads'] as const;
type OnDemandPlatform = (typeof ON_DEMAND_PLATFORMS)[number];

export type NewActionFormHandle = {
  /** Fill the form with a platform + permalink and run the scrape (6h cache = instant for a stored post). */
  loadPost: (platform: OnDemandPlatform, url: string) => void;
};

export const NewActionForm = forwardRef<
  NewActionFormHandle,
  { onScraped?: () => void }
>(function NewActionForm({ onScraped }, ref) {
  const { accounts } = useAccounts();
  const { enqueue } = useActions();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');

  // Step 1: platform + link.
  const [platform, setPlatform] = useState<OnDemandPlatform>('instagram');
  const [url, setUrl] = useState('');

  // Step 2: scrape result.
  const [scraping, setScraping] = useState(false);
  const [scrape, setScrape] = useState<ScrapeTargetResponse | null>(null);
  const [scrapeError, setScrapeError] = useState<string | null>(null);

  // Step 3: action + targets.
  const [flow, setFlow] = useState<Flow | null>(null);
  // accountId -> comment body (comment flow).
  const [text, setText] = useState<Record<string, string>>({});
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  // AI generation state.
  const [aiMode, setAiMode] = useState<'support' | 'counter'>('support');
  // How hard the AI draft may push; 3 is the server default.
  const [intensity, setIntensity] = useState<number>(INTENSITY_DEFAULT);
  // How long each draft is; medium is the server default.
  const [length, setLength] = useState<CommentLength>('medium');
  const [aiBusy, setAiBusy] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);
  // Accounts grouped by the worker's location (city; region fallback,
  // unassigned last). null = not fetched yet / fetch failed: the list then
  // falls back to the flat platformAccounts.
  const [regionGroups, setRegionGroups] = useState<AccountsByRegionResponse['regions'] | null>(null);

  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Only accounts on the scraped platform can act on it.
  const platformAccounts = useMemo(
    () =>
      accounts.filter(
        (a) => a.platform === platform && (a.status === 'ACTIVE' || a.status === 'PAUSED'),
      ),
    [accounts, platform],
  );

  const selectedAccounts = useMemo(
    () => platformAccounts.filter((a) => selected[a.id]),
    [platformAccounts, selected],
  );

  // The by-region list is the grouping source for the account picker: the
  // same accounts, keyed by the worker's location (city; region fallback;
  // unassigned last). One fetch on mount; a failure keeps regionGroups null,
  // so the picker falls back to the flat list — grouping is an enhancement,
  // never a blocker.
  useEffect(() => {
    let live = true;
    api
      .listAccountsByRegion()
      .then((res) => {
        if (live) setRegionGroups(res.regions);
      })
      .catch(() => {
        if (live) setRegionGroups(null);
      });
    return () => {
      live = false;
    };
  }, []);

  // The picker's rows: the platform's usable accounts, grouped by location.
  // regionGroups null (not fetched / failed) collapses to one unlabelled
  // group, i.e. today's flat list.
  const accountGroups = useMemo(() => {
    const usable = (a: (typeof accounts)[number]) =>
      a.platform === platform && (a.status === 'ACTIVE' || a.status === 'PAUSED');
    if (!regionGroups) {
      return [{ region: '', accounts: accounts.filter(usable) }];
    }
    return regionGroups
      .map((g) => ({ region: g.region, accounts: g.accounts.filter(usable) }))
      .filter((g) => g.accounts.length > 0);
  }, [accounts, regionGroups, platform]);

  // One scrape runner: explicit args so a history pick (platform/url set in
  // the same tick) scrapes the right post, not the previous state values.
  const runScrapeWith = useCallback(
    async (p: OnDemandPlatform, u: string) => {
      setScrapeError(null);
      setScrape(null);
      setFlow(null);
      if (!u.trim()) {
        setScrapeError('Paste a post link first.');
        return;
      }
      setScraping(true);
      try {
        // force: a keyword batch ingests a post without its comment bodies but
        // still stamps scraped_at, so the 6h cache would answer with the empty
        // comment list it stored — and the form renders nothing for an empty
        // list, leaving the operator no way to tell "no comments" from "cache
        // hit". Here the operator explicitly asked to scrape; pay for it.
        const res = await api.scrapeTarget(p, u.trim(), true);
        setScrape(res);
        onScraped?.();
        // A fresh target resets per-account selections.
        setSelected({});
        setText({});
      } catch (e) {
        setScrapeError(e instanceof Error ? e.message : 'Scrape failed');
      } finally {
        setScraping(false);
      }
    },
    [onScraped],
  );

  function runScrape() {
    void runScrapeWith(platform, url);
  }

  async function generateAI() {
    setAiError(null);
    if (!scrape) return;
    setAiBusy(true);
    try {
      // One draft per selected account. The server dedups candidates within a
      // batch, so asking for exactly N and handing them out in order is what
      // keeps two accounts from posting the same text. Round-robin wraps when
      // the LLM returns fewer than asked (a variant can be screened out by the
      // denylist) — a blank field would silently fall back to the template
      // pool, which is a worse outcome than a repeated draft the operator can
      // edit.
      const want = Math.min(Math.max(selectedAccounts.length, 1), MAX_BATCH);
      const res = await api.generateComment({
        postText: scrape.post.text ?? '',
        authorHandle: scrape.post.authorHandle,
        existingComments: scrape.comments.slice(0, 10).map((c) => c.text),
        mode: aiMode,
        intensity,
        length,
        count: want,
      });
      const drafts = res.comments.length ? res.comments : [''];
      setText((s) => {
        const copy = { ...s };
        selectedAccounts.forEach((a, i) => {
          if (!copy[a.id]) copy[a.id] = drafts[i % drafts.length] ?? '';
        });
        return copy;
      });
    } catch (e) {
      setAiError(e instanceof Error ? e.message : 'AI generation failed');
    } finally {
      setAiBusy(false);
    }
  }

  // History card hands a stored post back: fill platform + link, scrape to
  // refresh the preview (instant — 6h cache), bump the history list.
  const loadPost = useCallback(
    (p: OnDemandPlatform, u: string) => {
      setPlatform(p);
      setUrl(u);
      void runScrapeWith(p, u);
    },
    [runScrapeWith],
  );
  useImperativeHandle(ref, () => ({ loadPost }), [loadPost]);

  const canSubmit = flow !== null && selectedAccounts.length > 0 && selectedAccounts.length <= MAX_BATCH && !submitting;

  async function submit() {
    setError(null);
    setResult(null);
    if (!canSubmit || !flow) return;
    const items: ActionItem[] = selectedAccounts.map((a) => {
      const item: ActionItem = {
        accountId: a.id,
        targetUrl: url.trim(),
        actionType: flow === 'like' ? 'action_like' : 'action_comment',
      };
      const body = (text[a.id] ?? '').trim();
      if (flow === 'comment' && body) item.text = body;
      return item;
    });
    setSubmitting(true);
    try {
      await enqueue(items);
      setResult(`Queued ${items.length} ${flow}${items.length === 1 ? '' : 's'} for ${selectedAccounts.length} account(s).`);
      setSelected({});
      setText({});
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Enqueue failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>New action</CardTitle>
        <CardDescription>
          Scrape the target post first (stores metadata, caption and existing comments), then queue
          the action. Executed by the owning worker via Playwright.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        {/* 1. Platform + link + scrape */}
        <div className="space-y-2">
          <Label htmlFor="action-target-url">Target post</Label>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[180px_1fr_auto]">
            <Select
              value={platform}
              onValueChange={(v) => {
                if (v === 'instagram' || v === 'threads') setPlatform(v);
              }}
            >
              <SelectTrigger aria-label="Platform">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {/* Rendered from the constant, so the options and the type
                    cannot drift apart. Labels come from lib/platforms, the one
                    canonical map. */}
                {ON_DEMAND_PLATFORMS.map((p) => (
                  <SelectItem key={p} value={p}>
                    {PLATFORM_LABEL[p]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              id="action-target-url"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://www.instagram.com/p/…"
              className="font-mono text-xs"
              disabled={!canAct || scraping}
            />
            <Button type="button" onClick={runScrape} disabled={scraping || !canAct}>
              {scraping ? <Loader2 className="animate-spin" /> : <Search />}
              {scraping ? 'Scraping…' : 'Scrape'}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            Scrape pulls the post metadata, caption and existing comments (Apify). This always
            runs a fresh scrape rather than reusing a stored post, so comments show up here even
            for a post a batch ingested without them.
          </p>
        </div>

        {scrapeError ? (
          <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            <AlertCircle className="size-4" />
            {scrapeError}
          </div>
        ) : null}

        {/* 2. Scraped post preview */}
        {scrape ? (
          <div className="overflow-hidden rounded-xl border border-border/60 bg-secondary/30">
            {/* Header: who posted it, and how fresh this read is. */}
            <div className="flex items-center gap-2.5 border-b border-border/60 px-3 py-2.5">
              <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-secondary">
                <AtSign className="size-4 text-muted-foreground" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">@{scrape.post.authorHandle}</p>
                <p className="text-[11px] text-muted-foreground">
                  {platformLabel(scrape.post.platform) ?? scrape.post.platform} · scraped{' '}
                  {scrapedAgo(scrape.post.scrapedAt)}
                </p>
              </div>
            </div>

            <div className="space-y-3 p-3">
              {scrape.post.mediaUrls && scrape.post.mediaUrls.length > 0 ? (
                <PostMedia urls={scrape.post.mediaUrls} />
              ) : null}

              {scrape.post.text ? (
                // Long captions are the norm on IG; clamp so the form stays
                // scannable and let the operator expand when they care.
                <details className="group">
                  <summary className="line-clamp-4 cursor-pointer list-none text-sm group-open:line-clamp-none">
                    {scrape.post.text}
                  </summary>
                  <span className="text-xs text-muted-foreground underline underline-offset-2 group-open:hidden">
                    Show more
                  </span>
                </details>
              ) : (
                <p className="text-xs text-muted-foreground">(no caption)</p>
              )}

              {/* §3.4 — numbers are heroes: bold value, small muted label under. */}
              {metrics(scrape.post.metrics).length > 0 ? (
                <div className="flex flex-wrap gap-5 border-t border-border/60 pt-3">
                  {metrics(scrape.post.metrics).map((m) => (
                    <div key={m.label}>
                      <p className="text-lg font-semibold leading-none">{m.value}</p>
                      <p className="mt-1 text-[11px] uppercase tracking-wide text-muted-foreground">
                        {m.label}
                      </p>
                    </div>
                  ))}
                </div>
              ) : null}

              {scrape.comments.length > 0 ? (
                <p className="text-xs text-muted-foreground">
                  {scrape.comments.length} existing comment
                  {scrape.comments.length === 1 ? '' : 's'} loaded
                </p>
              ) : null}
              {scrape.comments.length === 0 ? (
                // Without this the empty case is indistinguishable from a cache
                // hit, and the operator cannot tell whether there is anything to
                // reply to.
                <p className="text-xs text-muted-foreground">
                  No existing comments on this post — a fresh scrape ran, so this is the real
                  answer, not a cached one.
                </p>
              ) : null}
            </div>
          </div>
        ) : null}

        {/* 3. Action type */}
        {scrape ? (
          <div className="space-y-2">
            <Label>Action</Label>
            <div className="flex gap-2">
              <Button
                type="button"
                variant={flow === 'comment' ? 'default' : 'outline'}
                onClick={() => setFlow('comment')}
                disabled={!canAct}
              >
                <MessageSquare /> Comment
              </Button>
              <Button
                type="button"
                variant={flow === 'like' ? 'default' : 'outline'}
                onClick={() => setFlow('like')}
                disabled={!canAct}
              >
                <Heart /> Like
              </Button>
            </div>
          </div>
        ) : null}

        {/* 4. Accounts (+ per-account comment when the flow is a comment) */}
        {scrape && flow ? (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <Label>
                Accounts ({accountGroups.reduce((n, g) => n + g.accounts.length, 0)} on{' '}
                {platformLabel(platform) ?? platform})
              </Label>
              {flow === 'comment' ? (
                <div className="flex items-center gap-1.5">
                  <Select value={aiMode} onValueChange={(v) => setAiMode(v as 'support' | 'counter')}>
                    <SelectTrigger className="h-8 w-32" aria-label="AI mode">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="support">Support</SelectItem>
                      <SelectItem value="counter">Counter</SelectItem>
                    </SelectContent>
                  </Select>
                  {/* How long each draft is; medium is the server default. */}
                  <Select value={length} onValueChange={(v) => setLength(v as CommentLength)}>
                    <SelectTrigger className="h-8 w-24" aria-label="Comment length">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="short">Short</SelectItem>
                      <SelectItem value="medium">Medium</SelectItem>
                      <SelectItem value="long">Long</SelectItem>
                    </SelectContent>
                  </Select>
                  {/* How hard the draft may push: a 1-5 slider, 3 = balanced.
                      The value label sits next to the track so the operator
                      sees which end of the scale they are on. */}
                  <div className="flex items-center gap-1.5" aria-label="Comment intensity">
                    <input
                      type="range"
                      min={INTENSITY_MIN}
                      max={INTENSITY_MAX}
                      step={1}
                      value={intensity}
                      onChange={(e) => setIntensity(Number(e.target.value))}
                      className="h-2 w-24 cursor-pointer accent-primary"
                      aria-label="Comment intensity"
                    />
                    <span className="w-16 text-xs text-muted-foreground" title="How hard the draft may push">
                      {INTENSITY_LABELS[intensity]}
                    </span>
                  </div>
                  <Button type="button" variant="outline" size="sm" onClick={generateAI} disabled={aiBusy || selectedAccounts.length === 0}>
                    {aiBusy ? <Loader2 className="animate-spin" /> : <Sparkles />}
                    AI generate
                  </Button>
                </div>
              ) : null}
            </div>
            {aiError ? <p className="text-sm text-destructive">{aiError}</p> : null}
            {accountGroups.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No usable account on this platform — add one on the Accounts page.
              </p>
            ) : (
              <div className="space-y-3">
                {accountGroups.map((g) => (
                  <div key={g.region || 'flat'} className="space-y-1.5">
                    {/* Group header only when there is more than one location
                        to distinguish; a single group is the flat list as
                        before. */}
                    {accountGroups.length > 1 ? (
                      <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                        {g.region === 'unassigned' ? 'Unassigned' : g.region} ({g.accounts.length})
                      </div>
                    ) : null}
                    {g.accounts.map((a) => {
                      const on = !!selected[a.id];
                      return (
                        <div key={a.id} className="flex items-start gap-2">
                          <Button
                            type="button"
                            variant={on ? 'default' : 'outline'}
                            size="sm"
                            className="mt-0.5 shrink-0"
                            aria-pressed={on}
                            aria-label={`${on ? 'Deselect' : 'Select'} @${a.username}`}
                            onClick={() => setSelected((s) => ({ ...s, [a.id]: !s[a.id] }))}
                          >
                            {on ? '✓' : '+'}
                          </Button>
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2 text-sm">
                              <span className="truncate font-medium">@{a.username}</span>
                              <Badge variant="outline">{a.status}</Badge>
                            </div>
                            {on && flow === 'comment' ? (
                              <Textarea
                                className="mt-1"
                                rows={2}
                                maxLength={MAX_COMMENT_LEN}
                                placeholder="Comment for this account (blank = template pool)"
                                value={text[a.id] ?? ''}
                                onChange={(e) => setText((s) => ({ ...s, [a.id]: e.target.value }))}
                              />
                            ) : null}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                ))}
              </div>
            )}
          </div>
        ) : null}

        {/* 5. Submit */}
        {flow ? (
          <div className="flex flex-wrap items-center gap-3">
            <Button type="button" onClick={submit} disabled={!canSubmit}>
              {submitting ? <Loader2 className="animate-spin" /> : null}
              Queue {selectedAccounts.length} {flow}
              {selectedAccounts.length === 1 ? '' : 's'}
            </Button>
            {selectedAccounts.length > MAX_BATCH ? (
              <span className="text-sm text-destructive">Over the {MAX_BATCH} batch cap.</span>
            ) : null}
            {result ? <span className="text-sm text-success">{result}</span> : null}
            {error ? <span className="text-sm text-destructive">{error}</span> : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
});
