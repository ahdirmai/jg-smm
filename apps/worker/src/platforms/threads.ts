/**
 * Threads adapter (MVP). Skeleton mirroring `instagram.ts`; see that file for
 * the rationale. Selectors live in `../sel`.
 */
import type { BrowserContext } from 'playwright';

import type { ActionJob } from '../types.js';
import type { AdapterResult, LoginCredentials, LoginResultLike, PlatformAdapter } from './adapter.js';

const NOT_IMPLEMENTED = 'threads adapter not implemented yet (P0-09 skeleton)';

function stub(): never {
  throw new Error(NOT_IMPLEMENTED);
}

export const threadsAdapter: PlatformAdapter = {
  platform: 'threads',
  async login(_ctx: BrowserContext, _credentials: LoginCredentials): Promise<LoginResultLike> {
    stub();
  },
  async like(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
    stub();
  },
  async comment(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
    stub();
  },
  async verify(_ctx: BrowserContext, _job: ActionJob): Promise<AdapterResult> {
    stub();
  },
};
