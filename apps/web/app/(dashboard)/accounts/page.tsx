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
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
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
import { useEffect, useMemo, useState } from 'react';

import { api } from '@/lib/api';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useContainers } from '@/lib/hooks/use-containers';
import type { Account, Container } from '@/lib/api';
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

const AUTH_STATUSES = [
  'PENDING',
  'AUTHENTICATING',
  'NEEDS_INPUT',
  'AUTHENTICATED',
  'FAILED',
  'QUARANTINED',
] as const;

function loginResultNote(error: string): string {
  const historical = error.match(/^historical_login_result: ([^ ]+)/);
  if (historical) {
    return historical[1] === 'OK'
      ? 'Login lama OK; session perlu diverifikasi ulang'
      : `Hasil login lama: ${historical[1]}`;
  }
  if (error.startsWith('historical_login_verified:'))
    return 'Pernah terverifikasi; session perlu dicek ulang';
  if (error.includes('wrong_password')) return 'Password salah';
  if (error.includes('captcha')) return 'Memerlukan CAPTCHA';
  if (error.includes('otp_required') || error.includes('codeentry') || error.includes('email_code'))
    return 'Perlu OTP';
  return 'Login belum selesai; cek melalui panel login';
}

// Action-first ordering: accounts that need the operator sit on top, a failed
// login next, the healthy fleet underneath. Ties break by platform, then name.
const AUTH_RANK: Record<Account['authStatus'], number> = {
  PENDING: 2,
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
        <div className="text-2xl leading-none font-semibold tracking-tight tabular-nums">
          {value}
        </div>
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
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [busy, setBusy] = useState<string | null>(null);
  const [operationError, setOperationError] = useState<string | null>(null);

  useEffect(() => setPage(1), [platform, status, auth, pageSize]);
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

  // Ticketed live-view URLs, keyed by account. In the stateless pool the live
  // view is a PUBLIC hostname, so the URL must be minted per open and expires in
  // minutes — it is not a standing heartbeat field. Kept in state because the
  // drawer needs it for the whole session of watching one account.
  // A ticket is valid for minutes, so the URL is cached WITH its expiry. Without
  // the expiry, reopening an account after a few minutes reused a dead ticket
  // and the iframe sat blank with nothing to explain it.
  const [liveView, setLiveView] = useState<Record<string, { url: string; expiresAt: number }>>({});
  const [liveViewBusy, setLiveViewBusy] = useState<string | null>(null);

  const novncFor = (a: Account): string | null => {
    const cached = liveView[a.id];
    // A minute of margin: a ticket that dies mid-handshake is the same blank
    // screen as one that was never minted.
    if (cached && cached.expiresAt - 60_000 > Date.now()) return cached.url;
    return a.workerId ? (novncByWorker.get(a.workerId) ?? null) : null;
  };

  /**
   * Mint a ticketed live-view URL and open the drawer on it. The API leases a
   * worker, opens the account profile there and signs a URL for that worker's
   * screen, so the operator sees the account's own logged-in browser.
   */
  const openLiveView = async (a: Account) => {
    setDetail({ id: a.id, watch: true });
    setLiveViewBusy(a.id);
    try {
      const view = await api.openAccountLiveView(a.id);
      const expiresAt = Date.parse(view.expiresAt);
      setLiveView((prev) => ({
        ...prev,
        [a.id]: { url: view.liveViewUrl, expiresAt: Number.isFinite(expiresAt) ? expiresAt : 0 },
      }));
    } catch (err) {
      setOperationError(err instanceof Error ? err.message : 'Could not open the live view');
    } finally {
      setLiveViewBusy(null);
    }
  };

  // workerId → desiredState, so the STATUS column can show what the fleet
  // controller is enforcing (RUNNING/PAUSED) next to the account's own
  // lifecycle. An account's ACTIVE used to read as "the process is up"; on a
  // paused fleet that was a lie the table told at a glance.
  const workerState = useMemo(() => {
    const m = new Map<string, Container['desiredState']>();
    for (const c of containers) m.set(c.id, c.desiredState);
    return m;
  }, [containers]);

  const workerStateFor = (a: Account): Container['desiredState'] | null =>
    a.workerId ? (workerState.get(a.workerId) ?? null) : null;

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

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const pageStart = (currentPage - 1) * pageSize;
  const pageEnd = Math.min(pageStart + pageSize, filtered.length);
  const pageAccounts = useMemo(
    () => filtered.slice(pageStart, pageEnd),
    [filtered, pageStart, pageEnd],
  );

  // The open panel's row, read live from the list (null = closed, or the row
  // was deleted while open — the panel unmounts in that case).
  const detailAccount = detail ? (accounts.find((a) => a.id === detail.id) ?? null) : null;
  const isWatching = detail?.watch ?? false;

  // Mint the ticketed URL as soon as the panel OPENS in watch mode (a login in
  // flight, or the operator's click). Waiting for a separate toggle left the
  // panel showing "the worker has not published its noVNC port" for an account
  // that simply had not been asked yet — in the pool the URL is minted per
  // account, never read off the worker list.
  useEffect(() => {
    if (!detailAccount || !isWatching) return;
    if (liveView[detailAccount.id]) return;
    if (liveViewBusy === detailAccount.id) return;
    void openLiveView(detailAccount);
    // openLiveView is stable enough for this trigger; re-running on every
    // render would mint a ticket per keystroke.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailAccount?.id, isWatching]);

  const filteredIds = useMemo(() => new Set(filtered.map((a) => a.id)), [filtered]);
  const pageIds = useMemo(() => new Set(pageAccounts.map((a) => a.id)), [pageAccounts]);
  // Drop selections that the current filter hid, so the bulk bar never
  // counts rows the operator cannot see.
  const effective = useMemo(
    () => [...selected].filter((id) => filteredIds.has(id)),
    [selected, filteredIds],
  );
  const allSelected = pageAccounts.length > 0 && pageAccounts.every((a) => selected.has(a.id));
  // Selections the filter is hiding. Not zero while a filter is on and rows are
  // checked, and the bar has to keep saying so: dropping the bar when `effective`
  // empties takes Clear with it, and Clear is the only way back — the operator
  // filters, and the selection they wanted to undo goes invisible with the button.
  const hiddenCount = selected.size - effective.length;

  const toggleAll = () => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (allSelected) pageIds.forEach((id) => next.delete(id));
      else pageIds.forEach((id) => next.add(id));
      return next;
    });
  };

  const onBulk = async (fn: (id: string) => Promise<void>) => {
    setBusy('__bulk__');
    setOperationError(null);
    try {
      for (const id of effective) await fn(id);
      setSelected(new Set());
    } catch (err) {
      setOperationError(err instanceof Error ? err.message : 'Account operation failed');
    } finally {
      setBusy(null);
    }
  };

  /**
   * Run one account action and, when the server says the account is still
   * leased, offer the one click that actually resolves it.
   *
   * A conflict here always means the same thing: an earlier live view or login
   * still holds a worker. Telling the operator to "wait for it to expire" is a
   * dead end they cannot see; closing the session is the action, so the error
   * carries it instead of just describing the problem.
   */
  const onAct = async (id: string, fn: (id: string) => Promise<void>) => {
    setBusy(id);
    setOperationError(null);
    try {
      await fn(id);
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Account operation failed';
      if (/already has a live session|worker busy|wait for it to expire/i.test(message)) {
        setOperationError(
          `${message}

This account still holds a worker from an earlier session. Use "Release session" to free it, then try again.`,
        );
      } else {
        // Keep the table usable; the SSE refresh restores truth on the next tick.
        setOperationError(message);
      }
    } finally {
      setBusy(null);
    }
  };

  /**
   * Free whatever an earlier session left behind: lease, Redis slot, and the
   * browser context on the worker. Idempotent, so it is safe on a clean account.
   */
  const releaseSession = async (id: string) => {
    setBusy(id);
    setOperationError(null);
    try {
      await api.closeAccountLiveView(id);
      setLiveView((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      await refresh();
    } catch (err) {
      setOperationError(err instanceof Error ? err.message : 'Could not release the session');
    } finally {
      setBusy(null);
    }
  };

  // Start an operator headful login. The worker opens the platform login page
  // in its noVNC view; the operator completes it there. The badge flips to
  // AUTHENTICATING now and the callback settles it.
  const startLogin = async (id: string) => {
    try {
      await api.startAccountLogin(id);
    } catch (err) {
      // Opening the cockpit on a fresh AUTHENTICATING row mints a watch lease,
      // and in the stateless pool that lease IS the login machine: the login
      // then 409s ("live view is open"). The lease is safe to drop here — the
      // operator asked for a login, not to keep watching a blank page.
      const message = err instanceof Error ? err.message : '';
      if (!/live view is open|already has a live session|worker busy/i.test(message)) throw err;
      await api.closeAccountLiveView(id).catch(() => undefined);
      await api.startAccountLogin(id);
    }
    refresh();
  };

  // Download the account's stored session (cookies only, never to the server
  // again). Useful before a rebuild, and for moving an account between fleets.
  const exportSession = async (a: Account) => {
    const session = await api.exportAccountSession(a.id);
    const blob = new Blob([JSON.stringify(session, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `session-${a.platform}-${a.username.replace(/[^a-z0-9._@-]/gi, '_')}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  // Submit a 2FA / checkpoint code to a parked login. The value arrives from
  // the detail drawer's form; the window.prompt version is gone with it.
  const submitInput = async (id: string, value: string) => {
    await api.submitAccountInput(id, value);
    refresh();
  };

  // Open the account's public profile in the account's WORKER virtual browser
  // (the noVNC live view), logged in through the account's session — not in
  // the operator's own browser. The dashboard sends the URL through the API
  // (SSRF-guarded to the platform hosts) and the worker navigates its live
  // Chromium; this page then opens the live view so the operator watches it.
  // No worker / no live view yet: fall back to an operator tab.
  const openProfile = async (a: Account) => {
    // A pool account has no bound worker until one is leased for it, so the
    // ticketed endpoint IS the "open profile in its own browser" action.
    if (!a.workerId || !novncFor(a)) {
      if (canAct) {
        await openLiveView(a);
        return;
      }
      window.open(profileUrlFor(a), '_blank', 'noopener,noreferrer');
      return;
    }
    // Legacy path: the account is already packed into a container that
    // publishes a standing noVNC URL, so no ticket is minted — the container
    // address IS the view.
    setDetail({ id: a.id, watch: true });
    try {
      await api.openBrowser(a.workerId, profileUrlFor(a), a.platform);
    } catch (err) {
      // The live view is already open above; surface the dispatch failure and
      // let the operator open the profile in their own tab instead.
      console.error('open profile in live view failed', err);
      window.open(profileUrlFor(a), '_blank', 'noopener,noreferrer');
    }
  };

  /**
   * "Live view" for one account, from ANY entry point.
   *
   * The pool has no standing noVNC URL: it is minted per open, expires in
   * minutes, and is bound to the worker the account is leased on. So every path
   * that wants to show a screen goes through openLiveView — the old code set
   * `watch: true` and hoped `novncByWorker` had an entry, which on the pool is
   * almost always null and rendered "the worker has not published its noVNC
   * port" for an account that simply had not been asked yet.
   */
  const showLiveView = (a: Account) => {
    if (canAct) {
      void openLiveView(a);
      return;
    }
    setDetail({ id: a.id, watch: true });
  };

  // Accounts that need the operator (a challenge in flight, or a live session
  // they want to watch) show the live view inside the drawer; a logged-in
  // account shows its profile instead — the two things an operator wants one
  // click away from this list.
  const needsLiveView = (a: Account): boolean =>
    a.authStatus === 'AUTHENTICATING' || a.authStatus === 'NEEDS_INPUT';

  // A panel that shows a screen must HAVE a screen: mint the ticket for the
  // login-in-flight case too, not only when the operator clicked a toggle.
  // Without this the drawer opened "watching" an account that had never been
  // asked for a worker, and said the worker published nothing.
  useEffect(() => {
    if (!detailAccount || !needsLiveView(detailAccount) || !canAct) return;
    if (liveView[detailAccount.id] || liveViewBusy === detailAccount.id) return;
    void openLiveView(detailAccount);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detailAccount?.id, detailAccount?.authStatus]);

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
            liveViewLoading={liveViewBusy === detailAccount.id}
            showLiveView={isWatching || needsLiveView(detailAccount)}
            canAct={canAct}
            busy={busy === detailAccount.id}
            onClose={() => {
              // Releasing on close is not cosmetic: an unclosed view keeps the
              // account's lease (a DB row) and hides it from the scheduler.
              // A login in flight is the exception — closing the panel is
              // closing the window, not cancelling the OTP the operator may
              // come back to. "Cancel this login" is the explicit exit.
              if (detailAccount && !needsLiveView(detailAccount)) {
                void api.closeAccountLiveView(detailAccount.id).catch(() => undefined);
              }
              setDetail(null);
            }}
            onStartLogin={(id) => void onAct(id, startLogin)}
            onImportSession={(a, session) =>
              void onAct(a.id, async () => {
                await api.importAccountSession(a.id, session);
                await refresh();
              })
            }
            onExportSession={(a) => void onAct(a.id, () => exportSession(a))}
            onSubmitInput={(id, value) => void onAct(id, (x) => submitInput(x, value))}
            onToggleWatch={() => {
              // Opening the view mints a fresh ticket (it expires in minutes);
              // closing just stops mounting the iframe.
              if (isWatching) {
                // A login in flight owns the worker: unmounting the iframe is
                // not the operator asking to abandon the OTP — it used to
                // release the session and close the very browser showing the
                // code form. Only a plain watch releases the lease.
                if (!needsLiveView(detailAccount)) {
                  void api.closeAccountLiveView(detailAccount.id).catch(() => undefined);
                }
                setDetail({ id: detailAccount.id, watch: false });
                return;
              }
              showLiveView(detailAccount);
            }}
            onOpenProfile={(a) => void openProfile(a)}
            onReleaseSession={(id) => void releaseSession(id)}
            onCancelParkedLogin={(id) => {
              // Closing the live view both frees the worker and settles the
              // parked attempt, so the account becomes retryable again.
              void api
                .closeAccountLiveView(id)
                .then(() => refresh())
                .catch((err: unknown) =>
                  setOperationError(err instanceof Error ? err.message : 'Could not cancel the login'),
                );
            }}
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
                  {loading ? 'Loading…' : `${filtered.length} of ${accounts.length} accounts`}
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

            {operationError || error ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                <AlertCircle className="size-4 shrink-0" />
                <span className="min-w-0 whitespace-pre-line">{operationError || error}</span>
                {/* The conflict text names an action; without the button the
                    operator has to guess which one and where. */}
                {detail ? (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy === detail.id}
                    onClick={() => void releaseSession(detail.id)}
                  >
                    Release session
                  </Button>
                ) : null}
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
                    {/* The account's own city. The "worker" column beside it
                        is a LEASE, not a home: an idle pool account has none. */}
                    <TableHead>Location</TableHead>
                    <TableHead>Worker</TableHead>
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
                    pageAccounts.map((a) => (
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
                          setDetail(
                            detail?.id === a.id ? null : { id: a.id, watch: needsLiveView(a) },
                          )
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
                                  : a.timezone || 'no timezone'}
                              </div>
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">{platformLabel(a.platform)}</Badge>
                        </TableCell>
                        <TableCell>
                          {a.location ? (
                            <Badge variant="outline">{a.location}</Badge>
                          ) : (
                            <span className="text-xs text-muted-foreground">—</span>
                          )}
                        </TableCell>
                        <TableCell className="font-mono text-xs text-muted-foreground">
                          {/* workerId here is the interactive LEASE, so a dash
                              means idle/schedulable — not unplaced. */}
                          {a.workerId ? a.workerId.slice(0, 12) : 'idle'}
                        </TableCell>
                        <TableCell>
                          <AuthBadge status={a.authStatus} />
                          {a.lastError && (
                            <p className="mt-1 max-w-56 text-xs text-muted-foreground">
                              {loginResultNote(a.lastError)}
                            </p>
                          )}
                        </TableCell>
                        <TableCell>
                          {/* Primary: the worker's enforced state (what is
                           * actually running). Secondary: the account's own
                           * lifecycle. Two pills, no colour lie. */}
                          <div className="flex items-center gap-1.5">
                            {workerStateFor(a) === 'PAUSED' ? (
                              <Badge variant="warning">PAUSED</Badge>
                            ) : workerStateFor(a) === 'RUNNING' ? (
                              <Badge variant="success">ACTIVE</Badge>
                            ) : null}
                            <StatusBadge status={a.status} />
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-1">
                            {a.authStatus === 'AUTHENTICATED' ? (
                              <Button
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8"
                                aria-label="Open profile in live view"
                                title="Open the profile in this account's worker browser (live view)"
                                disabled={busy === a.id || !canAct}
                                onClick={(e) => {
                                  e.stopPropagation();
                                  void onAct(a.id, () => openProfile(a));
                                }}
                              >
                                <ExternalLink className="size-3.5" />
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
                                  <DropdownMenuItem
                                    onClick={() => setDetail({ id: a.id, watch: true })}
                                  >
                                    <KeyRound />
                                    Enter code
                                  </DropdownMenuItem>
                                ) : null}
                                {/*
                              The live view is reachable from the menu in any auth
                              state as long as the worker publishes a noVNC URL: an
                              operator can watch the browser on demand (e.g. to
                              re-check a failed login) without starting a new one.
                            */}
                                {novncFor(a) ? (
                                  <DropdownMenuItem
                                    onClick={() => setDetail({ id: a.id, watch: true })}
                                  >
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
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <label htmlFor="accounts-page-size">Rows per page</label>
                <Select
                  value={String(pageSize)}
                  onValueChange={(value) => setPageSize(Number(value))}
                >
                  <SelectTrigger
                    id="accounts-page-size"
                    className="w-20"
                    aria-label="Rows per page"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[25, 50, 100].map((size) => (
                      <SelectItem key={size} value={String(size)}>
                        {size}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <span className="text-sm tabular-nums text-muted-foreground" role="status">
                {filtered.length === 0 ? 0 : pageStart + 1}–{pageEnd} of {filtered.length} accounts
              </span>
              <nav className="flex items-center gap-1" aria-label="Accounts pagination">
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="First page"
                  disabled={loading || currentPage === 1}
                  onClick={() => setPage(1)}
                >
                  <ChevronsLeft />
                </Button>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Previous page"
                  disabled={loading || currentPage === 1}
                  onClick={() => setPage(currentPage - 1)}
                >
                  <ChevronLeft />
                </Button>
                <span className="px-2 text-sm tabular-nums">
                  Page {currentPage} of {pageCount}
                </span>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Next page"
                  disabled={loading || currentPage === pageCount}
                  onClick={() => setPage(currentPage + 1)}
                >
                  <ChevronRight />
                </Button>
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Last page"
                  disabled={loading || currentPage === pageCount}
                  onClick={() => setPage(pageCount)}
                >
                  <ChevronsRight />
                </Button>
              </nav>
            </div>
          </CardContent>
        </Card>
      )}

      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} />
    </div>
  );
}
