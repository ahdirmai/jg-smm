'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { subscribeStream, type ApiSchemas } from '@smm/shared';

import { api, type Container } from '../api';

export type Location = ApiSchemas['Location'];

const SSE_URL = process.env.NEXT_PUBLIC_SSE_URL ?? 'http://localhost:24080/api/stream';

export type ContainersState = {
  containers: Container[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  create: (name: string, region: string, location: string) => Promise<void>;
  locations: Location[];
  remove: (id: string) => Promise<void>;
};

/**
 * The fleet (P4-01). A container is a desired-state row; its account rows are
 * the packing result. SSE worker-health + account frames both reconcile it:
 * a container card changes when an account lands or a heartbeat flips status.
 */
export function useContainers(): ContainersState {
  const [containers, setContainers] = useState<Container[]>([]);
  const [locations, setLocations] = useState<Location[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const seq = useRef(0);

  const refresh = useCallback(async () => {
    const id = ++seq.current;
    try {
      const list = await api.listContainers();
      if (id === seq.current) {
        setContainers(list.containers ?? []);
        setError(null);
      }
    } catch (err) {
      if (id === seq.current) {
        setError(err instanceof Error ? err.message : 'Failed to load containers');
      }
    } finally {
      if (id === seq.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    // The create-form dropdown renders this list; it is a domain constant on
    // the API, so one fetch at mount is enough.
    void api
      .listLocations()
      .then(setLocations)
      .catch(() => undefined);
  }, [refresh]);

  // Both frames can change a card: worker-health flips the container status,
  // account-updated changes its packed rows. Refetch is the reconcile path
  // (ADR 0010) — a frame carries one entity, not the whole packing.
  useEffect(() => {
    const offHealth = subscribeStream(SSE_URL, 'worker-health', {
      onEvent: () => void refresh(),
      onError: () => {},
    });
    const offAccount = subscribeStream(SSE_URL, 'account-updated', {
      onEvent: () => void refresh(),
      onError: () => {},
    });
    return () => {
      offHealth();
      offAccount();
    };
  }, [refresh]);

  const create = useCallback(
    async (name: string, region: string, location: string) => {
      await api.createContainer(name, region, location);
      await refresh();
    },
    [refresh],
  );

  const remove = useCallback(
    async (id: string) => {
      setContainers((prev) => prev.filter((c) => c.id !== id));
      try {
        await api.deleteContainer(id);
      } catch (err) {
        void refresh();
        throw err;
      }
    },
    [refresh],
  );

  return { containers, locations, loading, error, refresh, create, remove };
}
