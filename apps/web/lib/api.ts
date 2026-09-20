/**
 * Thin typed API client. Endpoints and types come from the generated OpenAPI
 * contract (`@smm/shared`), so the FE and BE cannot drift. Errors are mapped to
 * a single shape the components can render.
 */

import type { ApiSchemas } from '@smm/shared';

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:24080';

export type Account = ApiSchemas['Account'];
export type AccountList = ApiSchemas['AccountList'];
export type CreateAccountRequest = ApiSchemas['CreateAccountRequest'];
export type SetAccountStatusRequest = ApiSchemas['SetAccountStatusRequest'];
export type Container = ApiSchemas['Container'];
export type ContainerList = ApiSchemas['ContainerList'];
export type ActionJob = ApiSchemas['ActionJob'];
export type LogEntry = ApiSchemas['ProvisionLogEntry'];
export type ActionJobList = ApiSchemas['ActionJobList'];
export type EnqueueActionsRequest = ApiSchemas['EnqueueActionsRequest'];
export type ActionItem = ApiSchemas['ActionItem'];
export type JobStatus = ApiSchemas['JobStatus'];
export type CommentTemplate = ApiSchemas['CommentTemplate'];
export type TemplateList = ApiSchemas['TemplateList'];
export type CreateTemplateRequest = ApiSchemas['CreateTemplateRequest'];
export type Platform = ApiSchemas['Platform'];
export type ImportResult = ApiSchemas['ImportResult'];
export type MeResponse = ApiSchemas['MeResponse'];
export type ActionReport = ApiSchemas['ActionReport'];
export type ActionReportRow = ApiSchemas['ActionReportRow'];
export type TargetReport = ApiSchemas['TargetReport'];
export type TargetReportRow = ApiSchemas['TargetReportRow'];
export type AnalyticsReport = ApiSchemas['AnalyticsReport'];
export type TrendPoint = ApiSchemas['TrendPoint'];
export type AuditLog = ApiSchemas['AuditLog'];
export type AuditLogList = ApiSchemas['AuditLogList'];
export type User = ApiSchemas['User'];
export type UserList = ApiSchemas['UserList'];
export type CreateUserRequest = ApiSchemas['CreateUserRequest'];
export type UpdateUserRequest = ApiSchemas['UpdateUserRequest'];
export type Role = ApiSchemas['Role'];

export type ReportQuery = {
  kind?: 'actions' | 'targets' | 'analytics';
  format?: 'csv' | 'json';
  from?: string;
  to?: string;
  platform?: Platform;
  accountId?: string;
  metric?: string;
};

export type AuditQuery = {
  actorId?: string;
  action?: string;
  entity?: string;
  from?: string;
  to?: string;
  limit?: number;
  offset?: number;
};

function auditQuery(params: AuditQuery): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue;
    search.set(key, String(value));
  }
  const qs = search.toString();
  return qs ? `?${qs}` : '';
}

function reportQuery(params: ReportQuery): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === '') continue;
    search.set(key, String(value));
  }
  const qs = search.toString();
  return qs ? `?${qs}` : '';
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// A single in-flight redirect guard so an expired session mid-session (the
// token dies while the dashboard is open) lands on /login once, not once per
// concurrent request. Every API call funnels through `request`, so this is the
// only place the 401 needs handling — pages keep throwing ApiError as before.
let redirectingToLogin = false;

