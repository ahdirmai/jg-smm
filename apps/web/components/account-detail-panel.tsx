'use client';

import { Badge, Button, Input, Label } from '@smm/ui';
import { Download, ExternalLink, KeyRound, MonitorPlay, RefreshCw, Upload, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';

import type { Account } from '@/lib/api';
import { AuthBadge, StatusBadge } from '@/components/account-badges';
import { platformIcon } from '@/components/platform-icon';
import { LiveView, novncClientUrl } from '@/components/live-view';
import { platformLabel } from '@/lib/platforms';

/**
 * The public profile URL for a worker account. Worker accounts carry no
 * profileUrl in the contract (that field is official-account analytics only),
 * so the URL is derived from platform + username — the public handle the
 * platform resolves. The `handle` column is NOT used: it can hold a
 * platform-internal id (URL-encoded, not a public slug), which would point
 * at a page that does not exist.
 */
export function profileUrlFor(a: Account): string {
  const handle = a.username;
  switch (a.platform) {
    case 'instagram':
      return `https://www.instagram.com/${handle}/`;
    case 'threads':
      return `https://www.threads.net/@${handle}`;
    case 'tiktok':
      return `https://www.tiktok.com/@${handle}`;
    case 'linkedin':
      return `https://www.linkedin.com/in/${handle}`;
    case 'x':
      return `https://x.com/${handle}`;
    case 'youtube':
      return `https://www.youtube.com/@${handle}`;
    case 'facebook':
      return `https://www.facebook.com/${handle}`;
    default:
      return `https://${handle}`;
  }
}

/**
 * AccountDetailPanel — the per-account login cockpit, rendered IN the
 * /accounts page (no overlay, no modal): the table stays in the same view and
 * keeps updating while a login is in flight.
 *
 * One surface for the whole operator login lifecycle: the live browser (when
 * auth is in flight or the operator chose to watch), the 2FA/CAPTCHA code
 * input that used to be a `window.prompt`, and the state-specific next step.
 *
 * The iframe is mounted only while this panel is rendered AND `showLiveView`
 * is true — see `LiveView` for why a hidden client pins a worker display.
 */
export function AccountDetailPanel({
  account,
  novncUrl,
  liveViewLoading = false,
  showLiveView,
  canAct,
  busy,
  onClose,
  onStartLogin,
  onSubmitInput,
  onToggleWatch,
  onOpenProfile,
  onCancelParkedLogin,
  onReleaseSession,
  onImportSession,
  onExportSession,
}: {
  account: Account;
  /**
   * The worker's live view URL. In the stateless pool this is a TICKETED URL
   * fetched per open (it expires in minutes), not a heartbeat field.
   */
  novncUrl: string | null;
  /** The ticketed URL is being minted — the worker is waking. */
  liveViewLoading?: boolean;
  /** Auth in flight, or the operator explicitly chose to watch. */
  showLiveView: boolean;
  canAct: boolean;
  /** This account's op is in flight on the page (busy === account.id). */
  busy: boolean;
  onClose: () => void;
  onStartLogin: (id: string) => void;
  onSubmitInput: (id: string, value: string) => void;
  onToggleWatch: () => void;
  /**
   * Open the profile in the account's worker virtual browser (noVNC live
   * view), logged in through the account's session. The page falls back to
   * an operator tab when the account has no live view.
   */
  onOpenProfile: (account: Account) => void;
  /** Abandon a parked login: closes the browser and marks the attempt failed. */
  onCancelParkedLogin: (id: string) => void;
  /** Free whatever an earlier session left holding this account's worker. */
  onReleaseSession: (id: string) => void;
  /**
   * Adopt a Playwright storageState captured elsewhere (the local harness, or
   * another worker). The account runs from that session with no login step.
   */
  onImportSession: (account: Account, session: unknown) => void;
  /** Download this account's stored session so another container can adopt it. */
  onExportSession: (account: Account) => void;
}) {
  const [code, setCode] = useState('');
  const [sessionError, setSessionError] = useState<string | null>(null);
  const sessionFile = useRef<HTMLInputElement>(null);

  // Switching accounts must not leak the previous row's half-typed code.
  useEffect(() => setCode(''), [account.id]);

  const Icon = platformIcon(account.platform);
  const manualChallenge =
    account.authStatus === 'NEEDS_INPUT' && /^(captcha|checkpoint):/.test(account.lastError ?? '');

  return (
    <div className="space-y-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="grid size-9 shrink-0 place-items-center rounded-lg bg-secondary text-muted-foreground">
            <Icon className="size-4" />
          </span>
          <div className="min-w-0">
            <div className="truncate text-base font-medium tracking-tight">@{account.username}</div>
            <div className="flex flex-wrap items-center gap-2 font-mono text-xs text-muted-foreground">
              <span>
                {account.handle && account.handle !== account.username
                  ? account.handle
                  : account.workerId
                    ? account.workerId.slice(0, 12)
                    : 'idle'}
              </span>
              {/* The account's OWN city. On a shared pool worker the machine
                  says nothing about where the account appears to be. */}
              {account.location ? (
                <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] uppercase tracking-wide">
                  {account.location}
                  {account.timezone ? ` · ${account.timezone}` : ''}
                </span>
              ) : null}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <AuthBadge status={account.authStatus} />
          <StatusBadge status={account.status} />
          <Badge variant="outline">{platformLabel(account.platform)}</Badge>
          <Button variant="ghost" size="icon" aria-label="Close account panel" onClick={onClose}>
            <X />
          </Button>
        </div>
      </div>

      <div className="grid gap-5 md:grid-cols-2">
        {showLiveView ? (
          <div className="space-y-2">
            {liveViewLoading && !novncUrl ? (
              <p className="flex items-center gap-2 rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm text-muted-foreground">
                Starting the live view — the worker wakes on demand…
              </p>
            ) : novncUrl ? (
              <>
                <LiveView url={novncUrl} title={`Live browser — ${account.username}`} />
                <a
                  href={novncClientUrl(novncUrl)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                >
                  <ExternalLink className="size-3.5" />
                  Open in tab
                </a>
              </>
            ) : (
              <p className="rounded-lg border border-border/60 bg-secondary/40 p-3 text-sm text-muted-foreground">
                No live view yet — the worker has not published its noVNC port. Check the container
                on the Workers page.
              </p>
            )}
          </div>
        ) : null}

        <div className="space-y-3">
          {manualChallenge ? (
            <p role="status" className="text-sm text-muted-foreground">
              Complete the CAPTCHA or security check in the live browser. Login is saved
              automatically after verification succeeds.
            </p>
          ) : null}
          {account.authStatus === 'NEEDS_INPUT' && !manualChallenge ? (
            <div className="space-y-2">
              <Label htmlFor="account-panel-code">Verification code</Label>
              <div className="flex gap-2">
                <Input
                  id="account-panel-code"
                  autoFocus
                  inputMode="numeric"
                  maxLength={8}
                  placeholder="e.g. 123456"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
                <Button
                  disabled={!code.trim() || busy || !canAct}
                  onClick={() => {
                    onSubmitInput(account.id, code.trim());
                    setCode('');
                  }}
                >
                  <KeyRound />
                  Send
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                Type the code shown in the live view — or solve the challenge directly in the live
                view.
              </p>
              {/* Escape hatch. Without it a parked login is a trap: the row keeps
                  NEEDS_INPUT, every new attempt answers "submit the code
                  instead", and the only exit was the lease TTL. */}
              <div className="flex flex-wrap gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busy || !canAct}
                  onClick={() => onCancelParkedLogin(account.id)}
                >
                  <X />
                  Cancel this login
                </Button>
              </div>
            </div>
          ) : null}

          {account.authStatus === 'AUTHENTICATING' ? (
            <p className="text-sm text-muted-foreground">
              Login in progress — the state settles when the worker reports back.
            </p>
          ) : null}

          {account.authStatus === 'AUTHENTICATED' ? (
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={busy}
                onClick={() => onOpenProfile(account)}
              >
                <ExternalLink />
                Open profile
              </Button>
              <Button variant="ghost" size="sm" onClick={onToggleWatch}>
                <MonitorPlay />
                Live view
              </Button>
            </div>
          ) : null}

          {account.authStatus === 'FAILED' ? (
            <div className="space-y-2">
              {account.lastError ? (
                <p className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 font-mono text-xs break-words text-destructive">
                  {account.lastError}
                </p>
              ) : null}
              <p className="text-sm text-muted-foreground">Start the login again to retry.</p>
            </div>
          ) : null}

          {account.authStatus === 'QUARANTINED' ? (
            <p className="text-sm text-muted-foreground">
              This account is quarantined after repeated login failures. Resume it from the account
              menu to clear the streak.
            </p>
          ) : null}

          {account.authStatus !== 'QUARANTINED' ? (
            <div className="flex flex-wrap gap-2">
              <Button
                className="flex-1"
                disabled={busy || !canAct}
                onClick={() => onStartLogin(account.id)}
              >
                <KeyRound />
                Log in
              </Button>
              {account.workerId ||
              showLiveView ||
              account.authStatus === 'AUTHENTICATING' ||
              account.authStatus === 'NEEDS_INPUT' ? (
                <Button
                  variant="outline"
                  disabled={busy || !canAct}
                  onClick={() => onReleaseSession(account.id)}
                >
                  <RefreshCw />
                  Release session
                </Button>
              ) : null}
            </div>
          ) : null}

          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={busy || !canAct}
              onClick={() => sessionFile.current?.click()}
            >
              <Upload />
              Import session
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={busy || !canAct || account.authStatus !== 'AUTHENTICATED'}
              onClick={() => onExportSession(account)}
            >
              <Download />
              Export session
            </Button>
          </div>
          {sessionError ? <p className="text-xs text-destructive">{sessionError}</p> : null}
          <input
            ref={sessionFile}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={(e) => {
              const file = e.target.files?.[0];
              // Reset first: picking the same file again must still fire.
              e.target.value = '';
              if (!file) return;
              setSessionError(null);
              void file.text().then((text) => {
                try {
                  onImportSession(account, JSON.parse(text));
                } catch {
                  setSessionError('That file is not a Playwright storageState (invalid JSON).');
                }
              });
            }}
          />
        </div>
      </div>
    </div>
  );
}
