'use client';

import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@smm/ui';
import {
  AlertCircle,
  Download,
  Loader2,
  MoreVertical,
  MonitorPlay,
  Pause,
  Play,
  Plus,
  Server,
  Trash2,
} from 'lucide-react';
import { useMemo, useState } from 'react';

import { useAccounts } from '@/lib/hooks/use-accounts';
import { useContainers } from '@/lib/hooks/use-containers';
import { useLiveTicker } from '@/lib/hooks/use-live-ticker';
import { LiveBrowserModal } from '@/components/live-browser-modal';
import { PLATFORM_LABEL } from '@/lib/platforms';
import { api } from '@/lib/api';
import type { Container, LogEntry } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

function containerTone(
  status: Container['status'],
): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'READY':
    case 'IDLE':
      return 'success';
    case 'PENDING':
      return 'secondary';
    case 'BUSY':
      return 'secondary';
    case 'ERROR':
    case 'DEAD':
    case 'QUARANTINED':
      return 'destructive';
    default:
      return 'outline';
  }
}

// A freshly created worker sits at PENDING until its container heartbeats.
// The pulse marks it as in-flight, not stuck, and clears the moment the
// worker-health frame flips it to READY.
function isStarting(status: Container['status']): boolean {
  return status === 'PENDING';
}

