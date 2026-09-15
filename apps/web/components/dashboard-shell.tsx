'use client';

import {
  Activity,
  LayoutDashboard,
  MessageSquareText,
  MonitorSmartphone,
  ScrollText,
  Settings,
  Users,
} from 'lucide-react';
import Link from 'next/link';
import { usePathname } from 'next/navigation';

import { cn } from '@/lib/utils';

const NAV = [
  { href: '/', label: 'Dashboard', icon: LayoutDashboard },
  { href: '/workers', label: 'Workers', icon: MonitorSmartphone },
  { href: '/accounts', label: 'Accounts', icon: Users },
  { href: '/actions', label: 'Actions', icon: Activity },
  { href: '/templates', label: 'Templates', icon: MessageSquareText },
  { href: '/audit', label: 'Audit Log', icon: ScrollText },
  { href: '/settings', label: 'Settings', icon: Settings },
] as const;

/**
 * Application shell: fixed sidebar + content area. Kept intentionally plain
 * (DESIGN_SYSTEM: minimalist professional) — hierarchy via spacing, not color.
 */
export function DashboardShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

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
          {NAV.map(({ href, label, icon: Icon }) => {
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
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <main className="flex-1 p-6">{children}</main>
      </div>
    </div>
  );
}
