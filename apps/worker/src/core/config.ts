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
    internalSecret: env.INTERNAL_API_SECRET?.trim() || '',
    ...parseProxy(env),
  };
}

/**
 * Resolve the egress proxy (P1-07's `proxy_group`, delivered per container).
 *
 * Absent `PROXY_URL` is the direct-dial case and stays the default: a local
 * stack has no proxy to route through, and demanding one would break every dev
 * setup. The credentials are separate env vars rather than userinfo inside the
 * URL so the provider's password is never escaped into a string that gets
 * logged, and so a missing half is detectable instead of silently producing a
 * malformed URL.
 *
 * A URL that fails to parse is treated as absent, with the failure reported by
 * the caller's boot log — a container that silently dials direct when it was
 * configured to route is worse than one that says so.
 */
export function parseProxy(
  env: NodeJS.ProcessEnv,
): Pick<WorkerConfig, 'proxyUrl' | 'proxyAuth'> {
  const raw = env.PROXY_URL?.trim();
  if (!raw) return { proxyUrl: null, proxyAuth: null };

  let parsed: URL;
  try {
    parsed = new URL(raw);
  } catch {
    return { proxyUrl: null, proxyAuth: null };
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:' && parsed.protocol !== 'socks5:') {
    return { proxyUrl: null, proxyAuth: null };
  }
  if (!parsed.hostname || !parsed.port) {
    return { proxyUrl: null, proxyAuth: null };
  }

  const username = env.PROXY_USERNAME?.trim() ?? '';
  const password = env.PROXY_PASSWORD ?? '';
  // A half-supplied credential is refused outright. Playwright would send the
  // username with an empty password and collect a 407 on every request, and
  // dialling direct instead is at least a working, honest fallback.
  const hasUsername = username !== '';
  const hasPassword = password !== '';
  if (hasUsername !== hasPassword) return { proxyUrl: null, proxyAuth: null };

  return {
    // The operator's own string, not `parsed.toString()`: the WHATWG URL
    // normaliser appends a trailing slash to a bare authority
    // ("http://gate.example.com:8080" -> ".../"), which is noise in the boot
    // log and in every comparison against what was configured. The URL object
    // is used only to validate.
    proxyUrl: raw,
    proxyAuth: hasUsername ? { username, password } : null,
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
