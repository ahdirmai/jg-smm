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

export const SessionContext = createContext<SessionState>({ status: 'loading' });

export function useSession(): SessionState {
  return useContext(SessionContext);
}
