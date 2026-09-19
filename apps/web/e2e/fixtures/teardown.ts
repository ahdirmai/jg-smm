/**
 * Suite-scoped cleanup. Specs delete what they create in an afterEach, but a
 * test that times out or is interrupted never gets there — and the leftover
 * row makes the next run's empty-state and unique-row assertions flake on
 * state it did not create. This sweep runs after the whole suite, including on
 * failure, so a dirty database never survives a run.
 *
 * `anandashinta__` is a real hand-authenticated account, not suite state: it is
 * the only row the sweep must leave alone.
 */
import { ApiClient } from './api.js';

const API_URL = process.env.E2E_API_URL ?? 'http://localhost:24080';
const OWNER_EMAIL = process.env.E2E_OWNER_EMAIL ?? 'owner@smm.local';
const OWNER_PASSWORD = process.env.E2E_OWNER_PASSWORD ?? 'changeme-changeme';
const SUITE_PREFIX = 'e2e_';

export default async function globalTeardown(): Promise<void> {
  const api = new ApiClient(API_URL);
  try {
    await api.login(OWNER_EMAIL, OWNER_PASSWORD);
  } catch {
    // The stack is down or the login is rate-limited; nothing to sweep and
    // nothing left to do. A teardown error would mask the real failures.
    return;
  }

  const accounts = (await api.listAccounts().catch(() => ({ accounts: [] }))).accounts ?? [];
  for (const a of accounts) {
    if (a.username.startsWith(SUITE_PREFIX)) {
      await api.removeAccount(a.id).catch(() => undefined);
    }
  }

  const users = (await api.listUsers().catch(() => ({ users: [] }))).users ?? [];
  for (const u of users) {
    if (u.email.startsWith(SUITE_PREFIX)) {
      await api.removeUser(u.id).catch(() => undefined);
    }
  }

  const templates = (await api.listTemplates().catch(() => ({ templates: [] }))).templates ?? [];
  for (const t of templates) {
    if (t.text.startsWith(SUITE_PREFIX)) {
      await api.deleteTemplate(t.id).catch(() => undefined);
    }
  }
}
