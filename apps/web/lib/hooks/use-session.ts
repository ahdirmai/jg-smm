'use client';

import { useCallback, useEffect, useState } from 'react';

import { api } from '@/lib/api';
import type { SessionState } from '@/lib/auth/session-context';

/**
 * Loads the caller's role from `/api/auth/me`. The cookie is sent
 * automatically (credentials: include), so there is nothing to store here —
 * the role is read-only in the UI and the server re-checks every write.
 *
 * The probe runs once on mount and again whenever `refresh` is called, so a
 * client-side login/logout is picked up without a hard reload. `refresh`
 * resolves once the probe has settled so a caller can navigate only after the
 * context reflects the new state — otherwise the dashboard's anonymous guard
 * fires on the stale value and bounces back to /login.
 */
export function useSessionState(): SessionState & { refresh: () => Promise<void> } {
  const [session, setSession] = useState<SessionState>({ status: 'loading' });

  const probe = useCallback(async () => {
    try {
      const me = await api.me();
      setSession({ status: 'authenticated', userId: me.userId, role: me.role });
    } catch {
      // 401/403 = no usable session.
      setSession({ status: 'anonymous' });
    }
  }, []);

  useEffect(() => {
    void probe();
  }, [probe]);

  return { ...session, refresh: probe };
}