function redirectToLogin(): void {
  if (redirectingToLogin || typeof window === 'undefined') return;
  // Already there: the login page is under the root SessionProvider, so it also
  // probes /api/auth/me and gets a 401 — redirecting would loop on itself.
  if (window.location.pathname.startsWith('/login')) return;
  redirectingToLogin = true;
  const next = window.location.pathname + window.location.search;
  window.location.assign(`/login?next=${encodeURIComponent(next)}`);
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(`${API_URL}${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });

  if (!resp.ok) {
    // 401 = no usable session. Navigate to the login page rather than letting
    // every page surface this as a "Failed to fetch". The login form is a raw
    // fetch (not this client), so there is no redirect loop on a bad password —
    // that comes back 401 too and is rendered inline as a form error.
    if (resp.status === 401) redirectToLogin();

    // The error body is `{ error: { code, message } }` per the OpenAPI contract.
    let message = resp.statusText;
    try {
      const body = await resp.json();
      message = body?.error?.message ?? body?.error?.code ?? message;
    } catch {
      // Non-JSON error (proxy 502 etc.); fall back to status text.
    }
    throw new ApiError(resp.status, message);
  }

  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}

// RegionGroup is the hand-rolled shape of GET /api/accounts/by-region (not in
// the OpenAPI contract): accounts grouped by their worker's region ("wilayah"),
// each account the same DTO the list endpoint returns.
export interface RegionGroup {
  region: string;
  accounts: Account[];
}
export interface AccountsByRegionResponse {
  regions: RegionGroup[];
}

export const api = {
  listAccounts: (signal?: AbortSignal) =>
    request<AccountList>('/api/accounts', signal ? { signal } : undefined),
  listAccountsByRegion: (signal?: AbortSignal) =>
    request<AccountsByRegionResponse>('/api/accounts/by-region', signal ? { signal } : undefined),
  createAccount: (body: CreateAccountRequest) =>
    request<Account>('/api/accounts', { method: 'POST', body: JSON.stringify(body) }),
  setAccountStatus: (id: string, body: SetAccountStatusRequest) =>
    request<Account>(`/api/accounts/${id}`, { method: 'POST', body: JSON.stringify(body) }),
  removeAccount: (id: string) => request<void>(`/api/accounts/${id}`, { method: 'DELETE' }),
  // Operator headful login (P1-11 / P1-12): the credential is typed in the
  // worker's noVNC view, never here.
  startAccountLogin: (id: string) =>
    request<Account>(`/api/accounts/${id}/login`, { method: 'POST' }),
  submitAccountInput: (id: string, value: string) =>
    request<Account>(`/api/accounts/${id}/input`, {
      method: 'POST',
      body: JSON.stringify({ value }),
    }),

  // Session (cookie) export/import (P-C). Cookies are CREDENTIALS: these are
  // owner/admin-only on the server, and the exported session is handed straight
  // to the operator (returned here as the raw storageState JSON), never stored.
  exportAccountSession: (id: string) =>
    request<unknown>(`/api/accounts/${id}/session/export`, { method: 'POST' }),
  importAccountSession: (id: string, session: unknown) =>
    request<void>(`/api/accounts/${id}/session/import`, {
      method: 'POST',
      body: JSON.stringify(session),
    }),

  // Bulk import (P4-07). Per-row failures come back in the body with a 200;
  // only call-level preconditions (empty, over cap) or the rate limit error.
  importAccounts: (rows: CreateAccountRequest[]) =>
    request<ImportResult>('/api/accounts/import', {
      method: 'POST',
      body: JSON.stringify({ rows }),
    }),

  listContainers: (signal?: AbortSignal) =>
    request<ContainerList>('/api/containers', signal ? { signal } : undefined),
  createContainer: (
    name: string,
    region: string,
    location: string,
    novncPort?: number,
  ) =>
    request<Container>('/api/containers', {
      method: 'POST',
      body: JSON.stringify({ name, region, location, novncPort: novncPort ?? null }),
    }),
  listLocations: (signal?: AbortSignal) =>
    request<ApiSchemas['Location'][]>('/api/locations', signal ? { signal } : undefined),
  containerLogs: (id: string, limit = 20) =>
    request<ApiSchemas['ProvisionLogList']>(`/api/containers/${id}?limit=${limit}`),
  deleteContainer: (id: string) => request<void>(`/api/containers/${id}`, { method: 'DELETE' }),

  // The queue (P3-13). Enqueue is intent only: account + permalink + type.
  // Comment text is composed server-side from templates, never sent here.
  listActions: (status?: JobStatus, signal?: AbortSignal) =>
    request<ActionJobList>(
      `/api/actions${status ? `?status=${status}` : ''}`,
      signal ? { signal } : undefined,
    ),
  enqueueActions: (items: ActionItem[]) =>
    request<ActionJobList>('/api/actions', {
      method: 'POST',
      body: JSON.stringify({ items }),
    }),

  // Comment templates (P3-02/P3-03). The pool per platform.
  listTemplates: (platform?: Platform, signal?: AbortSignal) =>
    request<TemplateList>(
      `/api/templates${platform ? `?platform=${platform}` : ''}`,
      signal ? { signal } : undefined,
    ),
  createTemplate: (body: CreateTemplateRequest) =>
    request<CommentTemplate>('/api/templates', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  updateTemplate: (id: string, body: CreateTemplateRequest) =>
    request<CommentTemplate>(`/api/templates/${id}`, {
      method: 'PUT',
      body: JSON.stringify(body),
    }),
  deleteTemplate: (id: string) => request<void>(`/api/templates/${id}`, { method: 'DELETE' }),

  // Session (P4-09). The role drives UI gating; every write is still checked
  // server-side, so this is a UX layer, not a security boundary.
  me: () => request<MeResponse>('/api/auth/me'),
  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),

  // Reports (P4-04 / P4-05). Read-only pivots; export is a separate
  // permission (`export`) and returns a file, so it is a window location, not
  // a JSON fetch.
  reportActions: (params: ReportQuery, signal?: AbortSignal) =>
    request<ActionReport>(
      `/api/reports/actions${reportQuery(params)}`,
      signal ? { signal } : undefined,
    ),
  reportTargets: (params: ReportQuery, signal?: AbortSignal) =>
    request<TargetReport>(
      `/api/reports/targets${reportQuery(params)}`,
      signal ? { signal } : undefined,
    ),
  reportAnalytics: (params: ReportQuery, signal?: AbortSignal) =>
    request<AnalyticsReport>(
      `/api/reports/analytics${reportQuery(params)}`,
      signal ? { signal } : undefined,
    ),
  reportExportURL: (params: ReportQuery) => `${API_URL}/api/reports/export${reportQuery(params)}`,

  // Audit trail (P6-10). Who did what, when — read-only, filtered.
  listAudit: (params: AuditQuery, signal?: AbortSignal) =>
    request<AuditLogList>(`/api/audit${auditQuery(params)}`, signal ? { signal } : undefined),

  // Team (P6-11). Single-team MVP roster; writes are OWNER-only.
  listUsers: (signal?: AbortSignal) =>
    request<UserList>('/api/users', signal ? { signal } : undefined),
  createUser: (body: CreateUserRequest) =>
    request<User>('/api/users', { method: 'POST', body: JSON.stringify(body) }),
  updateUser: (id: string, body: UpdateUserRequest) =>
    request<User>(`/api/users/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  removeUser: (id: string) => request<void>(`/api/users/${id}`, { method: 'DELETE' }),
};
