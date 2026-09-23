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
} from '@smm/ui';
import {
  AlertCircle,
  ExternalLink,
  KeyRound,
  MonitorPlay,
  MoreVertical,
  Pause,
  Play,
  Trash2,
  Upload,
  UserPlus,
} from 'lucide-react';
import Link from 'next/link';
import { useMemo, useState } from 'react';

import { api } from '@/lib/api';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useContainers } from '@/lib/hooks/use-containers';
import type { Account } from '@/lib/api';
import { AddAccountDialog } from '@/components/add-account-dialog';
import { ImportAccountsDialog } from '@/components/import-accounts-dialog';
import { LiveBrowserModal } from '@/components/live-browser-modal';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

const PLATFORMS = [
  'INSTAGRAM',
  'THREADS',
  'FACEBOOK',
  'TIKTOK',
  'LINKEDIN',
  'X',
  'YOUTUBE',
] as const;
type Platform = (typeof PLATFORMS)[number];

const PLATFORM_LABEL: Record<Platform, string> = {
  INSTAGRAM: 'Instagram',
  THREADS: 'Threads',
  FACEBOOK: 'Facebook',
  TIKTOK: 'TikTok',
  LINKEDIN: 'LinkedIn',
  X: 'X',
  YOUTUBE: 'YouTube',
};

const STATUSES = ['PENDING', 'ACTIVE', 'PAUSED', 'QUARANTINED', 'DEAD', 'ARCHIVED'] as const;

function statusTone(
  status: Account['status'],
): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'ACTIVE':
      return 'success';
    case 'PAUSED':
      return 'secondary';
    case 'ARCHIVED':
    case 'DEAD':
      return 'destructive';
    default:
      return 'outline';
  }
}

