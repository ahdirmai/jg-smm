/**
 * YouTube adapter (authenticates through the Google account flow). OTP-over-web
 * is wired and real; credentialed login and the like/comment/verify DOM
 * automation are honest stubs (see `stub.ts`).
 */
import type { PlatformAdapter } from './adapter.js';
import { makeStubAdapter } from './stub.js';

export const youtubeAdapter: PlatformAdapter = makeStubAdapter({
  platform: 'youtube',
  loginUrl: 'https://accounts.google.com/ServiceLogin?service=youtube',
  // Best-effort proof cookie: Google sets `__Secure-1PSID` on an authenticated
  // session (the modern successor to the bare `SID`/`SSID` pair).
  sessionCookies: ['__Secure-1PSID'],
  // Google's 2-step code field is `idvPin`; the generic one-time-code hint
  // covers the newer prompt variants.
  otpFieldSelector: 'input[name="idvPin"], input[autocomplete="one-time-code"]',
});
