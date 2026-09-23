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
import { AlertCircle, Heart, Loader2, MessageSquare, Search, Sparkles } from 'lucide-react';
import { forwardRef, useCallback, useImperativeHandle, useMemo, useState } from 'react';

import { api, type ScrapeTargetResponse } from '@/lib/api';
import { useAccounts } from '@/lib/hooks/use-accounts';
import { useActions } from '@/lib/hooks/use-actions';
import { PLATFORM_LABEL } from '@/lib/platforms';
import type { ActionItem } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';
import { PostMedia } from '@/components/post-media';

const MAX_BATCH = 50;

type Flow = 'comment' | 'like';

function metricsLine(m: Record<string, number> | null): string {
  if (!m) return '';
  const parts: string[] = [];
  for (const key of ['likes', 'likeCount', 'comments', 'commentCount', 'views', 'viewCount', 'playCount']) {
    if (typeof m[key] === 'number') parts.push(`${key}: ${m[key]}`);
  }
  return parts.join(' · ');
}

export type NewActionFormHandle = {
  /** Fill the form with a platform + permalink and run the scrape (6h cache = instant for a stored post). */
  loadPost: (platform: 'instagram' | 'threads', url: string) => void;
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
  const [platform, setPlatform] = useState<'instagram' | 'threads'>('instagram');
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
  const [aiBusy, setAiBusy] = useState(false);
  const [aiError, setAiError] = useState<string | null>(null);

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

  // One scrape runner: explicit args so a history pick (platform/url set in
  // the same tick) scrapes the right post, not the previous state values.
  const runScrapeWith = useCallback(
    async (p: 'instagram' | 'threads', u: string) => {
      setScrapeError(null);
      setScrape(null);
      setFlow(null);
      if (!u.trim()) {
        setScrapeError('Paste a post link first.');
        return;
      }
      setScraping(true);
      try {
        const res = await api.scrapeTarget(p, u.trim());
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
      const res = await api.generateComment({
        postText: scrape.post.text ?? '',
        authorHandle: scrape.post.authorHandle,
        existingComments: scrape.comments.slice(0, 10).map((c) => c.text),
        mode: aiMode,
        count: 3,
      });
      // First variant prefills every selected account still empty.
      const draft = res.comments[0] ?? '';
      if (draft) {
        setText((s) => {
          const copy = { ...s };
          for (const a of selectedAccounts) {
            if (!copy[a.id]) copy[a.id] = draft;
          }
          return copy;
        });
      }
    } catch (e) {
      setAiError(e instanceof Error ? e.message : 'AI generation failed');
    } finally {
      setAiBusy(false);
    }
  }

  // History card hands a stored post back: fill platform + link, scrape to
  // refresh the preview (instant — 6h cache), bump the history list.
  const loadPost = useCallback(
    (p: 'instagram' | 'threads', u: string) => {
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
          <Label>Target post</Label>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[180px_1fr_auto]">
            <Select value={platform} onValueChange={(v) => setPlatform(v as 'instagram' | 'threads')}>
              <SelectTrigger aria-label="Platform">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="instagram">Instagram</SelectItem>
                <SelectItem value="threads">Threads</SelectItem>
              </SelectContent>
            </Select>
            <Input
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
            Scrape pulls the post metadata, caption and existing comments (Apify). Already-stored
            posts are reused for 6 hours.
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
          <div className="rounded-lg border border-border/60 bg-secondary/30 p-3 space-y-1.5">
            <div className="flex items-center gap-2">
              <Badge variant="outline">{PLATFORM_LABEL[scrape.post.platform] ?? scrape.post.platform}</Badge>
              <span className="text-sm font-medium">@{scrape.post.authorHandle}</span>
              <span className="text-xs text-muted-foreground">{metricsLine(scrape.post.metrics)}</span>
            </div>
            {scrape.post.text ? (
              <p className="text-sm whitespace-pre-wrap">{scrape.post.text}</p>
            ) : (
              <p className="text-xs text-muted-foreground">(no caption)</p>
            )}
            {scrape.post.mediaUrls && scrape.post.mediaUrls.length > 0 ? (
              <div className="pt-1">
                <PostMedia urls={scrape.post.mediaUrls} />
              </div>
            ) : null}
            {scrape.comments.length > 0 ? (
              <p className="text-xs text-muted-foreground">
                {scrape.comments.length} existing comment{scrape.comments.length === 1 ? '' : 's'} loaded
              </p>
            ) : null}
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
                Accounts ({platformAccounts.length} on {PLATFORM_LABEL[platform] ?? platform})
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
                No usable account on this platform — add one on the Accounts page.
              </p>
            ) : (
              <div className="space-y-1.5">
                {platformAccounts.map((a) => {
                  const on = !!selected[a.id];
                  return (
                    <div key={a.id} className="flex items-start gap-2">
                      <Button
                        type="button"
                        variant={on ? 'default' : 'outline'}
                        size="sm"
                        className="mt-0.5 shrink-0"
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
