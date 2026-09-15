/**
 * Instagram adapter (MVP). Skeleton: the DOM selectors, login flow and actions
 * land in the P3 Instagram tickets, but the interface is fixed now so the
 * controller wiring is real. Selectors live in `../sel` (single patch point).
 */
import type { BrowserContext } from 'playwright';

import type { ActionJob } from '../types.js';
import type { AdapterResult, LoginCredentials, LoginResultLike, PlatformAdapter } from './adapter.js';

const NOT_IMPLEMENTED = 'instagram adapter not implemented yet (P0-09 skeleton)';

function stub(): never {
  throw new Error(NOT_IMPLEMENTED);
}

export const instagramAdapter: PlatformAdapter = {
  platform: 'instagram',
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
