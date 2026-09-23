'use client';

import {
  Activity,
  ChevronDown,
  FileText,
  Gauge,
  LayoutDashboard,
  Loader2,
  LogOut,
  Menu,
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
import { Button } from '@smm/ui';
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
  '/accounts': { title: 'Accounts', subtitle: 'Click a row: live view during login, profile once authenticated' },
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

/**
 * The nav tree, rendered by both the desktop sidebar and the mobile drawer —
 * one implementation, so a nav change lands in both places.
 */
function NavTree({
  entries,
  pathname,
  onNavigate,
}: {
  entries: (Leaf | Group)[];
  pathname: string;
  onNavigate?: () => void;
}) {
  return (
    <nav className="flex-1 space-y-1 overflow-y-auto p-2">
      {entries.map((entry) =>
        'items' in entry ? (
          <NavGroup
            key={entry.label}
            group={entry}
            pathname={pathname}
            {...(onNavigate ? { onNavigate } : {})}
          />
        ) : (
          <NavLeaf
            key={entry.href}
            item={entry}
            pathname={pathname}
            {...(onNavigate ? { onNavigate } : {})}
          />
        ),
      )}
    </nav>
  );
}

function NavLeaf({
  item,
  pathname,
  onNavigate,
}: {
  item: Leaf;
  pathname: string;
  onNavigate?: () => void;
}) {
  const active = isActive(item.href, pathname);
  return (
    <Link
      href={item.href}
      aria-current={active ? 'page' : undefined}
      {...(onNavigate ? { onClick: onNavigate } : {})}
      className={cn(
        'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
        active
          ? // Homies Lab principle 5: active/selected is solid ink (black),
            // never gray — it is the "second accent".
            'bg-foreground font-medium text-background'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      <item.icon className="size-4" />
      {item.label}
    </Link>
  );
}

function NavGroup({
  group,
  pathname,
  onNavigate,
}: {
  group: Group;
  pathname: string;
  onNavigate?: () => void;
}) {
  const hasActive = group.items.some((i) => isActive(i.href, pathname));
  const [open, setOpen] = useState(hasActive);
  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
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
                {...(onNavigate ? { onClick: onNavigate } : {})}
                className={cn(
                  'flex items-center gap-3 rounded-lg px-3 py-1.5 text-[0.8125rem] transition-colors',
                  active
                    ? 'bg-foreground font-medium text-background'
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
      className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
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
  // Mobile nav drawer. Declared with the other hooks — the session early
  // returns below would otherwise make this a conditional hook call.
  const [navOpen, setNavOpen] = useState(false);

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

  // Mobile nav drawer. The desktop sidebar is hidden below md, so without this
  // a phone has no navigation at all.
  const sidebarFooter = (
    <div className="space-y-2 border-t border-border/60 p-3">
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
  );

  const brand = (
    <Link
      href="/"
      onClick={() => setNavOpen(false)}
      className="flex h-14 items-center gap-2.5 border-b border-border/60 px-4"
    >
      <span className="grid size-7 place-items-center rounded-lg bg-primary text-primary-foreground">
        <Activity className="size-4" />
      </span>
      <span className="text-sm font-semibold tracking-tight">SMM Automation</span>
    </Link>
  );

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-60 shrink-0 flex-col border-r border-border/60 bg-card md:flex">
        {brand}
        <NavTree entries={visible} pathname={pathname} />
        {sidebarFooter}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-3 border-b border-border/60 bg-card/40 px-4 backdrop-blur-sm md:px-6">
          {/* Below md the sidebar is gone; this button is the only way to reach
           * another page on a phone. */}
          <Button
            type="button"
            variant="outline"
            size="icon"
            className="md:hidden"
            aria-label="Open navigation"
            aria-expanded={navOpen}
            onClick={() => setNavOpen(true)}
          >
            <Menu className="size-4" />
          </Button>
          <div className="min-w-0">
            <h1 className="truncate text-base font-semibold tracking-tight">{title}</h1>
            {subtitle ? <p className="truncate text-xs text-muted-foreground">{subtitle}</p> : null}
          </div>
          <div className="ml-auto flex items-center gap-2">
            <ModeToggle header />
          </div>
        </header>

        <main className="flex-1 p-4 md:p-6">
          <div className="space-y-4">{children}</div>
        </main>
      </div>

      {/* Mobile drawer: a fixed overlay, not a component dependency — the shell
       * stays dependency-light and the drawer closes itself on navigation
       * (NavTree's onNavigate) and on Escape. */}
      {navOpen ? (
        <div className="fixed inset-0 z-50 flex md:hidden" role="dialog" aria-label="Navigation">
          <button
            type="button"
            aria-label="Close navigation"
            className="absolute inset-0 bg-foreground/40"
            onClick={() => setNavOpen(false)}
          />
          <div className="relative flex w-72 max-w-[85vw] flex-col bg-card shadow-popover">
            {brand}
            <NavTree
              entries={visible}
              pathname={pathname}
              onNavigate={() => setNavOpen(false)}
            />
            {sidebarFooter}
          </div>
        </div>
      ) : null}
    </div>
  );
}
