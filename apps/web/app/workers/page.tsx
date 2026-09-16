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
  DropdownMenuTrigger,
  Input,
  Label,
} from '@smm/ui';
import {
  AlertCircle,
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
import type { Container } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

function containerTone(
  status: Container['status'],
): 'success' | 'outline' | 'secondary' | 'destructive' {
  switch (status) {
    case 'READY':
    case 'IDLE':
      return 'success';
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

export default function WorkersPage() {
  const { containers, locations, loading, error, create, remove } = useContainers();
  const { setStatus, remove: removeAccount } = useAccounts();
  const ticker = useLiveTicker();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');

  const [live, setLive] = useState<{ name: string; url: string } | null>(null);

  const [name, setName] = useState('');
  const [location, setLocation] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);

  const totalAccounts = useMemo(
    () => containers.reduce((n, c) => n + (c.accounts?.length ?? 0), 0),
    [containers],
  );

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
      await create(name.trim(), 'ID', location);
      setName('');
      setLocation('');
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Create failed');
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

  return (
    <div className="mx-auto max-w-6xl space-y-6">
      <header className="space-y-1">
        <h1 className="text-3xl font-semibold tracking-tight">Workers</h1>
        <p className="text-sm text-muted-foreground">
          {loading
            ? 'Loading…'
            : `${containers.length} container · ${totalAccounts} account slot${totalAccounts === 1 ? '' : 's'}`}
        </p>
      </header>

      {error ? (
        <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertCircle className="size-4" />
          {error}
        </div>
      ) : null}

      <LiveTicker ticker={ticker} />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {containers.length === 0 && !loading ? (
          <Card className="col-span-full">
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
          containers.map((c) => (
            <Card key={c.id}>
              <CardHeader>
                <div className="flex items-start justify-between">
                  <div className="space-y-1">
                    <CardTitle className="text-base">{c.name}</CardTitle>
                    <CardDescription className="font-mono text-xs">
                      {c.id.slice(0, 8)} · {c.region}
                      {c.location ? ` · ${c.location}` : null}
                    </CardDescription>
                  </div>
                  <div className="flex items-center gap-1">
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
                    ) : null}
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="size-8" disabled={!canAct}>
                          <MoreVertical className="size-4" />
                          <span className="sr-only">Open container menu</span>
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem
                          className="text-destructive"
                          disabled={busy === c.id || (c.accounts?.length ?? 0) > 0}
                          onClick={() => onAct(c.id, remove)}
                        >
                          <Trash2 />
                          Delete
                          {(c.accounts?.length ?? 0) > 0 ? ' (remove accounts first)' : ''}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </div>
                </div>
                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Badge variant={containerTone(c.status)}>{c.status}</Badge>
                  <Badge variant="outline">{c.source}</Badge>
                  <Badge variant="outline">{c.desiredState}</Badge>
                  {c.observedGeneration !== c.generation ? (
                    <Badge variant="outline" className="text-muted-foreground">
                      reconciling
                    </Badge>
                  ) : null}
                </div>
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
          ))
        )}
      </div>

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
                className="h-9 rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
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
            {formError ? (
              <div className="flex items-center gap-2 text-sm text-destructive">
                <AlertCircle className="size-4" />
                {formError}
              </div>
            ) : null}
            <Button type="submit" disabled={busy === '__create__' || !canAct}>
              <Plus />
              Create
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
    </div>
  );
}

/**
 * Live ticker (P4-08). A compact, auto-scrolling tail of the SSE stream. It
 * doubles as a connection health indicator: an empty list with a green dot
 * means connected-but-quiet, a grey dot means the stream is down.
 */
function LiveTicker({ ticker }: { ticker: ReturnType<typeof useLiveTicker> }) {
  const { items, connected } = ticker;
  return (
    <div className="rounded-md border bg-card/60 p-3">
      <div className="mb-2 flex items-center gap-2">
        <span
          className={`inline-block size-2 rounded-full ${connected ? 'bg-emerald-500' : 'bg-muted-foreground/40'}`}
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
