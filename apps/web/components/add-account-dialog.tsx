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
} from '@smm/ui';
import { Check, CheckCircle2, Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import { api, type Account } from '@/lib/api';
import type { ApiSchemas } from '@smm/shared';
import { PLATFORM_LABEL, PLATFORMS } from '@/lib/platforms';

type Platform = ApiSchemas['CreateAccountRequest']['platform'];
type CreateAccountRequest = ApiSchemas['CreateAccountRequest'];

const STEPS = ['Location', 'Platform', 'Credentials', 'Review'] as const;

/**
 * Add account, as a modal.
 *
 * It deliberately mirrors /accounts/new step for step — city first, then
 * platform, then credentials/tags, then review — so the modal is not a
 * reduced second version of the same form. The only difference is that the
 * account list stays behind the dialog, which is the point of moving it here.
 */
export function AddAccountDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated?: (account: Account) => void;
}) {
  const [step, setStep] = useState(0);
  const [cities, setCities] = useState<string[]>([]);
  const [citiesLoading, setCitiesLoading] = useState(true);
  // null = no choice yet; 'auto' = API distributes by account id.
  const [location, setLocation] = useState<string | null>(null);
  const [platform, setPlatform] = useState<Platform | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [tags, setTags] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<Account | null>(null);

  const reset = () => {
    setStep(0);
    setLocation(null);
    setPlatform(null);
    setUsername('');
    setPassword('');
    setTags('');
    setSubmitting(false);
    setError(null);
    setCreated(null);
  };

  useEffect(() => {
    if (!open) return;
    let alive = true;
    setCitiesLoading(true);
    setError(null);
    api
      .listLocations()
      .then((rows) => {
        if (alive) setCities(rows.map((c) => c.name));
      })
      .catch(() => {
        if (alive) setError('Failed to load locations');
      })
      .finally(() => {
        if (alive) setCitiesLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [open]);

  const canNext =
    step === 0
      ? location !== null
      : step === 1
        ? platform !== null
        : username.trim() !== '' && password !== '';

  const close = () => {
    onOpenChange(false);
    // Reset after the dialog animates out so reopening starts clean.
    setTimeout(reset, 200);
  };

  const submit = async () => {
    if (!platform || !location) return;
    setSubmitting(true);
    setError(null);
    const body: CreateAccountRequest = {
      platform,
      username: username.trim(),
      password,
      ...(location !== 'auto' ? { location } : {}),
      tags: tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
    };
    try {
      const account = await api.createAccount(body);
      setCreated(account);
      onCreated?.(account);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create account');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] w-[min(680px,calc(100vw-2rem))] max-w-[680px] overflow-y-auto p-0">
        <div className="p-5 sm:p-6">
          <DialogHeader className="pr-8 text-left">
            <DialogTitle>Add account</DialogTitle>
            <DialogDescription>
              {created
                ? 'Account added. It joins the worker pool and is scheduled on the next free machine.'
                : 'Same setup as the full account page, without leaving the fleet. The credential is encrypted before it is stored.'}
            </DialogDescription>
          </DialogHeader>

          {created ? (
            <div className="mt-5 space-y-4">
              <div className="flex items-start gap-3 rounded-2xl border border-success/25 bg-success/8 p-4">
                <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-success" />
                <div className="min-w-0">
                  <p className="text-sm font-semibold">@{created.username}</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">
                    {PLATFORM_LABEL[created.platform]} · {location === 'auto' ? 'Auto location' : location}
                  </p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Auth status: {created.authStatus}. The worker logs in on first use; a 2FA prompt appears in the live view if the platform asks for one.
                  </p>
                </div>
              </div>
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={close}>Close</Button>
                <Button onClick={close}>View accounts</Button>
              </div>
            </div>
          ) : (
            <>
              <ol className="mt-5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-muted-foreground" aria-label="Add account steps">
                {STEPS.map((label, index) => (
                  <li key={label} className="flex items-center gap-2">
                    <span
                      className={`grid size-5 place-items-center rounded-full text-[10px] font-semibold ${
                        index < step
                          ? 'bg-success text-white'
                          : index === step
                            ? 'bg-primary text-primary-foreground'
                            : 'bg-secondary text-muted-foreground'
                      }`}
                    >
                      {index < step ? <Check className="size-3" /> : index + 1}
                    </span>
                    <span className={`hidden sm:inline ${index === step ? 'font-semibold text-foreground' : ''}`}>{label}</span>
                    {index < STEPS.length - 1 ? <span className="hidden sm:inline" aria-hidden="true">·</span> : null}
                  </li>
                ))}
              </ol>

              <div className="mt-5 min-h-[260px]">
                {step === 0 ? (
                  <div className="space-y-3">
                    <div>
                      <h2 className="text-sm font-semibold">Pick a location</h2>
                      <p className="mt-1 text-xs leading-5 text-muted-foreground">
                        Where this account claims to be. The API assigns a coordinate inside the city and the matching timezone; the browser context reports both, so the shared worker never leaks its own position.
                      </p>
                    </div>
                    {citiesLoading ? (
                      <p className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="size-4 animate-spin" /> Loading locations…
                      </p>
                    ) : (
                      <div className="grid max-h-[300px] grid-cols-1 gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
                        <LocationChoice
                          active={location === 'auto'}
                          title="Auto"
                          detail="Distribute evenly across cities"
                          onClick={() => setLocation('auto')}
                        />
                        {cities.map((city) => (
                          <LocationChoice
                            key={city}
                            active={location === city}
                            title={city}
                            detail={
                              city === 'Makassar' || city === 'Denpasar' || city === 'Balikpapan'
                                ? 'Asia/Makassar'
                                : 'Asia/Jakarta'
                            }
                            onClick={() => setLocation(city)}
                          />
                        ))}
                      </div>
                    )}
                  </div>
                ) : null}

                {step === 1 ? (
                  <div className="space-y-3">
                    <div>
                      <h2 className="text-sm font-semibold">Pick a platform</h2>
                      <p className="mt-1 text-xs leading-5 text-muted-foreground">
                        Any platform may be added. A worker hosts one account at a time, so two accounts of the same platform do not collide.
                      </p>
                    </div>
                    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      {PLATFORMS.map((value) => (
                        <button
                          key={value}
                          type="button"
                          onClick={() => setPlatform(value)}
                          aria-pressed={platform === value}
                          className={`rounded-xl border px-3 py-2.5 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 ${
                            platform === value
                              ? 'border-primary bg-primary/5 ring-1 ring-primary'
                              : 'hover:bg-white/55 dark:hover:bg-white/10'
                          }`}
                        >
                          {PLATFORM_LABEL[value]}
                        </button>
                      ))}
                    </div>
                  </div>
                ) : null}

                {step === 2 ? (
                  <div className="space-y-4">
                    <div>
                      <h2 className="text-sm font-semibold">Credentials</h2>
                      <p className="mt-1 text-xs text-muted-foreground">Sealed at rest on submit; never returned or logged.</p>
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="aa-username">Username</Label>
                      <Input id="aa-username" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="account@example.com" autoComplete="off" />
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="aa-password">Password</Label>
                      <Input id="aa-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="Enter the account password" autoComplete="new-password" />
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="aa-tags">Tags (optional)</Label>
                      <Input id="aa-tags" value={tags} onChange={(e) => setTags(e.target.value)} placeholder="campaign-a, priority" />
                      <p className="text-xs text-muted-foreground">Comma-separated.</p>
                    </div>
                  </div>
                ) : null}

                {step === 3 ? (
                  <div className="space-y-3">
                    <div>
                      <h2 className="text-sm font-semibold">Review</h2>
                      <p className="mt-1 text-xs leading-5 text-muted-foreground">
                        The account joins the pool and is scheduled onto a free worker. Login happens headful in that worker&apos;s browser.
                      </p>
                    </div>
                    <dl className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-2xl border border-border/60 bg-white/35 p-4 text-sm sm:grid-cols-2 dark:bg-white/5">
                      <div>
                        <dt className="text-xs text-muted-foreground">Location</dt>
                        <dd className="mt-0.5 font-medium">{location === 'auto' ? 'Auto' : location}</dd>
                      </div>
                      <div>
                        <dt className="text-xs text-muted-foreground">Platform</dt>
                        <dd className="mt-0.5 font-medium">{platform ? PLATFORM_LABEL[platform] : '—'}</dd>
                      </div>
                      <div>
                        <dt className="text-xs text-muted-foreground">Username</dt>
                        <dd className="mt-0.5 truncate font-medium">{username}</dd>
                      </div>
                      <div>
                        <dt className="text-xs text-muted-foreground">Tags</dt>
                        <dd className="mt-0.5 font-medium">{tags.trim() || '—'}</dd>
                      </div>
                    </dl>
                  </div>
                ) : null}
              </div>

              {error ? (
                <p className="mt-3 rounded-xl border border-destructive/25 bg-destructive/10 p-3 text-sm text-destructive" role="alert">
                  {error}
                </p>
              ) : null}

              <DialogFooter className="mt-5 gap-2 border-t border-border/50 pt-4">
                {step === 0 ? (
                  <>
                    <Button variant="outline" onClick={close}>Cancel</Button>
                    <Button disabled={!canNext} onClick={() => setStep(1)}>Continue</Button>
                  </>
                ) : null}
                {step > 0 && step < 3 ? (
                  <>
                    <Button variant="outline" onClick={() => setStep((value) => value - 1)}>Back</Button>
                    <Button disabled={!canNext} onClick={() => setStep((value) => value + 1)}>Continue</Button>
                  </>
                ) : null}
                {step === 3 ? (
                  <>
                    <Button variant="outline" onClick={() => setStep(2)} disabled={submitting}>Back</Button>
                    <Button onClick={() => void submit()} disabled={submitting}>
                      {submitting ? <Loader2 className="animate-spin" /> : null}
                      {submitting ? 'Adding…' : 'Add account'}
                    </Button>
                  </>
                ) : null}
              </DialogFooter>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

function LocationChoice({
  active,
  title,
  detail,
  onClick,
}: {
  active: boolean;
  title: string;
  detail: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`rounded-xl border px-3 py-2.5 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 ${
        active ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'hover:bg-white/55 dark:hover:bg-white/10'
      }`}
    >
      <span className="font-medium">{title}</span>
      <span className="mt-0.5 block text-xs text-muted-foreground">{detail}</span>
    </button>
  );
}
