import { DashboardShell } from '@/components/dashboard-shell';

/**
 * Every route in this group renders inside the dashboard shell (sidebar +
 * h-14 header + main). Standalone routes — login — live outside the group and
 * get their own chrome.
 */
export default function DashboardGroupLayout({ children }: { children: React.ReactNode }) {
  return <DashboardShell>{children}</DashboardShell>;
}
