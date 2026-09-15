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
};
