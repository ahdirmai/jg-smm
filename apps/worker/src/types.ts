/** Shared worker types. Kept dependency-free so every layer can import them. */
import type { Platform } from '@smm/shared';

/** Action job as published by the BE onto `queue:action:<workerId>`. */
export interface ActionJob {
  /** BE-owned job id; echoed back on every callback. */
  id: string;
  /** The account (inside this container) the job belongs to. */
  accountId: string;
  platform: Platform;
  /** e.g. `comment`, `like`, `report`, `reply_comment`. */
  action: string;
  /** Target resource (post/comment) URL. The BE has already SSRF-guarded it. */
  targetUrl: string;
  /** Rendered text for comment/reply jobs, already composed from a template. */
  text?: string;
  /** 1-based attempt counter used for idempotency and backoff. */
  attempt: number;
}

/** Control-channel instruction (private to one container). */
export interface ControlMessage {
  /** e.g. `auth-login`, `auth-input`, `auth-clear`. */
  type: string;
  accountId: string;
  platform: Platform;
  /** Free-form payload, validated by the control dispatcher. */
  payload?: Record<string, unknown>;
}

/** Verdict posted back to the BE via `/internal/action-callback`. */
export interface ActionResult {
  jobId: string;
  accountId: string;
  workerId: string;
  attempt: number;
  /** `success` | `failed` | `running`. */
  status: 'running' | 'success' | 'failed';
  /** Rendered text seen on the feed, when applicable. */
  renderedText?: string;
  /** Deterministic screenshot basename, never a path or URL. */
  screenshot?: string;
  /** Human-readable failure reason (truncated by the BE). */
  error?: string;
  durationMs?: number;
}

/** Runtime configuration, parsed once at boot from the environment. */
export interface WorkerConfig {
  workerId: string;
  apiUrl: string;
  redisUrl: string;
  platforms: readonly Platform[];
  dryRun: boolean;
  display: string;
  /** Seconds between heartbeats. */
  heartbeatIntervalSec: number;
  /**
   * Browser-reachable URL of this worker's noVNC view (P4-08), or null when
   * the live view is not published. Reported in the heartbeat so the dashboard
   * can open a LiveBrowserModal without guessing the host.
   */
  novncUrl: string | null;
}
