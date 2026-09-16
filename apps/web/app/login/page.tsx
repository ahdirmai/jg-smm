'use client';

import { Suspense } from 'react';
import { useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Activity, Loader2 } from 'lucide-react';

import { Button, Card, CardContent, Input, Label } from '@smm/ui';
import { ModeToggle } from '@/components/mode-toggle';

/**
 * Login page (P6-03, mirrors docs/prototype/login.html).
 *
 * Standalone — not wrapped in the dashboard shell — because the caller has no
 * session yet and no nav to route to. The theme still applies (the floating
 * switch) so the login screen matches the rest of the app in both themes.
 *
 * The API sets the session cookie on success (credentials: include), so the
 * redirect to `/` is already authenticated on first paint.
 */
export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className="relative flex min-h-screen items-center justify-center bg-background p-4">
          <Card className="w-full max-w-sm">
            <CardContent className="space-y-5 p-6">Loading…</CardContent>
          </Card>
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
  const next = search.get('next') ?? '/';
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const resp = await fetch(
        `${process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:24080'}/api/auth/login`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'include',
          body: JSON.stringify({ email, password }),
        },
      );
      if (!resp.ok) {
        let message = resp.statusText;
        try {
          const body = await resp.json();
          message = body?.error?.message ?? message;
        } catch {
          // Non-JSON error; fall back to status text.
        }
        throw new Error(message);
      }
      router.replace(next);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Sign in failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="relative flex min-h-screen items-center justify-center bg-background p-4">
      <div className="fixed right-4 top-4">
        <ModeToggle header />
      </div>

      <Card className="w-full max-w-sm">
        <CardContent className="space-y-5 p-6">
          <div className="flex flex-col items-center gap-2">
            <span className="grid size-10 place-items-center rounded-lg bg-primary text-primary-foreground">
              <Activity className="size-5" />
            </span>
            <div className="text-center">
              <h1 className="text-lg font-semibold tracking-tight">SMM Automation</h1>
              <p className="text-xs text-muted-foreground">Sign in to the dashboard</p>
            </div>
          </div>

          <form onSubmit={submit} className="space-y-3">
            <div>
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="owner@smm.local"
                autoComplete="email"
                required
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
                autoComplete="current-password"
                required
              />
            </div>

            {error ? (
              <p className="text-sm text-destructive" role="alert">
                {error}
              </p>
            ) : null}

            <Button type="submit" className="w-full" disabled={busy}>
              {busy ? <Loader2 className="animate-spin" /> : null}
              Sign in
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