export default function WorkersPage() {
  const { containers, locations, loading, error, create, remove } = useContainers();
  const { setStatus, remove: removeAccount } = useAccounts();
  const ticker = useLiveTicker();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');
  // Exporting a session dumps cookies (credentials); the server gates it to
  // owner/admin, so the button is only offered to that role.
  const canExport = can(role, 'admin');

  const [live, setLive] = useState<{ name: string; url: string } | null>(null);
  // Delete-with-accounts confirm (P-C): a container still holding packed
  // accounts can lose their sessions on delete, so the operator is offered a
  // session export before deleting anyway.
  const [deleteTarget, setDeleteTarget] = useState<Container | null>(null);
  const [sessionBusy, setSessionBusy] = useState(false);
  const [sessionError, setSessionError] = useState<string | null>(null);

  const [name, setName] = useState('');
  const [location, setLocation] = useState('');
  // P4-08: the operator may pin the live-view host port; empty lets the API
  // allocate one. The hint mirrors the server's NOVNC_PORT_MIN..MAX range.
  const [novncPort, setNovncPort] = useState('');
  // P6-05: group the fleet by city. "all" keeps every container in one grid.
  const [city, setCity] = useState('all');
  const [busy, setBusy] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  // F-07: a card whose provisioning looks stuck can be expanded to show the
  // provisioner's own audit trail, fetched on demand rather than per render.
  const [logFor, setLogFor] = useState<string | null>(null);
  const [logs, setLogs] = useState<LogEntry[] | null>(null);

  const cities = useMemo(() => {
    type CityAnchor = { name: string; latitude: number; longitude: number; radiusKm: number };
    const seen = new Map<string, CityAnchor>();
    for (const c of containers) {
      if (c.location && !seen.has(c.location)) {
        const anchor = locations.find((l) => l.name === c.location);
        if (anchor) seen.set(c.location, anchor);
        else
          seen.set(c.location, {
            name: c.location,
            latitude: c.latitude ?? 0,
            longitude: c.longitude ?? 0,
            radiusKm: 0,
          });
      }
    }
    return [...seen.values()];
  }, [containers, locations]);

  const visible = useMemo(
    () => (city === 'all' ? containers : containers.filter((c) => c.location === city)),
    [containers, city],
  );

  // P6-05: containers grouped under their city anchor so an operator can
  // read the fleet geographically. "all" collapses to the flat grid.
  const groups = useMemo(() => {
    if (city !== 'all') return [{ city: null, items: visible }];
    const byCity = new Map<string, typeof visible>();
    for (const c of visible) {
      const key = c.location || 'Unlocated';
      const bucket = byCity.get(key);
      if (bucket) bucket.push(c);
      else byCity.set(key, [c]);
    }
    return [...byCity.entries()].map(([name, items]) => ({ city: name, items }));
  }, [visible, city]);

  const onCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    if (!name.trim()) {
      setFormError('A container needs a name.');
      return;
    }
    if (!location) {
      setFormError('Pick the city the worker operates from.');
      return;
    }
    setBusy('__create__');
    try {
      // Region is constant for the MVP (Indonesia only); the city is what
      // varies and what the GPS spoof anchors to.
      const port = novncPort.trim() ? Number.parseInt(novncPort.trim(), 10) : undefined;
      if (novncPort.trim() && !Number.isFinite(port)) {
        setFormError('noVNC port must be a number, or empty to let the API pick one.');
        return;
      }
      await create(name.trim(), 'ID', location, port);
      setName('');
      setLocation('');
      setNovncPort('');
    } catch (err) {
      // A dropped connection surfaces from fetch as a TypeError 'Failed to
      // fetch'; that is the symptom the retry layer is built for, so name it
      // instead of echoing the browser's generic string.
      const isNetwork = err instanceof TypeError;
      setFormError(
        isNetwork
          ? 'Create failed: the network dropped the request. Check the connection and try again.'
          : err instanceof Error
            ? err.message
            : 'Create failed',
      );
    } finally {
      setBusy(null);
    }
  };

  const onAct = async (id: string, fn: (id: string) => Promise<void>) => {
    setBusy(id);
    try {
      await fn(id);
    } catch (err) {
      console.error('container op failed', err);
    } finally {
      setBusy(null);
    }
  };

  // F-07: expand a card to read the provisioner's audit trail. Fetched on
  // demand so a fleet listing never pays for N log reads, and closed again on
  // the same click.
  const toggleLogs = async (id: string) => {
    if (logFor === id) {
      setLogFor(null);
      setLogs(null);
      return;
    }
    setLogFor(id);
    setLogs(null);
    try {
      const res = await api.containerLogs(id);
      setLogs(res.logs ?? []);
    } catch (err) {
      console.error('container logs failed', err);
      setLogs([]);
    }
  };

  // Clicking Delete on a container with packed accounts opens the confirm
  // dialog; an empty container is deleted straight away.
  const onDeleteContainer = (c: Container) => {
    if ((c.accounts?.length ?? 0) > 0) {
      setSessionError(null);
      setDeleteTarget(c);
    } else {
      void onAct(c.id, remove);
    }
  };

  // Export every packed account's session (cookies) and hand the operator a
  // single JSON file to re-import into a fresh container. The value is never
  // logged; it lives only in the downloaded file.
  const onExportSessions = async (c: Container) => {
    setSessionBusy(true);
    setSessionError(null);
    try {
      const accounts = c.accounts ?? [];
      const sessions: Array<Record<string, unknown>> = [];
      const failures: string[] = [];
      for (const a of accounts) {
        try {
          const sessionData = await api.exportAccountSession(a.id);
          sessions.push({
            accountId: a.id,
            username: a.username,
            platform: a.platform,
            session: sessionData,
          });
        } catch (err) {
          failures.push(`@${a.username}: ${err instanceof Error ? err.message : 'export failed'}`);
        }
      }
      if (sessions.length > 0) {
        const payload = {
          container: { id: c.id, name: c.name },
          exportedAt: new Date().toISOString(),
          sessions,
        };
        const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = `sessions-${c.name || c.id}.json`;
        document.body.appendChild(anchor);
        anchor.click();
        anchor.remove();
        URL.revokeObjectURL(url);
      }
      if (failures.length > 0) {
        setSessionError(`Some sessions could not be exported — ${failures.join('; ')}`);
      } else if (sessions.length === 0) {
        setSessionError('No sessions were available to export from this container.');
      }
    } finally {
      setSessionBusy(false);
    }
  };

  // Delete anyway: release the packed accounts (which removes their rows) and
  // then delete the container. The sessions are gone once this completes, which
  // is exactly why the export above is offered first.
  const onDeleteAnyway = async (c: Container) => {
    setSessionBusy(true);
    setSessionError(null);
    try {
      for (const a of c.accounts ?? []) {
        await removeAccount(a.id);
      }
      await remove(c.id);
      setDeleteTarget(null);
    } catch (err) {
      setSessionError(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setSessionBusy(false);
    }
  };

  return (
    <div className="mx-auto max-w-6xl space-y-4">
      {error ? (
        <div className="flex items-center gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertCircle className="size-4" />
          {error}
        </div>
      ) : null}

      <LiveTicker ticker={ticker} />

      <section className="grid grid-cols-2 gap-4 lg:grid-cols-5">
        {(
          [
            ['Containers', containers.length],
            ['Ready', containers.filter((c) => c.status === 'READY' || c.status === 'IDLE').length],
            ['Busy', containers.filter((c) => c.status === 'BUSY').length],
            [
              'Error',
              containers.filter((c) => ['ERROR', 'DEAD', 'QUARANTINED'].includes(c.status)).length,
            ],
            ['Accounts enrolled', containers.reduce((n, c) => n + (c.accounts?.length ?? 0), 0)],
          ] as [string, number][]
        ).map(([label, value]) => (
          <Card key={label}>
            <CardContent className="space-y-2 p-5">
              <div className="text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
              <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
            </CardContent>
          </Card>
        ))}
      </section>

      <section className="flex flex-wrap items-center gap-2">
        <Select value={city} onValueChange={setCity}>
          <SelectTrigger className="w-48" aria-label="All cities">
            <SelectValue placeholder="All cities" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All cities</SelectItem>
            {cities.map((c) => (
              <SelectItem key={c.name} value={c.name}>
                {c.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {city !== 'all'
          ? (() => {
              const anchor = cities.find((c) => c.name === city);
              return anchor ? (
                <span className="font-mono text-xs text-muted-foreground">
                  anchor {anchor.latitude.toFixed(4)}, {anchor.longitude.toFixed(4)} · ±
                  {anchor.radiusKm}km
                </span>
              ) : null;
            })()
          : null}
      </section>

      {containers.length === 0 && !loading ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-3 py-12 text-center">
            <Server className="size-8 text-muted-foreground" />
            <div className="space-y-1">
              <p className="font-medium">No containers</p>
              <p className="text-sm text-muted-foreground">
                Containers are created manually; accounts pack into the first one with a free
                platform slot.
              </p>
            </div>
          </CardContent>
        </Card>
      ) : (
        groups.map((g) => (
          <section key={g.city ?? 'unlocated'} className="space-y-3">
            {g.city ? (
              <div className="flex items-baseline gap-2">
                <h2 className="text-sm font-semibold">{g.city}</h2>
                {(() => {
                  const anchor = cities.find((c) => c.name === g.city);
                  return anchor ? (
                    <span className="font-mono text-xs text-muted-foreground">
                      {anchor.latitude.toFixed(4)}, {anchor.longitude.toFixed(4)} · ±
                      {anchor.radiusKm}km
                    </span>
                  ) : null;
                })()}
                <span className="ml-auto text-xs text-muted-foreground">
                  {g.items.length} container{g.items.length === 1 ? '' : 's'}
                </span>
              </div>
            ) : null}

            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {g.items.map((c) => (
                <Card key={c.id}>
                  <CardHeader>
                    <div className="flex items-start justify-between">
                      <div className="space-y-1">
                        <CardTitle className="text-base">{c.name}</CardTitle>
                        <CardDescription className="font-mono text-xs">
                          {c.id.slice(0, 8)} · {c.region}
                          {c.location ? ` · ${c.location}` : null}
                          {c.latitude != null && c.longitude != null
                            ? ` · ${c.latitude.toFixed(4)}, ${c.longitude.toFixed(4)}`
                            : null}
                          {c.novncUrl ? ` · noVNC ${novncPortOf(c.novncUrl)}` : null}
                        </CardDescription>
                      </div>
                      <div className="flex items-center gap-1">
                        {/*
                          The button is always rendered (REMEDIATION_PLAN R-04): a
                          missing control reads as "this build has no live view",
                          while a disabled one names the real reason — the worker
                          has not published its URL yet — so OTP/2FA work is not
                          blocked on a support round-trip.
                        */}
                        {c.novncUrl ? (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-8"
                            onClick={() => setLive({ name: c.name, url: c.novncUrl as string })}
                          >
                            <MonitorPlay className="size-3.5" />
                            Live view
                          </Button>
                        ) : (
                          <Button
                            variant="outline"
                            size="sm"
                            className="h-8"
                            disabled
                            title="The live view is published when the worker reports its noVNC URL. This card flips to a working button on the first heartbeat."
                          >
                            <MonitorPlay className="size-3.5" />
                            Live view
                          </Button>
                        )}
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon"
                              className="size-8"
                              disabled={!canAct}
                            >
                              <MoreVertical className="size-4" />
                              <span className="sr-only">Open container menu</span>
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              className="text-destructive"
                              disabled={busy === c.id}
                              onClick={() => onDeleteContainer(c)}
                            >
                              <Trash2 />
                              Delete
                              {(c.accounts?.length ?? 0) > 0 ? '…' : ''}
                            </DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </div>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 pt-1">
                      <Badge
                        variant={containerTone(c.status)}
                        className={isStarting(c.status) ? 'animate-pulse' : undefined}
                      >
                        {isStarting(c.status) ? `${c.status} · starting…` : c.status}
                      </Badge>
                      <Badge variant="outline">{c.source}</Badge>
                      <Badge variant="outline">{c.desiredState}</Badge>
                      {c.observedGeneration !== c.generation ? (
                        <Badge variant="outline" className="text-muted-foreground">
                          reconciling
                        </Badge>
                      ) : null}
                      <button
                        type="button"
                        onClick={() => toggleLogs(c.id)}
                        className="ml-auto text-xs text-muted-foreground underline-offset-2 hover:underline"
                      >
                        {logFor === c.id ? 'hide provisioning log' : 'provisioning log'}
                      </button>
                    </div>
                    {logFor === c.id ? (
                      <div className="mt-2 rounded-lg border border-border/60 bg-secondary/30 p-2 text-xs">
                        {logs === null ? (
                          <p className="text-muted-foreground">loading…</p>
                        ) : logs.length === 0 ? (
                          <p className="text-muted-foreground">No provisioning ops recorded yet.</p>
                        ) : (
                          <ul className="space-y-1 font-mono">
                            {logs.map((l) => (
                              <li key={l.id} className="flex gap-2">
                                <span className="text-muted-foreground">
                                  {new Date(l.ts).toLocaleTimeString()}
                                </span>
                                <span>{l.op}</span>
                                <span>gen {l.generation}</span>
                                <span className={l.status === 'FAILED' ? 'text-destructive' : 'text-success'}>
                                  {l.status}
                                </span>
                                {l.error ? <span className="truncate text-destructive">{l.error}</span> : null}
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    ) : null}
                  </CardHeader>
                  <CardContent>
                    {(c.accounts?.length ?? 0) === 0 ? (
                      <p className="py-4 text-center text-sm text-muted-foreground">
                        No accounts packed.
                      </p>
                    ) : (
                      <div className="divide-y">
                        {c.accounts?.map((a) => (
                          <div
                            key={a.id}
                            className="flex items-center justify-between gap-2 py-2 first:pt-0 last:pb-0"
                          >
                            <div className="min-w-0">
                              <div className="truncate text-sm font-medium">@{a.username}</div>
                              <div className="text-xs text-muted-foreground">
                                {PLATFORM_LABEL[a.platform] ?? a.platform} · {a.authStatus}
                              </div>
                            </div>
                            <div className="flex items-center gap-1">
                              <Badge
                                variant={a.status === 'ACTIVE' ? 'success' : 'secondary'}
                                className="mr-1"
                              >
                                {a.status}
                              </Badge>
                              {a.status === 'ACTIVE' ? (
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="size-7"
                                  disabled={busy === a.id || !canAct}
                                  onClick={() => onAct(a.id, (id) => setStatus(id, 'PAUSED'))}
                                >
                                  <Pause className="size-3.5" />
                                  <span className="sr-only">Pause @{a.username}</span>
                                </Button>
                              ) : (
                                <Button
                                  variant="ghost"
                                  size="icon"
                                  className="size-7"
                                  disabled={busy === a.id || !canAct}
                                  onClick={() => onAct(a.id, (id) => setStatus(id, 'ACTIVE'))}
                                >
                                  <Play className="size-3.5" />
                                  <span className="sr-only">Resume @{a.username}</span>
                                </Button>
                              )}
                              <Button
                                variant="ghost"
                                size="icon"
                                className="size-7 text-destructive"
                                disabled={busy === a.id || !canAct}
                                onClick={() => onAct(a.id, removeAccount)}
                              >
                                <Trash2 className="size-3.5" />
                                <span className="sr-only">Remove @{a.username}</span>
                              </Button>
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))}
            </div>
          </section>
        ))
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Add container</CardTitle>
          <CardDescription>
            Manual containers are the only way the fleet grows (empty-by-default design).
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3">
            <div className="grid w-full max-w-xs gap-2">
              <Label htmlFor="c-name">Name</Label>
              <Input
                id="c-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="worker-us-01"
                disabled={busy === '__create__' || !canAct}
              />
            </div>
            <div className="grid w-full max-w-xs gap-2">
              <Label htmlFor="c-location">Location</Label>
              <select
                id="c-location"
                className="h-9 rounded-lg border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                value={location}
                onChange={(e) => setLocation(e.target.value)}
                disabled={busy === '__create__' || !canAct}
              >
                <option value="">Select a city…</option>
                {locations.map((c) => (
                  <option key={c.name} value={c.name}>
                    {c.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="grid w-full max-w-[8rem] gap-2">
              <Label htmlFor="c-novnc">noVNC port</Label>
              <Input
                id="c-novnc"
                inputMode="numeric"
                value={novncPort}
                onChange={(e) => setNovncPort(e.target.value)}
                placeholder="24100–24299"
                disabled={busy === '__create__' || !canAct}
              />
            </div>
            {formError ? (
              <div className="flex items-center gap-2 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {formError}
              </div>
            ) : null}
            <Button type="submit" disabled={busy === '__create__' || !canAct}>
              {busy === '__create__' ? <Loader2 className="animate-spin" /> : <Plus />}
              {busy === '__create__' ? 'Creating…' : 'Create'}
            </Button>
          </form>
        </CardContent>
      </Card>

      <LiveBrowserModal
        open={live !== null}
        onOpenChange={(open) => {
          if (!open) setLive(null);
        }}
        url={live?.url ?? ''}
        workerName={live?.name ?? ''}
      />

      <Dialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open && !sessionBusy) {
            setDeleteTarget(null);
            setSessionError(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>Delete {deleteTarget?.name}?</DialogTitle>
            <DialogDescription>
              This container still has {deleteTarget?.accounts?.length ?? 0} packed account
              {(deleteTarget?.accounts?.length ?? 0) === 1 ? '' : 's'}. Deleting it discards their
              saved sessions (cookies). Export the sessions first if you want to re-import them into
              a new container.
            </DialogDescription>
          </DialogHeader>

          {deleteTarget?.accounts && deleteTarget.accounts.length > 0 ? (
            <ul className="max-h-40 space-y-1 overflow-auto rounded-lg border border-border/60 bg-secondary/30 p-2 text-sm">
              {deleteTarget.accounts.map((a) => (
                <li key={a.id} className="flex items-center justify-between gap-2">
                  <span className="truncate">@{a.username}</span>
                  <span className="text-xs text-muted-foreground">
                    {PLATFORM_LABEL[a.platform] ?? a.platform}
                  </span>
                </li>
              ))}
            </ul>
          ) : null}

          {!canExport ? (
            <p className="text-xs text-muted-foreground">
              Exporting sessions is owner/admin only. Ask an owner to export before deleting if
              these sessions are worth keeping.
            </p>
          ) : null}

          {sessionError ? (
            <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span>{sessionError}</span>
            </div>
          ) : null}

          <DialogFooter className="gap-2 sm:justify-between">
            <Button
              variant="outline"
              disabled={sessionBusy || !canExport}
              onClick={() => deleteTarget && void onExportSessions(deleteTarget)}
            >
              {sessionBusy ? <Loader2 className="animate-spin" /> : <Download className="size-4" />}
              Export sessions
            </Button>
            <Button
              variant="destructive"
              disabled={sessionBusy || !canAct}
              onClick={() => deleteTarget && void onDeleteAnyway(deleteTarget)}
            >
              {sessionBusy ? <Loader2 className="animate-spin" /> : <Trash2 className="size-4" />}
              Delete anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** The host port a worker's live view is bound on, from its stored URL. */
function novncPortOf(url: string): string {
  const i = url.lastIndexOf(':');
  return i < 0 ? '—' : url.slice(i + 1);
}

/**
 * Live ticker (P4-08). A compact, auto-scrolling tail of the SSE stream. It
 * doubles as a connection health indicator: an empty list with a green dot
 * means connected-but-quiet, a grey dot means the stream is down.
 */
function LiveTicker({ ticker }: { ticker: ReturnType<typeof useLiveTicker> }) {
  const { items, connected } = ticker;
  return (
    <div className="rounded-lg border border-border/60 bg-card/60 p-3">
      <div className="mb-2 flex items-center gap-2">
        <span
          className={`inline-block size-2 rounded-full ${connected ? 'bg-success' : 'bg-muted-foreground/40'}`}
          aria-hidden
        />
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Live activity
        </span>
        <span className="ml-auto text-xs text-muted-foreground">
          {connected ? 'streaming' : 'reconnecting…'}
        </span>
      </div>
      {items.length === 0 ? (
        <p className="py-2 text-center text-xs text-muted-foreground">
          No events yet. Worker heartbeats, account updates and action results appear here.
        </p>
      ) : (
        <ol className="space-y-1">
          {items.slice(0, 8).map((item) => (
            <li
              key={item.id}
              className="flex items-center gap-2 font-mono text-xs text-muted-foreground"
            >
              <span className="tabular-nums text-muted-foreground/60">
                {new Date(item.at).toLocaleTimeString()}
              </span>
              <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] uppercase">
                {item.kind.replace('-updated', '').replace('worker-', '')}
              </span>
              <span className="truncate">{item.label}</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
