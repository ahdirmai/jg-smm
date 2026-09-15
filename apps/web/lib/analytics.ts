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

export async function analyticsRefresh(): Promise<AnalyticsIngestRun> {
  return request<AnalyticsIngestRun>('/api/analytics/refresh', { method: 'POST' });
}

export { ApiError };
