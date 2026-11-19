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
  cn,
} from '@smm/ui';
import {
  AlertCircle,
  ChevronDown,
  ExternalLink,
  Heart,
  Loader2,
  MessageSquare,
  PenLine,
  RefreshCw,
  Reply,
  Search,
} from 'lucide-react';
import Link from 'next/link';
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
import { platformIcon } from '@/components/platform-icon';
import { AccountPicker, CommentComposer } from './account-picker';

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

function SectionHeading({
  id,
  step,
  title,
  hint,
}: {
  id?: string | undefined;
  step: number;
  title: string;
  hint?: string | undefined;
}) {
  return (
    <div className="mb-2 flex flex-wrap items-baseline gap-x-2 gap-y-1">
      <span className="grid size-5 shrink-0 place-items-center rounded-full bg-primary/10 text-[10px] font-semibold text-primary">
        {step}
      </span>
      <h2 id={id} className="text-sm font-semibold tracking-tight">
        {title}
      </h2>
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
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
  { onScraped?: () => void; onEnqueued?: () => void }
>(function NewActionForm({ onScraped, onEnqueued }, ref) {
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
  // Manual writing is the primary path; the AI controls are progressive
  // disclosure so they do not crowd the editor before they are wanted.
  const [aiOpen, setAiOpen] = useState(false);

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

  // A scraped target is tied to its platform and URL. Editing either one
  // invalidates the preview, the action, the selected accounts and the drafts;
  // queueing a comment against a post the operator is no longer looking at is
  // the one mistake this form must make impossible.
  function resetSetup() {
    // Invalidate the stored-post comment read: it may still be in flight and
    // would otherwise restore the preview after the target changed.
    storedLoadId.current += 1;
    setScrape(null);
    setFlow(null);
    setReplyCommentId(null);
    setManualReplyUrl('');
    setSelected({});
    setText({});
    setScrapeError(null);
    setResult(null);
    setError(null);
    setAiError(null);
  }

  function changePlatform(value: string) {
    if ((ON_DEMAND_PLATFORMS as readonly string[]).includes(value)) {
      setPlatform(value as OnDemandPlatform);
      if (scrape) {
        resetSetup();
        setUrl('');
      }
    }
  }

  function changeTargetUrl(value: string) {
    setUrl(value);
    if (scrape) resetSetup();
  }

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

  // One update for the whole batch. Copying inside the composer would call
  // setText once per account and render a half-applied map.
  function applyTextToAll(value: string) {
    setText(Object.fromEntries(selectedAccounts.map((a) => [a.id, value])));
  }

  const customDrafts = selectedAccounts.filter((a) => (text[a.id] ?? '').trim()).length;
  const templateDrafts = selectedAccounts.length - customDrafts;
  const flowNoun = flow === 'like' ? 'like' : flow === 'reply' ? 'reply' : 'comment';
  const flowPlural = flow === 'reply' ? 'replies' : `${flowNoun}s`;
  const queueLabel =
    selectedAccounts.length === 0
      ? 'Select accounts'
      : `Queue ${selectedAccounts.length} ${
          selectedAccounts.length === 1 ? flowNoun : flowPlural
        }`;
  const queueBlocked =
    !flow
      ? 'Choose an action first.'
      : selectedAccounts.length === 0
        ? 'Select at least one account.'
        : selectedAccounts.length > MAX_BATCH
          ? `Keep the batch at ${MAX_BATCH} accounts or fewer.`
          : flow === 'reply' && !replyTargetUrl
            ? 'Pick the comment to reply to, or paste its link.'
            : null;
  const TargetIcon = platformIcon(scrape?.post.platform ?? platform);
  const postUrl = scrape ? postPermalink(scrape.post) : null;
  const analysedComments = scrape
    ? scrape.comments.filter((comment) => comment.sentiment).length
    : 0;

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
      onEnqueued?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Enqueue failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card className="overflow-visible">
      <CardHeader>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <CardTitle>Create an action</CardTitle>
            <CardDescription>
              Load a post, choose who acts, then write the comment or let AI draft it.
            </CardDescription>
          </div>
          <p className="max-w-xs text-xs leading-5 text-muted-foreground">
            One post per batch. Existing comments stay visible while you write.
          </p>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* 1. Target */}
        <section aria-labelledby="target-heading">
          <SectionHeading
            id="target-heading"
            step={1}
            title="Target post"
            hint="Paste a link and scrape it once. Reusing storage is free."
          />
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[150px_minmax(0,1fr)_auto]">
            <Select value={platform} onValueChange={changePlatform}>

              <SelectTrigger aria-label="Platform" disabled={!canAct || scraping}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ON_DEMAND_PLATFORMS.map((p) => (
                  <SelectItem key={p} value={p}>
                    {PLATFORM_LABEL[p]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input
              id="action-target-url"
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              value={url}
              onChange={(e) => changeTargetUrl(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  runScrape();
                }
              }}
              placeholder={
                platform === 'youtube'
                  ? 'https://youtube.com/shorts/... or a watch link'
                  : 'https://www.instagram.com/p/...'
              }
              className="font-mono text-xs"
              aria-label="Target post URL"
              disabled={!canAct || scraping}
            />
            <Button
              type="button"
              onClick={runScrape}
              disabled={scraping || !canAct || !url.trim()}
              className="sm:w-auto"
            >
              {scraping ? <Loader2 className="animate-spin" /> : <Search />}
              {scraping ? 'Scraping...' : 'Scrape'}
            </Button>
          </div>
          <p className="mt-1.5 text-xs leading-5 text-muted-foreground">
            {platform === 'youtube'
              ? 'YouTube loads the video title, channel, and metrics. The actor does not return comment bodies.'
              : 'A post scraped in the last 6 hours is served from storage. A new or stale post runs the paid actor once.'}
          </p>
        </section>

        {scrapeError ? (
          <div
            className="flex items-center gap-2 rounded-xl border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
            role="alert"
          >
            <AlertCircle className="size-4" />
            {scrapeError}
          </div>
        ) : null}

        {scrape ? (
          <div className="grid min-w-0 gap-4 xl:grid-cols-[minmax(0,0.92fr)_minmax(0,1.08fr)]">
            {/* Post and existing comments stay in view beside the composer on desktop. */}
            <section className="min-w-0 space-y-3 self-start rounded-2xl border border-border/60 bg-secondary/20 p-3">
              <div className="flex items-center justify-between gap-2">
                <p className="text-[11px] font-semibold uppercase tracking-[0.12em] text-muted-foreground">
                  Post preview
                </p>
                <span className="text-[11px] text-muted-foreground">
                  {scrape.cached ? 'Stored copy' : 'Fresh scrape'}
                </span>
              </div>

              <div className="flex items-center gap-2.5 border-y border-border/60 py-2.5">
                <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-secondary">
                  <TargetIcon className="size-4 text-muted-foreground" />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">@{scrape.post.authorHandle}</p>
                  <p className="text-[11px] text-muted-foreground">
                    {platformLabel(scrape.post.platform) ?? scrape.post.platform} · scraped{' '}
                    {scrapedAgo(scrape.post.scrapedAt)}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  {postUrl ? (
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2 text-xs"
                      asChild
                    >
                      <a href={postUrl} target="_blank" rel="noreferrer noopener">
                        <ExternalLink />
                        Open
                      </a>
                    </Button>
                  ) : null}
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="h-7 px-2 text-xs"
                    onClick={runScrapeForce}
                    disabled={scraping || !canAct}
                    title="Run a fresh paid scrape even if the post is already in storage"
                  >
                    {scraping ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                    Re-scrape
                  </Button>
                </div>
              </div>

              <div
                className={cn(
                  'grid gap-3',
                  scrape.post.mediaUrls && scrape.post.mediaUrls.length > 0
                    ? 'sm:grid-cols-[180px_minmax(0,1fr)]'
                    : 'grid-cols-1',
                )}
              >
                {scrape.post.mediaUrls && scrape.post.mediaUrls.length > 0 ? (
                  <PostMedia urls={scrape.post.mediaUrls} compact />
                ) : null}
                <div className="min-w-0 space-y-2">
                  {scrape.post.text ? (
                    <details className="group">
                      <summary className="line-clamp-5 cursor-pointer list-none text-sm leading-5 group-open:line-clamp-none">
                        {scrape.post.text}
                      </summary>
                      <span className="text-xs text-muted-foreground underline underline-offset-2 group-open:hidden">
                        Show more
                      </span>
                    </details>
                  ) : (
                    <p className="text-xs text-muted-foreground">(no caption)</p>
                  )}

                  {metrics(scrape.post.metrics).length > 0 ? (
                    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
                      {metrics(scrape.post.metrics).map((m) => (
                        <span key={m.label}>
                          <span className="font-semibold text-foreground">{m.value}</span> {m.label}
                        </span>
                      ))}
                    </div>
                  ) : null}
                </div>
              </div>

              <div className="border-t border-border/60 pt-3">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <p className="text-xs font-medium">Existing comments</p>
                    <p className="text-[11px] leading-5 text-muted-foreground">
                      {flow === 'reply'
                        ? 'Click the comment you want to answer.'
                        : 'Read the thread before writing yours.'}
                    </p>
                  </div>
                  <span className="shrink-0 text-right text-[11px] leading-5 tabular-nums text-muted-foreground">
                    {scrape.comments.length} loaded
                    {scrape.comments.length > 0 ? (
                      <>
                        <br />
                        {analysedComments > 0
                          ? `${analysedComments} analysed`
                          : 'not analysed'}
                      </>
                    ) : null}
                  </span>
                </div>

                {scrape.comments.length > 0 ? (
                  <div
                    className={cn(
                      'mt-2 max-h-[320px] overflow-y-auto rounded-xl border p-2',
                      flow === 'reply'
                        ? 'border-primary/30 bg-primary/[0.03]'
                        : 'border-border/60',
                    )}
                  >
                    <CommentThread
                      comments={scrape.comments}
                      selectable={flow === 'reply'}
                      selectedId={replyCommentId}
                      onSelect={(c) => {
                        if (!c.url) return;
                        setReplyCommentId(c.id);
                        setManualReplyUrl('');
                      }}
                    />
                  </div>
                ) : platform === 'youtube' ? (
                  <p className="mt-2 text-xs leading-5 text-muted-foreground">
                    YouTube metadata is loaded. Comment bodies are not returned by this actor, so
                    the video URL is enough to queue a top-level comment.
                  </p>
                ) : (
                  <p className="mt-2 text-xs leading-5 text-muted-foreground">
                    The scrape returned no comments. That is the real answer for this post, not a
                    cached placeholder.
                  </p>
                )}
              </div>
            </section>

            <div className="min-w-0 space-y-4">
              {/* 2. Action + accounts */}
              <section className="rounded-2xl border border-border/60 bg-secondary/20 p-3">
                <SectionHeading
                  step={2}
                  title="Action and audience"
                  hint="Pick what each account does."
                />

                <div
                  className={cn(
                    'grid gap-2',
                    platform === 'youtube' ? 'grid-cols-1' : 'grid-cols-1 sm:grid-cols-3',
                  )}
                  role="group"
                  aria-label="Action type"
                >
                  <Button
                    type="button"
                    variant={flow === 'comment' ? 'default' : 'outline'}
                    aria-pressed={flow === 'comment'}
                    onClick={() => setFlow('comment')}
                    disabled={!canAct}
                    className="justify-start"
                  >
                    <MessageSquare /> Comment
                  </Button>
                  {platform !== 'youtube' ? (
                    <>
                      <Button
                        type="button"
                        variant={flow === 'like' ? 'default' : 'outline'}
                        aria-pressed={flow === 'like'}
                        onClick={() => setFlow('like')}
                        disabled={!canAct}
                        className="justify-start"
                      >
                        <Heart /> Like
                      </Button>
                      <Button
                        type="button"
                        variant={flow === 'reply' ? 'default' : 'outline'}
                        aria-pressed={flow === 'reply'}
                        onClick={() => setFlow('reply')}
                        disabled={!canAct}
                        className="justify-start"
                        title="Reply to one existing comment"
                      >
                        <Reply /> Reply
                      </Button>
                    </>
                  ) : null}
                </div>

                <p className="mt-2 text-xs leading-5 text-muted-foreground">
                  {platform === 'youtube'
                    ? 'YouTube supports top-level comments in this build. Like and reply are not available.'
                    : flow === 'comment'
                      ? 'Post a new top-level comment under this post.'
                      : flow === 'like'
                        ? 'Like the post once from every selected account.'
                        : flow === 'reply'
                          ? 'Answer one existing comment. Pick it in the preview.'
                          : 'Choose Comment, Like, or Reply to see the accounts that can act.'}
                </p>

                {flow ? (
                  <div className="mt-3 space-y-3">
                    {flow === 'reply' ? (
                      <div className="rounded-xl border border-border/60 bg-background/50 p-2.5">
                        <div className="flex items-center justify-between gap-2">
                          <p className="text-xs font-medium">Reply target</p>
                          {replyComment ? (
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              className="h-7 px-2 text-xs"
                              onClick={() => setReplyCommentId(null)}
                            >
                              Clear
                            </Button>
                          ) : null}
                        </div>
                        {replyComment ? (
                          <div className="mt-1.5 rounded-lg border border-primary/25 bg-primary/5 p-2 text-xs">
                            <p className="font-medium">@{replyComment.authorHandle}</p>
                            <p className="mt-0.5 line-clamp-2 text-muted-foreground">
                              {replyComment.text}
                            </p>
                          </div>
                        ) : (
                          <p className="mt-1.5 text-xs text-muted-foreground">
                            Select a comment in the preview, or paste its link below.
                          </p>
                        )}
                        <div className="mt-2 space-y-1">
                          <Label htmlFor="manual-reply-url" className="text-xs">
                            Comment link
                          </Label>
                          <Input
                            id="manual-reply-url"
                            placeholder="https://www.instagram.com/p/.../c/..."
                            value={manualReplyUrl}
                            onChange={(e) => setManualReplyUrl(e.target.value)}
                            disabled={!canAct}
                            className="font-mono text-xs"
                          />
                          <p className="text-[11px] leading-5 text-muted-foreground">
                            A pasted link overrides the selected comment.
                          </p>
                        </div>
                      </div>
                    ) : null}

                    <div>
                      <div className="mb-1.5">
                        <p className="text-xs font-medium">Accounts</p>
                      </div>
                      {platformAccounts.length === 0 ? (
                        <p className="rounded-lg border border-dashed border-border/70 p-3 text-sm leading-6 text-muted-foreground">
                          No account is ready on {platformLabel(platform) ?? platform}. Finish a
                          login on the{' '}
                          <Link href="/accounts" className="font-medium text-primary underline">
                            Accounts page
                          </Link>
                          . An account without a session would act logged out.
                        </p>
                      ) : (
                        <AccountPicker
                          accounts={platformAccounts}
                          selected={selected}
                          onToggle={(id) => setSelected((s2) => ({ ...s2, [id]: !s2[id] }))}
                          onSelectAll={(ids) =>
                            setSelected(Object.fromEntries(ids.map((id) => [id, true])))
                          }
                          onClear={() => {
                            setSelected({});
                            setText({});
                          }}
                          query={accountQuery}
                          onQuery={setAccountQuery}
                          maxBatch={MAX_BATCH}
                        />
                      )}
                    </div>
                  </div>
                ) : null}
              </section>

              {/* 3. Message */}
              {flow === 'comment' || flow === 'reply' ? (
                <section className="rounded-2xl border border-border/60 bg-secondary/20 p-3">
                  <SectionHeading
                    step={3}
                    title={flow === 'reply' ? 'Reply' : 'Comment'}
                    hint={
                      selectedAccounts.length > 0
                        ? 'Write once per account, or apply one draft to all.'
                        : undefined
                    }
                  />

                  <CommentComposer
                    accounts={selectedAccounts}
                    text={text}
                    onText={(id, value) => setText((s2) => ({ ...s2, [id]: value }))}
                    onApplyAll={applyTextToAll}
                    maxTextLen={MAX_COMMENT_LEN}
                    placeholder={
                      flow === 'reply'
                        ? 'Write the reply for this account...'
                        : 'Write the comment for this account...'
                    }
                  />

                  {aiError ? (
                    <p className="mt-2 text-sm text-destructive" role="alert">
                      {aiError}
                    </p>
                  ) : null}

                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="mt-2"
                    onClick={() => setAiOpen((open) => !open)}
                    aria-expanded={aiOpen}
                    disabled={selectedAccounts.length === 0}
                    title={
                      selectedAccounts.length === 0
                        ? 'Select at least one account before drafting.'
                        : 'Generate drafts for the selected accounts'
                    }
                  >
                    <PenLine />
                    Draft with AI
                    <ChevronDown className={cn('transition-transform', aiOpen && 'rotate-180')} />
                  </Button>

                  {aiOpen ? (
                    <div className="mt-2 space-y-2 rounded-xl border border-border/60 bg-background/50 p-2.5">
                      <div className="flex flex-wrap items-center gap-2">
                        <Select
                          value={aiMode}
                          onValueChange={(v) => setAiMode(v as 'support' | 'counter')}
                        >
                          <SelectTrigger className="w-32" aria-label="AI mode">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="support">Support</SelectItem>
                            <SelectItem value="counter">Counter</SelectItem>
                          </SelectContent>
                        </Select>
                        <Select
                          value={length}
                          onValueChange={(v) => setLength(v as CommentLength)}
                        >
                          <SelectTrigger className="w-24" aria-label="Comment length">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="short">Short</SelectItem>
                            <SelectItem value="medium">Medium</SelectItem>
                            <SelectItem value="long">Long</SelectItem>
                          </SelectContent>
                        </Select>
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
                          <span
                            className="w-16 text-xs text-muted-foreground"
                            title="How hard the draft may push"
                          >
                            {INTENSITY_LABELS[intensity]}
                          </span>
                        </div>
                        <Button
                          type="button"
                          size="sm"
                          onClick={generateAI}
                          disabled={
                            aiBusy || selectedAccounts.length === 0 || selectedAccounts.length > MAX_BATCH
                          }
                        >
                          {aiBusy ? <Loader2 className="animate-spin" /> : <PenLine />}
                          {aiBusy
                            ? 'Drafting...'
                            : selectedAccounts.length === 0
                              ? 'Generate drafts'
                              : `Generate ${selectedAccounts.length} draft${
                                  selectedAccounts.length === 1 ? '' : 's'
                                }`}
                        </Button>
                      </div>
                      <p className="text-[11px] leading-5 text-muted-foreground">
                        Drafts replace the current bodies for every selected account. Review before
                        queueing.
                      </p>
                    </div>
                  ) : null}
                </section>
              ) : null}

              {/* Queue bar */}
              {flow ? (
                <div
                  className="rounded-2xl border border-primary/20 bg-primary/[0.04] p-3"
                  aria-label="Queue action"
                >
                  <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                    <div className="min-w-0">
                      <p className="text-sm font-medium">
                        {selectedAccounts.length === 0
                          ? 'No accounts selected'
                          : `${selectedAccounts.length} account${
                              selectedAccounts.length === 1 ? '' : 's'
                            } · ${flowNoun}`}
                      </p>
                      <p className="mt-0.5 text-xs leading-5 text-muted-foreground">
                        {queueBlocked ??
                          (flow === 'like'
                            ? 'Each account will like this post once.'
                            : customDrafts === 0
                              ? 'Blank bodies use the template pool.'
                              : `${customDrafts} custom draft${
                                  customDrafts === 1 ? '' : 's'
                                }${templateDrafts > 0 ? ` · ${templateDrafts} template` : ''}.`)}
                      </p>
                    </div>
                    <Button
                      type="button"
                      onClick={submit}
                      disabled={!canSubmit}
                      className="shrink-0"
                    >
                      {submitting ? <Loader2 className="animate-spin" /> : null}
                      {submitting ? 'Queueing...' : queueLabel}
                    </Button>
                  </div>
                  <div aria-live="polite" className="mt-2 empty:mt-0">
                    {result ? <p className="text-sm text-success">{result}</p> : null}
                    {error ? (
                      <p className="text-sm text-destructive" role="alert">
                        {error}
                      </p>
                    ) : null}
                  </div>
                </div>
              ) : null}
            </div>
          </div>
        ) : (
          <div className="rounded-2xl border border-dashed border-border/70 p-4 sm:p-5">
            <div className="flex items-start gap-3">
              <span className="grid size-9 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                <Search className="size-4" />
              </span>
              <div>
                <p className="text-sm font-medium">Start with a post</p>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">
                  Scrape a link, or open Scraped posts above and choose a stored post. Its existing
                  comments load here for context.
                </p>
              </div>
            </div>
            <ol className="mt-4 grid gap-2 sm:grid-cols-3">
              {[
                ['1', 'Load the post', 'Paste a link and scrape it.'],
                ['2', 'Choose the audience', 'Pick an action and accounts.'],
                ['3', 'Write and queue', 'Set one draft per account, then send.'],
              ].map(([n, title, body]) => (
                <li key={n} className="flex gap-2 rounded-xl bg-secondary/30 p-2.5">
                  <span className="grid size-5 shrink-0 place-items-center rounded-full bg-background text-[10px] font-semibold text-muted-foreground">
                    {n}
                  </span>
                  <div>
                    <p className="text-xs font-medium">{title}</p>
                    <p className="mt-0.5 text-[11px] leading-4 text-muted-foreground">{body}</p>
                  </div>
                </li>
              ))}
            </ol>
          </div>
        )}
      </CardContent>
    </Card>
  );
});
