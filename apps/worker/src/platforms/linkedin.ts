/**
 * LinkedIn adapter. OTP-over-web is wired and real; credentialed login and the
 * like/comment/verify DOM automation are honest stubs (see `stub.ts`).
 */
import type { PlatformAdapter } from './adapter.js';
import { makeStubAdapter } from './stub.js';

export const linkedinAdapter: PlatformAdapter = makeStubAdapter({
  platform: 'linkedin',
  loginUrl: 'https://www.linkedin.com/login',
  // Best-effort proof cookie: `li_at` is LinkedIn's authenticated session token.
  sessionCookies: ['li_at'],
  // LinkedIn's PIN challenge field is `pin`; the generic one-time-code hint
  // covers the app-verification variants.
  otpFieldSelector: 'input[name="pin"], input[autocomplete="one-time-code"]',
});
