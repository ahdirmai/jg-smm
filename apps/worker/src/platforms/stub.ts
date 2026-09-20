/**
 * Honest stub factory for platforms whose anti-bot UI automation is NOT yet
 * built (facebook, linkedin, x, youtube, tiktok).
 *
 * What IS real on these adapters: the OTP-over-web surface. Each still carries a
 * correct `loginUrl`, its best-known proof `sessionCookies`, and a live 2FA
 * field selector wired through the shared `otpHandlers`. So the operator headful
 * login (open URL in noVNC → type credentials → answer the 2FA prompt) works
 * uniformly across all seven platforms, and `pollLoginOutcome` verifies the
 * session by the same cookie standard as Instagram/Threads.
 *
 * What is NOT real: the credentialed `login` and the `like`/`comment`/`verify`
 * DOM steps. Those need per-platform selectors and anti-bot handling that this
 * milestone does not deliver, so they return a structured failure and log a
 * `not_implemented` line — never a fabricated success (DEVELOPMENT_RULE §7.4).
 */
import type { BrowserContext } from 'playwright';

import type { Platform } from '@smm/shared';

import { createLogger } from '../core/logger.js';
import type { ActionJob } from '../types.js';
import type {
  AdapterResult,
  LoginCredentials,
  LoginResultLike,
  PlatformAdapter,
} from './adapter.js';
import { otpHandlers } from './otp.js';

const log = createLogger({ component: 'adapter-stub' });

export interface StubConfig {
  platform: Platform;
  /** The platform's real credential login URL (opened headful by the operator). */
  loginUrl: string;
  /** Best-known proof cookies for a verified session on this platform. */
  sessionCookies: string[];
  /** Best-known 2FA / checkpoint code field selector. */
  otpFieldSelector: string;
}

/** A login the worker cannot drive programmatically yet. Honest `failed`. */
function notImplementedLogin(platform: Platform): LoginResultLike {
  log.warn('adapter login not implemented (out of scope for this milestone)', {
    platform,
    status: 'not_implemented',
  });
  return { outcome: 'failed' };
}

/** An action whose DOM automation is not built. Honest structured failure. */
function notImplementedAction(platform: Platform, action: string): AdapterResult {
  log.warn('adapter action not implemented (out of scope for this milestone)', {
    platform,
    action,
    status: 'not_implemented',
  });
  return {
    ok: false,
    error: `not_implemented: ${platform} ${action} automation is not built (out of scope)`,
  };
}

/**
 * Build a full `PlatformAdapter` with a real OTP surface and honest not-built
 * action methods. Each of the five new platform files is a one-liner over this,
 * so the OTP contract is proven identically for all of them (DRY).
 */
export function makeStubAdapter(config: StubConfig): PlatformAdapter {
  const { platform } = config;
  return {
    platform,
    loginUrl: config.loginUrl,
    sessionCookies: config.sessionCookies,
    ...otpHandlers(config.otpFieldSelector),

    login(_ctx: BrowserContext, _credentials: LoginCredentials): Promise<LoginResultLike> {
      return Promise.resolve(notImplementedLogin(platform));
    },
    like(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
      return Promise.resolve(notImplementedAction(platform, 'like'));
    },
    comment(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
      return Promise.resolve(notImplementedAction(platform, 'comment'));
    },
    replyComment(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
      return Promise.resolve(notImplementedAction(platform, 'reply_comment'));
    },
    likeComment(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
      return Promise.resolve(notImplementedAction(platform, 'like_comment'));
    },
    verify(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
      return Promise.resolve(notImplementedAction(platform, 'verify'));
    },
  };
}
