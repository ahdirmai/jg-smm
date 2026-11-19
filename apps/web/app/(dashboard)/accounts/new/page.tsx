'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Check, ChevronRight, Loader2 } from 'lucide-react';

import { Button, Card, CardContent, Input, Label } from '@smm/ui';
import { api } from '@/lib/api';
import type { ApiSchemas } from '@smm/shared';

type Platform = ApiSchemas['CreateAccountRequest']['platform'];
type CreateAccountRequest = ApiSchemas['CreateAccountRequest'];

// The seven ids and their labels come from lib/platforms.ts. This page used to
// re-type all seven as `{id, label}` pairs — a fourth hand-rolled copy of the
// same fact — so the pair list and the label map could disagree with each
// other and with the two other pages that declared the same thing.
import { PLATFORMS, PLATFORM_LABEL } from '@/lib/platforms';
const STEPS = ['Container', 'Platform', 'Credentials', 'Review'] as const;

/**
 * Add-account wizard (P6-07, mirrors docs/prototype/add-account.html).
 *
 * Four steps: location → platform → credentials → review.
 *
 * The first step used to pick a CONTAINER, because an account lived inside one.
 * In the stateless pool an account is scheduled onto whichever worker is free,
 * so the only placement decision left is the account's own city — and that
 * decides the GPS and timezone its browser reports. The final submit is the
 * only write; earlier steps are local state only.
 */
