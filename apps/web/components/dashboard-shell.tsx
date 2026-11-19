'use client';

import {
  Activity,
  FileText,
  LayoutDashboard,
  Loader2,
  LogOut,
  Menu,
  MonitorSmartphone,
  Plus,
  Search,
  Settings,
  ShieldCheck,
  Users,
  X,
  type LucideIcon,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useEffect, useRef, useState } from 'react';

import { ModeToggle } from '@/components/mode-toggle';
import { WorkerGate } from '@/components/worker-gate';
import { Button } from '@smm/ui';
import { api } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { ROLE_LABEL, can } from '@/lib/auth/permissions';
import { PLATFORMS, PLATFORM_LABEL } from '@/lib/platforms';
import type { Permission } from '@/lib/auth/permissions';

type Leaf = { href: string; label: string; icon: LucideIcon; perm?: Permission };
type Section = { label: string; items: Leaf[] };

const PLATFORM_LIST = PLATFORMS.map((p) => PLATFORM_LABEL[p]).join(', ');

/**
 * Navigation is deliberately flat and short. Monitoring and Templates were
 * removed from the product surface, so there is no group here that can expand
 * into routes the sidebar no longer owns.
 */
const NAV: Section[] = [
  {
    label: 'Workspace',
    items: [
      { href: '/', label: 'Overview', icon: LayoutDashboard },
      { href: '/workers', label: 'Workers', icon: MonitorSmartphone, perm: 'act' },
      { href: '/accounts', label: 'Accounts', icon: Users, perm: 'act' },
      { href: '/actions', label: 'Actions', icon: Activity, perm: 'act' },
      { href: '/scrape', label: 'Scrape', icon: Search, perm: 'act' },
    ],
  },
  {
    label: 'Insights',
    items: [
      { href: '/reports', label: 'Reports', icon: FileText },
      { href: '/audit', label: 'Audit log', icon: ShieldCheck },
    ],
  },
  {
    label: 'System',
    items: [{ href: '/settings', label: 'Settings', icon: Settings, perm: 'admin' }],
  },
];

const TITLES: Record<string, { title: string; subtitle: string }> = {
  '/': { title: 'Overview', subtitle: 'What needs your attention across the workspace' },
  '/workers': { title: 'Workers', subtitle: 'Fleet health, capacity, and live screens' },
  '/accounts': { title: 'Accounts', subtitle: 'Credentials, sessions, and login state' },
  '/actions': { title: 'Actions', subtitle: 'Queue, retry, and verify social actions' },
  '/scrape': { title: 'Scrape', subtitle: `Keyword search across ${PLATFORM_LIST}` },
  '/reports': { title: 'Reports', subtitle: 'Export the work that has already happened' },
  '/audit': { title: 'Audit log', subtitle: 'Who changed what, and when' },
  '/settings': { title: 'Settings', subtitle: 'Workspace access and appearance' },
  '/login': { title: 'Sign in', subtitle: '' },
};

function titleFor(pathname: string): { title: string; subtitle: string } {
  const exact = TITLES[pathname];
  if (exact) return exact;
  for (const prefix of ['/accounts', '/workers', '/actions', '/scrape']) {
    const fallback = TITLES[prefix];
    if (pathname.startsWith(prefix) && fallback) return fallback;
  }
  return { title: 'Workspace', subtitle: '' };
}

function isActive(href: string, pathname: string): boolean {
  return href === '/' ? pathname === '/' : pathname.startsWith(href);
}

function ShellSplash() {
  return (
    <div className="flex min-h-dvh items-center justify-center" data-testid="shell-splash">
      <div className="card-surface flex items-center gap-3 rounded-2xl px-5 py-4 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Loading workspace…
      </div>
    </div>
  );
}

function Brand({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <Link
      href="/"
      {...(onNavigate ? { onClick: onNavigate } : {})}
      className="flex items-center gap-3 px-4 pt-5 pb-4"
    >
      <span className="grid size-10 place-items-center rounded-[14px] bg-primary text-primary-foreground shadow-[inset_0_1px_0_rgb(255_255_255/0.22),0_10px_20px_-12px_hsl(var(--primary)/0.8)]">
        <Activity className="size-5" />
      </span>
      <span className="min-w-0">
        <span className="block truncate text-[15px] font-semibold tracking-tight">SMM Automation</span>
        <span className="block truncate text-[11px] text-muted-foreground">Social operations</span>
      </span>
    </Link>
  );
}

