'use client';

import { useCallback, useEffect, useRef, useState } from 'react';

import { subscribeStream, type ApiSchemas } from '@smm/shared';

import { api, ApiError, type Container } from '../api';

export type Location = ApiSchemas['Location'];

const SSE_URL = process.env.NEXT_PUBLIC_SSE_URL ?? 'http://localhost:24080/api/stream';

// A flaky network (proxies, unstable wifi) can kill the POST mid-flight: the
// browser aborts the fetch, the server logs `context canceled`, and the row
// may or may not have landed. Retrying turns that into a hiccup instead of a
// dead click. The retry is safe because create is idempotent on the name — a
// repeat that hits the conflict means the first one got through, so the
// existing row is fetched and returned as if it were the create response.
const CREATE_RETRY = { attempts: 3, delayMs: 400 };

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function createContainerWithRetry(
  name: string,
  region: string,
  location: string,
  novncPort?: number,
): Promise<Container | undefined> {
  let lastErr: unknown;
  for (let attempt = 1; attempt <= CREATE_RETRY.attempts; attempt += 1) {
    try {
      return await api.createContainer(name, region, location, novncPort);
    } catch (err) {
      lastErr = err;
      // 409 = the name is already taken by our own earlier attempt; the create
      // succeeded, so resolve the row that got through instead of failing.
      if (err instanceof ApiError && err.status === 409) {
        const list = await api.listContainers();
        return list.containers?.find((c) => c.name === name);
      }
      // A 4xx other than conflict is a real validation error — retrying cannot
      // fix it, so let it surface immediately.
      if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
        throw err;
      }
      // Network drop / abort / 5xx: back off and try again.
      if (attempt < CREATE_RETRY.attempts) await sleep(CREATE_RETRY.delayMs * attempt);
    }
  }
  throw lastErr;
}

export type ContainersState = {
  containers: Container[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
  create: (name: string, region: string, location: string, novncPort?: number) => Promise<void>;
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
  // A frame carries the full entity (ADR 0010), so the reconcile is a pure
  // upsert/delete on local state — no refetch. This is what makes the create
  // flow feel live: the API publishes provision-updated the moment the row
  // lands, and worker-health flips PENDING → READY as soon as the container
  // heartbeats.
  const upsertFrame = useCallback((frame: unknown) => {
    const c = frame as Partial<Container> & { id?: string; removed?: boolean };
    if (!c || typeof c !== 'object' || !c.id) return;
    if (c.removed) {
      setContainers((prev) => prev.filter((x) => x.id !== c.id));
      return;
    }
    setContainers((prev) => {
      const i = prev.findIndex((x) => x.id === c.id);
      if (i === -1) return [...prev, c as Container];
      const next = [...prev];
      next[i] = { ...next[i], ...c } as Container;
      return next;
    });
  }, []);

  useEffect(() => {
    const offHealth = subscribeStream(SSE_URL, 'worker-health', {
      onEvent: ({ data }) => upsertFrame(data),
      onError: () => {},
    });
    const offProvision = subscribeStream(SSE_URL, 'provision-updated', {
      onEvent: ({ data }) => upsertFrame(data),
      onError: () => {},
    });
    const offAccount = subscribeStream(SSE_URL, 'account-updated', {
      onEvent: () => void refresh(),
      onError: () => {},
    });
    return () => {
      offHealth();
      offProvision();
      offAccount();
    };
  }, [refresh, upsertFrame]);

  const create = useCallback(
    async (name: string, region: string, location: string, novncPort?: number) => {
      // Fire-and-forget: the POST is a fast DB insert that returns the row, and
      // the card is inserted the moment it lands. The reconcile (refresh) is
      // kicked off but never awaited — the button unblocks immediately and the
      // provision-updated / worker-health frames converge the card in the
      // background. This is the whole point of the SSE path: the create itself
      // is background work, the UI only registers the intent.
      const created = await createContainerWithRetry(name, region, location, novncPort);
      if (created) setContainers((prev) => [...prev, created]);
      void refresh();
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
