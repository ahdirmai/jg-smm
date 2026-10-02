'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  cn,
} from '@smm/ui';
import {
  AlertCircle,
  CheckCircle2,
  ExternalLink,
  KeyRound,
  MonitorPlay,
  MoreVertical,
  Pause,
  Play,
  Trash2,
  Upload,
  UserPlus,
  Users,
  type LucideIcon,
} from 'lucide-react';
import Link from 'next/link';
import { useMemo, useState } from 'react';

import { api } from '@/lib/api';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useContainers } from '@/lib/hooks/use-containers';
import type { Account } from '@/lib/api';
import { AddAccountDialog } from '@/components/add-account-dialog';
import { ImportAccountsDialog } from '@/components/import-accounts-dialog';
import { AccountDetailPanel, profileUrlFor } from '@/components/account-detail-panel';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';
// The platform list, the label map and the label lookup were all re-declared
// here as a third hand-typed copy of the same seven ids, alongside two in
// accounts/new and lib/analytics-config. Keyed off lib/platforms.ts, an eighth
// platform added to the backend cannot land in this page silently missing a
// label.
import { PLATFORMS, PLATFORM_LABEL, platformLabel } from '@/lib/platforms';
import { AuthBadge, StatusBadge } from '@/components/account-badges';
import { platformIcon } from '@/components/platform-icon';

const STATUSES = ['PENDING', 'ACTIVE', 'PAUSED', 'QUARANTINED', 'DEAD', 'ARCHIVED'] as const;

const AUTH_STATUSES = ['AUTHENTICATING', 'NEEDS_INPUT', 'AUTHENTICATED', 'FAILED', 'QUARANTINED'] as const;

// Action-first ordering: accounts that need the operator sit on top, a failed
// login next, the healthy fleet underneath. Ties break by platform, then name.
const AUTH_RANK: Record<Account['authStatus'], number> = {
  AUTHENTICATING: 0,
  NEEDS_INPUT: 0,
  FAILED: 1,
  QUARANTINED: 2,
  AUTHENTICATED: 3,
};

/** One hero number (principle 4: number large and bold, label small beneath). */
/**
 * One hero number (principle 4: number large and bold, label small beneath).
 * With `onSelect` the card becomes a filter toggle — the number stays
 * fleet-wide; only the table below obeys it.
 */
function Stat({
  label,
  value,
  hint,
  icon: Icon,
  onSelect,
  active,
}: {
  label: string;
  value: number;
  hint?: string;
  icon: LucideIcon;
  onSelect?: () => void;
  active?: boolean;
}) {
  const inner = (
    <div className="flex items-start gap-3 p-5">
      <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-secondary text-muted-foreground">
        <Icon className="size-4" />
      </span>
      <div className="min-w-0 space-y-1">
        <div className="text-2xl leading-none font-semibold tracking-tight tabular-nums">{value}</div>
        <div className="text-xs tracking-wide text-muted-foreground uppercase">{label}</div>
        {hint ? <div className="truncate text-xs text-muted-foreground">{hint}</div> : null}
      </div>
    </div>
  );
  return (
    <Card className={cn('overflow-hidden', active && 'ring-2 ring-ring/60')}>
      {onSelect ? (
        <button
          type="button"
          aria-pressed={active}
          onClick={onSelect}
          className="block w-full text-left transition-colors hover:bg-accent/40"
        >
          {inner}
        </button>
      ) : (
        inner
      )}
    </Card>
  );
}

