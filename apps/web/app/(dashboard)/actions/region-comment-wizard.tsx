'use client';

/**
 * Region-select comment flow (Phase 1). Target Link → pick regions ("wilayah")
 * → pick N accounts per region (all / random) → write a comment per account →
 * enqueue one action_comment per selected account, each carrying its own text
 * (the API stores per-account text and the scheduler posts it verbatim). An
 * empty comment for an account falls back to the template pool server-side.
 *
 * Phase 2 will fill each comment via AI from an Apify scrape of the target link;
 * the per-account text field is the seam that feature slots into.
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
  Textarea,
} from '@smm/ui';
import { Loader2, MapPin, Send } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';

import { api, type Account, type ActionItem, type RegionGroup } from '@/lib/api';
import { useActions } from '@/lib/hooks/use-actions';
import { PLATFORM_LABEL } from '@/lib/platforms';

const MAX_BATCH = 50;
const USABLE = new Set(['ACTIVE', 'PAUSED']);

function accountName(a: Account): string {
  return a.handle ?? a.username ?? a.id.slice(0, 8);
}

// Pick n distinct accounts at random (Fisher–Yates on a copy). n>=len returns all.
function sampleN(accounts: Account[], n: number): Account[] {
  if (n >= accounts.length) return accounts;
  const copy = accounts.slice();
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    const tmp = copy[i]!;
    copy[i] = copy[j]!;
    copy[j] = tmp;
  }
  return copy.slice(0, n);
}

export function RegionCommentWizard() {
  const { enqueue } = useActions();

  const [groups, setGroups] = useState<RegionGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [targetUrl, setTargetUrl] = useState('');
  // region -> selected. A region is "in play" when true.
  const [regionOn, setRegionOn] = useState<Record<string, boolean>>({});
  // accountId -> selected.
  const [accountOn, setAccountOn] = useState<Record<string, boolean>>({});
  // accountId -> comment body.
  const [text, setText] = useState<Record<string, string>>({});
  // per-region random-N input.
  const [randomN, setRandomN] = useState<Record<string, string>>({});

  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    api
      .listAccountsByRegion(ctrl.signal)
      .then((r) => {
        // Only usable accounts can act; drop the rest so the counts are honest.
        const usable = r.regions
          .map((g) => ({ ...g, accounts: g.accounts.filter((a) => USABLE.has(a.status)) }))
          .filter((g) => g.accounts.length > 0);
        setGroups(usable);
        setLoadError(null);
      })
      .catch((e) => {
        if (!ctrl.signal.aborted) setLoadError(e instanceof Error ? e.message : 'failed to load regions');
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false);
      });
    return () => ctrl.abort();
  }, []);

  const activeRegions = useMemo(() => groups.filter((g) => regionOn[g.region]), [groups, regionOn]);

  const selectedAccounts = useMemo(() => {
    const out: Account[] = [];
    for (const g of activeRegions) {
      for (const a of g.accounts) if (accountOn[a.id]) out.push(a);
    }
    return out;
  }, [activeRegions, accountOn]);

  function toggleRegion(region: string, accounts: Account[]) {
    const next = !regionOn[region];
    setRegionOn((s) => ({ ...s, [region]: next }));
    // Turning a region on selects all its accounts by default; off clears them.
    setAccountOn((s) => {
      const copy = { ...s };
      for (const a of accounts) copy[a.id] = next;
      return copy;
    });
  }

  function toggleAccount(id: string) {
    setAccountOn((s) => ({ ...s, [id]: !s[id] }));
  }

  function selectAll(accounts: Account[], on: boolean) {
    setAccountOn((s) => {
      const copy = { ...s };
      for (const a of accounts) copy[a.id] = on;
      return copy;
    });
  }

  function applyRandom(region: string, accounts: Account[]) {
    const n = parseInt(randomN[region] ?? '', 10);
    if (!Number.isFinite(n) || n <= 0) return;
    const chosen = new Set(sampleN(accounts, n).map((a) => a.id));
    setAccountOn((s) => {
      const copy = { ...s };
      for (const a of accounts) copy[a.id] = chosen.has(a.id);
      return copy;
    });
  }

  const canSubmit =
    targetUrl.trim().length > 0 && selectedAccounts.length > 0 && selectedAccounts.length <= MAX_BATCH && !submitting;

  async function submit() {
    setError(null);
    setResult(null);
    if (!canSubmit) return;
    const items: ActionItem[] = selectedAccounts.map((a) => {
      const body = (text[a.id] ?? '').trim();
      const item: ActionItem = {
        accountId: a.id,
        targetUrl: targetUrl.trim(),
        actionType: 'action_comment',
      };
      if (body) item.text = body;
      return item;
    });
    setSubmitting(true);
    try {
      await enqueue(items);
      setResult(`Queued ${items.length} comment${items.length === 1 ? '' : 's'} across ${activeRegions.length} location(s).`);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'enqueue failed');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <MapPin className="h-4 w-4" /> Comment by location
        </CardTitle>
        <CardDescription>
          Paste a target link, pick cities and accounts, then write a comment per account. Leave a comment
          blank to fall back to the template pool.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        {/* Step 1: target link */}
        <div className="space-y-2">
          <Label htmlFor="rcw-url">Target link</Label>
          <Input
            id="rcw-url"
            placeholder="https://www.instagram.com/p/…"
            value={targetUrl}
            onChange={(e) => setTargetUrl(e.target.value)}
          />
        </div>

        {/* Step 2+3: regions and account selection */}
        {loading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Loading locations…
          </div>
        ) : loadError ? (
          <p className="text-sm text-destructive">{loadError}</p>
        ) : groups.length === 0 ? (
          <p className="text-sm text-muted-foreground">No usable accounts found.</p>
        ) : (
          <div className="space-y-3">
            <Label>Locations (cities)</Label>
            <div className="flex flex-wrap gap-2">
              {groups.map((g) => (
                <Button
                  key={g.region}
                  type="button"
                  variant={regionOn[g.region] ? 'default' : 'outline'}
                  size="sm"
                  onClick={() => toggleRegion(g.region, g.accounts)}
                >
                  {g.region} <span className="ml-1 opacity-70">({g.accounts.length})</span>
                </Button>
              ))}
            </div>

            {activeRegions.map((g) => (
              <div key={g.region} className="rounded-lg border border-border/60 p-3 space-y-3">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">{g.region}</span>
                  <Button type="button" variant="outline" size="sm" onClick={() => selectAll(g.accounts, true)}>
                    Select all
                  </Button>
                  <Button type="button" variant="outline" size="sm" onClick={() => selectAll(g.accounts, false)}>
                    Clear
                  </Button>
                  <div className="flex items-center gap-1">
                    <Input
                      type="number"
                      min={1}
                      max={g.accounts.length}
                      className="h-8 w-20"
                      placeholder="N"
                      value={randomN[g.region] ?? ''}
                      onChange={(e) => setRandomN((s) => ({ ...s, [g.region]: e.target.value }))}
                    />
                    <Button type="button" variant="outline" size="sm" onClick={() => applyRandom(g.region, g.accounts)}>
                      Random N
                    </Button>
                  </div>
                </div>

                <div className="space-y-2">
                  {g.accounts.map((a) => {
                    const on = !!accountOn[a.id];
                    return (
                      <div key={a.id} className="flex items-start gap-2">
                        <Button
                          type="button"
                          variant={on ? 'default' : 'outline'}
                          size="sm"
                          className="mt-0.5 shrink-0"
                          onClick={() => toggleAccount(a.id)}
                        >
                          {on ? '✓' : '+'}
                        </Button>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2 text-sm">
                            <span className="truncate font-medium">{accountName(a)}</span>
                            <Badge variant="outline">{PLATFORM_LABEL[a.platform] ?? a.platform}</Badge>
                          </div>
                          {on && (
                            <Textarea
                              className="mt-1"
                              rows={2}
                              placeholder="Comment for this account (blank = template)"
                              value={text[a.id] ?? ''}
                              onChange={(e) => setText((s) => ({ ...s, [a.id]: e.target.value }))}
                            />
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Step 4: submit */}
        <div className="flex items-center gap-3">
          <Button type="button" onClick={submit} disabled={!canSubmit}>
            {submitting ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : <Send className="mr-2 h-4 w-4" />}
            Queue {selectedAccounts.length} comment{selectedAccounts.length === 1 ? '' : 's'}
          </Button>
          {selectedAccounts.length > MAX_BATCH && (
            <span className="text-sm text-destructive">Over the {MAX_BATCH} batch cap.</span>
          )}
          {result && <span className="text-sm text-success">{result}</span>}
          {error && <span className="text-sm text-destructive">{error}</span>}
        </div>
      </CardContent>
    </Card>
  );
}
