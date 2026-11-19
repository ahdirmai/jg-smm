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

// --- On-demand scrape + AI generation (hand-rolled, not in the contract) ----

// ScrapedComment mirrors domain.Comment on the wire (the scrape detail returns
// domain rows directly).
export interface ScrapedComment {
  id: string;
  postId: string;
  platform: string;
  externalId: string;
  authorHandle: string;
  text: string;
  scrapedAt: string;
  /** The comment's own permalink (…/p/<code>/c/<id>/), derived by the API.
   *  This is the targetUrl a reply must use: the API rejects a reply aimed at
   *  a bare post. Empty when the post's shortcode is unknown — then no reply
   *  can be offered for this comment. */
  url: string;
  /** The analysed stance, null when this comment has not been analysed (no LLM
   *  configured, or the call failed). Null renders as "not analysed" — the UI
   *  must never show a default verdict for a row nobody looked at. */
  sentiment: 'POSITIVE' | 'NEUTRAL' | 'NEGATIVE' | null;
  /** The model's own confidence, null when it reported none. */
  sentimentScore: number | null;
  /** The comment this replies to, when it is a reply. Resolved to the parent's
   *  id so the UI can nest it; null for a top-level comment. */
  parentId: string | null;
}

/** Per-label counts for one post's comments. `unanalysed` is kept separate from
 *  `neutral` on purpose: "we did not look" and "we looked and it is neutral"
 *  are different facts, and merging them would make a partly-analysed post read
 *  as a calm one. */
export interface SentimentBreakdown {
  positive: number;
  neutral: number;
  negative: number;
  unanalysed: number;
}

/**
 * One scraped post's ranking, computed server-side (service.RankInsights).
 *
 * A score rather than "sort by likes": a raw like count ranks a 3-day-old post
 * above a 3-hour-old one and ranks a 200k-follower account above a 2k one for
 * reasons unrelated to the content. The score combines engagement (log-scaled
 * so one viral outlier cannot flatten the list), engagement-per-follower, and a
 * freshness decay.
 */
export interface PostInsight {
  postId: string;
  externalId: string;
  platform: string;
  author: string;
  permalink: string;
  /** First line of the caption, for the ranking row. */
  caption: string;
  mediaUrl: string;
  likes: number;
  comments: number;
  shares: number;
  /** likes + comments + shares. */
  engagement: number;
  /** Engagement as a percentage of the author's followers. Null when the
   *  follower count is unknown — a rate against a missing denominator is not
   *  0%, it is no number. */
  engagementPerFollowerPct: number | null;
  ageHours: number;
  /** Ranking key, higher first. Shown so the order is explicable. */
  score: number;
  /** 1-based position within the ranked list. */
  rank: number;
}

export interface ScrapedPost {
  id: string;
  platform: string;
  externalId: string;
  authorHandle: string;
  text: string | null;
  mediaUrls: string[] | null;
  metrics: Record<string, number> | null;
  scrapedAt: string;
  /** When the post was published. Null for rows ingested before migration
   *  000016 — the date was read, used to evaluate the window, and discarded.
   *  `scrapedAt` is the fallback and is NOT the same thing. */
  publishedAt: string | null;
  /** True when a permalink scrape fetched this post's comment bodies. A
   *  keyword-batch row is false: its search actor returns no comment bodies,
   *  so the on-demand cache will not serve it as a "no comments" answer. */
  commentsFetched: boolean;
}

export interface ScrapeTargetResponse {
  post: ScrapedPost;
  comments: ScrapedComment[];
  /** Comment sentiment counts, computed on read. */
  sentiment: SentimentBreakdown;
  /** True when this was served from the 6h freshness cache instead of a paid
   *  actor run. The dashboard uses it to say "served from storage" and offer a
   *  re-scrape rather than implying a fresh fetch. */
  cached: boolean;
}

export interface GenerateCommentResponse {
  comments: string[];
}

export interface KeywordBatch {
  id: string;
  platform: string;
  keywords: string[];
  windowFrom: string | null;
  windowTo: string | null;
  maxPosts: number;
  actorId: string;
  status: 'PENDING' | 'RUNNING' | 'SUCCEEDED' | 'FAILED';
  apifyRunId: string | null;
  itemsRead: number;
  postsCount: number;
  commentsCount: number;
  error: string | null;
  createdBy: string | null;
  createdAt: string;
  finishedAt: string | null;
}

