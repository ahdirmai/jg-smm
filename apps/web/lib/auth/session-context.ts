'use client';

import { createContext, useContext } from 'react';

import type { Role } from '@/lib/auth/permissions';

export type SessionUser = {
  userId: string;
  role: Role;
};

export type SessionState =
  | { status: 'loading' }
  | { status: 'anonymous' }
  | (SessionUser & { status: 'authenticated' });

/**
 * The session probe lives in the root layout, so it runs exactly once per
 * full page load. That is not enough after a client-side state change: a
 * successful login leaves the cached context `anonymous`, and the dashboard
 * guard would bounce the freshly-authenticated caller straight back to
 * /login. `refresh` re-probes so login and logout are reflected immediately
 * without a hard reload.
 */
export type SessionContextValue = SessionState & {
  refresh: () => Promise<void>;
};

export const SessionContext = createContext<SessionContextValue>({
  status: 'loading',
  refresh: () => Promise.resolve(),
});

export function useSession(): SessionContextValue {
  return useContext(SessionContext);
}
