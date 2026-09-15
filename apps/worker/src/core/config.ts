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
  };
}