function authTone(status: Account['authStatus']): 'success' | 'outline' | 'destructive' {
  switch (status) {
    case 'AUTHENTICATED':
      return 'success';
    case 'FAILED':
      return 'destructive';
    default:
      return 'outline';
  }
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
  const [busy, setBusy] = useState<string | null>(null);
  // The live-view modal is shared by the row action and the dropdown item.
  const [live, setLive] = useState<{ name: string; url: string } | null>(null);
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

  const novncFor = (a: Account): string | null => (a.workerId ? novncByWorker.get(a.workerId) ?? null : null);

  const filtered = useMemo(() => {
    return accounts.filter((a) => {
      if (platform !== 'all' && a.platform !== platform) return false;
      if (status !== 'all' && a.status !== status) return false;
      return true;
    });
  }, [accounts, platform, status]);

  const visibleIds = useMemo(() => new Set(filtered.map((a) => a.id)), [filtered]);
  // Drop selections that the current filter hid, so the bulk bar never
  // counts rows the operator cannot see.
  const effective = useMemo(
    () => [...selected].filter((id) => visibleIds.has(id)),
    [selected, visibleIds],
  );
  const allSelected = effective.length > 0 && effective.length === filtered.length;

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

  // Submit a 2FA / checkpoint code to a parked login. The browser prompt is the
  // MVP input affordance; a dedicated dialog can replace it without changing
  // the contract.
  const submitInput = async (id: string) => {
    const value = window.prompt('Enter the verification code shown in the live view:');
    if (!value) return;
    await api.submitAccountInput(id, value);
    refresh();
  };

  // The primary row action depends on where the account is in its login life:
  // an account that needs the operator (challenge in progress, or a live
  // session they want to watch) opens the worker's live browser; an already
  // authenticated account opens its public profile instead — the two things an
  // operator actually wants one click away from this list.
  const needsLiveView = (a: Account): boolean =>
    a.authStatus === 'AUTHENTICATING' || a.authStatus === 'NEEDS_INPUT';

  const openAccount = (a: Account) => {
    // During a login the only useful destination is the worker's screen —
    // watching or driving the challenge. Once authenticated the account's
    // public profile is the destination.
    if (needsLiveView(a)) {
      const url = novncFor(a);
      if (url) setLive({ name: a.username, url });
      return;
    }
    if (a.authStatus === 'AUTHENTICATED') {
      window.open(profileUrlFor(a), '_blank', 'noopener,noreferrer');
    }
  };

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

      <ImportAccountsDialog
        open={importOpen}
        onOpenChange={setImportOpen}
        onImported={() => void refresh()}
      />

      <Card>
        <CardHeader>
          <CardTitle>Fleet accounts</CardTitle>
          <CardDescription>
            {loading ? 'Loading…' : `${filtered.length} of ${accounts.length} shown`}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <section className="flex flex-wrap items-center gap-2">
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
          </section>

          {effective.length > 0 ? (
            <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border/60 bg-secondary/40 p-3">
              <span className="text-sm font-medium">{effective.length} selected</span>
              <Button
                variant="outline"
                size="sm"
                disabled={busy === '__bulk__' || !canAct}
                onClick={() => void onBulk((id) => setStatus(id, 'PAUSED'))}
              >
                <Pause className="size-3.5" />
                Pause
              </Button>
              <Button
                variant="outline"
                size="sm"
                disabled={busy === '__bulk__' || !canAct}
                onClick={() => void onBulk((id) => setStatus(id, 'ACTIVE'))}
              >
                <Play className="size-3.5" />
                Resume
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="text-destructive"
                disabled={busy === '__bulk__' || !canAct}
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
                <TableRow>
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
                    <TableCell colSpan={7} className="py-10 text-center text-muted-foreground">
                      No accounts yet. Add one to provision a container.
                    </TableCell>
                  </TableRow>
                ) : (
                  filtered.map((a) => (
                    <TableRow
                      key={a.id}
                      // The row is the primary action: one click opens the live
                      // view during a login, or the profile once authenticated.
                      // Controls inside stopPropagation so they keep working.
                      // (role/tabIndex stay off a <tr> — ARIA forbids it — and
                      // the icon buttons in the last cell are real buttons, so
                      // keyboard users have the same actions one Tab away.)
                      className="cursor-pointer transition-colors hover:bg-accent/60"
                      onClick={() => openAccount(a)}
                    >
                      <TableCell>
                        <input
                          type="checkbox"
                          className="size-4 rounded border-input"
                          aria-label={`Select @${a.username}`}
                          checked={selected.has(a.id)}
                          onChange={(e) => {
                            e.stopPropagation();
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
                        <div className="font-medium">@{a.username}</div>
                        <div className="text-xs text-muted-foreground">
                          {a.handle && a.handle !== a.username ? a.handle : a.workerId ? a.workerId.slice(0, 12) : 'unassigned'}
                        </div>
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline">
                          {PLATFORM_LABEL[a.platform as Platform] ?? a.platform}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {a.workerId ? a.workerId.slice(0, 12) : '—'}
                      </TableCell>
                      <TableCell>
                        <Badge variant={authTone(a.authStatus)}>{a.authStatus}</Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusTone(a.status)}>{a.status}</Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center justify-end gap-1">
                          {/*
                            The row click is the primary action now; these are
                            affordances, not the only path. An icon button is
                            shown only when there is something to open — a
                            login with a published noVNC URL, or an authenticated
                            account.
                          */}
                          {needsLiveView(a) && novncFor(a) ? (
                            <Button
                              variant="outline"
                              size="icon"
                              className="h-8 w-8"
                              aria-label="Live view"
                              onClick={(e) => {
                                e.stopPropagation();
                                const url = novncFor(a);
                                if (url) setLive({ name: a.username, url });
                              }}
                            >
                              <MonitorPlay className="size-3.5" />
                            </Button>
                          ) : null}
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
                            <DropdownMenuContent align="end">
                              <DropdownMenuLabel>Account ops</DropdownMenuLabel>
                              <DropdownMenuSeparator />
                              {a.status === 'PAUSED' ? (
                                <DropdownMenuItem
                                  onClick={() => void onAct(a.id, (id) => setStatus(id, 'ACTIVE'))}
                                >
                                  <Play />
                                  Resume
                                </DropdownMenuItem>
                              ) : (
                                <DropdownMenuItem
                                  onClick={() => void onAct(a.id, (id) => setStatus(id, 'PAUSED'))}
                                >
                                  <Pause />
                                  Pause
                                </DropdownMenuItem>
                            )}
                            <DropdownMenuSeparator />
                            <DropdownMenuItem
                              onClick={() => void onAct(a.id, startLogin)}
                            >
                              <KeyRound />
                              Log in
                            </DropdownMenuItem>
                            {a.authStatus === 'NEEDS_INPUT' ? (
                              <DropdownMenuItem
                                onClick={() => void onAct(a.id, submitInput)}
                              >
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
                              <DropdownMenuItem
                                onClick={() =>
                                  setLive({ name: a.username, url: novncFor(a) as string })
                                }
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
        </CardContent>
      </Card>

      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} />

      <LiveBrowserModal
        open={live !== null}
        onOpenChange={(open) => {
          if (!open) setLive(null);
        }}
        url={live?.url ?? ''}
        workerName={live?.name ?? ''}
      />
    </div>
  );
}

/**
 * The public profile URL for a worker account. Worker accounts carry no
 * profileUrl in the contract (that field is official-account analytics only),
 * so the URL is derived from platform + handle/username — the same link the
 * worker itself would open.
 */
function profileUrlFor(a: Account): string {
  // Prefer the handle (canonical, may differ from the login username); fall
  // back to it when the platform never reported one.
  const handle = a.handle || a.username;
  switch (a.platform) {
    case 'instagram':
      return `https://www.instagram.com/${handle}/`;
    case 'threads':
      return `https://www.threads.net/@${handle}`;
    case 'tiktok':
      return `https://www.tiktok.com/@${handle}`;
    case 'linkedin':
      return `https://www.linkedin.com/in/${handle}`;
    case 'x':
      return `https://x.com/${handle}`;
    case 'youtube':
      return `https://www.youtube.com/@${handle}`;
    case 'facebook':
      return `https://www.facebook.com/${handle}`;
    default:
      return `https://${handle}`;
  }
}