export default function AddAccountPage() {
  const router = useRouter();
  const [step, setStep] = useState(0);
  const [cities, setCities] = useState<string[]>([]);
  const [citiesLoading, setCitiesLoading] = useState(true);
  // 'auto' lets the API distribute by account id.
  const [location, setLocation] = useState<string | null>(null);
  const [platform, setPlatform] = useState<Platform | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [tags, setTags] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
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
  }, []);

  const canNext =
    step === 0
      ? location !== null
      : step === 1
        ? platform !== null
        : username.trim() !== '' && password !== '';

  const submit = async () => {
    if (!platform) return;
    setSubmitting(true);
    setError(null);
    const body: CreateAccountRequest = {
      platform,
      username: username.trim(),
      password,
      ...(location && location !== 'auto' ? { location } : {}),
      tags: tags
        .split(',')
        .map((t) => t.trim())
        .filter(Boolean),
    };
    try {
      await api.createAccount(body);
      router.push('/accounts');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create account');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="mx-auto max-w-xl space-y-3">
      <Button variant="ghost" size="sm" onClick={() => router.back()} className="-ml-2">
        <ArrowLeft />
        Back to accounts
      </Button>

      {/* Stepper: Location → Platform → Credentials → Review */}
      <div className="flex items-center justify-center gap-1.5">
        {STEPS.map((label, i) => (
          <div key={label} className="flex items-center gap-2">
            <div
              className={
                'flex size-7 items-center justify-center rounded-full text-xs font-medium ' +
                (i < step
                  ? 'bg-success text-white'
                  : i === step
                    ? 'bg-primary text-primary-foreground'
                    : 'bg-muted text-muted-foreground')
              }
            >
              {i < step ? <Check className="size-3.5" /> : i + 1}
            </div>
            <span
              className={
                'text-sm ' + (i === step ? 'font-medium text-foreground' : 'text-muted-foreground')
              }
            >
              {label}
            </span>
            {i < STEPS.length - 1 ? <ChevronRight className="size-4 text-muted-foreground" /> : null}
          </div>
        ))}
      </div>

      <Card>
        <CardContent className="space-y-3 p-5">
          {step === 0 ? (
            <div className="space-y-3">
              <div>
                <h2 className="text-sm font-semibold">Pick a location</h2>
                <p className="text-xs text-muted-foreground">
                  Where this account claims to be. The API assigns a coordinate inside the city
                  and the matching timezone, and the browser context reports both — the machine it
                  runs on is shared and says nothing about the account.
                </p>
              </div>
              {citiesLoading ? (
                <p className="flex items-center gap-2 text-sm text-muted-foreground">
                  <Loader2 className="size-4 animate-spin" /> Loading locations…
                </p>
              ) : (
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  <button
                    type="button"
                    onClick={() => setLocation('auto')}
                    className={
                      'rounded-lg border px-3 py-2.5 text-left text-sm transition-colors ' +
                      (location === 'auto'
                        ? 'border-primary bg-primary/5 ring-1 ring-primary'
                        : 'hover:bg-accent')
                    }
                  >
                    <span className="font-medium">Auto</span>
                    <span className="mt-0.5 block text-xs text-muted-foreground">
                      Distribute evenly across cities
                    </span>
                  </button>
                  {cities.map((city) => (
                    <button
                      key={city}
                      type="button"
                      onClick={() => setLocation(city)}
                      className={
                        'rounded-lg border px-3 py-2.5 text-left text-sm transition-colors ' +
                        (location === city
                          ? 'border-primary bg-primary/5 ring-1 ring-primary'
                          : 'hover:bg-accent')
                      }
                    >
                      <span className="font-medium">{city}</span>
                      <span className="mt-0.5 block text-xs text-muted-foreground">
                        {city === 'Makassar' || city === 'Denpasar' || city === 'Balikpapan'
                          ? 'Asia/Makassar'
                          : 'Asia/Jakarta'}
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          ) : null}

          {step === 1 ? (
            <div className="space-y-3">
              <div>
                <h2 className="text-sm font-semibold">Pick a platform</h2>
                <p className="text-xs text-muted-foreground">
                  Any platform may be added here: a worker hosts one account at a time, so two
                  accounts of the same platform no longer collide.
                </p>
              </div>
              <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                {PLATFORMS.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => setPlatform(p)}
                    className={
                      'rounded-lg border px-3 py-2.5 text-left text-sm transition-colors ' +
                      (platform === p
                        ? 'border-primary bg-primary/5 ring-1 ring-primary'
                        : 'hover:bg-accent')
                    }
                  >
                    {PLATFORM_LABEL[p]}
                  </button>
                ))}
              </div>
            </div>
          ) : null}

          {step === 2 ? (
            <div className="space-y-3">
              <div>
                <h2 className="text-sm font-semibold">Credentials</h2>
                <p className="text-xs text-muted-foreground">
                  Sealed at rest on submit; never returned or logged.
                </p>
              </div>
              <div className="space-y-3">
                <div>
                  <Label htmlFor="username">Username</Label>
                  <Input
                    id="username"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    placeholder="account@example.com"
                    autoComplete="off"
                  />
                </div>
                <div>
                  <Label htmlFor="password">Password</Label>
                  <Input
                    id="password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="••••••••"
                    autoComplete="new-password"
                  />
                </div>
                <div>
                  <Label htmlFor="tags">Tags (optional)</Label>
                  <Input
                    id="tags"
                    value={tags}
                    onChange={(e) => setTags(e.target.value)}
                    placeholder="campaign-a, priority"
                  />
                  <p className="mt-1 text-xs text-muted-foreground">Comma-separated.</p>
                </div>
              </div>
            </div>
          ) : null}

          {step === 3 ? (
            <div className="space-y-3">
              <div>
                <h2 className="text-sm font-semibold">Review</h2>
                <p className="text-xs text-muted-foreground">
                  The account joins the worker pool and is scheduled onto a free machine. The
                  login is headful (Playwright) inside that worker&apos;s browser.
                </p>
              </div>
              <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Location</dt>
                  <dd className="font-medium">{location === 'auto' ? 'Auto' : location}</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Platform</dt>
                  <dd className="font-medium">
                    {platform ? PLATFORM_LABEL[platform] : null}
                  </dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Username</dt>
                  <dd className="font-medium font-mono text-xs">{username}</dd>
                </div>
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Password</dt>
                  <dd className="font-medium font-mono text-xs">
                    {'•'.repeat(Math.min(password.length, 12))}
                  </dd>
                </div>
                <div className="col-span-full flex justify-between gap-4">
                  <dt className="text-muted-foreground">Tags</dt>
                  <dd className="font-medium">{tags ? tags : '—'}</dd>
                </div>
              </div>
            </div>
          ) : null}

          {error ? (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          ) : null}

          <div className="flex justify-between gap-3 pt-2">
            {step > 0 ? (
              <Button variant="outline" onClick={() => setStep((s) => s - 1)} disabled={submitting}>
                Back
              </Button>
            ) : (
              <span />
            )}
            {step < STEPS.length - 1 ? (
              <Button onClick={() => setStep((s) => s + 1)} disabled={!canNext}>
                Continue
              </Button>
            ) : (
              <Button onClick={() => void submit()} disabled={submitting}>
                {submitting ? <Loader2 className="animate-spin" /> : null}
                Create account
              </Button>
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
