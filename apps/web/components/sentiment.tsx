import type { SentimentBreakdown, ScrapedComment } from '@/lib/api';

/**
 * Sentiment display for scraped comments.
 *
 * Two rules drive the whole component:
 *
 *  1. "Not analysed" is a visible state, never a silent default. A comment with
 *     no label shows no badge at all, and the distribution says how many were
 *     skipped. Rendering an unlabelled comment as NEUTRAL would tell the
 *     operator a machine read it calmly when nothing read it.
 *  2. The counts are a summary, not the payload. If the breakdown is empty but
 *     comments exist, the comments still render — an absent analysis must not
 *     hide scraped content.
 */

/** Per-label presentation. Colour carries meaning here (red = pushback), so the
 *  label text always accompanies it rather than relying on colour alone. */
const SENTIMENT_VIEW: Record<
  'POSITIVE' | 'NEUTRAL' | 'NEGATIVE',
  { label: string; dot: string; chip: string }
> = {
  POSITIVE: {
    label: 'Positive',
    dot: 'bg-emerald-500',
    chip: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  },
  NEUTRAL: {
    label: 'Neutral',
    dot: 'bg-muted-foreground/40',
    chip: 'bg-secondary text-muted-foreground',
  },
  NEGATIVE: {
    label: 'Negative',
    dot: 'bg-rose-500',
    chip: 'bg-rose-500/10 text-rose-700 dark:text-rose-400',
  },
};

/** One comment's label. Renders nothing when unanalysed — an empty badge would
 *  read as "neutral" at a glance. */
export function SentimentBadge({ sentiment }: { sentiment: ScrapedComment['sentiment'] }) {
  if (!sentiment) return null;
  const v = SENTIMENT_VIEW[sentiment];
  return (
    <span
      className={`inline-flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-[10px] font-medium ${v.chip}`}
    >
      <span className={`size-1.5 rounded-full ${v.dot}`} aria-hidden />
      {v.label}
    </span>
  );
}

/** The distribution bar + legend for a post's comments.
 *
 *  Deliberately renders nothing when there is nothing to say (no comments, or
 *  none analysed): an all-zero chart is noise, and the caller already shows the
 *  "no comments" state. */
export function SentimentSummary({ breakdown }: { breakdown: SentimentBreakdown }) {
  const analysed = breakdown.positive + breakdown.neutral + breakdown.negative;
  const total = analysed + breakdown.unanalysed;
  if (total === 0) return null;

  const parts = [
    { key: 'positive' as const, n: breakdown.positive },
    { key: 'neutral' as const, n: breakdown.neutral },
    { key: 'negative' as const, n: breakdown.negative },
  ].filter((p) => p.n > 0);

  return (
    <div className="space-y-2 border-t border-border/60 pt-3">
      <div className="flex items-baseline justify-between">
        <p className="text-[11px] uppercase tracking-wide text-muted-foreground">Comment sentiment</p>
        <p className="text-[11px] text-muted-foreground">
          {analysed} of {total} analysed
          {breakdown.unanalysed > 0 ? ` · ${breakdown.unanalysed} not analysed` : ''}
        </p>
      </div>

      {analysed > 0 ? (
        <>
          {/* Stacked bar: the proportion is the point, and it reads faster than
              three numbers. Widths are percentages of ANALYSED comments, so an
              unanalysed tail cannot make the split look milder than it is. */}
          <div
            className="flex h-2 overflow-hidden rounded-full bg-secondary"
            role="img"
            aria-label={`${breakdown.positive} positive, ${breakdown.neutral} neutral, ${breakdown.negative} negative`}
          >
            {parts.map((p) => (
              <div
                key={p.key}
                className={SENTIMENT_VIEW[p.key.toUpperCase() as 'POSITIVE'].dot}
                style={{ width: `${(p.n / analysed) * 100}%` }}
              />
            ))}
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            {(['POSITIVE', 'NEUTRAL', 'NEGATIVE'] as const).map((k) => {
              const n = breakdown[k.toLowerCase() as 'positive' | 'neutral' | 'negative'];
              return (
                <span key={k} className="flex items-center gap-1.5 text-xs">
                  <span className={`size-2 rounded-full ${SENTIMENT_VIEW[k].dot}`} aria-hidden />
                  <span className="font-semibold tabular-nums">{n}</span>
                  <span className="text-muted-foreground">{SENTIMENT_VIEW[k].label}</span>
                </span>
              );
            })}
          </div>
        </>
      ) : (
        <p className="text-xs text-muted-foreground">
          Not analysed yet — opening this post again will label the comments.
        </p>
      )}
    </div>
  );
}
