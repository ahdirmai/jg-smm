/**
 * Worker claim (the local tier's assignment handshake).
 *
 * A container booted by `docker compose up --scale worker=N` knows only its own
 * hostname. The dashboard-created rows it is meant to serve have UUID ids, so
 * the worker cannot address its queue, its control channel or its heartbeat
 * until it learns which row is its. Claim fixes that: the worker posts its boot
 * identity, the API binds the oldest free PENDING row to it, and hands back the
 * row's real id plus the frozen GPS point the browser must spoof.
 *
 * Best-effort by design: the API may be unreachable at the instant the container
 * starts. The worker keeps its boot id and retries on the next heartbeat cycle,
 * so a slow-to-start API degrades to "unclaimed" instead of "dead".
 */
import type { Logger } from './logger.js';

/** What the worker presents about itself. */
export interface ClaimRequest {
  /** WORKER_ID=worker-<hostname> — the identity the row's container_id holds. */
  containerId: string;
  /** Redis channel the worker is subscribed to; defaults from containerId. */
  controlChannel?: string | undefined;
  /** Redis key the worker BLPOPs; defaults from containerId. */
  actionQueue?: string | undefined;
  /** Session PVC claim; defaults from containerId. */
  sessionPvc?: string | undefined;
  /** noVNC URL when published (P4-08). */
  novncUrl?: string | null | undefined;
}

/** What the worker needs back from the row it claimed. */
export interface ClaimResponse {
  /** The row's real UUID — every queue key and heartbeat uses this. */
  workerId: string;
  name: string;
  region: string;
  location?: string | undefined;
  latitude?: number | undefined;
  longitude?: number | undefined;
}

export interface ClaimOptions {
  apiUrl: string;
  logger: Logger;
  /** Override for tests / DI. Defaults to global fetch. */
  fetchImpl?: typeof fetch;
}

/**
 * Claim the row this container should serve. Returns the row's real id, or the
 * boot id unchanged when the API is unreachable or has nothing free — the
 * caller keeps operating on its boot identity so the retry path can claim
 * later, rather than crashing the container.
 */
export async function claimRow(req: ClaimRequest, options: ClaimOptions): Promise<ClaimResponse> {
  const logger = options.logger.child({ component: 'claim' });
  const doFetch = options.fetchImpl ?? fetch;
  const url = `${options.apiUrl.replace(/\/+$/, '')}/internal/claim`;

  try {
    const res = await doFetch(url, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        containerId: req.containerId,
        controlChannel: req.controlChannel,
        actionQueue: req.actionQueue,
        sessionPvc: req.sessionPvc,
        novncUrl: req.novncUrl ?? undefined,
      }),
      signal: AbortSignal.timeout(8_000),
    });

    if (res.status === 404) {
      // No free row: the fleet is already fully claimed, or the operator has
      // not created enough containers. Not fatal — the worker idles on its
      // boot id and will claim as soon as a row appears.
      logger.info('no unclaimed container row available; running unassigned', {
        containerId: req.containerId,
      });
      return toBootIdentity(req);
    }

    if (!res.ok) {
      logger.warn('claim failed', { status: res.status });
      return toBootIdentity(req);
    }

    const body = (await res.json()) as Partial<ClaimResponse>;
    if (!body.workerId) {
      logger.warn('claim response missing workerId', { body });
      return toBootIdentity(req);
    }

    logger.info('claimed container row', {
      workerId: body.workerId,
      name: body.name,
      location: body.location,
    });
    return {
      workerId: body.workerId,
      name: body.name ?? req.containerId,
      region: body.region ?? '',
      location: body.location,
      latitude: body.latitude,
      longitude: body.longitude,
    };
  } catch (err) {
    // Startup race: the API is still coming up. Retry happens on the next boot
    // attempt; nothing here is worth killing the container over.
    logger.warn('claim unavailable', { error: (err as Error).message });
    return toBootIdentity(req);
  }
}

/** Fall back to the boot id so the worker is still addressable pre-claim. */
function toBootIdentity(req: ClaimRequest): ClaimResponse {
  return {
    workerId: req.containerId,
    name: req.containerId,
    region: '',
  };
}
