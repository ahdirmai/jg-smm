'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Check, ChevronRight, Loader2 } from 'lucide-react';

import { Button, Card, CardContent, Input, Label } from '@smm/ui';
import { api } from '@/lib/api';
import type { ApiSchemas } from '@smm/shared';

type Platform = ApiSchemas['CreateAccountRequest']['platform'];
type CreateAccountRequest = ApiSchemas['CreateAccountRequest'];

// All seven platforms the product handles; the MVP ships IG + Threads active,
// the rest are created but not yet provisioned (see PLATFORM_MATRIX).
const PLATFORMS: { id: Platform; label: string }[] = [
  { id: 'instagram', label: 'Instagram' },
  { id: 'threads', label: 'Threads' },
  { id: 'facebook', label: 'Facebook' },
  { id: 'linkedin', label: 'LinkedIn' },
  { id: 'x', label: 'X' },
  { id: 'youtube', label: 'YouTube' },
  { id: 'tiktok', label: 'TikTok' },
];

const STEPS = ['Platform', 'Credentials', 'Review'] as const;

/**
 * Add-account wizard (P6-07, mirrors docs/prototype/add-account.html).
 *
 * Three steps: pick platform → credentials → review. The final submit is the
 * only write; earlier steps are local state only, so backing out costs nothing.
 * The quick path on the Accounts list stays the AddAccountDialog.
 */
export default function AddAccountPage() {
  const router = useRouter();
  const [step, setStep] = useState(0);
  const [platform, setPlatform] = useState<Platform | null>(null);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [tags, setTags] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canNext = step === 0 ? platform !== null : username.trim() !== '' && password !== '';

  const submit = async () => {
    if (!platform) return;
    setSubmitting(true);
    setError(null);
    const body: CreateAccountRequest = {
      platform,
      username: username.trim(),
      password,
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
    <div className="mx-auto max-w-2xl space-y-4">
      <Button variant="ghost" size="sm" onClick={() => router.back()} className="-ml-2">
        <ArrowLeft />
        Back to accounts
      </Button>

      {/* Stepper: Platform → Credentials → Review */}
      <div className="flex items-center justify-center gap-2">
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
        <CardContent className="space-y-4 p-6">
          {step === 0 ? (
            <div className="space-y-4">
              <div>
                <h2 className="text-sm font-semibold">Pick a platform</h2>
                <p className="text-xs text-muted-foreground">
                  One credential set per platform per worker — a duplicate is rejected, never
                  overwritten.
                </p>
              </div>
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                {PLATFORMS.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    onClick={() => setPlatform(p.id)}
                    className={
                      'rounded-lg border p-4 text-left text-sm transition-colors ' +
                      (platform === p.id
                        ? 'border-primary bg-primary/5 ring-1 ring-primary'
                        : 'hover:bg-accent')
                    }
                  >
                    {p.label}
                  </button>
                ))}
              </div>
            </div>
          ) : null}

          {step === 1 ? (
            <div className="space-y-4">
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

          {step === 2 ? (
            <div className="space-y-4">
              <div>
                <h2 className="text-sm font-semibold">Review</h2>
                <p className="text-xs text-muted-foreground">
                  The worker logs in interactively (headful, Playwright) once the account is packed
                  into a container.
                </p>
              </div>
              <dl className="space-y-2 text-sm">
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Platform</dt>
                  <dd className="font-medium">
                    {PLATFORMS.find((p) => p.id === platform)?.label}
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
                <div className="flex justify-between gap-4">
                  <dt className="text-muted-foreground">Tags</dt>
                  <dd className="font-medium">{tags ? tags : '—'}</dd>
                </div>
              </dl>
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
