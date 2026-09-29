/**
 * Per-post performance table (P6-06).
 *
 * One row per piece of content, ordered by reach. Every cell that the provider
 * did not report renders as "—" and never as 0: a post nobody saw and a post
 * nobody engaged with are different facts, and collapsing them to a zero would
 * make a data gap look like a result. Instagram makes this concrete — it
 * deprecated impressions outright and gives a static image no views at all.
 */

import {
  Badge,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@smm/ui';
import { ExternalLink } from 'lucide-react';

import type { ContentMetric } from '@/lib/analytics';

/** A missing value. `0` is a measurement; null is the absence of one. */
function num(v: number | null | undefined): string {
  return v == null ? '—' : v.toLocaleString();
}

function pct(v: number | null | undefined): string {
  return v == null ? '—' : `${v.toFixed(1)}%`;
}

function day(v: string | null | undefined): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime())
    ? '—'
    : d.toLocaleDateString(undefined, { day: '2-digit', month: 'short', year: 'numeric' });
}

/**
 * A caption is free text that can run to thousands of characters and start with
 * a newline. Collapsing whitespace and clipping keeps one post from dictating
 * the height of the whole table.
 */
function excerpt(caption: string | null | undefined, max = 80): string {
  if (!caption) return '';
  const flat = caption.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max - 1)}…` : flat;
}

export function ContentMetricsTable({
  items,
  platformLabel,
  loading,
  accountLabel,
}: {
  items: ContentMetric[];
  platformLabel: string;
  loading: boolean;
  /** The account filter currently applied, or "All official accounts". */
  accountLabel: string;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Top content</CardTitle>
        <CardDescription>
          Per-post performance for {accountLabel === 'All official accounts' ? `every account on ${platformLabel}` : accountLabel}, ordered by reach. Figures are lifetime totals, not daily increments.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {loading ? (
          <p className="py-6 text-center text-sm text-muted-foreground">Loading content…</p>
        ) : items.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-10 text-center">
            <p className="text-sm font-medium">No per-post data for {platformLabel} yet</p>
            <p className="max-w-md text-xs text-muted-foreground">
              Per-post metrics come from the same sync as the charts. Run a sync, and this table fills in
              once the provider has reported on each post.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[30%]">Post</TableHead>
                  <TableHead className="text-right">Reach</TableHead>
                  <TableHead className="text-right">Views</TableHead>
                  <TableHead className="text-right">Likes</TableHead>
                  <TableHead className="text-right">Comments</TableHead>
                  <TableHead className="text-right">Saves</TableHead>
                  <TableHead className="text-right">Shares</TableHead>
                  <TableHead className="text-right">Eng. rate</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((it) => {
                  const caption = excerpt(it.caption);
                  return (
                    <TableRow key={it.id}>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          <div className="flex items-center gap-2">
                            {it.contentType ? (
                              <Badge variant="info">{it.contentType}</Badge>
                            ) : null}
                            <span className="text-xs text-muted-foreground">{day(it.publishedAt)}</span>
                            {it.permalink ? (
                              <a
                                href={it.permalink}
                                target="_blank"
                                rel="noreferrer noopener"
                                aria-label={`Open post ${it.externalId} on ${platformLabel}`}
                                className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-2 hover:underline"
                              >
                                open
                                <ExternalLink className="size-3" aria-hidden />
                              </a>
                            ) : null}
                          </div>
                          {/* The caption IS the post: with no thumbnail and no
                              title, it is the only thing that tells one row
                              from another. */}
                          {caption ? (
                            <span className="line-clamp-2 text-sm">{caption}</span>
                          ) : (
                            <span className="text-sm italic text-muted-foreground">No caption</span>
                          )}
                          <span className="text-xs text-muted-foreground">@{it.accountHandle}</span>
                        </div>
                      </TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.reach)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.views)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.likes)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.comments)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.saves)}</TableCell>
                      <TableCell className="text-right tabular-nums">{num(it.shares)}</TableCell>
                      <TableCell className="text-right tabular-nums">{pct(it.engagementRatePct)}</TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
