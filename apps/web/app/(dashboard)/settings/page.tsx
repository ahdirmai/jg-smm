'use client';

import { KeyRound, Shield, Users } from 'lucide-react';

import { Badge, Card, CardContent } from '@smm/ui';
import { useSession } from '@/lib/auth/session-context';
import { ROLES, ROLE_LABEL } from '@/lib/auth/permissions';

const ROLE_PERMISSIONS: Record<string, string[]> = {
  OWNER: ['read', 'act', 'export', 'admin'],
  STRATEGIST: ['read'],
  OPERATOR: ['read', 'act'],
  ANALYST: ['read', 'export'],
};

/**
 * Settings page (P6-11, mirrors docs/prototype/settings.html).
 *
 * The MVP exposes the caller's own session + the permission matrix. Team CRUD
 * (create user, flip role) is an OWNER-only write surface that needs a users
 * endpoint which does not exist yet — it renders an explicit stub rather than
 * fake controls, per the P6 scope guard.
 */
export default function SettingsPage() {
  const session = useSession();

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

      <Card>
        <CardContent className="space-y-4 p-6">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Users className="size-4" />
            Team
          </div>
          <div className="rounded-lg border border-dashed p-6 text-center">
            <p className="text-sm font-medium">Team management is not wired yet</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Creating users and changing roles needs a users endpoint on the API. This stub is
              deliberate — the P6 phase is UI + wiring only, and no control here is allowed to fake
              a call it cannot make.
            </p>
            <div className="mt-3 flex justify-center gap-2">
              {ROLES.map((r) => (
                <Badge key={r} variant="outline">
                  {ROLE_LABEL[r]}
                </Badge>
              ))}
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
