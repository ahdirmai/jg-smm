/**
 * Thin typed API client. Endpoints and types come from the generated OpenAPI
 * contract (`@smm/shared`), so the FE and BE cannot drift. Errors are mapped to
 * a single shape the components can render.
 */

import type { ApiSchemas } from '@smm/shared';

const API_URL = process.env.NEXT_PUBLIC_API_URL ?? 'http://localhost:8080';

export type Account = ApiSchemas['Account'];
export type AccountList = ApiSchemas['AccountList'];
export type CreateAccountRequest = ApiSchemas['CreateAccountRequest'];
export type SetAccountStatusRequest = ApiSchemas['SetAccountStatusRequest'];
export type Container = ApiSchemas['Container'];
export type ContainerList = ApiSchemas['ContainerList'];
export type ActionJob = ApiSchemas['ActionJob'];
export type ActionJobList = ApiSchemas['ActionJobList'];
export type EnqueueActionsRequest = ApiSchemas['EnqueueActionsRequest'];
export type ActionItem = ApiSchemas['ActionItem'];
export type JobStatus = ApiSchemas['JobStatus'];
export type CommentTemplate = ApiSchemas['CommentTemplate'];
export type TemplateList = ApiSchemas['TemplateList'];
export type CreateTemplateRequest = ApiSchemas['CreateTemplateRequest'];
export type Platform = ApiSchemas['Platform'];

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(`${API_URL}${path}`, {
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    ...init,
  });

  if (!resp.ok) {
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

export const api = {
  listAccounts: (signal?: AbortSignal) =>
    request<AccountList>('/api/accounts', signal ? { signal } : undefined),
  createAccount: (body: CreateAccountRequest) =>
    request<Account>('/api/accounts', { method: 'POST', body: JSON.stringify(body) }),
  setAccountStatus: (id: string, body: SetAccountStatusRequest) =>
    request<Account>(`/api/accounts/${id}`, { method: 'POST', body: JSON.stringify(body) }),
  removeAccount: (id: string) => request<void>(`/api/accounts/${id}`, { method: 'DELETE' }),

  listContainers: (signal?: AbortSignal) =>
    request<ContainerList>('/api/containers', signal ? { signal } : undefined),
  createContainer: (name: string, region: string) =>
    request<Container>('/api/containers', {
      method: 'POST',
      body: JSON.stringify({ name, region }),
    }),
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
};
