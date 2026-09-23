'use client';

import {
  KeyRound,
  Moon,
  Palette,
  Shield,
  SlidersHorizontal,
  Sun,
  TriangleAlert,
  Users,
  Wifi,
} from 'lucide-react';
import { useState } from 'react';
import { useTheme } from 'next-themes';

import { Badge, Button, Card, CardContent } from '@smm/ui';
import { useSession } from '@/lib/auth/session-context';
import { ROLES, ROLE_LABEL } from '@/lib/auth/permissions';
import { TeamPanel } from './team-panel';

const ROLE_PERMISSIONS: Record<string, string[]> = {
  OWNER: ['read', 'act', 'export', 'admin'],
  STRATEGIST: ['read'],
  OPERATOR: ['read', 'act'],
  ANALYST: ['read', 'export'],
};

type Tab = 'team' | 'proxy' | 'limits' | 'appearance' | 'danger';

const TABS: { id: Tab; label: string; icon: typeof Users; destructive?: boolean }[] = [
  { id: 'team', label: 'Team', icon: Users },
  { id: 'proxy', label: 'Proxy groups', icon: Wifi },
  { id: 'limits', label: 'Limits', icon: SlidersHorizontal },
  { id: 'appearance', label: 'Appearance', icon: Palette },
  { id: 'danger', label: 'Danger zone', icon: TriangleAlert, destructive: true },
];

/**
 * Settings page (P6-11, mirrors docs/prototype/settings.html).
 *
 * The MVP backend exposes the session + permission matrix only. Panels whose
 * write surface has no endpoint (team CRUD, proxy groups, rate limits, danger
 * ops) render an explicit "not wired" state rather than controls that fake a
 * call — per the P6 scope guard: no fake data, no dead buttons that imply a
 * round trip. Appearance is the exception: theme is a client concern and is
 * wired for real through next-themes.
 */
export default function SettingsPage() {
  const session = useSession();
  const { theme, setTheme } = useTheme();
  const [tab, setTab] = useState<Tab>('team');

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Card>
          <CardContent className="space-y-3 p-6">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <KeyRound className="size-4" />
              Your session
            </div>
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Status</dt>
                <dd>
                  {session.status === 'authenticated' ? (
                    <Badge variant="success">authenticated</Badge>
                  ) : session.status === 'loading' ? (
                    <Badge variant="secondary">loading</Badge>
                  ) : (
                    <Badge variant="outline">anonymous</Badge>
                  )}
                </dd>
              </div>
              {session.status === 'authenticated' ? (
                <>
                  <div className="flex justify-between gap-4">
                    <dt className="text-muted-foreground">Role</dt>
                    <dd className="font-medium">{ROLE_LABEL[session.role]}</dd>
                  </div>
                  <div className="flex justify-between gap-4">
                    <dt className="text-muted-foreground">User ID</dt>
                    <dd className="font-mono text-xs">{session.userId}</dd>
                  </div>
                </>
              ) : null}
            </dl>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="space-y-3 p-6">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <Shield className="size-4" />
              Permissions
            </div>
            {session.status === 'authenticated' ? (
              <div className="flex flex-wrap gap-2">
                {(ROLE_PERMISSIONS[session.role] ?? []).map((p) => (
                  <Badge key={p} variant="info">
                    {p}
                  </Badge>
                ))}
              </div>
            ) : (
              <p className="text-xs text-muted-foreground">Sign in to see your permissions.</p>
            )}
            <p className="text-xs text-muted-foreground">
              The server re-checks every write; these gates are UX only, never a security boundary.
            </p>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[200px_1fr]">
        <nav className="flex gap-1 lg:flex-col" aria-label="Settings sections">
          {TABS.map((t) => {
            const Icon = t.icon;
            return (
              <Button
                key={t.id}
                variant={tab === t.id ? 'secondary' : 'ghost'}
                className={`justify-start ${tab === t.id ? 'bg-foreground text-background hover:bg-foreground hover:text-background' : ''} ${t.destructive ? 'text-destructive' : ''}`}
                onClick={() => setTab(t.id)}
              >
                <Icon className="size-4" />
                {t.label}
              </Button>
            );
          })}
        </nav>

        {tab === 'team' ? (
          <TeamPanel admin={session.status === 'authenticated' && session.role === 'OWNER'} />
        ) : null}

        {tab === 'proxy' ? (
          <NotWired
            icon={Wifi}
            title="Proxy groups"
            what="Workers run through the container's own egress. Per-group proxy assignment has no endpoint yet."
          />
        ) : null}

        {tab === 'limits' ? (
          <NotWired
            icon={SlidersHorizontal}
            title="Rate limits"
            what="Rate, jitter, and cooldown gates are enforced by the scheduler config, not a writable API surface yet."
          />
        ) : null}

        {tab === 'appearance' ? (
          <Card>
            <CardContent className="space-y-4 p-6">
              <div className="flex items-center gap-2 text-sm font-semibold">
                <Palette className="size-4" />
                Appearance
              </div>
              <div className="space-y-2">
                <span className="text-sm text-muted-foreground">Theme</span>
                <div className="flex gap-2">
                  {(
                    [
                      ['light', 'Light', Sun],
                      ['dark', 'Dark', Moon],
                      ['system', 'System', null],
                    ] as const
                  ).map(([value, label, Icon]) => (
                    <Button
                      key={value}
                      variant={theme === value ? 'secondary' : 'outline'}
                      onClick={() => setTheme(value)}
                    >
                      {Icon ? <Icon className="size-4" /> : null}
                      {label}
                    </Button>
                  ))}
                </div>
                <p className="text-xs text-muted-foreground">
                  Stored locally; the whole dashboard follows this choice, including the login
                  screen.
                </p>
              </div>
              <div className="space-y-2">
                <span className="text-sm text-muted-foreground">Density</span>
                <div className="flex gap-2">
                  <Button variant="outline" disabled title="Single density in the MVP">
                    Comfortable
                  </Button>
                  <Button variant="outline" disabled title="Single density in the MVP">
                    Compact
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">One density in the MVP.</p>
              </div>
            </CardContent>
          </Card>
        ) : null}

        {tab === 'danger' ? (
          <NotWired
            icon={TriangleAlert}
            title="Danger zone"
            destructive
            what="Kill workers, purge the audit log, and delete the team are irreversible writes with no endpoint yet. They stay inert until the API can actually enforce them."
          />
        ) : null}
      </div>
    </div>
  );
}

/** A panel whose write surface does not exist on the API yet. */
function NotWired({
  icon: Icon,
  title,
  what,
  destructive,
  roles,
}: {
  icon: typeof Users;
  title: string;
  what: string;
  destructive?: boolean;
  roles?: boolean;
}) {
  return (
    <Card className={destructive ? 'border-destructive/40' : undefined}>
      <CardContent className="space-y-4 p-6">
        <div
          className={`flex items-center gap-2 text-sm font-semibold ${destructive ? 'text-destructive' : ''}`}
        >
          <Icon className="size-4" />
          {title}
        </div>
        <div className="rounded-lg border border-dashed p-6 text-center">
          <p className="text-sm font-medium">Not wired in the MVP</p>
          <p className="mt-1 text-xs text-muted-foreground">{what}</p>
          {roles ? (
            <div className="mt-3 flex justify-center gap-2">
              {ROLES.map((r) => (
                <Badge key={r} variant="outline">
                  {ROLE_LABEL[r]}
                </Badge>
              ))}
            </div>
          ) : null}
        </div>
      </CardContent>
    </Card>
  );
}
