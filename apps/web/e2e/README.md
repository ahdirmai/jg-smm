# E2E test suite (QA)

Playwright end-to-end tests for the SMM dashboard. The suite runs against the
live compose stack — not a Playwright-spawned server — because the workers spec
asserts on real provisioning: a container created from the web must appear in
`docker ps` under a name an operator can recognise.

## Preconditions

1. `make up` (or `docker compose up -d`) — the full stack: api, web, postgres,
   redis, minio.
2. `make seed` — creates the owner the fixtures log in as.
3. A docker daemon reachable by the API (local tier mounts the socket).
4. Playwright browsers installed once:

   ```sh
   cd apps/web && pnpm exec playwright install chromium
   ```

## Running

```sh
cd apps/web

# whole suite
pnpm exec playwright test --config e2e/playwright.config.ts

# one spec
pnpm exec playwright test --config e2e/playwright.config.ts e2e/specs/workers.spec.ts

# one test by name
pnpm exec playwright test --config e2e/playwright.config.ts -g "create from the web"

# headed, for a failing test
pnpm exec playwright test --config e2e/playwright.config.ts --headed

# trace for a failure
pnpm exec playwright show-trace test-results/<spec>-chromium/trace.zip
```

The `--config` flag is required: the config lives in `e2e/`, not the package
root, so a bare `playwright test` resolves no `baseURL` and every navigation
fails with "Cannot navigate to invalid URL".

## Configuration

| Env var | Default | Purpose |
| --- | --- | --- |
| `E2E_WEB_URL` | `http://localhost:24081` | Where the browser points |
| `E2E_API_URL` | `http://localhost:24080` | Node-side API client + cookie domain |
| `E2E_OWNER_EMAIL` | `owner@smm.local` | The seeded owner |
| `E2E_OWNER_PASSWORD` | `changeme-changeme` | The seeded owner's password |

## Fixtures

- `api` — a Node-side `ApiClient`, logged in once per run. Specs seed state
  (create a container, enqueue a job) and read it back without going through
  the UI.
- `authedPage` — a browser context holding the owner's `smm_at` cookie. The
  cookie comes from one shared login, not one per test: the API caps login at
  10 attempts per source IP per minute, so a per-test login rate-limits the
  suite partway through.
- Specs that test the login page use the plain `page` fixture instead — they
  need an anonymous browser.
- `fixtures/teardown.ts` — the suite-level cleanup backstop (see Conventions).
  It logs in separately from the shared fixture: a teardown must still sweep
  when the fixture login is the thing that failed.

`page.goto` is patched to `waitUntil: 'domcontentloaded'` in `fixtures/base.ts`.
Every dashboard page holds the SSE stream open for its lifetime, so the `load`
event never fires and the default `waitUntil` hangs until timeout.

## Coverage

| Spec | Area | What it proves |
| --- | --- | --- |
| `auth.spec.ts` | Login | Owner session bootstrap, bad-password inline error, empty-submit guard, unauthenticated redirect, `next` open-redirect sanitisation, logged-in /login hides the form |
| `workers.spec.ts` | Fleet | The create form renders the city list; **Add container lands a container in `docker ps` as `smm-worker-<slug>-<id>` and the card flips READY**; live-view disabled until noVNC publishes; provisioning log panel; delete removes row and container; delete refused while accounts packed; city grouping |
| `accounts.spec.ts` | Accounts | Empty state, new-account route, row renders with auth badge, pause/resume flips the badge, bulk select counts only visible rows, status filter, row removal |
| `actions.spec.ts` | Queue | Empty state, enqueue refuses without account/URL, like batch enqueues and renders, over-cap refusal with the count, status filter narrows and reports the count, job card opens the detail dialog |
| `templates.spec.ts` | Comment pool | Empty state, create/edit/delete variant, empty-body refusal, platform filter |
| `monitoring.spec.ts` | Monitoring + shell | KPI strip and its empty state, sync round trip settles, platform subroute, every nav entry routes to its page, header title/subtitle |
| `reports.spec.ts` | Reports | Filters, empty actions report, targets report, CSV export endpoint, analytics metric picker |
| `settings.spec.ts` | Settings | Session card and owner permission set, team roster, invite/create/remove member round trip, role change shows Save and reverts, the not-wired panels say so instead of faking controls, theme switch writes `dark` on `<html>` |
| `audit.spec.ts` | Audit trail | Filter surface renders, an API write appears as a row with its outcome, the text filter narrows to exactly the match, the action filter is populated from real actions, refresh keeps the client filter |
| `dead-links.spec.ts` | Dead links | Every internal `<a href>` on every dashboard page answers 200, and every monitoring platform subroute the sidebar generates renders the shell |

Docker assertions (`workers.spec.ts`) are skipped when no daemon is reachable,
so the rest of the suite still runs in CI without a socket.

## Known gaps

- **Worker actions on a live Instagram post are not covered here.** Liking and
  commenting need a manually-authenticated IG session in the worker's noVNC
  view; that is a human step, not something a spec can perform. Verify it by
  hand after re-authenticating: enqueue `action_like` then `action_comment` on
  the target post and confirm both reach `SUCCESS` with `renderedText`.
- **`ACTION_DRY_RUN`**: when the API runs with it set, workers skip the real
  action. The suite asserts the enqueue and queue rendering, not that IG state
  changed. Check the API's env before claiming a live success.
- **noVNC login flow** (`Accounts → Log in → complete IG login in the live
  view`): the modal opens and the worker parks at the login page, but finishing
  it is manual. Specs assert the affordance, not the outcome.
- **Reports values** come from the analytics provider; the suite asserts the
  filter surface and empty states, not metric numbers.

## Failure triage

1. **`Cannot navigate to invalid URL`** — the `--config` flag is missing.
2. **`fixture login failed: 429`** — too many logins in the last minute. Wait
   60s, or check whether a spec added a new login path. The suite logs in once.
3. **A create/delete hangs with the button stuck on `Creating…`** — the SSE
   stream saturated the browser's per-origin connection limit. One shared
   `EventSource` (`packages/shared/src/stream.ts`) is the fix; a regression
   here means a hook opened its own connection again.
4. **`Target page, context or browser has been closed`** — the page navigated
   mid-assertion. Most often a form submitted before React hydrated, or a 401
   triggering the client's redirect-to-login.
5. **`resolved to N elements` (strict mode violation)** — leftover `e2e_*` rows
   from a run that died before its `afterEach`. The same row renders twice and
   every `getByRole` match becomes ambiguous. The global teardown sweeps them;
   if a run was killed hard (`kill -9`), it does not, so re-run the sweep:
   `psql` `DELETE FROM account WHERE username LIKE 'e2e%'`.
6. **`docker ps` assertions skipped** — no daemon on the API's socket.

## Conventions

- Locators are role-based (`getByRole`, `getByLabel`) and scoped with
  `.filter({ hasText })` on `getByRole('row')` — no CSS selectors, no XPath.
- No `waitForTimeout`. Assertions are web-first; state settled through the API
  is polled with `expect.poll`.
- Every spec cleans up what it creates (`afterEach`), because several specs
  assert on empty states and a leftover row would make the next run flake.
  `fixtures/teardown.ts` is the backstop: a suite-level sweep of every `e2e_*`
  row that runs after the whole suite, including on failure. A `kill -9` skips
  it — see triage #5. It deliberately leaves `anandashinta__` alone; that is a
  hand-authenticated account, not suite state.
- The fleet is a shared resource: `workers: 1`, `fullyParallel: false`. Parallel
  container creates race the reconciler.
