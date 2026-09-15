'use client';

import { useEffect, useState } from 'react';

import { api } from '@/lib/api';
import type { SessionState } from '@/lib/auth/session-context';

/**
 * Loads the caller's role once from `/api/auth/me`. The cookie is sent
 * automatically (credentials: include), so there is nothing to store here —
 * the role is read-only in the UI and the server re-checks every write.
 */
export function useSessionState(): SessionState {
  const [session, setSession] = useState<SessionState>({ status: 'loading' });

  useEffect(() => {
    let active = true;
    api
      .me()
      .then((me) => {
        if (!active) return;
        setSession({ status: 'authenticated', userId: me.userId, role: me.role });
      })
      .catch(() => {
        // 401/403 = no usable session; the shell still renders read-only.
        if (active) setSession({ status: 'anonymous' });
      });
    return () => {
      active = false;
    };
  }, []);

  return session;
}