function NavTree({
  entries,
  pathname,
  onNavigate,
}: {
  entries: Section[];
  pathname: string;
  onNavigate?: () => void;
}) {
  return (
    <nav className="flex-1 overflow-y-auto px-2 pb-4" aria-label="Workspace navigation">
      {entries.map((section) => (
        <div key={section.label}>
          <p className="nav-section-label">{section.label}</p>
          <div className="space-y-1">
            {section.items.map((item) => {
              const active = isActive(item.href, pathname);
              const Icon = item.icon;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  aria-current={active ? 'page' : undefined}
                  {...(onNavigate ? { onClick: onNavigate } : {})}
                  className="nav-link"
                >
                  <Icon className="size-4 shrink-0" aria-hidden="true" />
                  <span className="truncate">{item.label}</span>
                </Link>
              );
            })}
          </div>
        </div>
      ))}
    </nav>
  );
}

function SignOutButton({ onNavigate }: { onNavigate?: () => void }) {
  const router = useRouter();
  const session = useSession();
  const [busy, setBusy] = useState(false);

  const signOut = async () => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // A dead cookie is still a dead session; clear the local view either way.
    } finally {
      setBusy(false);
      onNavigate?.();
      await session.refresh();
      router.replace('/login');
    }
  };

  return (
    <button
      type="button"
      onClick={signOut}
      disabled={busy}
      data-testid="sign-out"
      className="nav-link w-full disabled:opacity-50"
    >
      {busy ? <Loader2 className="size-4 animate-spin" /> : <LogOut className="size-4" />}
      Sign out
    </button>
  );
}

function SidebarFooter({ role, onNavigate }: { role: string | null; onNavigate?: () => void }) {
  return (
    <div className="mt-auto space-y-2 border-t border-white/40 px-3 py-3 dark:border-white/10">
      <div className="flex items-center gap-2.5 rounded-[14px] bg-white/30 px-2.5 py-2 dark:bg-white/5">
        <span className="grid size-8 shrink-0 place-items-center rounded-full bg-primary/12 text-[11px] font-bold text-primary">
          {(role ?? 'OP').slice(0, 2).toUpperCase()}
        </span>
        <div className="min-w-0">
          <p className="truncate text-xs font-semibold">{role ? ROLE_LABEL[role as keyof typeof ROLE_LABEL] : 'Operator'}</p>
          <p className="truncate text-[10px] text-muted-foreground" data-testid="session-role">
            signed in
          </p>
        </div>
      </div>
      <SignOutButton {...(onNavigate ? { onNavigate } : {})} />
      <ModeToggle />
    </div>
  );
}

/** Primary destinations that stay visible on a phone, one thumb away. */
function MobileDock({
  pathname,
  onMore,
  navOpen,
}: {
  pathname: string;
  onMore: () => void;
  navOpen: boolean;
}) {
  const primary = [
    { href: '/', label: 'Home', icon: LayoutDashboard },
    { href: '/accounts', label: 'Accounts', icon: Users },
    { href: '/actions', label: 'Actions', icon: Activity },
    { href: '/scrape', label: 'Scrape', icon: Search },
  ];
  return (
    <nav className="mobile-dock" aria-label="Primary">
      {primary.map((item) => {
        const active = isActive(item.href, pathname);
        const Icon = item.icon;
        return (
          <Link key={item.href} href={item.href} aria-current={active ? 'page' : undefined}>
            <span className="grid place-items-center">
              <Icon className="size-[18px]" aria-hidden="true" />
              <span className="mobile-dock__label">{item.label}</span>
            </span>
          </Link>
        );
      })}
      <button
        type="button"
        onClick={onMore}
        aria-label="Open more navigation"
        aria-haspopup="dialog"
        aria-expanded={navOpen}
      >
        <span className="grid place-items-center">
          <Menu className="size-[18px]" aria-hidden="true" />
          <span className="mobile-dock__label">More</span>
        </span>
      </button>
    </nav>
  );
}

