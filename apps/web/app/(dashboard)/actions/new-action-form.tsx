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
} from '@smm/ui';
import {
  AlertCircle,
  AtSign,
  Heart,
  Loader2,
  MessageSquare,
  RefreshCw,
  Reply,
  Search,
  Sparkles,
} from 'lucide-react';
import { forwardRef, useCallback, useImperativeHandle, useMemo, useRef, useState } from 'react';

import {
  api,
  type ScrapedComment,
  type ScrapedPost,
  type ScrapeTargetResponse,
  type SentimentBreakdown,
} from '@/lib/api';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import {
  isOnDemandPlatform,
  ON_DEMAND_PLATFORMS,
  PLATFORM_LABEL,
  platformLabel,
  type OnDemandPlatform,
} from '@/lib/platforms';
import type { ActionItem } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';
import { PostMedia } from '@/components/post-media';
import { postPermalink } from '@/lib/permalink';
import { CommentThread } from '@/components/comment-thread';
import { AccountPicker } from './account-picker';
import { SentimentSummary } from '@/components/sentiment';

const MAX_BATCH = 50;
// Mirrors MaxCommentTextLen in apps/api/internal/service/action.go — the API
// rejects anything longer, so cap it here rather than letting the operator type
// a paste that is guaranteed to fail at enqueue.
const MAX_COMMENT_LEN = 2200;

type Flow = 'comment' | 'like' | 'reply';
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

/** Counts from the stored comments; the same shape the scrape endpoint returns. */
function sentimentOf(comments: ScrapedComment[]): SentimentBreakdown {
  const out: SentimentBreakdown = { positive: 0, neutral: 0, negative: 0, unanalysed: 0 };
  for (const c of comments) {
    if (c.sentiment === 'POSITIVE') out.positive++;
    else if (c.sentiment === 'NEGATIVE') out.negative++;
    else if (c.sentiment === 'NEUTRAL') out.neutral++;
    else out.unanalysed++;
  }
  return out;
}

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

