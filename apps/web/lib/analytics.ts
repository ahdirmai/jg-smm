/**
 * Official-account (monitored) + analytics client (P2-13 / P2-14).
 *
 * Types come from the generated OpenAPI contract so the FE and BE cannot drift.
 * An official account is a read-only analytics subject — never a worker account.
 */

import type { ApiSchemas } from '@smm/shared';

import { ApiError, request } from './api';

export type OfficialAccount = ApiSchemas['OfficialAccount'];
export type OfficialAccountList = ApiSchemas['OfficialAccountList'];
export type CreateOfficialAccountRequest = ApiSchemas['CreateOfficialAccountRequest'];
export type AnalyticsOverview = ApiSchemas['AnalyticsOverview'];
export type PlatformAnalytics = ApiSchemas['PlatformAnalytics'];
export type ContentMetric = ApiSchemas['ContentMetric'];
export type ContentMetricsResponse = ApiSchemas['ContentMetricsResponse'];
export type AnalyticsIngestRun = ApiSchemas['AnalyticsIngestRun'];

export async function listOfficialAccounts(platform?: string): Promise<OfficialAccountList> {
  const qs = platform ? `?platform=${encodeURIComponent(platform)}` : '';
  return request<OfficialAccountList>(`/api/official-accounts${qs}`);
}

export async function createOfficialAccount(body: CreateOfficialAccountRequest): Promise<OfficialAccount> {
  return request<OfficialAccount>('/api/official-accounts', { method: 'POST', body: JSON.stringify(body) });
}

export async function archiveOfficialAccount(id: string): Promise<OfficialAccount> {
  return request<OfficialAccount>(`/api/official-accounts/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function analyticsOverview(windowDays = 30): Promise<AnalyticsOverview> {
  return request<AnalyticsOverview>(`/api/analytics/overview?windowDays=${windowDays}`);
}

export async function analyticsByPlatform(
  platform: string,
  metric = 'followers',
  windowDays = 30,
): Promise<PlatformAnalytics> {
  const qs = new URLSearchParams({ metric, windowDays: String(windowDays) });
  return request<PlatformAnalytics>(`/api/analytics/${encodeURIComponent(platform)}?${qs}`);
}

/**
 * Per-post performance. `accountId` narrows to one monitored account; omit it
 * for the whole platform.
 */
export async function analyticsContent(
  platform: string,
  // `| undefined` on accountId is deliberate: the workspace sets
  // exactOptionalPropertyTypes, and a caller filtering "all accounts" holds a
  // possibly-undefined id, not an absent key.
  opts: { accountId?: string | undefined; windowDays?: number; limit?: number } = {},
): Promise<ContentMetricsResponse> {
  const qs = new URLSearchParams({ windowDays: String(opts.windowDays ?? 30) });
  // The query is built key by key rather than by assigning undefined: the
  // workspace sets exactOptionalPropertyTypes, where an explicit undefined is
  // not interchangeable with an absent key.
  if (opts.accountId != null) qs.set('accountId', opts.accountId);
  if (opts.limit != null) qs.set('limit', String(opts.limit));
  return request<ContentMetricsResponse>(`/api/analytics/${encodeURIComponent(platform)}/content?${qs}`);
}

export async function analyticsRefresh(): Promise<AnalyticsIngestRun> {
  return request<AnalyticsIngestRun>('/api/analytics/refresh', { method: 'POST' });
}

export { ApiError };
