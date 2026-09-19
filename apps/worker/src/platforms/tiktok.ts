/**
 * TikTok adapter. OTP-over-web is wired and real; credentialed login and the
 * like/comment/verify DOM automation are honest stubs (see `stub.ts`).
 */
import type { PlatformAdapter } from './adapter.js';
import { makeStubAdapter } from './stub.js';

export const tiktokAdapter: PlatformAdapter = makeStubAdapter({
  platform: 'tiktok',
  loginUrl: 'https://www.tiktok.com/login',
  // Best-effort proof cookies: `sessionid` + `sid_tt` are both set on an
  // authenticated TikTok web session.
  sessionCookies: ['sessionid', 'sid_tt'],
  // TikTok's verification code field is `code`; the generic one-time-code hint
  // is the fallback.
  otpFieldSelector: 'input[name="code"], input[autocomplete="one-time-code"]',
});
