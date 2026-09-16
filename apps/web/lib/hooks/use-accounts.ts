'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { subscribeStream } from '@smm/shared';

import { api, type Account } from '../api';

const SSE_URL = process.env.NEXT_PUBLIC_SSE_URL ?? 'http://localhost:24080/api/stream';

export type AccountsState = {
  accounts: Account[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  setStatus: (id: string, status: 'ACTIVE' | 'PAUSED') => Promise<void>;
  remove: (id: string) => Promise<void>;
};

export function useAccounts(): AccountsState {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const seq = useRef(0);

  const refresh = useCallback(async () => {
    const id = ++seq.current;
    try {
      const list = await api.listAccounts();
      if (id === seq.current) {
        // The list response may wrap rows or return a bare array; tolerate both
        // until the list shape is finalized.
        const rows = (list as { accounts?: Account[] })?.accounts ?? (list as unknown as Account[]);
        setAccounts(rows);
        setError(null);
      }
    } catch (err) {
      if (id === seq.current) {
        setError(err instanceof Error ? err.message : 'Failed to load accounts');
      }
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Live updates: an account-updated frame means a row changed. The frame
  // carries the full entity, but the list may have grown or shrunk (remove), so
  // refetch is the reconcile path (ADR 0010 "reconnect and refetch").
  useEffect(() => {
    return subscribeStream(SSE_URL, 'account-updated', {
      onEvent: () => void refresh(),
      onError: () => {
        // EventSource auto-reconnects; nothing to do here but let it.
      },
    });
  }, [refresh]);

  const setStatus = useCallback(
    async (id: string, status: 'ACTIVE' | 'PAUSED') => {
      // Optimistic: flip locally so the UI never shows a stale state while the
      // request is in flight; the SSE frame confirms or corrects.
      setAccounts((prev) => prev.map((a) => (a.id === id ? { ...a, status } : a)));
      try {
        await api.setAccountStatus(id, { status });
      } catch (err) {
        void refresh(); // revert; the caller surfaces the error
        throw err;
      }
    },
    [refresh],
  );

  const remove = useCallback(
    async (id: string) => {
      setAccounts((prev) => prev.filter((a) => a.id !== id));
      try {
        await api.removeAccount(id);
      } catch (err) {
        void refresh();
        throw err;
      }
    },
    [refresh],
  );

  return { accounts, loading, error, refresh, setStatus, remove };
}
