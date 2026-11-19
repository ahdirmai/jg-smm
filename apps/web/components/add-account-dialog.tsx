'use client';

import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@smm/ui';
import { Check, Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, type Account } from '@/lib/api';
import { PLATFORM_LABEL } from '@/lib/platforms';

type Step = 'credentials' | 'proxy' | 'provisioning' | 'done';

// The API speaks lowercase: domain/platform.go declares
// `PlatformInstagram Platform = "instagram"`, and account.Validate() rejects
// anything else with "unknown platform". This list used to send UPPERCASE
// values, which made every quick-add submit a 400 — the `as Account['platform']`
// cast at the call site is what hid it from the compiler. Typing the state as
// Account['platform'] removes both the cast and the ability to reintroduce it.
//
// Only the two platforms with a real login flow are offered. An account exists
// to drive a Playwright container, and actorForPlatform (cmd/server/main.go)
// returns a post actor for Instagram and Threads only — offering the other five
// would create accounts that can never be scraped. The API would accept them;
// that is a product gate, not a validation one.
const ACCOUNT_PLATFORMS: readonly Account['platform'][] = ['instagram', 'threads'];

// Radix's Select reports a plain string. Narrow it before it reaches state
// rather than casting at the call site, so the state type stays the API's own
// union and an unexpected value is dropped instead of forwarded.
function isAccountPlatform(v: string): v is Account['platform'] {
  return (ACCOUNT_PLATFORMS as readonly string[]).includes(v);
}

/**
 * Add Account is a short funnel (P1-15): pick platform + credentials, optional
 * proxy, then submit. Provisioning status is driven by the SSE stream — the
 * account row lands and the dashboard picks the frame up.
 */
export function AddAccountDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const [step, setStep] = useState<Step>('credentials');
  const [platform, setPlatform] = useState<Account['platform']>('instagram');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [proxyGroup, setProxyGroup] = useState<string>('none');
  // Where the account says it is. Empty means "let the API distribute", which
  // spreads an import across cities instead of pinning everyone to one.
  const [location, setLocation] = useState<string>('auto');
  const [cities, setCities] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<Account | null>(null);

  const reset = () => {
    setStep('credentials');
    setPlatform('instagram');
    setUsername('');
    setPassword('');
    setProxyGroup('none');
    setLocation('auto');
    setError(null);
    setCreated(null);
  };

  const canContinue = platform && username.trim() !== '' && password !== '';

  // The city list is the API's own reference list, so the dropdown can never
  // offer a value the create endpoint rejects.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .listLocations()
      .then((rows) => {
        if (!cancelled) setCities(rows.map((c) => c.name));
      })
      .catch(() => {
        // Best effort: without the list the field stays on "auto", which the
        // API resolves itself.
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  const submit = async () => {
    setStep('provisioning');
    setError(null);
    try {
      const account = await api.createAccount({
        platform,
        username: username.trim(),
        password,
        tags: [],
        ...(location === 'auto' ? {} : { location }),
      });
      setCreated(account);
      setStep('done');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add account');
      setStep('credentials');
    }
  };

  const close = () => {
    onOpenChange(false);
    // Reset after the dialog animates out so reopening starts clean.
    setTimeout(reset, 200);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Add account</DialogTitle>
          <DialogDescription>
            {step === 'done'
              ? 'Account added. It joins the worker pool and is scheduled on the next free machine.'
              : 'The credential is encrypted before it is stored; it is never returned.'}
          </DialogDescription>
        </DialogHeader>

        {step === 'credentials' ? (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="aa-platform">Platform</Label>
              <Select
                value={platform}
                onValueChange={(v) => {
                  if (isAccountPlatform(v)) setPlatform(v);
                }}
              >
                <SelectTrigger id="aa-platform">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ACCOUNT_PLATFORMS.map((p) => (
                    <SelectItem key={p} value={p}>
                      {PLATFORM_LABEL[p]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="aa-username">Username</Label>
              <Input
                id="aa-username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="growth.hacks.id"
                autoComplete="off"
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="aa-password">Password</Label>
              <Input
                id="aa-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
                autoComplete="new-password"
              />
            </div>

            {error ? <p className="text-sm text-destructive">{error}</p> : null}
          </div>
        ) : null}

        {step === 'proxy' ? (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="aa-proxy">Proxy group</Label>
              <Select value={proxyGroup} onValueChange={setProxyGroup}>
                <SelectTrigger id="aa-proxy">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">None (use worker egress)</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Region-matched proxy binding lands with P1-14; none is correct for the MVP.
              </p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="aa-location">Location</Label>
              <Select value={location} onValueChange={setLocation}>
                <SelectTrigger id="aa-location">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">Auto (spread across cities)</SelectItem>
                  {cities.map((city) => (
                    <SelectItem key={city} value={city}>
                      {city}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                The account reports this city&apos;s GPS and timezone. It travels with the
                account, not the machine, so a shared worker cannot leak its own position.
              </p>
            </div>
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
          </div>
        ) : null}

        {step === 'provisioning' ? (
          <div className="flex items-center gap-3 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Adding the account to the worker pool…
          </div>
        ) : null}

        {step === 'done' ? (
          <div className="space-y-3 py-2">
            <div className="flex items-center gap-2 text-sm">
              <Check className="size-4 text-green-600 dark:text-green-500" />
              <span>
                <span className="font-medium">@{created?.username}</span> on{' '}
                {created?.platform}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              Auth status: {created?.authStatus}. The worker logs in on first use; a 2FA prompt
              appears in the Workers view if the platform asks for one.
            </p>
          </div>
        ) : null}

        <DialogFooter>
          {step === 'credentials' ? (
            <>
              <Button variant="outline" onClick={close}>
                Cancel
              </Button>
              <Button disabled={!canContinue} onClick={() => setStep('proxy')}>
                Continue
              </Button>
            </>
          ) : null}

          {step === 'proxy' ? (
            <>
              <Button variant="outline" onClick={() => setStep('credentials')}>
                Back
              </Button>
              <Button onClick={() => void submit()}>Add account</Button>
            </>
          ) : null}

          {step === 'done' ? <Button onClick={close}>Done</Button> : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
