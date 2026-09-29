/**
 * Account-level engagement breakdown (P6-06).
 *
 * The KPI strip can only say "14 interactions" — one opaque number. The
 * provider already reports the parts inside the snapshot's metric bag, so this
 * splits them into likes / comments / saves / shares and adds the rate those
 * parts make against reach.
 *
 * Every figure is nullable and null renders as "—". A zero here would be a
 * claim: "nobody saved anything" is a finding, "we cannot see saves for this
 * account" is a gap, and only the second one is true.
 */

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@smm/ui';

export type EngagementBreakdownRow = {
  officialAccountId: string;
  handle: string;
  likes: number | null;
  comments: number | null;
  saves: number | null;
  shares: number | null;
  accountsEngaged: number | null;
  engagementRatePct: number | null;
};

function num(v: number | null | undefined): string {
  return v == null ? '—' : v.toLocaleString();
}

function Cell({ label, value }: { label: string; value: number | null }) {
  return (
    <div className="flex flex-col">
      <span className="text-lg font-semibold tabular-nums">{num(value)}</span>
      <span className="text-xs text-muted-foreground">{label}</span>
    </div>
  );
}

export function EngagementBreakdownCard({
  rows,
  platformLabel,
  loading,
}: {
  rows: EngagementBreakdownRow[];
  platformLabel: string;
  loading: boolean;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Engagement breakdown</CardTitle>
        <CardDescription>
          What the interaction totals on {platformLabel} are actually made of. Accounts with no synced
          snapshot are absent; a part the provider did not report shows as —.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {loading ? (
          <p className="py-6 text-center text-sm text-muted-foreground">Loading breakdown…</p>
        ) : rows.length === 0 ? (
          <div className="flex flex-col items-center gap-2 py-10 text-center">
            <p className="text-sm font-medium">No engagement breakdown for {platformLabel} yet</p>
            <p className="max-w-md text-xs text-muted-foreground">
              The breakdown is split out of the same sync that fills the charts. Run a sync to populate
              it.
            </p>
          </div>
        ) : (
          <ul className="divide-y divide-border/60">
            {rows.map((r) => (
              <li key={r.officialAccountId} className="py-4">
                <div className="mb-3 flex items-baseline justify-between gap-4">
                  <span className="font-medium">{r.handle}</span>
                  <span className="text-xs text-muted-foreground">
                    {r.engagementRatePct == null
                      ? 'Engagement rate unavailable'
                      : `${r.engagementRatePct.toFixed(1)}% of reach interacted`}
                  </span>
                </div>
                <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
                  <Cell label="Likes" value={r.likes} />
                  <Cell label="Comments" value={r.comments} />
                  <Cell label="Saves" value={r.saves} />
                  <Cell label="Shares" value={r.shares} />
                  <Cell label="Accounts engaged" value={r.accountsEngaged} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