export default function AccountsPage() {
  const { accounts, loading, error, setStatus, remove, refresh } = useAccounts();
  // The live view lives on the account's worker container, so this page needs
  // the fleet to resolve an account → its noVNC URL.
  const { containers } = useContainers();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');
  const [addOpen, setAddOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [platform, setPlatform] = useState<string>('all');
  const [status, setStatusFilter] = useState<string>('all');
  const [auth, setAuthFilter] = useState<string>('all');
  const [busy, setBusy] = useState<string | null>(null);
  // The detail drawer is shared by the row click and the dropdown items. The
  // row is addressed by id so the panel re-renders from the live `accounts`
  // list (an in-flight login settling is visible without reopening). `watch`
  // forces the live view for an authenticated account the operator chose to
  // monitor.
  const [detail, setDetail] = useState<{ id: string; watch: boolean } | null>(null);
  // P6 parity: bulk select over the filtered set. Ops pause/resume/remove
  // many accounts at once; each call hits the real per-id endpoints.
  const [selected, setSelected] = useState<Set<string>>(new Set());

  // account id → its worker's noVNC URL. Workers without a heartbeat publish
  // nothing, so absent means "no live view yet", not a broken link.
  const novncByWorker = useMemo(() => {
    const m = new Map<string, string>();
    for (const c of containers) {
      if (c.novncUrl) m.set(c.id, c.novncUrl as string);
    }
    return m;
  }, [containers]);

  const novncFor = (a: Account): string | null =>
    a.workerId ? (novncByWorker.get(a.workerId) ?? null) : null;

  // Containers that can take a login right now. Read by the first-run panel,
  // where "no accounts" is only actionable if the fleet says whether there is
  // anywhere to put one.
  const ready = useMemo(
    () => containers.filter((c) => c.status === 'READY' || c.status === 'IDLE'),
    [containers],
  );

  const filtered = useMemo(() => {
    return accounts
      .filter((a) => {
        if (platform !== 'all' && a.platform !== platform) return false;
        if (status !== 'all' && a.status !== status) return false;
        if (auth !== 'all' && a.authStatus !== auth) return false;
        return true;
      })
      .slice()
      .sort(
        (a, b) =>
          AUTH_RANK[a.authStatus] - AUTH_RANK[b.authStatus] ||
          a.platform.localeCompare(b.platform) ||
          a.username.localeCompare(b.username),
      );
  }, [accounts, platform, status, auth]);

  // The open panel's row, read live from the list (null = closed, or the row
  // was deleted while open — the panel unmounts in that case).
  const detailAccount = detail ? accounts.find((a) => a.id === detail.id) ?? null : null;
  const isWatching = detail?.watch ?? false;

  const visibleIds = useMemo(() => new Set(filtered.map((a) => a.id)), [filtered]);
  // Drop selections that the current filter hid, so the bulk bar never
  // counts rows the operator cannot see.
  const effective = useMemo(
    () => [...selected].filter((id) => visibleIds.has(id)),
    [selected, visibleIds],
  );
  const allSelected = effective.length > 0 && effective.length === filtered.length;
  // Selections the filter is hiding. Not zero while a filter is on and rows are
  // checked, and the bar has to keep saying so: dropping the bar when `effective`
  // empties takes Clear with it, and Clear is the only way back — the operator
  // filters, and the selection they wanted to undo goes invisible with the button.
  const hiddenCount = selected.size - effective.length;

  const toggleAll = () => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allSelected) visibleIds.forEach((id) => next.delete(id));
      else visibleIds.forEach((id) => next.add(id));
      return next;
    });
  };

  const onBulk = async (fn: (id: string) => Promise<void>) => {
    setBusy('__bulk__');
    try {
      for (const id of effective) await fn(id);
      setSelected(new Set());
    } catch (err) {
      console.error('bulk account op failed', err);
    } finally {
      setBusy(null);
    }
  };

  const onAct = async (id: string, fn: (id: string) => Promise<void>) => {
    setBusy(id);
    try {
      await fn(id);
    } catch (err) {
      // Keep the table usable; the SSE refresh restores truth on the next tick.
      console.error('account op failed', err);
    } finally {
      setBusy(null);
    }
  };

  // Start an operator headful login. The worker opens the platform login page
  // in its noVNC view; the operator completes it there. The badge flips to
  // AUTHENTICATING now and the callback settles it.
  const startLogin = async (id: string) => {
    await api.startAccountLogin(id);
    refresh();
  };

  // Submit a 2FA / checkpoint code to a parked login. The value arrives from
  // the detail drawer's form; the window.prompt version is gone with it.
  const submitInput = async (id: string, value: string) => {
    await api.submitAccountInput(id, value);
    refresh();
  };

  // Accounts that need the operator (a challenge in flight, or a live session
  // they want to watch) show the live view inside the drawer; a logged-in
  // account shows its profile instead — the two things an operator wants one
  // click away from this list.
  const needsLiveView = (a: Account): boolean =>
    a.authStatus === 'AUTHENTICATING' || a.authStatus === 'NEEDS_INPUT';


  // Fleet rollup for the stat strip. Counted over ALL accounts, not the
  // filtered view: the numbers answer "how healthy is the fleet", and a filter
  // that silently moved them would read as a bug.
  const stats = useMemo(() => {
    const byPlatform = new Map<string, number>();
    for (const a of accounts) byPlatform.set(a.platform, (byPlatform.get(a.platform) ?? 0) + 1);
    const top = [...byPlatform.entries()].sort((a, b) => b[1] - a[1])[0];
    return {
      total: accounts.length,
      authenticated: accounts.filter((a) => a.authStatus === 'AUTHENTICATED').length,
      needsInput: accounts.filter((a) => a.authStatus === 'NEEDS_INPUT').length,
      failed: accounts.filter((a) => a.authStatus === 'FAILED').length,
      paused: accounts.filter((a) => a.status === 'PAUSED').length,
      unassigned: accounts.filter((a) => !a.workerId).length,
      topPlatform: top ? `${platformLabel(top[0])} · ${top[1]}` : undefined,
    };
  }, [accounts]);

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      {/* Page-level actions. The title/subtitle live in the shell header (h-14),
          so this row carries only the write controls. */}
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button variant="outline" disabled={!canAct} onClick={() => setImportOpen(true)}>
          <Upload />
          Import
        </Button>
        <Button asChild disabled={!canAct}>
          <Link href="/accounts/new">
            <UserPlus />
            Add account
          </Link>
        </Button>
      </div>

      {/* A wall of zeros is not a stat strip — with no accounts the numbers
          carry nothing, so the empty state below speaks alone. */}
      {accounts.length > 0 ? (
        <section className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <Stat
            icon={Users}
            label="Accounts"
            value={stats.total}
            {...(stats.topPlatform ? { hint: `top: ${stats.topPlatform}` } : {})}
          />
          <Stat
            icon={CheckCircle2}
            label="Authenticated"
            value={stats.authenticated}
            hint={`${stats.total - stats.authenticated} pending`}
          />
          <Stat
            icon={KeyRound}
            label="Needs input"
            value={stats.needsInput}
            {...(stats.failed > 0
              ? { hint: `${stats.failed} failed` }
              : { hint: '2FA / checkpoint' })}
            onSelect={() => setAuthFilter(auth === 'NEEDS_INPUT' ? 'all' : 'NEEDS_INPUT')}
            active={auth === 'NEEDS_INPUT'}
          />
          <Stat
            icon={Pause}
            label="Paused"
            value={stats.paused}
            {...(stats.unassigned > 0 ? { hint: `${stats.unassigned} unassigned` } : {})}
          />
        </section>
      ) : null}

      {/* Login cockpit: selecting a row puts the whole login lifecycle on the
          page — live view, code input, next step — without an overlay, so the
          table below stays in the same view and keeps updating. */}
      {detailAccount ? (
        <Card>
          <AccountDetailPanel
            account={detailAccount}
            novncUrl={novncFor(detailAccount)}
            showLiveView={isWatching || needsLiveView(detailAccount)}
            canAct={canAct}
            busy={busy === detailAccount.id}
            onClose={() => setDetail(null)}
            onStartLogin={(id) => void onAct(id, startLogin)}
            onSubmitInput={(id, value) => void onAct(id, (x) => submitInput(x, value))}
            onToggleWatch={() => setDetail({ id: detailAccount.id, watch: !isWatching })}
          />
        </Card>
      ) : null}

      <ImportAccountsDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onImported={() => void refresh()}
      />

      {/* First run: an empty table states nothing. The two ways in are the
          whole content of this page until an account exists. */}
      {!loading && accounts.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-5 px-6 py-14 text-center">
            <span className="grid size-12 place-items-center rounded-xl bg-secondary text-muted-foreground">
              <Users className="size-6" />
            </span>
            <div className="max-w-md space-y-1.5">
              <p className="text-lg font-semibold tracking-tight">No accounts yet</p>
              <p className="text-sm text-muted-foreground">
                An account pairs a login with a container. Add one to pick its platform and
                container, or import a CSV to enrol a batch at once.
              </p>
            </div>
            {/* The fleet is built before the roster: saying how much room is
                waiting turns "add an account" into a concrete next step. */}
            <Badge variant={ready.length > 0 ? 'success' : 'outline'}>
              {ready.length > 0 ? (
                <CheckCircle2 className="size-3" />
              ) : (
                <AlertCircle className="size-3" />
              )}
              {ready.length > 0
                ? `${ready.length} container${ready.length === 1 ? '' : 's'} ready`
                : 'No container ready — create one first'}
            </Badge>
            <div className="flex flex-wrap items-center justify-center gap-2">
              <Button asChild disabled={!canAct}>
                <Link href="/accounts/new">
                  <UserPlus />
                  Add account
                </Link>
              </Button>
              <Button variant="outline" disabled={!canAct} onClick={() => setImportOpen(true)}>
                <Upload />
                Import CSV
              </Button>
            </div>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardHeader className="gap-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="space-y-1.5">
                <CardTitle>Fleet accounts</CardTitle>
                <CardDescription>
                  {loading ? 'Loading…' : `${filtered.length} of ${accounts.length} shown`}
                </CardDescription>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Select value={platform} onValueChange={setPlatform}>
                  <SelectTrigger className="w-40" aria-label="All platforms">
                    <SelectValue placeholder="All platforms" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All platforms</SelectItem>
                    {PLATFORMS.map((p) => (
                      <SelectItem key={p} value={p}>
                        {PLATFORM_LABEL[p]}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Select value={status} onValueChange={setStatusFilter}>
                  <SelectTrigger className="w-40" aria-label="All statuses">
                    <SelectValue placeholder="All statuses" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All statuses</SelectItem>
                    {STATUSES.map((s) => (
                      <SelectItem key={s} value={s}>
                        {s[0] + s.slice(1).toLowerCase()}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>

                <Select value={auth} onValueChange={setAuthFilter}>
                  <SelectTrigger className="w-44" aria-label="All auth statuses">
                    <SelectValue placeholder="All auth statuses" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All auth statuses</SelectItem>
                    {AUTH_STATUSES.map((s) => (
                      <SelectItem key={s} value={s}>
                        {s}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            {selected.size > 0 ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/60 bg-secondary/40 p-3">
                <span className="text-sm font-medium">
                  {effective.length} selected
                  {hiddenCount > 0 ? (
                    <span className="text-muted-foreground">
                      {' · '}
                      {hiddenCount} hidden by filter
                    </span>
                  ) : null}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy === '__bulk__' || !canAct || effective.length === 0}
                  onClick={() => void onBulk((id) => setStatus(id, 'PAUSED'))}
                >
                  <Pause className="size-3.5" />
                  Pause
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={busy === '__bulk__' || !canAct || effective.length === 0}
                  onClick={() => void onBulk((id) => setStatus(id, 'ACTIVE'))}
                >
                  <Play className="size-3.5" />
                  Resume
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="text-destructive"
                  disabled={busy === '__bulk__' || !canAct || effective.length === 0}
                  onClick={() => void onBulk(remove)}
                >
                  <Trash2 className="size-3.5" />
                  Remove
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy === '__bulk__'}
                  onClick={() => setSelected(new Set())}
                >
                  Clear
                </Button>
              </div>
            ) : null}

            {error ? (
              <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {error}
              </div>
            ) : null}

            <div className="overflow-hidden rounded-lg border border-border/60">
              <Table>
                <TableHeader>
                  <TableRow className="bg-secondary/40 hover:bg-secondary/40">
                    <TableHead className="w-10">
                      <input
                        type="checkbox"
                        className="size-4 rounded border-input"
                        aria-label="Select all visible accounts"
                        checked={allSelected}
                        onChange={toggleAll}
                        disabled={!canAct || filtered.length === 0}
                      />
                    </TableHead>
                    <TableHead>Account</TableHead>
                    <TableHead>Platform</TableHead>
                    <TableHead>Container</TableHead>
                    <TableHead>Auth</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="w-10" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filtered.length === 0 && !loading ? (
                    <TableRow>
                      <TableCell colSpan={7} className="py-0">
                        {/* Reached only with accounts present — the zero-account
                          case never renders this table (see above). */}
                        <div className="flex flex-col items-center gap-3 py-12 text-center">
                          <span className="grid size-10 place-items-center rounded-xl bg-secondary text-muted-foreground">
                            <Users className="size-5" />
                          </span>
                          <div className="space-y-1">
                            <p className="font-medium">Nothing matches</p>
                            <p className="text-sm text-muted-foreground">
                              Clear the filters to see the rest of the fleet.
                            </p>
                          </div>
                        </div>
                      </TableCell>
                    </TableRow>
                  ) : (
                    filtered.map((a) => (
                      <TableRow
                        key={a.id}
                        // The row is the primary action: one click opens the
                        // account panel above the table (live view during a
                        // login, profile for an authenticated account); the
                        // same row again closes it. Controls inside
                        // stopPropagation so they keep working. (role/tabIndex
                        // stay off a <tr> — ARIA forbids it — and the icon
                        // buttons in the last cell are real buttons, so
                        // keyboard users have the same actions one Tab away.)
                        className="cursor-pointer transition-colors hover:bg-accent/60"
                        onClick={() =>
                          setDetail(detail?.id === a.id ? null : { id: a.id, watch: needsLiveView(a) })
                        }
                      >
                        <TableCell>
                          <input
                            type="checkbox"
                            className="size-4 rounded border-input"
                            aria-label={`Select @${a.username}`}
                            checked={selected.has(a.id)}
                            // The row's handler is onClick, so stopping the
                            // click here is what keeps the row action from
                            // firing — stopping `change` (a different event)
                            // does not, and clicking a checkbox to select it
                            // would otherwise also open the account.
                            onClick={(e) => e.stopPropagation()}
                            onChange={() => {
                              setSelected((prev) => {
                                const next = new Set(prev);
                                if (next.has(a.id)) next.delete(a.id);
                                else next.add(a.id);
                                return next;
                              });
                            }}
                            disabled={!canAct}
                          />
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-2.5">
                            {/* Principle 6: monochrome glyph in a soft square —
                              the same device the container cards use, so the
                              two operator pages read as one surface. */}
                            <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-secondary text-muted-foreground">
                              {(() => {
                                const Icon = platformIcon(a.platform);
                                return <Icon className="size-4" />;
                              })()}
                            </span>
                            <div className="min-w-0">
                              <div className="truncate font-medium">@{a.username}</div>
                              <div className="truncate font-mono text-xs text-muted-foreground">
                                {a.handle && a.handle !== a.username
                                  ? a.handle
                                  : a.workerId
                                    ? a.workerId.slice(0, 12)
                                    : 'unassigned'}
                              </div>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">{platformLabel(a.platform)}</Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">
                          {a.workerId ? a.workerId.slice(0, 12) : '—'}
                        </TableCell>
                        <TableCell>
                          <AuthBadge status={a.authStatus} />
                        </TableCell>
                        <TableCell>
                          <StatusBadge status={a.status} />
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-1">
                            {a.authStatus === 'AUTHENTICATED' ? (
                              <Button
                                asChild
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8"
                                aria-label="Open profile"
                              >
                                <a
                                  href={profileUrlFor(a)}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  onClick={(e) => e.stopPropagation()}
                                >
                                  <ExternalLink className="size-3.5" />
                                </a>
                              </Button>
                            ) : null}
                            <DropdownMenu>
                              <DropdownMenuTrigger asChild>
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  aria-label="Open account menu"
                                  disabled={busy === a.id || !canAct}
                                  onClick={(e) => e.stopPropagation()}
                                >
                                  <MoreVertical />
                                </Button>
                              </DropdownMenuTrigger>
                              <DropdownMenuContent
                                align="end"
                                // Radix renders this in a portal, but React
                                // still bubbles its events up the React tree —
                                // to the TableRow, whose onClick opens the
                                // account. Without this, choosing "Pause" also
                                // opens the live browser over the menu.
                                onClick={(e) => e.stopPropagation()}
                              >
                                <DropdownMenuLabel>Account ops</DropdownMenuLabel>
                                <DropdownMenuSeparator />
                                {a.status === 'PAUSED' ? (
                                  <DropdownMenuItem
                                    onClick={() =>
                                      void onAct(a.id, (id) => setStatus(id, 'ACTIVE'))
                                    }
                                  >
                                    <Play />
                                    Resume
                                  </DropdownMenuItem>
                                ) : (
                                  <DropdownMenuItem
                                    onClick={() =>
                                      void onAct(a.id, (id) => setStatus(id, 'PAUSED'))
                                    }
                                  >
                                    <Pause />
                                    Pause
                                  </DropdownMenuItem>
                                )}
                                <DropdownMenuSeparator />
                                <DropdownMenuItem onClick={() => void onAct(a.id, startLogin)}>
                                  <KeyRound />
                                  Log in
                                </DropdownMenuItem>
                                {a.authStatus === 'NEEDS_INPUT' ? (
                                  <DropdownMenuItem onClick={() => setDetail({ id: a.id, watch: true })}>
                                    <KeyRound />
                                    Enter code
                                  </DropdownMenuItem>
                                ) : null}
                                {/*
                              The live view is also reachable from the menu for an
                              authenticated account, so an operator can still
                              watch a running session without starting a login.
                            */}
                                {a.authStatus === 'AUTHENTICATED' && novncFor(a) ? (
                                  <DropdownMenuItem onClick={() => setDetail({ id: a.id, watch: true })}>
                                    <MonitorPlay />
                                    Live view
                                  </DropdownMenuItem>
                                ) : null}
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  className="text-destructive focus:text-destructive"
                                  onClick={() => void onAct(a.id, remove)}
                                >
                                  <Trash2 />
                                  Remove
                                </DropdownMenuItem>
                              </DropdownMenuContent>
                            </DropdownMenu>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      )}

      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} />

    </div>
  );
}

