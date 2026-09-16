import { MVP_PLATFORMS, PLATFORMS, type Platform } from '@smm/shared';

import type { WorkerConfig } from '../types.js';

const DEFAULT_HEARTBEAT_INTERVAL_SEC = 30;

/** Parse a comma-separated platform list, dropping unknown values. */
export function parsePlatforms(raw: string | undefined, fallback: readonly Platform[]): Platform[] {
  if (!raw?.trim()) return [...fallback];
  const known = new Set<string>(PLATFORMS);
  const parsed = raw
    .split(',')
    .map((value) => value.trim().toLowerCase())
    .filter((value): value is Platform => known.has(value));
  return parsed.length > 0 ? parsed : [...fallback];
}

/** Parse a positive integer env var, falling back when absent or invalid. */
export function parsePositiveInt(raw: string | undefined, fallback: number): number {
  const parsed = Number.parseInt(raw ?? '', 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

/** Read worker configuration from a `process.env`-like record. */
export function loadConfig(env: NodeJS.ProcessEnv = process.env): WorkerConfig {
  return {
    workerId: env.WORKER_ID?.trim() || 'worker-local',
    apiUrl: (env.API_URL?.trim() || 'http://api:8080').replace(/\/+$/, ''),
    redisUrl: env.REDIS_URL?.trim() || 'redis://redis:6379',
    platforms: parsePlatforms(env.PLATFORMS, MVP_PLATFORMS),
    dryRun: (env.ACTION_DRY_RUN ?? 'true') !== 'false',
    display: env.DISPLAY?.trim() || ':99',
    heartbeatIntervalSec: parsePositiveInt(
      env.HEARTBEAT_INTERVAL_SEC,
      DEFAULT_HEARTBEAT_INTERVAL_SEC,
    ),
    novncUrl: parseNovncUrl(env),
  };
}

/**
 * Resolve the browser-reachable noVNC URL (P4-08).
 *
 * The container always runs noVNC on NOVNC_PORT (6080), but the *browser*
 * reaches it through whatever the operator published. The worker cannot
 * discover its own published host port from inside the container, so the URL
 * is explicit config:
 *   NOVNC_URL=http://localhost:6080   (full URL, wins when set)
 *   NOVNC_BASE_URL=http://localhost     (+ NOVNC_PORT, for a shared host)
 * When neither is set the live view is not published and the value is null —
 * the dashboard then hides the modal rather than linking to a dead URL.
 */
export function parseNovncUrl(env: NodeJS.ProcessEnv): string | null {
  const full = env.NOVNC_URL?.trim();
  if (full) return full.replace(/\/+$/, '');
  const base = env.NOVNC_BASE_URL?.trim();
  if (!base) return null;
  const port = parsePositiveInt(env.NOVNC_PORT, 6080);
  return `${base.replace(/\/+$/, '')}:${port}`;
}
