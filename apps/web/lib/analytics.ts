/**
 * Analytics client for the overview that remains after Monitoring was removed
 * from the dashboard. The backend endpoints stay for API compatibility.
 */

import type { ApiSchemas } from '@smm/shared';

import { ApiError, request } from './api';

export type AnalyticsOverview = ApiSchemas['AnalyticsOverview'];

export async function analyticsOverview(windowDays = 30): Promise<AnalyticsOverview> {
  return request<AnalyticsOverview>(`/api/analytics/overview?windowDays=${windowDays}`);
}

export { ApiError };
