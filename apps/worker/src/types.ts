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
  /**
   * The comment_template behind `text`, echoed from the BE. The BE stores it on
   * the attempt so the 7-day per-target dedupe can see the variant as used;
   * absent for an operator-supplied body.
   */
  templateId?: string;
  /**
   * The handle a reply must tag, without the leading @. Only set for
   * `reply_comment`: IG pre-fills a mention when a comment's reply box opens,
   * and the worker verifies it names THIS person before posting. Without it any
   * `@mention` passes — including the post author's, which a top-level composer
   * pre-fills — and the reply tags the wrong user.
   */
  expectedMention?: string;
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
  attempt: number;
  /** `success` | `failed` | `running`. */
  status: 'running' | 'success' | 'failed';
  /** Rendered text seen on the feed, when applicable. */
  renderedText?: string;
  /** The template the BE composed `text` from; echoed back, never set here. */
  templateId?: string;
  /** Deterministic screenshot basename, never a path or URL. */
  screenshotPath?: string;
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
  /**
   * Shared secret for the API's `/internal/*` machine routes. Sent on every
   * call there. Empty when the API runs without `INTERNAL_API_SECRET` (the
   * single-host dev case); the API skips the check in that case too.
   */
  internalSecret: string;
}
