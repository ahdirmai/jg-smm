/**
 * Thin API client for the E2E suite. Mirrors apps/web/lib/api.ts but runs from
 * Node (no browser), so specs can seed and read state directly — the UI is what
 * is under test, the API is the setup/verification back door.
 *
 * Cookies are carried manually: the dashboard auth is a cookie jar, so a login
 * here is the same identity the browser fixture reuses.
 */
export class ApiClient {
  private readonly base: string;
  private cookie = '';

  constructor(base: string) {
    this.base = base;
  }

  private async req(path: string, init: RequestInit = {}): Promise<Response> {
    const res = await fetch(`${this.base}${path}`, {
      ...init,
      headers: {
        'Content-Type': 'application/json',
        ...(this.cookie ? { cookie: this.cookie } : {}),
        ...(init.headers ?? {}),
      },
    });
    const setCookie = res.headers.get('set-cookie');
    if (setCookie) {
      // Keep only the access token; the refresh cookie is same-site only and
      // not needed to drive the API from Node.
      const access = setCookie.split(',').find((c) => c.trim().startsWith('smm_at='));
      if (access) this.cookie = (access.split(';')[0] ?? '').trim();
    }
    return res;
  }

  async login(email: string, password: string): Promise<void> {
    const res = await this.req('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    });
    if (!res.ok) throw new Error(`login failed: ${res.status}`);
  }

  /** Adopt a cookie obtained elsewhere (the shared fixture login). */
  async setCookie(value: string): Promise<void> {
    this.cookie = `smm_at=${value}`;
  }

  async json<T>(path: string, init?: RequestInit): Promise<T> {
    const res = await this.req(path, init);
    if (!res.ok) {
      const body = await res.text().catch(() => '');
      throw new Error(`GET/POST ${path} -> ${res.status}: ${body.slice(0, 200)}`);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  // Fleet ---------------------------------------------------------------
  listContainers() {
    return this.json<ContainerList>('/api/containers');
  }
  createContainer(name: string, location: string, novncPort?: number) {
    return this.json<Container>('/api/containers', {
      method: 'POST',
      body: JSON.stringify({ name, region: 'ID', location, novncPort: novncPort ?? null }),
    });
  }
  deleteContainer(id: string) {
    return this.json<void>(`/api/containers/${id}`, { method: 'DELETE' });
  }
  containerLogs(id: string) {
    return this.json<ProvisionLogList>(`/api/containers/${id}?limit=20`);
  }
  listLocations() {
    return this.json<Location[]>('/api/locations');
  }

  // Accounts ------------------------------------------------------------
  listAccounts() {
    return this.json<AccountList>('/api/accounts');
  }
  createAccount(username: string, platform: string) {
    // The contract requires a password. Actions never use it — the worker runs
    // on a stored session, not this value — so a fixed placeholder keeps the
    // row packable without inventing a real credential.
    return this.json<Account>('/api/accounts', {
      method: 'POST',
      body: JSON.stringify({
        username,
        platform,
        password: 'e2e-placeholder-password',
        tags: [],
      }),
    });
  }
  setAccountStatus(id: string, status: 'ACTIVE' | 'PAUSED') {
    return this.json<Account>(`/api/accounts/${id}`, {
      method: 'POST',
      body: JSON.stringify({ status }),
    });
  }
  removeAccount(id: string) {
    return this.json<void>(`/api/accounts/${id}`, { method: 'DELETE' });
  }

  // Templates -----------------------------------------------------------
  listTemplates(platform?: string) {
    return this.json<TemplateList>(
      `/api/templates${platform ? `?platform=${platform}` : ''}`,
    );
  }
  createTemplate(platform: string, text: string) {
    return this.json<CommentTemplate>('/api/templates', {
      method: 'POST',
      body: JSON.stringify({ platform, text, weight: 1 }),
    });
  }
  deleteTemplate(id: string) {
    return this.json<void>(`/api/templates/${id}`, { method: 'DELETE' });
  }

  // Actions -------------------------------------------------------------
  listActions(status?: string) {
    return this.json<ActionJobList>(`/api/actions${status ? `?status=${status}` : ''}`);
  }
  enqueue(items: { accountId: string; targetUrl: string; actionType: string }[]) {
    return this.json<ActionJobList>('/api/actions', {
      method: 'POST',
      body: JSON.stringify({ items }),
    });
  }

  // Team / audit / reports (read-only in the suite) ---------------------
  listUsers() {
    return this.json<UserList>('/api/users');
  }
  createUser(body: {
    email: string;
    name: string;
    password: string;
    role?: string;
  }) {
    return this.json<User>('/api/users', {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }
  updateUser(id: string, body: { role?: string }) {
    return this.json<User>(`/api/users/${id}`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }
  removeUser(id: string) {
    return this.json<void>(`/api/users/${id}`, { method: 'DELETE' });
  }
  listAudit() {
    return this.json<AuditLogList>('/api/audit?limit=20');
  }
  me() {
    return this.json<MeResponse>('/api/auth/me');
  }
}

// Contract-shaped types (mirrors @smm/shared ApiSchemas; duplicated here so the
// suite has no build dependency on the generated package).
export type Container = {
  id: string;
  name: string;
  desiredState: string;
  source: string;
  region: string;
  status: string;
  generation: number;
  createdAt: string;
  novncUrl?: string | null;
  location?: string | null;
  accounts?: { id: string; platform: string; username: string; authStatus: string; status: string }[];
};
export type ContainerList = { containers: Container[] };
export type Location = { name: string; latitude: number; longitude: number; radiusKm: number };
export type Account = {
  id: string;
  username: string;
  platform: string;
  authStatus: string;
  status: string;
  workerId?: string | null;
  lastError?: string | null;
};
export type AccountList = { accounts: Account[] };
export type CommentTemplate = { id: string; platform: string; text: string };
export type TemplateList = { templates: CommentTemplate[] };
export type ActionJob = {
  id: string;
  actionType: string;
  accountId: string;
  targetId: string;
  // Resolved from the target at dispatch; null on a PENDING job the queue then
  // renders by targetId.
  targetUrl?: string | null;
  status: string;
  attempts: number;
  renderedText?: string | null;
  error?: string | null;
  errorClass?: string | null;
};
export type ActionJobList = { actions: ActionJob[] };
export type ProvisionLogEntry = {
  id: string;
  workerId: string;
  op: string;
  generation: number;
  status: string;
  ts: string;
  error?: string | null;
};
export type ProvisionLogList = { logs: ProvisionLogEntry[] };
export type AuditLog = { id: string; actorId: string; action: string; entity: string; ts: string };
export type AuditLogList = { logs: AuditLog[] };
export type User = { id: string; email: string; name: string; role: string };
export type UserList = { users: User[] };
// /api/auth/me returns only the session's role + id — no email, no user wrapper.
// Operator email is not surfaced by the session; the team roster (listUsers) is
// the source of an account's email.
export type MeResponse = { role: string; userId: string };
