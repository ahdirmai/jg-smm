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
  likePost,
  missingText,
  runAction,
  waitForCommentText,
  failShot,
} from './dom.js';

const LOGIN_URL = 'https://www.threads.net/login';

export const threadsAdapter: PlatformAdapter = {
  platform: 'threads',

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

  verify(ctx: BrowserContext, job: ActionJob): Promise<AdapterResult> {
    if (!job.text) return Promise.resolve(missingText());
    return runAction(ctx, 'threads', job, async (d) => {
      const rendered = await waitForCommentText(d, job.text as string);
      if (rendered) return { ok: true, renderedText: rendered };
      return failShot(d, 'comment not visible in feed (verification failed)');
    });
  },
};
