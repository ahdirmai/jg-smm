/**
 * Facebook adapter. OTP-over-web is wired and real; credentialed login and the
 * like/comment/verify DOM automation are honest stubs (see `stub.ts`).
 */
import type { PlatformAdapter } from './adapter.js';
import { makeStubAdapter } from './stub.js';

export const facebookAdapter: PlatformAdapter = makeStubAdapter({
  platform: 'facebook',
  loginUrl: 'https://www.facebook.com/login',
  // Best-effort proof cookies: `c_user` (the account id) + `xs` (the session
  // secret) are both set only on a fully-authenticated Facebook session.
  sessionCookies: ['c_user', 'xs'],
  // Facebook's 2FA code field is `approvals_code`; fall back to the generic
  // one-time-code autocomplete hint for the newer flows.
  otpFieldSelector: 'input[name="approvals_code"], input[autocomplete="one-time-code"]',
});
