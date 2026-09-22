'use client';

import {
  Activity,
  ChevronDown,
  FileText,
  Gauge,
  LayoutDashboard,
  Loader2,
  LogOut,
  MessageSquareText,
  MonitorSmartphone,
  ScrollText,
  Search,
  Settings,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';

import { ModeToggle } from '@/components/mode-toggle';
import { cn } from '@/lib/utils';
import { api } from '@/lib/api';
import { useSession } from '@/lib/auth/session-context';
import { ROLE_LABEL, can } from '@/lib/auth/permissions';
import type { Permission } from '@/lib/auth/permissions';

/**
 * Navigation tree (mirrors docs/prototype/shell.js).
 *
 * Flat items first; the "Monitoring" group expands into an Overview plus one
 * page per platform — each platform has its own analytics layout.
 */
type Leaf = { href: string; label: string; icon: typeof Users; perm?: Permission };
type Group = {
  label: string;
  icon: typeof Users;
  items: { href: string; label: string }[];
};

const PLATFORMS = [
  'Instagram',
  'Threads',
  'Facebook',
  'LinkedIn',
  'X',
  'YouTube',
  'TikTok',
] as const;

const NAV: (Leaf | Group)[] = [
  { href: '/', label: 'Dashboard', icon: LayoutDashboard },
  { href: '/workers', label: 'Workers', icon: MonitorSmartphone, perm: 'act' },
  { href: '/accounts', label: 'Accounts', icon: Users, perm: 'act' },
  { href: '/actions', label: 'Actions', icon: Activity, perm: 'act' },
  { href: '/scrape', label: 'Scrape', icon: Search, perm: 'act' },
  { href: '/templates', label: 'Templates', icon: MessageSquareText, perm: 'act' },
  {
    label: 'Monitoring',
    icon: Gauge,
    items: [
      { href: '/monitoring', label: 'Overview' },
      ...PLATFORMS.map((p) => ({
        href: `/monitoring/${p.toLowerCase()}`,
        label: p,
      })),
    ],
  },
  { href: '/reports', label: 'Reports', icon: FileText },
  { href: '/audit', label: 'Audit Log', icon: ScrollText },
  { href: '/settings', label: 'Settings', icon: Settings, perm: 'admin' },
];

/**
 * Per-route header copy. The header is owned by the shell (not each page) so
 * the h-14 contract and the page title stay in sync by construction; a page
 * only contributes its own action buttons inside `main`.
 */
const TITLES: Record<string, { title: string; subtitle: string }> = {
  '/': { title: 'Dashboard', subtitle: 'Overview · last 30 days · official accounts' },
  '/workers': { title: 'Workers', subtitle: 'Containers, health, and anchored location' },
  '/accounts': { title: 'Accounts', subtitle: 'One credential set per platform per worker' },
  '/actions': { title: 'Actions', subtitle: 'Targeted like, comment, and report jobs' },
  '/scrape': { title: 'Scrape', subtitle: 'Keyword search over Instagram and Threads posts' },
  '/templates': { title: 'Templates', subtitle: 'Comment pool per platform' },
  '/monitoring': { title: 'Monitoring', subtitle: 'Official-account reach across platforms' },
  '/reports': { title: 'Reports', subtitle: 'Read-only pivots over executed work' },
  '/audit': { title: 'Audit Log', subtitle: 'Who did what, when' },
  '/settings': { title: 'Settings', subtitle: 'Team and system configuration' },
  '/login': { title: 'Sign in', subtitle: '' },
};

const DEFAULT_TITLE: { title: string; subtitle: string } = {
  title: 'SMM Automation',
  subtitle: '',
};

function titleFor(pathname: string): { title: string; subtitle: string } {
  const exact = TITLES[pathname];
  if (exact) return exact;
  // /monitoring/<platform> → the platform's analytics page.
  if (pathname.startsWith('/monitoring/')) {
    const name = pathname.split('/')[2] || '';
    const label = PLATFORMS.find((p) => p.toLowerCase() === name.toLowerCase());
    return {
      title: label ?? 'Analytics',
      subtitle: label ? `${label} · official account · reach & engagement` : '',
    };
  }
  if (pathname.startsWith('/accounts')) return TITLES['/accounts'] ?? DEFAULT_TITLE;
  if (pathname.startsWith('/workers')) return TITLES['/workers'] ?? DEFAULT_TITLE;
  return DEFAULT_TITLE;
}

function isActive(href: string, pathname: string): boolean {
  return href === '/' ? pathname === '/' : pathname.startsWith(href);
}

function NavLeaf({ item, pathname }: { item: Leaf; pathname: string }) {
  const active = isActive(item.href, pathname);
  return (
    <Link
      href={item.href}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
        active
          ? 'bg-primary/12 font-medium text-primary'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      <item.icon className="size-4" />
      {item.label}
    </Link>
  );
}

function NavGroup({ group, pathname }: { group: Group; pathname: string }) {
  const hasActive = group.items.some((i) => isActive(i.href, pathname));
  const [open, setOpen] = useState(hasActive);
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        <group.icon className="size-4" />
        {group.label}
        <ChevronDown className={cn('ml-auto size-4 transition-transform', open && 'rotate-180')} />
      </button>
      {open ? (
        <div className="mt-0.5 space-y-0.5 pl-6">
          {group.items.map((i) => {
            const active = isActive(i.href, pathname);
            return (
              <Link
                key={i.href}
                href={i.href}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'flex items-center gap-3 rounded-md px-3 py-1.5 text-[0.8125rem] transition-colors',
                  active
                    ? 'bg-primary/12 font-medium text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                )}
              >
                {i.label}
              </Link>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

/**
 * Application shell: sidebar (w-60) + header (h-14) + main (p-6).
 *
 * Layout contract (docs/prototype/shell.js): every page is a `space-y-4`
 * column inside `main` — page authors write top-level sections with no manual
 * mt-*, vertical rhythm comes from the gap.
 */
function ShellSplash() {
  return (
    <div
      className="flex min-h-screen items-center justify-center bg-background"
      data-testid="shell-splash"
    >
      <Loader2 className="size-6 animate-spin text-muted-foreground" />
    </div>
  );
}

/**
 * Sign out: clear the session server-side (the refresh cookie is what the API
 * needs to revoke), then re-probe so the context flips to anonymous and the
 * shell guard sends us to /login rather than a half-logged-out dashboard.
 */
function SignOutButton() {
  const router = useRouter();
  const session = useSession();
  const [busy, setBusy] = useState(false);

  const signOut = async () => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // Even if the network call failed the local session is stale; still
      // re-probe so the UI does not leave a dead cookie behind.
    } finally {
      setBusy(false);
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
      className="flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
    >
      {busy ? <Loader2 className="size-4 animate-spin" /> : <LogOut className="size-4" />}
      Sign out
    </button>
  );
}

export function DashboardShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const session = useSession();

  // No session = no dashboard. The API client also redirects on a mid-session
  // 401 (see lib/api.ts); this guard covers a hard navigation/refresh where
  // /api/auth/me is the only call that runs. Login keeps its `next` param so a
  // successful sign-in lands back on the page the caller wanted.
  useEffect(() => {
    if (session.status === 'anonymous') {
      router.replace(`/login?next=${encodeURIComponent(pathname)}`);
    }
  }, [session.status, pathname, router]);

  // Resolve before painting: otherwise the nav flashes for an unauthenticated
  // caller between the first render and the redirect firing.
  if (session.status === 'loading') {
    return <ShellSplash />;
  }
  if (session.status === 'anonymous') {
    return <ShellSplash />;
  }

  const role = session.role;
  const { title, subtitle } = titleFor(pathname);

  const visible = NAV.filter((entry) => {
    // Read-only roles still see the dashboards; write surfaces are hidden so
    // the nav cannot route to a page of blocked controls.
    if ('perm' in entry && entry.perm) return can(role, entry.perm);
    return true;
  });

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-60 shrink-0 flex-col border-r bg-card md:flex">
        <Link href="/" className="flex h-14 items-center gap-2 border-b px-4">
          <span className="grid size-7 place-items-center rounded-md bg-primary text-primary-foreground">
            <Activity className="size-4" />
          </span>
          <span className="text-sm font-semibold tracking-tight">SMM Automation</span>
        </Link>
        <nav className="flex-1 space-y-1 overflow-y-auto p-2">
          {visible.map((entry) =>
            'items' in entry ? (
              <NavGroup key={entry.label} group={entry} pathname={pathname} />
            ) : (
              <NavLeaf key={entry.href} item={entry} pathname={pathname} />
            ),
          )}
        </nav>
        <div className="space-y-2 border-t p-3">
          {role ? (
            <div className="flex items-center gap-2.5">
              <span className="grid size-8 shrink-0 place-items-center rounded-full bg-secondary text-xs font-semibold">
                {ROLE_LABEL[role].slice(0, 2).toUpperCase()}
              </span>
              <div className="min-w-0">
                <div className="truncate text-sm font-medium">ahdirmai</div>
                <div className="truncate text-xs text-muted-foreground" data-testid="session-role">
                  {ROLE_LABEL[role].toLowerCase()}@local.test
                </div>
              </div>
            </div>
          ) : null}
          <SignOutButton />
          <ModeToggle />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4 md:px-6">
          <div className="min-w-0">
            <h1 className="truncate text-base font-semibold tracking-tight">{title}</h1>
            {subtitle ? <p className="truncate text-xs text-muted-foreground">{subtitle}</p> : null}
          </div>
          <div className="ml-auto flex items-center gap-2">
            <div className="hidden h-9 w-56 items-center gap-2 rounded-md border bg-background px-2.5 text-sm text-muted-foreground lg:flex">
              <Search className="size-4" />
              <span>Search accounts, jobs…</span>
              <kbd className="ml-auto rounded border px-1.5 text-xs">⌘K</kbd>
            </div>
            <ModeToggle header />
          </div>
        </header>

        <main className="flex-1 p-4 md:p-6">
          <div className="space-y-4">{children}</div>
        </main>
      </div>
    </div>
  );
}
