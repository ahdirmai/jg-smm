'use client';

import { useCallback, useEffect, useState } from 'react';

import { ApiError, api } from '@/lib/api';
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
    } catch (err) {
      // Only an explicit 401/403 means "no session". This used to catch
      // everything, so one dropped packet, one 502 during an API blip, or one
      // aborted request during a hot reload reported the operator as anonymous
      // and threw them back to /login with the page they were working on
      // discarded. No `error` variant is added on purpose: the state union is
      // consumed as authenticated / loading / else, so a fourth member would
      // silently change what every `else` branch means. Leaving the previous
      // value in place is the whole fix — a `loading` session never trips the
      // anonymous guard, so the shell keeps rendering, and the next probe
      // (a later refresh, or any request that re-auths) settles it.
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
        setSession({ status: 'anonymous' });
      }
    }
  }, []);

  useEffect(() => {
    void probe();
  }, [probe]);

  return { ...session, refresh: probe };
}
