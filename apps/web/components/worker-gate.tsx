'use client';

import { AlertCircle, Loader2, Play, ShieldCheck } from 'lucide-react';
import { usePathname } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';

import { Button, Card } from '@smm/ui';
import { api, type WorkerHealth } from '@/lib/api';

/** A worker that has been idle for this long can be hibernated by boxd. */
const IDLE_MS = 60 * 60 * 1000;
const LAST_ACTIVE = 'smm:last-active';
const POLL_MS = 2000;
const WAKE_ATTEMPTS = 20;

type Phase = 'idle' | 'waking' | 'ready';

const sleep = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms));

/**
 * Workspace gate.
 *
 * The stateless boxd fleet is allowed to hibernate after an idle hour. This
 * gate mirrors that contract in the browser: after an hour without an
 * interaction it blocks the workspace and asks the operator to start the
 * workers. Start sends an authenticated keepalive to every worker; the modal
 * then polls /api/workers/health every two seconds and unlocks only when every
 * worker answers. That health request is also the wake request on boxd.
 *
 * The gate deliberately does not poll while the workspace is open: an inbound
 * probe every few seconds would keep every microVM awake and defeat the idle
 * policy it is guarding.
 */
export function WorkerGate({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  // Only gate the Accounts page on the dedicated login worker (smm-login-01).
  // Action workers (smm-worker-*) must stay hibernated until an action job is
  // actually submitted on /actions.
  const needsLoginGate = pathname === '/accounts' || pathname.startsWith('/accounts/');
  const [blocked, setBlocked] = useState(false);
  const [phase, setPhase] = useState<Phase>('idle');
  const [workers, setWorkers] = useState<WorkerHealth[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const contentRef = useRef<HTMLDivElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);
  const startRef = useRef<HTMLButtonElement>(null);
  const lastActive = useRef(Date.now());

  const markActive = () => {
    const now = Date.now();
    lastActive.current = now;
    try {
      localStorage.setItem(LAST_ACTIVE, String(now));
    } catch {
      // Private mode / storage disabled: the gate still works for this tab.
    }
  };

  // Restore the idle clock. A tab that was closed for more than an hour must
  // show the gate before any worker request is attempted.
  useEffect(() => {
    let last = 0;
    try {
      last = Number(localStorage.getItem(LAST_ACTIVE) ?? 0);
    } catch {
      last = 0;
    }
    if (!last) {
      markActive();
      return;
    }
    lastActive.current = last;
    if (Date.now() - last >= IDLE_MS) {
      setBlocked(true);
    }
  }, []);

  // Track real interaction, not page load. Only an operator using the
  // workspace keeps the fleet awake.
  useEffect(() => {
    if (blocked) return;
    const onActivity = () => markActive();
    window.addEventListener('pointerdown', onActivity, { capture: true, passive: true });
    window.addEventListener('keydown', onActivity, { capture: true });
    window.addEventListener('touchstart', onActivity, { capture: true, passive: true });
    const timer = window.setInterval(() => {
      if (Date.now() - lastActive.current >= IDLE_MS) setBlocked(true);
    }, 30_000);
    return () => {
      window.removeEventListener('pointerdown', onActivity, { capture: true });
      window.removeEventListener('keydown', onActivity, { capture: true });
      window.removeEventListener('touchstart', onActivity, { capture: true });
      window.clearInterval(timer);
    };
  }, [blocked]);

  // While blocked on /accounts, the workspace is inert and the document cannot scroll.
  useEffect(() => {
    const content = contentRef.current;
    if (!content) return;
    const active = blocked && needsLoginGate;
    content.toggleAttribute('inert', active);
    if (!active) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    startRef.current?.focus();
    return () => {
      document.body.style.overflow = previous;
    };
  }, [blocked, needsLoginGate]);

  // Focus trap for the blocking dialog. There is intentionally no Escape
  // close: the workspace is not usable until a worker answers.
  useEffect(() => {
    if (!blocked) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Tab' || !dialogRef.current) return;
      const focusable = Array.from(
        dialogRef.current.querySelectorAll<HTMLElement>(
          'button:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((el) => el.offsetParent !== null);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (!first || !last) return;
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [blocked]);

  const unlock = () => {
    markActive();
    setBlocked(false);
    setPhase('ready');
    setError(null);
  };

  const startWorkers = async () => {
    setPhase('waking');
    setError(null);
    setAttempt(0);
    try {
      const first = await api.wakeWorkers('login');
      setWorkers(first.workers);
      if (first.workers.length > 0 && first.workers.every((w) => w.awake)) {
        unlock();
        return;
      }

      for (let i = 0; i < WAKE_ATTEMPTS; i += 1) {
        setAttempt(i + 1);
        await sleep(POLL_MS);
        const health = await api.workerHealth('login');
        setWorkers(health.workers);
        if (health.workers.length > 0 && health.workers.every((w) => w.awake)) {
          unlock();
          return;
        }
      }
      setPhase('idle');
      setError('Login worker did not answer yet. Retry to send another wake request.');
    } catch (err) {
      setPhase('idle');
      setError(err instanceof Error ? err.message : 'Could not start the login worker');
    }
  };

  const showGate = blocked && needsLoginGate;
  const awake = workers.filter((w) => w.awake).length;
  const total = workers.length;

  return (
    <>
      <div ref={contentRef}>{children}</div>

      {showGate ? (
        <div className="fixed inset-0 z-[80] flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-[#07131c]/45 backdrop-blur-[3px]" aria-hidden="true" />
          <Card
            ref={dialogRef}
            role="dialog"
            aria-modal="true"
            aria-labelledby="worker-gate-title"
            aria-describedby="worker-gate-description"
            className="relative z-10 w-full max-w-[520px] rounded-[28px] p-6 shadow-[var(--shadow-float)] sm:p-7"
          >
            <span className="grid size-11 place-items-center rounded-[15px] bg-primary/12 text-primary">
              <ShieldCheck className="size-5" />
            </span>
            <p className="mt-5 text-[11px] font-bold uppercase tracking-[0.14em] text-primary">
              Workspace paused
            </p>
            <h2 id="worker-gate-title" className="mt-1 text-[24px] font-semibold tracking-[-0.04em]">
              Start login worker to continue
            </h2>
            <p id="worker-gate-description" className="mt-2 text-sm leading-6 text-muted-foreground">
              The login worker may have hibernated while idle. Start it to manage accounts —
              action workers stay asleep until you run an action.
            </p>

            {error ? (
              <p className="mt-4 flex items-start gap-2 rounded-xl border border-destructive/25 bg-destructive/10 p-3 text-sm text-destructive" role="alert">
                <AlertCircle className="mt-0.5 size-4 shrink-0" />
                {error}
              </p>
            ) : null}

            {phase === 'waking' ? (
              <div className="mt-5 rounded-2xl border border-border/60 bg-white/35 p-3 dark:bg-white/5">
                <div className="flex items-center justify-between gap-3 text-xs">
                  <span className="flex items-center gap-2 font-medium">
                    <Loader2 className="size-3.5 animate-spin" />
                    Starting workers…
                  </span>
                  <span className="tabular-nums text-muted-foreground">
                    {total > 0 ? `${awake}/${total} awake` : `attempt ${attempt}/${WAKE_ATTEMPTS}`}
                  </span>
                </div>
                <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-secondary">
                  <div
                    className="h-full rounded-full bg-primary transition-[width] duration-500"
                    style={{ width: total > 0 ? `${Math.max(8, (awake / total) * 100)}%` : '18%' }}
                  />
                </div>
              </div>
            ) : null}

            {workers.length > 0 ? (
              <ul className="mt-4 max-h-48 space-y-1 overflow-y-auto rounded-2xl border border-border/50 p-1.5" aria-label="Worker status">
                {workers.map((worker) => (
                  <li key={worker.id} className="flex items-center justify-between gap-3 rounded-xl px-2 py-1.5 text-xs">
                    <span className="flex min-w-0 items-center gap-2">
                      <span
                        className={`size-1.5 shrink-0 rounded-full ${worker.awake ? 'bg-success' : 'bg-warning'}`}
                        aria-hidden="true"
                      />
                      <span className="truncate font-medium">{worker.name}</span>
                    </span>
                    <span className="shrink-0 text-muted-foreground">
                      {worker.awake ? 'awake' : worker.error ? 'unreachable' : 'sleeping'}
                    </span>
                  </li>
                ))}
              </ul>
            ) : null}

            <div className="mt-5 flex flex-wrap items-center gap-2">
              <Button ref={startRef} onClick={() => void startWorkers()} disabled={phase === 'waking'}>
                {phase === 'waking' ? <Loader2 className="animate-spin" /> : <Play />}
                {phase === 'waking' ? 'Starting…' : 'Start login worker'}
              </Button>
              <span className="text-xs text-muted-foreground">
                Unlocks as soon as the login worker responds.
              </span>
            </div>
          </Card>
        </div>
      ) : null}
    </>
  );
}