export function DashboardShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const session = useSession();
  const [navOpen, setNavOpen] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (session.status === 'anonymous') {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    }
  }, [session.status, pathname, router]);

  useEffect(() => {
    setNavOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!navOpen) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setNavOpen(false);
        return;
      }
      if (event.key !== 'Tab' || !panelRef.current) return;
      const focusable = Array.from(
        panelRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      );
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
    document.body.style.overflow = 'hidden';
    closeRef.current?.focus();
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.style.overflow = '';
    };
  }, [navOpen]);

  if (session.status === 'loading' || session.status === 'anonymous') {
    return <ShellSplash />;
  }

  const role = session.role;
  const { title, subtitle } = titleFor(pathname);
  const visible = NAV.map((section) => ({
    ...section,
    items: section.items.filter((item) => !item.perm || can(role, item.perm)),
  })).filter((section) => section.items.length > 0);

  return (
    <WorkerGate>
      <div className="app-shell">
      <a
        href="#main"
        className="fixed left-4 top-4 z-[100] -translate-y-24 rounded-xl bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground focus:translate-y-0"
      >
        Skip to content
      </a>

      <div className="atmosphere" aria-hidden="true">
        <div className="atmosphere__cloud atmosphere__cloud--one" />
        <div className="atmosphere__cloud atmosphere__cloud--two" />
      </div>

      <aside className="app-sidebar" aria-label="Workspace">
        <Brand />
        <NavTree entries={visible} pathname={pathname} />
        <SidebarFooter role={role} />
      </aside>

      <div className="app-main-wrap">
        <header className="app-header">
          <button
            type="button"
            className="grid size-10 shrink-0 place-items-center rounded-xl border border-border/70 bg-white/35 text-muted-foreground transition-colors hover:bg-white/60 hover:text-foreground lg:hidden dark:bg-white/5 dark:hover:bg-white/10"
            aria-label="Open navigation"
            aria-expanded={navOpen}
            onClick={() => setNavOpen(true)}
          >
            <Menu className="size-4" />
          </button>

          <div className="min-w-0 flex-1">
            <h1 className="truncate text-[19px] font-semibold tracking-[-0.025em] md:text-[22px]">{title}</h1>
            {subtitle ? <p className="mt-0.5 truncate text-xs text-muted-foreground">{subtitle}</p> : null}
          </div>

          <div className="flex shrink-0 items-center gap-2">
            <div className="hidden sm:block">
              <ModeToggle header />
            </div>
            <Button asChild size="sm" className="hidden sm:inline-flex">
              <Link href="/actions">
                <Plus />
                New action
              </Link>
            </Button>
          </div>
        </header>

        <main id="main" tabIndex={-1} className="app-content">
          <div className="page-stack">{children}</div>
        </main>
      </div>

      <MobileDock pathname={pathname} onMore={() => setNavOpen(true)} navOpen={navOpen} />

      {navOpen ? (
        <div className="fixed inset-0 z-[60] flex lg:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
          <button
            type="button"
            className="absolute inset-0 bg-[#07131c]/40 backdrop-blur-[2px]"
            aria-label="Close navigation"
            onClick={() => setNavOpen(false)}
          />
          <div ref={panelRef} className="glass-strong relative ml-auto flex h-full w-[300px] max-w-[86vw] flex-col rounded-l-[28px] border-y-0 border-r-0">
            <div className="flex items-center justify-between pr-3">
              <Brand onNavigate={() => setNavOpen(false)} />
              <button
                ref={closeRef}
                type="button"
                className="grid size-10 place-items-center rounded-xl text-muted-foreground hover:bg-white/40 hover:text-foreground dark:hover:bg-white/10"
                aria-label="Close navigation"
                onClick={() => setNavOpen(false)}
              >
                <X className="size-4" />
              </button>
            </div>
            <NavTree entries={visible} pathname={pathname} onNavigate={() => setNavOpen(false)} />
            <SidebarFooter role={role} onNavigate={() => setNavOpen(false)} />
          </div>
        </div>
      ) : null}
      </div>
    </WorkerGate>
  );
}
