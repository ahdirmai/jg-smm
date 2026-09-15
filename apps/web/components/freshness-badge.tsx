/**
 * FreshnessBadge renders the sync state of the analytics path (P2-15 AC:
 * stale when the last successful ingest is older than 60 minutes). It is the
 * single place the dashboard tells an operator "you are reading old numbers".
 */

import { Badge } from '@smm/ui';
import { Clock, RefreshCw } from 'lucide-react';

import type { ApiSchemas } from '@smm/shared';

type Freshness = ApiSchemas['AnalyticsFreshness'];

const THRESHOLD_MINUTES = 60;

export function FreshnessBadge({ freshness }: { freshness?: Freshness | null | undefined }) {
  if (!freshness) return null;

  const stale = freshness.stale;
  const lastRun = freshness.lastRunAt ? new Date(freshness.lastRunAt) : null;
  const minsAgo = lastRun
    ? Math.max(0, Math.round((Date.now() - lastRun.getTime()) / 60_000))
    : null;

  const label = stale
    ? minsAgo === null
      ? 'Never synced'
      : `Stale · ${minsAgo}m ago`
    : minsAgo === null
      ? 'In sync'
      : `In sync · ${minsAgo}m ago`;

  return (
    <Badge variant={stale ? 'destructive' : 'success'} className="gap-1">
      {stale ? <Clock className="h-3 w-3" /> : <RefreshCw className="h-3 w-3" />}
      {label}
    </Badge>
  );
}

export { THRESHOLD_MINUTES };
