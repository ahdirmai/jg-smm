/**
 * Threads adapter (P3-05). Same DOM shape as Instagram (both Meta), so it
 * shares `dom.ts` and differs only in identity, login URL and its selector
 * set in `../sel`. See `instagram.ts` for the rationale per method.
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

const LOGIN_URL = 'https://www.threads.net/login';

export const threadsAdapter: PlatformAdapter = {
  platform: 'threads',
  loginUrl: LOGIN_URL,
  // Threads rides the Instagram session: a single `sessionid` proves it.
  // ds_user_id ships alongside sessionid on a real Threads login (ref:
  // jg/automation authCookies) — both prove the session, not the URL alone.
  sessionCookies: ['sessionid', 'ds_user_id'],
  // Same Meta markup as Instagram — reuse the shared OTP handlers (DRY).
  ...otpHandlers(META_OTP_SELECTOR),

  login(ctx: BrowserContext, credentials: LoginCredentials): Promise<LoginResultLike> {
    return credentialLogin(ctx, 'threads', credentials, LOGIN_URL);
  },

  like(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    return runAction(ctx, 'threads', job, likePost);
  },

  comment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'threads', job, (d) => commentOnPost(d, job.text as string));
  },

  replyComment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'threads', job, (d) => replyToComment(d, job.text as string));
  },

  // Threads' comment-like selector is not pinned yet, so likeComment reports an
  // honest "not supported" (it never falls back to liking the POST). Wired so
  // the adapter contract is complete; enable by setting `commentLikeButton` in
  // sel/index.ts once verified live against a healthy Threads account.
  likeComment(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    return runAction(ctx, 'threads', job, likeComment);
  },

  verify(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'threads', job, async (d) => {
      const rendered = await waitForCommentText(d, job.text as string);
      if (rendered) return { ok: true, renderedText: rendered };
      return failShot(d, 'comment not visible in feed (verification failed)');
    });
  },
};
