'use client';

import {
  Activity,
  LayoutDashboard,
  LineChart,
  MessageSquareText,
  MonitorSmartphone,
  ScrollText,
  Settings,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';

import { ModeToggle } from '@/components/mode-toggle';
import { cn } from '@/lib/utils';
import { useSession } from '@/lib/auth/session-context';
import { ROLE_LABEL, can } from '@/lib/auth/permissions';
import type { Permission } from '@/lib/auth/permissions';

type NavItem = {
  href: string;
  label: string;
  icon: typeof LayoutDashboard;
  perm?: Permission;
};

const NAV: NavItem[] = [
  { href: '/', label: 'Dashboard', icon: LayoutDashboard },
  { href: '/workers', label: 'Workers', icon: MonitorSmartphone, perm: 'act' },
  { href: '/accounts', label: 'Accounts', icon: Users, perm: 'act' },
  { href: '/actions', label: 'Actions', icon: Activity, perm: 'act' },
  { href: '/templates', label: 'Templates', icon: MessageSquareText, perm: 'act' },
  { href: '/monitoring', label: 'Monitoring', icon: LineChart },
  { href: '/audit', label: 'Audit Log', icon: ScrollText },
  { href: '/settings', label: 'Settings', icon: Settings, perm: 'admin' },
];

/**
 * Application shell: fixed sidebar + content area. Kept intentionally plain
 * (DESIGN_SYSTEM: minimalist professional) — hierarchy via spacing, not color.
 */
export function DashboardShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const session = useSession();
  const role = session.status === 'authenticated' ? session.role : undefined;

  return (
    <div className="flex min-h-screen">
      <aside className="hidden w-56 shrink-0 flex-col border-r bg-card md:flex">
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <span className="grid size-7 place-items-center rounded-md bg-primary text-primary-foreground">
            <Activity className="size-4" />
          </span>
          <span className="text-sm font-semibold tracking-tight">SMM Automation</span>
        </div>
        <nav className="flex-1 space-y-1 p-2">
          {NAV.map(({ href, label, icon: Icon, perm }) => {
            // Read-only roles still see the dashboards; write surfaces are
            // hidden so the nav cannot route to a page of blocked controls.
            if (perm && !can(role, perm)) return null;
            const active = href === '/' ? pathname === '/' : pathname.startsWith(href);
            return (
              <Link
                key={href}
                href={href}
                aria-current={active ? 'page' : undefined}
                className={cn(
                  'flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors',
                  active
                    ? 'bg-accent text-accent-foreground'
                    : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
                )}
              >
                <Icon className="size-4" />
                {label}
              </Link>
            );
          })}
        </nav>
        <div className="space-y-2 border-t p-2">
          {role ? (
            <div className="px-3 text-xs text-muted-foreground" data-testid="session-role">
              Signed in as <span className="font-medium text-foreground">{ROLE_LABEL[role]}</span>
            </div>
          ) : null}
          <ModeToggle />
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <main className="flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
