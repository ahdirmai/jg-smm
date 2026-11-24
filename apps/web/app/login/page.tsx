'use client';

import { Suspense, useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Activity, Eye, EyeOff, Loader2 } from 'lucide-react';

import { Button, Input, Label } from '@smm/ui';
import { ModeToggle } from '@/components/mode-toggle';
import SpotlightCard from '@/components/reactbits/spotlight-card';
import { useSession } from '@/lib/auth/session-context';

/**
 * Standalone sign-in. The shell's anonymous guard owns every dashboard route,
 * so this page deliberately has no sidebar and no navigation.
 */
function safeNext(raw: string | null): string {
  if (!raw) return '/';
  if (raw.startsWith('/') && !raw.startsWith('//') && !raw.startsWith('\\')) return raw;
  return '/';
}

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className="relative flex min-h-dvh items-center justify-center p-4">
          <div className="card-surface w-full max-w-md rounded-[26px] p-8 text-sm text-muted-foreground">
            Loading sign-in…
          </div>
        </div>
      }
    >
      <LoginForm />
    </Suspense>
  );
}

function LoginForm() {
  const router = useRouter();
  const search = useSearchParams();
  const next = safeNext(search.get('next'));
  const session = useSession();

  useEffect(() => {
    if (session.status === 'authenticated') router.replace(next);
  }, [session.status, next, router]);

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const response = await fetch(
        `${process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:24080'}/api/auth/login`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ email, password }),
        },
      );
      if (!response.ok) {
        let message = response.statusText;
        try {
          const body = await response.json();
          message = body?.error?.message ?? message;
        } catch {
          // Keep the status text when the error body is not JSON.
        }
        throw new Error(message);
      }
      await session.refresh();
      router.replace(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign in failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="relative flex min-h-dvh items-center justify-center p-4 sm:p-6">
      <div className="atmosphere" aria-hidden="true">
        <div className="atmosphere__cloud atmosphere__cloud--one" />
        <div className="atmosphere__cloud atmosphere__cloud--two" />
      </div>

      <div className="fixed right-4 top-4 z-20">
        <ModeToggle header />
      </div>

      <SpotlightCard className="w-full max-w-[430px] rounded-[28px] p-6 sm:p-8">
        <div className="flex flex-col items-start">
          <span className="grid size-11 place-items-center rounded-[15px] bg-primary text-primary-foreground shadow-[inset_0_1px_0_rgb(255_255_255/0.22),0_12px_24px_-14px_hsl(var(--primary)/0.8)]">
            <Activity className="size-5" />
          </span>
          <h1 className="mt-5 text-[26px] font-semibold tracking-[-0.04em]">Welcome back</h1>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            Sign in to manage accounts, workers, and the action queue.
          </p>
        </div>

        <form onSubmit={submit} className="mt-7 space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              placeholder="owner@smm.local"
              autoComplete="email"
              autoCapitalize="none"
              spellCheck={false}
              required
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <div className="relative">
              <Input
                id="password"
                type={showPassword ? 'text' : 'password'}
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="Enter your password"
                autoComplete="current-password"
                className="pr-12"
                required
              />
              <button
                type="button"
                onClick={() => setShowPassword((value) => !value)}
                className="absolute inset-y-0 right-0 grid w-11 place-items-center rounded-r-[12px] text-muted-foreground transition-colors hover:text-foreground"
                aria-label={showPassword ? 'Hide password' : 'Show password'}
                aria-pressed={showPassword}
              >
                {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
              </button>
            </div>
          </div>

          {error ? (
            <p className="rounded-xl border border-destructive/25 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
              {error}
            </p>
          ) : null}

          <Button type="submit" size="lg" className="w-full" disabled={busy}>
            {busy ? <Loader2 className="animate-spin" /> : null}
            {busy ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>

        <p className="mt-5 border-t border-border/50 pt-4 text-xs leading-5 text-muted-foreground">
          Need access? Ask a workspace owner to invite your email.
        </p>
      </SpotlightCard>
    </div>
  );
}
