'use client';

/**
 * Add container. The fleet grows only by manual creation (empty-by-default
 * design), so this form sits above the list: it is the page's primary action,
 * not a footnote under it.
 *
 * Region is constant for the MVP (Indonesia only); the city is what varies and
 * what the GPS spoof anchors to.
 */

import { Button, Card, CardContent, CardDescription, CardHeader, CardTitle, Input, Label } from '@smm/ui';
import { AlertCircle, Loader2, Plus } from 'lucide-react';
import { useState } from 'react';

import { useContainers } from '@/lib/hooks/use-containers';
import { useSession } from '@/lib/auth/session-context';
import { can } from '@/lib/auth/permissions';

export function AddContainerForm() {
  const { locations, create } = useContainers();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;
  const canAct = can(role, 'act');

  const [name, setName] = useState('');
  const [location, setLocation] = useState('');
  // The operator may pin the live-view host port; empty lets the API allocate
  // one. The hint mirrors the server's NOVNC_PORT_MIN..MAX range.
  const [novncPort, setNovncPort] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);

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
    const port = novncPort.trim() ? Number.parseInt(novncPort.trim(), 10) : undefined;
    if (novncPort.trim() && !Number.isFinite(port)) {
      setFormError('noVNC port must be a number, or empty to let the API pick one.');
      return;
    }
    setBusy(true);
    try {
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
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Add container</CardTitle>
        <CardDescription>Pick a city; the container is anchored to a GPS point inside it.</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3">
          <div className="grid w-full max-w-xs gap-2">
            <Label htmlFor="c-name">Name</Label>
            <Input
              id="c-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="worker-jkt-01"
              disabled={busy || !canAct}
            />
          </div>
          <div className="grid w-full max-w-xs gap-2">
            <Label htmlFor="c-location">Location</Label>
            <select
              id="c-location"
              className="h-9 rounded-lg border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={location}
              onChange={(e) => setLocation(e.target.value)}
              disabled={busy || !canAct}
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
              disabled={busy || !canAct}
            />
          </div>
          {formError ? (
            <div className="flex items-center gap-2 text-sm text-destructive">
              <AlertCircle className="size-4" />
              {formError}
            </div>
          ) : null}
          <Button type="submit" disabled={busy || !canAct}>
            {busy ? <Loader2 className="animate-spin" /> : <Plus />}
            {busy ? 'Creating…' : 'Create'}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
