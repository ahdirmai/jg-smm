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
import { useState } from 'react';

import { api, type Account } from '@/lib/api';

type Step = 'credentials' | 'proxy' | 'provisioning' | 'done';

const PLATFORMS = [
  { value: 'INSTAGRAM', label: 'Instagram' },
  { value: 'THREADS', label: 'Threads' },
] as const;

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
  const [platform, setPlatform] = useState<string>('INSTAGRAM');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [proxyGroup, setProxyGroup] = useState<string>('none');
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<Account | null>(null);

  const reset = () => {
    setStep('credentials');
    setPlatform('INSTAGRAM');
    setUsername('');
    setPassword('');
    setProxyGroup('none');
    setError(null);
    setCreated(null);
  };

  const canContinue = platform && username.trim() !== '' && password !== '';

  const submit = async () => {
    setStep('provisioning');
    setError(null);
    try {
      const account = await api.createAccount({
        platform: platform as Account['platform'],
        username: username.trim(),
        password,
        tags: [],
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
              ? 'Account added. It is being packed into a container.'
              : 'The credential is encrypted before it is stored; it is never returned.'}
          </DialogDescription>
        </DialogHeader>

        {step === 'credentials' ? (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="aa-platform">Platform</Label>
              <Select value={platform} onValueChange={setPlatform}>
                <SelectTrigger id="aa-platform">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {PLATFORMS.map((p) => (
                    <SelectItem key={p.value} value={p.value}>
                      {p.label}
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
                  <SelectItem value="none">None (use container default)</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Region-matched proxy binding lands with P1-14; none is correct for the MVP.
              </p>
            </div>
            {error ? <p className="text-sm text-destructive">{error}</p> : null}
          </div>
        ) : null}

        {step === 'provisioning' ? (
          <div className="flex items-center gap-3 py-6 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Adding the account and packing it into a container…
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
