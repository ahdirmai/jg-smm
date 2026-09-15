'use client';

import { SessionContext } from '@/lib/auth/session-context';
import { useSessionState } from '@/lib/hooks/use-session';

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const session = useSessionState();
  return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}