// Already-scraped history (the "recent posts" card on /actions).
export interface RecentPostsResponse {
  posts: ScrapedPost[];
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
  /**
   * Lease a pool worker and open the account's browser there.
   *
   * Returns a TICKETED noVNC URL: on boxd the live view is on a public hostname,
   * and the short-lived ticket is the only thing between that hostname and a
   * browser logged into the account.
   */
  openAccountLiveView: (id: string) =>
    request<ApiSchemas['LiveView']>(`/api/accounts/${id}/live-view`, { method: 'POST' }),
  /**
   * End the account's interactive session.
   *
   * Needs calling whenever the operator stops watching: the lease is a DATABASE
   * row, so a view that is merely hidden keeps the worker and the account
   * locked until someone closes it.
   */
  closeAccountLiveView: (id: string) =>
    request<void>(`/api/accounts/${id}/live-view`, { method: 'DELETE' }),
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
  // Already-scraped posts, newest first (the "recent" history card).
  listRecentPosts: (signal?: AbortSignal) =>
    request<RecentPostsResponse>(
      '/api/scrape/recent',
      signal ? { signal } : undefined,
    ),
  // The same posts, SCORED and ordered by insight on the server. The page only
  // renders the order it is given; re-deriving the score here would let the two
  // implementations drift.
  rankedRecent: (limit = 200, signal?: AbortSignal) => {
    const qs = new URLSearchParams({ ranked: 'true', limit: String(limit) });
    return request<{ insights: PostInsight[] }>(`/api/scrape/recent?${qs}`, signal ? { signal } : undefined);
  },
  // On-demand scrape: run the platform actor synchronously for one post URL.
  // Long request (the actor run lasts tens of seconds); the caller shows a loader.
  // force=true skips the 6h freshness cache — needed for comments, because a
  // keyword batch marks its posts fresh without ever fetching comment bodies.
  scrapeTarget: (platform: string, url: string, force = false) =>
    request<ScrapeTargetResponse>('/api/scrape/target', {
      method: 'POST',
      body: JSON.stringify({ platform, url, force }),
    }),
  // Keyword search: batch (keyword → many posts) is async 202 batch, vs post satuan (1 URL) sync.
  scrapeKeywords: (body: {
    platform: string;
    keywords: string[];
    from?: string | undefined;
    to?: string | undefined;
    maxPosts?: number | undefined;
  }) =>
    request<{ batch: KeywordBatch }>('/api/scrape/keywords', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
  listKeywordBatches: (params?: { limit?: number; offset?: number }) => {
    const qs = new URLSearchParams();
    if (params?.limit) qs.set('limit', String(params.limit));
    if (params?.offset) qs.set('offset', String(params.offset));
    const q = qs.toString() ? `?${qs}` : '';
    return request<{ batches: KeywordBatch[] }>(`/api/scrape/batches${q}`);
  },
  getKeywordBatch: (id: string) => request<{ batch: KeywordBatch }>(`/api/scrape/batches/${id}`),
  listKeywordBatchPosts: (id: string, params?: { limit?: number; offset?: number }) => {
    const qs = new URLSearchParams();
    if (params?.limit) qs.set('limit', String(params.limit));
    if (params?.offset) qs.set('offset', String(params.offset));
    const q = qs.toString() ? `?${qs}` : '';
    return request<{ posts: ScrapedPost[] }>(`/api/scrape/batches/${id}/posts${q}`);
  },
  // Comments the batch ingest already stored for one post. A read, not a
  // scrape — opening the detail dialog costs no Apify credit.
  listPostComments: (postId: string, params?: { limit?: number; offset?: number }) => {
    const qs = new URLSearchParams();
    if (params?.limit) qs.set('limit', String(params.limit));
    if (params?.offset) qs.set('offset', String(params.offset));
    const q = qs.toString() ? `?${qs}` : '';
    return request<{ comments: ScrapedComment[]; sentiment: SentimentBreakdown }>(
      `/api/scrape/posts/${postId}/comments${q}`,
    );
  },
  // AI comment candidates from a scraped post's context.
  generateComment: (body: {
    postText: string;
    authorHandle?: string;
    existingComments?: string[];
    // Set when drafting a REPLY: the comment being answered. The API then
    // writes drafts that engage with that argument, and is told NOT to emit the
    // handle (the platform tags it).
    replyTo?: { handle: string; text: string };
    mode: 'support' | 'counter';
    // How hard the draft pushes, 1 (gentle) to 5 (sharp); 0 = server default.
    intensity?: number;
    // How long each comment is; empty = medium (the server default).
    length?: 'short' | 'medium' | 'long';
    count?: number;
    language?: string;
  }) =>
    request<GenerateCommentResponse>('/api/comments/generate', {
      method: 'POST',
      body: JSON.stringify(body),
    }),
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
  // Closes every browser the pool holds (all parked logins + live views).
  // The recovery path for a worker stuck "busy" with an account no lease names.
  clearWorkerSessions: () =>
    request<{ cleared: string[]; failed: Array<{ worker: string; error: string }> }>(
      '/api/containers/clear-sessions',
      { method: 'POST' },
    ),
  listLocations: (signal?: AbortSignal) =>
    request<ApiSchemas['Location'][]>('/api/locations', signal ? { signal } : undefined),
  containerLogs: (id: string, limit = 20) =>
    request<ApiSchemas['ProvisionLogList']>(`/api/containers/${id}?limit=${limit}`),
  deleteContainer: (id: string) => request<void>(`/api/containers/${id}`, { method: 'DELETE' }),
  // Open a URL in the account's live, session-hydrated browser on the worker's
  // VNC display; the noVNC live view then shows the page already logged in.
  openBrowser: (containerId: string, url: string | null, platform: string) =>
    request<{ status: string }>(`/api/containers/${containerId}/browser`, {
      method: 'POST',
      body: JSON.stringify({ url, platform }),
    }),

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
  // Manual retry of one FAILED job. Server rejects anything else (400), so the
  // button only needs to be enabled for failed rows.
  retryAction: (jobId: string) =>
    request<ActionJob>(`/api/actions/${jobId}/retry`, { method: 'POST' }),
  // Pull the whole PENDING queue forward to now, so jobs sitting in a backoff
  // are claimed on the next worker poll. Returns how many were moved; 0 is a
  // normal answer, not an error.
  runPendingNow: () =>
    request<{ moved: number }>('/api/actions/run-pending', { method: 'POST' }),
  // Cancel one queued or running job. A terminal job is a 400 (nothing to
  // cancel), so the button only shows for PENDING/RUNNING rows. The row is
  // terminal afterwards; a RUNNING job's worker may still finish its action,
  // but its callback cannot re-grade the row.
  cancelAction: (jobId: string) =>
    request<ActionJob>(`/api/actions/${jobId}/cancel`, { method: 'POST' }),
  // Cancel the whole PENDING queue in one statement. Returns how many rows
  // moved; 0 is a normal answer, not an error.
  cancelPendingNow: () =>
    request<{ cancelled: number }>('/api/actions/cancel-pending', { method: 'POST' }),

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
