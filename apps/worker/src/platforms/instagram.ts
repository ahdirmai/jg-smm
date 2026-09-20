/**
 * Instagram adapter (P3-04). Actions are thin: the DOM steps live once in
 * `dom.ts` (shared with Threads — both are Meta markup) and the selectors in
 * `../sel`, so this file is only the platform identity + its login URL. When
 * IG changes markup, `sel/index.ts` is the single patch point (§7.3).
 *
 * No action trusts optimism: like counts only when the button state flipped,
 * a comment only when its text is visible in the feed (§7.4). A dead session
 * is reported as AUTH (never retried) by the shared wrapper.
 */
import type { BrowserContext } from 'playwright';

import type { ActionJob } from '../types.js';
import type {
  AdapterResult,
  LoginCredentials,
  LoginResultLike,
  PlatformAdapter,
} from './adapter.js';
import {
  commentOnPost,
  credentialLogin,
  likeComment,
  likePost,
  missingText,
  replyToComment,
  runAction,
  waitForCommentText,
  failShot,
} from './dom.js';
import { META_OTP_SELECTOR, otpHandlers } from './otp.js';

const LOGIN_URL = 'https://www.instagram.com/accounts/login/';

export const instagramAdapter: PlatformAdapter = {
  platform: 'instagram',
  loginUrl: LOGIN_URL,
  // `sessionid` alone is set pre-2FA; `ds_user_id` only lands once the account
  // is fully resolved, so both are required to call a login proven.
  sessionCookies: ['sessionid', 'ds_user_id'],
  // IG + Threads share Meta's 2FA/checkpoint markup (DRY: one selector, one
  // detect/submit implementation).
  ...otpHandlers(META_OTP_SELECTOR),

  login(ctx: BrowserContext, credentials: LoginCredentials): Promise<LoginResultLike> {
    return credentialLogin(ctx, 'instagram', credentials, LOGIN_URL);
  },

  like(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    return runAction(ctx, 'instagram', job, likePost);
  },

  comment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'instagram', job, (d) => commentOnPost(d, job.text as string));
  },

  replyComment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'instagram', job, (d) => replyToComment(d, job.text as string));
  },

  likeComment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    return runAction(ctx, 'instagram', job, likeComment);
  },

  verify(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'instagram', job, async (d) => {
      const rendered = await waitForCommentText(d, job.text as string);
      if (rendered) return { ok: true, renderedText: rendered };
      return failShot(d, 'comment not visible in feed (verification failed)');
    });
  },
};
