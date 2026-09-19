/**
 * X (Twitter) adapter. OTP-over-web is wired and real; credentialed login and
 * the like/comment/verify DOM automation are honest stubs (see `stub.ts`).
 */
import type { PlatformAdapter } from './adapter.js';
import { makeStubAdapter } from './stub.js';

export const xAdapter: PlatformAdapter = makeStubAdapter({
  platform: 'x',
  loginUrl: 'https://x.com/login',
  // Best-effort proof cookies: `auth_token` (the session) + `ct0` (the CSRF
  // token) are both present only on an authenticated X session.
  sessionCookies: ['auth_token', 'ct0'],
  // X funnels its verification code through the login challenge text input
  // (`ocfEnterTextTextInput`); the generic one-time-code hint is the fallback.
  otpFieldSelector:
    'input[data-testid="ocfEnterTextTextInput"], input[autocomplete="one-time-code"]',
});
