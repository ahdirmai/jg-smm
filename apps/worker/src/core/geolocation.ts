/**
 * Worker geolocation. The API anchors each worker to one randomized point
 * inside its city at create time and freezes it; the worker reads that point
 * once at boot and spoofs it on every browser context so Playwright reports a
 * stable position instead of the datacentre's real one.
 *
 * A worker with no location (unknown id, or a row created before geolocation)
 * gets a 404 and spoofs nothing — the browser then reports its true location.
 * That is logged, never fatal: a missing row must not stop the worker serving
 * jobs.
 */
import type { Logger } from './logger.js';

export interface Geolocation {
  latitude: number;
  longitude: number;
  location?: string;
}

export interface GeolocationReader {
  /** The frozen coordinate, or null when the worker has none. */
  get(): Promise<Geolocation | null>;
}

export interface GeolocationOptions {
  apiUrl: string;
  workerId: string;
  logger: Logger;
  /** Override for tests / DI. Defaults to global fetch. */
  fetchImpl?: typeof fetch;
}

export function createGeolocation(
  options: GeolocationOptions,
): GeolocationReader & AsyncDisposable {
  const logger = options.logger.child({ component: 'geolocation' });
  const doFetch = options.fetchImpl ?? fetch;
  let cached: Geolocation | null = null;
  let loaded = false;

  async function load(): Promise<void> {
    const url = `${options.apiUrl.replace(/\/+$/, '')}/internal/worker/${encodeURIComponent(
      options.workerId,
    )}/geolocation`;
    try {
      const res = await doFetch(url, { signal: AbortSignal.timeout(5_000) });
      if (res.status === 404) {
        logger.info('no location assigned; using the real device position', {
          workerId: options.workerId,
        });
        return;
      }
      if (!res.ok) {
        logger.warn('geolocation lookup failed', { status: res.status });
        return;
      }
      const body = (await res.json()) as Geolocation;
      if (typeof body.latitude !== 'number' || typeof body.longitude !== 'number') {
        logger.warn('geolocation body missing coordinates', { body });
        return;
      }
      const point: Geolocation = {
        latitude: body.latitude,
        longitude: body.longitude,
      };
      if (typeof body.location === 'string') {
        point.location = body.location;
      }
      cached = point;
      logger.info('anchored to frozen coordinate', {
        location: point.location,
        latitude: point.latitude,
        longitude: point.longitude,
      });
    } catch (err) {
      // Startup ordering: the API may not be reachable the instant the worker
      // boots. The heartbeat retry loop keeps the worker alive; geolocation is
      // best-effort, so this degrades to "no spoofing" instead of crashing.
      logger.warn('geolocation unavailable', { error: (err as Error).message });
    } finally {
      loaded = true;
    }
  }

  const ready = load();

  return {
    async get() {
      if (!loaded) await ready.catch(() => undefined);
      return cached;
    },
    async [Symbol.asyncDispose]() {
      await ready.catch(() => undefined);
    },
  };
}