export type NewActionFormHandle = {
  /**
   * Fill the form with a platform + permalink, run the scrape, and optionally
   * preselect the action (the scrape itself resets `flow` to null).
   */
  loadPost: (platform: OnDemandPlatform, url: string, flow?: 'comment' | 'like') => void;
  /**
   * Load a post that is already stored (a Scrape History row). No actor run:
   * the row is the target, and its comment bodies are a DB read. This is what
   * keeps "act on this saved post" from paying for a second scrape.
   */
  loadStoredPost: (post: ScrapedPost, flow?: 'comment' | 'like') => void;
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
  // Reply flow: which comment the reply threads under. The job's targetUrl is
  // that comment's permalink, not the post's — the API rejects a reply aimed at
  // a bare post. Two ways in: pick a scraped comment, or paste its link (the
  // platform's own "Copy link"). Pasting is the direct route and the only one
  // for a comment the scrape never delivered.
  const [replyCommentId, setReplyCommentId] = useState<string | null>(null);
  const [manualReplyUrl, setManualReplyUrl] = useState('');
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
  // Picker search. Kept in the form (not the picker) so it survives a re-render
  // when a selection changes.
  const [accountQuery, setAccountQuery] = useState('');

  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Only accounts that can actually act: same platform, ACTIVE, and holding a
  // completed login. A FAILED/PENDING account opens the platform logged out, so
  // offering it here only produces a job that dies at enqueue.
  const platformAccounts = useMemo(
    () =>
      accounts.filter(
        (a) =>
          a.platform === platform &&
          a.status === 'ACTIVE' &&
          a.authStatus === 'AUTHENTICATED',
      ),
    [accounts, platform],
  );

  const selectedAccounts = useMemo(
    () => platformAccounts.filter((a) => selected[a.id]),
    [platformAccounts, selected],
  );

  // One scrape runner: explicit args so a history pick (platform/url set in
  // the same tick) scrapes the right post, not the previous state values.
  // force=false is the default: a post already scraped within 6h (with its
  // comments) is served straight from storage — no paid actor run. The
  // "Re-scrape" button passes force=true to fetch fresh data on purpose.
  const runScrapeWith = useCallback(
    async (p: OnDemandPlatform, u: string, force = false) => {
      setScrapeError(null);
      setScrape(null);
      setFlow(null);
      // The previous post's comments are gone; a stale id would aim the reply
      // at a comment no longer in the list.
      setReplyCommentId(null);
      setManualReplyUrl('');
      if (!u.trim()) {
        setScrapeError('Paste a post link first.');
        return;
      }
      setScraping(true);
      try {
        const res = await api.scrapeTarget(p, u.trim(), force);
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
    void runScrapeWith(platform, url, false);
  }

  // Re-scrape spends a paid actor run even when the post is fresh in storage.
  // It is the escape hatch when the operator wants live data, not the cache.
  function runScrapeForce() {
    void runScrapeWith(platform, url, true);
  }

  // The comment a reply answers. Declared above generateAI because the AI
  // drafts are built from it: the generator must be told which comment is being
  // answered, and `scrape` alone does not say that.
  const replyComment = useMemo(
    () => scrape?.comments.find((c) => c.id === replyCommentId) ?? null,
    [scrape, replyCommentId],
  );

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
      // Reply flow: tell the generator WHICH comment is being answered. The
      // first N comments of a busy thread say very little about the argument in
      // front of the operator, so the target leads and the rest become
      // background. Without this the drafts read as generic top-level comments
      // posted into a reply slot.
      const target = flow === 'reply' ? replyComment : null;
      const otherComments = scrape.comments
        .filter((c) => c.id !== target?.id)
        .slice(0, 9)
        .map((c) => c.text);
      const res = await api.generateComment({
        postText: scrape.post.text ?? '',
        authorHandle: scrape.post.authorHandle,
        existingComments: target
          ? [target.text, ...otherComments]
          : scrape.comments.slice(0, 10).map((c) => c.text),
        ...(target ? { replyTo: { handle: target.authorHandle, text: target.text } } : {}),
        mode: aiMode,
        intensity,
        length,
        count: want,
      });
      // Every press of AI generate is an explicit request for new drafts, so the
      // fields are overwritten, mode and intensity included. The old guard only
      // filled EMPTY fields, so generating counter then switching to support
      // produced drafts the UI never showed: the textareas still held the
      // counter text and the mode change looked broken.
      const drafts = res.comments.length ? res.comments : [''];
      setText((s) => {
        const copy = { ...s };
        selectedAccounts.forEach((a, i) => {
          copy[a.id] = drafts[i % drafts.length] ?? '';
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
    (p: OnDemandPlatform, u: string, flow?: 'comment' | 'like') => {
      setPlatform(p);
      setUrl(u);
      // runScrapeWith clears the action at its start; selecting it after the
      // scrape lands is what makes a history row open as the right flow.
      void runScrapeWith(p, u).then(() => {
        if (flow) setFlow(flow);
      });
    },
    [runScrapeWith],
  );
  // A stored post is shown immediately, then its saved comments arrive from the
  // DB. The token prevents a slow read for post A from overwriting post B.
  const storedLoadId = useRef(0);
  const loadStoredPost = useCallback((post: ScrapedPost, flow?: 'comment' | 'like') => {
    const platform = post.platform.toLowerCase();
    if (!isOnDemandPlatform(platform)) return;
    const loadId = ++storedLoadId.current;
    setPlatform(platform);
    setUrl(postPermalink(post) ?? '');
    setScrapeError(null);
    setReplyCommentId(null);
    setManualReplyUrl('');
    setSelected({});
    setText({});
    // The preview is the stored row, not a fresh scrape. `cached: true` is the
    // honest label: this data came from storage, and re-scraping is optional.
    setScrape({
      post,
      comments: [],
      sentiment: { positive: 0, neutral: 0, negative: 0, unanalysed: 0 },
      cached: true,
    });
    setFlow(flow ?? null);
    void api
      .listPostComments(post.id, { limit: 100 })
      .then((r) => {
        if (storedLoadId.current !== loadId) return;
        const comments = r.comments ?? [];
        setScrape({ post, comments, sentiment: sentimentOf(comments), cached: true });
      })
      .catch(() => {
        // Comment bodies are optional for a comment/like target. Keep the
        // stored post usable instead of blocking the action on a read failure.
      });
  }, []);
  useImperativeHandle(ref, () => ({ loadPost, loadStoredPost }), [loadPost, loadStoredPost]);

  // A reply needs an addressable target: either a picked comment with a
  // derived permalink, or a pasted comment link. The pasted link wins when
  // both are present — it is the operator's explicit choice.
  const replyTargetUrl =
    flow === 'reply' ? manualReplyUrl.trim() || (replyComment?.url ?? '') : '';
  const canSubmit =
    flow !== null &&
    selectedAccounts.length > 0 &&
    selectedAccounts.length <= MAX_BATCH &&
    !submitting &&
    (flow !== 'reply' || replyTargetUrl !== '');

  async function submit() {
    setError(null);
    setResult(null);
    if (!canSubmit || !flow) return;
    const targetUrl = flow === 'reply' ? replyTargetUrl : url.trim();
    const items: ActionItem[] = selectedAccounts.map((a) => {
      const item: ActionItem = {
        accountId: a.id,
        targetUrl,
        actionType:
          flow === 'like' ? 'action_like' : flow === 'reply' ? 'action_reply_comment' : 'action_comment',
      };
      const body = (text[a.id] ?? '').trim();
      if (flow !== 'like' && body) item.text = body;
      return item;
    });
    setSubmitting(true);
    try {
      await enqueue(items);
      setResult(
        `Queued ${items.length} ${flow}${items.length === 1 ? '' : 's'} for ${selectedAccounts.length} account(s).`,
      );
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
          Load a target (scrape it once, or pick a stored post from Scrape History), then queue the
          action. Executed by the owning worker via Playwright.
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
                if ((ON_DEMAND_PLATFORMS as readonly string[]).includes(v)) {
                  setPlatform(v as OnDemandPlatform);
                }
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
              placeholder={
                platform === 'youtube'
                  ? 'https://youtube.com/shorts/… or https://www.youtube.com/watch?v=…'
                  : 'https://www.instagram.com/p/…'
              }
              className="font-mono text-xs"
              disabled={!canAct || scraping}
            />
            <Button type="button" onClick={runScrape} disabled={scraping || !canAct}>
              {scraping ? <Loader2 className="animate-spin" /> : <Search />}
              {scraping ? 'Scraping…' : 'Scrape'}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">
            {platform === 'youtube'
              ? 'Scrape pulls the video title, channel, thumbnail and metrics (Apify). The YouTube actor does not return comment bodies, and its targets are not comment-cached, so each scrape is a paid run. Paste the Short, watch or youtu.be link.'
              : 'Scrape pulls the post metadata, caption and existing comments (Apify). A post already scraped within 6h is reused from storage — no paid run; only a new post, a stale one, or a batch-ingested post without comments triggers a fresh scrape.'}
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
            {/* Header: who posted it, how fresh this read is, and a way to
                force a fresh run when the operator wants live data. */}
            <div className="flex items-center gap-2.5 border-b border-border/60 px-3 py-2.5">
              <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-secondary">
                <AtSign className="size-4 text-muted-foreground" />
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">@{scrape.post.authorHandle}</p>
                <p className="text-[11px] text-muted-foreground">
                  {platformLabel(scrape.post.platform) ?? scrape.post.platform} · scraped{' '}
                  {scrapedAgo(scrape.post.scrapedAt)}
                  {scrape.cached ? ' · served from storage' : ''}
                </p>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 shrink-0 px-2 text-xs"
                onClick={runScrapeForce}
                disabled={scraping || !canAct}
                title="Run a fresh paid scrape even if the post is already in storage"
              >
                {scraping ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                Re-scrape
              </Button>
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

              {/* Comment sentiment: the distribution over what was actually
                  analysed, with the unanalysed count kept visible. */}
              {scrape.comments.length > 0 ? (
                <SentimentSummary breakdown={scrape.sentiment} />
              ) : null}

              {scrape.comments.length > 0 ? (
                <div className="space-y-2 border-t border-border/60 pt-3">
                  <p className="text-[11px] uppercase tracking-wide text-muted-foreground">
                    {scrape.comments.length} comment{scrape.comments.length === 1 ? '' : 's'} loaded
                  </p>
                  {/* Threaded by parentId so a reply sits under what it answers.
                      A flat list reads as non-sequiturs on a busy post. */}
                  <div className="max-h-72 overflow-y-auto rounded-lg border border-border/60 p-2">
                    <CommentThread comments={scrape.comments} />
                  </div>
                </div>
              ) : null}
              {scrape.comments.length === 0 ? (
                platform === 'youtube' ? (
                  // The YouTube actor returns the video row only. Saying "no
                  // comments" here would be a fabricated answer; the target URL
                  // is all a top-level comment job needs.
                  <p className="text-xs text-muted-foreground">
                    YouTube metadata loaded. Comment bodies are not returned by this actor — the
                    video URL is enough to queue a comment.
                  </p>
                ) : (
                  // The 6h cache only serves posts whose comments were actually
                  // fetched, so an empty list here is always the real answer, not
                  // a cached "no comments" placeholder. Without this the operator
                  // cannot tell whether there is anything to reply to.
                  <p className="text-xs text-muted-foreground">
                    No existing comments on this post — the comments were fetched, so this is the
                    real answer, not a cached placeholder.
                  </p>
                )
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
              {platform !== 'youtube' ? (
                <>
                  <Button
                    type="button"
                    variant={flow === 'like' ? 'default' : 'outline'}
                    onClick={() => setFlow('like')}
                    disabled={!canAct}
                  >
                    <Heart /> Like
                  </Button>
                  <Button
                    type="button"
                    variant={flow === 'reply' ? 'default' : 'outline'}
                    onClick={() => {
                      setFlow('reply');
                      // Default to the first comment that can actually be replied
                      // to, so the common case is one click. Comments with no
                      // derivable permalink have no legal target and are skipped;
                      // with none at all the operator pastes the comment link.
                      setReplyCommentId(
                        (prev) => prev ?? scrape?.comments.find((c) => c.url)?.id ?? null,
                      );
                    }}
                    disabled={!canAct}
                    title={
                      scrape.comments.some((c) => c.url)
                        ? 'Reply to one of the scraped comments'
                        : 'Paste a comment link to reply'
                    }
                  >
                    <Reply /> Reply
                  </Button>
                </>
              ) : null}
            </div>
            {platform === 'youtube' ? (
              <p className="text-xs text-muted-foreground">
                YouTube automation currently ships top-level comments only; like and reply are not
                built yet.
              </p>
            ) : null}
          </div>
        ) : null}

        {/* 3b. Reply target: which comment the reply threads under. */}
        {scrape && flow === 'reply' ? (
          <div className="space-y-2">
            <Label>Reply to which comment</Label>
            {/* Paste route first: the platform's own "Copy link" on a reply is
                the shortest path, and the only one when the scrape did not
                deliver that comment. A pasted link overrides the picker. */}
            <Input
              placeholder="Paste the comment link (optional) — e.g. https://www.threads.com/@user/post/ABC123"
              value={manualReplyUrl}
              onChange={(e) => setManualReplyUrl(e.target.value)}
              disabled={!canAct}
            />
            {scrape.comments.some((c) => c.url) ? (
              <div className="max-h-64 overflow-y-auto rounded-lg border border-border/60 p-2">
                {/* The picker uses the same threaded view as the card, so the
                    operator picks a target in the shape they will see on the
                    platform. Comments without a derivable permalink stay
                    visible but unselectable: hiding a parent would strand its
                    replies, and hiding the row would misrepresent the thread. */}
                <CommentThread
                  comments={scrape.comments}
                  selectable
                  selectedId={replyCommentId}
                  onSelect={(c) => {
                    if (!c.url) return;
                    setReplyCommentId(c.id);
                    // Picking a row clears a stale pasted link so the visible
                    // selection is the one that ships.
                    setManualReplyUrl('');
                  }}
                />
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">
                No scraped comment has a permalink, so paste the comment link above to reply.
              </p>
            )}
          </div>
        ) : null}

        {/* 4. Accounts (+ per-account comment when the flow is a comment) */}
        {scrape && flow ? (
          <div className="space-y-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <Label>
                Accounts ({platformAccounts.length} ready on{' '}
                {platformLabel(platform) ?? platform})
              </Label>
              {flow === 'comment' || flow === 'reply' ? (
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
            {platformAccounts.length === 0 ? (
              <p className="text-sm text-muted-foreground">
                No authenticated account on this platform. Finish a login on the Accounts page —
                an account without a session would act logged out.
              </p>
            ) : (
              <AccountPicker
                accounts={platformAccounts}
                selected={selected}
                onToggle={(id) => setSelected((s2) => ({ ...s2, [id]: !s2[id] }))}
                onSelectAll={(ids) => setSelected(Object.fromEntries(ids.map((id) => [id, true])))}
                onClear={() => {
                  setSelected({});
                  setText({});
                }}
                query={accountQuery}
                onQuery={setAccountQuery}
                text={text}
                onText={(id, value) => setText((s2) => ({ ...s2, [id]: value }))}
                textPlaceholder={
                  flow === 'comment' || flow === 'reply'
                    ? flow === 'reply'
                      ? 'Reply for this account (blank = template pool)'
                      : 'Comment for this account (blank = template pool)'
                    : undefined
                }
                maxBatch={MAX_BATCH}
                maxTextLen={MAX_COMMENT_LEN}
              />
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
