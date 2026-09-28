# AUDIT PROMPT — MVP-1-SMM

> Paste this whole file into the CLI agent (Claude Code, or equivalent) that
> should audit the system. Everything below the line is the prompt.

---

You are auditing **MVP-1-SMM**, a social-media-management platform: scrape
(Apify), monitoring, and automated IG/Threads actions (Playwright workers).
Produce a **detailed audit**, not a summary. Every finding must cite
`path:line` and reproduce from the code you actually read — no guesses, no
findings from the docs alone.

Repo root is the current working directory. Read code before you conclude.

## What the system is

Monorepo, single-tenant, local colima + docker compose (K8s is the production
path only):

- `apps/api` — Go 1.26 (Echo + pgx + sqlc + golang-migrate). Port `24080`.
- `apps/web` — Next.js 15 dashboard. Port `24081`.
- `apps/worker` — Node 22 Playwright workers, provisioned per-account by the
  dashboard's docker driver (they do NOT appear in `docker compose ps`).
- `infra/` — docker + k8s manifests, runbooks, load tests.
- `docs/` — PRD, ERD, SYSTEM_DESIGN, DEVELOPMENT_RULE (the convention
  contract), SECURITY_REVIEW, SEVERITY, PROGRESS, CHANGELOG, per-platform
  briefs.

Live stack right now: `smm-api-1`, `smm-web-1`, `smm-postgres-1`,
`smm-redis-1`, `smm-minio-1`, and ~7 named `smm-worker-*` containers. The
running stack is a legitimate evidence source — query it, but **never print
secrets or credentials into your output** (tokens, keys, password hashes,
session cookies, cookie values). Refer to them by name and say where they
live, not what they are.

## How to audit

1. **Read the contract first.** `docs/DEVELOPMENT_RULE.md` is the convention
   the code is held to. `docs/SECURITY_REVIEW.md` already records a sign-off
   as of post-P4-08 — **verify it still holds**, do not inherit it.
   `docs/SEVERITY.md` defines the severity scale to use in your report.
2. **Follow the data.** Each of these paths has a documented contract; walk
   the code and check the code honors it:
   - Scrape: `POST /api/scrape/keywords` → `KeywordScrapeService.processBatch`
     → `runOnce` → `Runner.RunSearch` (Apify `run-sync-get-dataset-items`)
     → MinIO `apify/search-<runID>/<i>.json` → `IngestRun` → posts/comments.
   - On-demand scrape: `ScrapeTarget` → `Runner.Run` → ingest → `readBack`.
   - Actions: `ActionService.Enqueue` → Redis BLPOP → worker
     `core/controller.ts` → callback `POST /internal/callback` →
     `JobService.RecordAttempt` → retry policy.
   - Worker provisioning: dashboard row → docker driver → `worker` container.
3. **Check the tests actually bite.** A test that passes but cannot fail is a
   coverage lie. For each test you rely on, name what mutation would break it.
   If you cannot find one, that is itself a finding.
4. **Verify claims in the docs against code.** Several recent commits changed
   behaviour after the docs were written. Where doc and code disagree, the
   code wins and the disagreement is a finding.

## Trust boundaries (audit each, explicitly)

- **RBAC** — `apps/api/internal/http/*` route groups vs
  `internal/domain/role.go`. SECURITY_REVIEW says the `/api` floor is `read`
  and mutating routes carry `act`/`export`/`admin`. Confirm on every route,
  including the ones added since (`/actions/:jobId/retry`,
  `/actions/run-pending`, the keyword-batch endpoints). A missing gate is the
  finding that matters most.
- **SSRF** — `safeTargetURL` is a strict allowlist. Every place a URL enters
  from a request (enqueue, scrape target, image/media URLs stored from actor
  output) must go through it or have an equivalent.
- **Credential storage** — AES-GCM in `adapter/crypto`, key from
  `CREDENTIAL_KEY` only, never baked into an image. Check `.env` handling,
  the backup output under `infra/backup/`, and that nothing equivalent is
  committed. `.gitignore` was recently extended to drop backup output —
  verify the ignore actually catches what the scripts write.
- **Worker ingress** — `/internal/*` has no user auth by design (machine-only,
  outside `/api`). Confirm nothing sensitive is reachable there and that a
  forged callback cannot mark another tenant's/another account's job.
- **Apify payload drift** — a wrong field name on an actor is **SILENT**: the
  actor falls back to its own default and returns the wrong thing, not an
  error. Check the payload construction in `adapter/apify/runner.go` against
  the per-actor briefs in `docs/platforms/`, and that the tests pin the
   load-bearing fields (e.g. `includeReplies: true` is the only source of
  Threads comments).

## Areas with recent, high-churn code — scrutinize these

These shipped in the last nine commits and are where regressions are most
likely. Each has a known defect class to hunt:

- **`internal/service/scrape_ingestor.go`** — the Threads reply join. A reply
  names its parent by numeric `parent_post_id` while posts are keyed by
  shortcode, joined by an **in-memory run-scoped map**. That map is only
  correct because payloads are read in `received_at` order. Check: what breaks
   if the order assumption breaks? Is the ordering actually guaranteed
  everywhere the ingestor is called? A reply whose parent is missing now
  falls back to being a top-level post — is any caller's count now wrong?
- **`internal/service/action.go` `RunPendingNow`** — recently changed from a
  50-row cap to a paged forward sweep. Check the termination proof: the
  offset advances by rows read, a short page ends the loop, and
  `maxRunPendingRows` bounds it. Find a queue state where it loops forever,
  double-moves a row, or reports a count that does not match what it did.
- **`internal/service/scrape_keyword.go`** — `items_read` vs `posts_count`.
  They were both written from the upsert count, which hid window drops. Now
  split. Verify no remaining caller conflates them, and that the read-back
  the operator sees is consistent with the stored counters.
- **`internal/service/session_refresh.go`** + `runLogin` on the worker — the
  refresh dispatches the same `auth-login` control message the dashboard's
  login button sends. Check the quarantine state actually gates dispatch
  (not just the packable pool), and that a stuck `QUARANTINED` account can be
  recovered by an operator.
- **`apps/web/app/(dashboard)/actions/page.tsx`** — the queue header shows the
  backoff timer only when jobs are parked. Check the button's disabled state
  and every status the page can render, especially loading and error.
- **`internal/adapter/llm/client.go`** — timeout/max_tokens raised for
  reasoning models; SSE-shaped body tolerance. Check a malformed or hostile
  response cannot exhaust memory or hang a request.

## What to check on the running stack

Use the live DB and containers as evidence where it is faster than reading:

- `docker exec smm-postgres-1 psql -U smm -d smm` — DB user is `smm`.
- Check `action_job` status distribution, `keyword_batch` counters vs the
  posts actually joined to each batch, `apify_run.run_id` population (was
  empty for all rows until recently), `account.status` for quarantined rows,
  and `worker.last_heartbeat` for stale workers.
- `docker logs smm-api-1` / `smm-web-1` for errors after the last rebuild.
- Verify the API is actually serving the built code: the last rebuild
  predates some changes in this window, so a finding about a fix "not live"
   is a real finding, not noise.

Do **not** mutate production state: no `UPDATE`/`DELETE`, no worker
provisioning, no enqueueing actions, no triggering paid Apify runs.

## Output format

Write the report to `docs/AUDIT_REPORT_2026-09.md` (new file — create it).
There is an **older** audit at `docs/AUDIT_REPORT.md` (2026-09-18, container
provisioning + noVNC, by a previous auditor); do not overwrite it, but do
check whether its findings are still open and reference them by section in
your report. Give me a terminal summary. Structure:

1. **Verdict** — one paragraph: does the system do what its contracts claim,
   and would I ship it?
2. **Findings**, each with: severity (per `docs/SEVERITY.md`), `path:line`,
   what is wrong, **the failure scenario** (concrete input/state → wrong
   outcome), and the fix. Rank most severe first. If a "known issue" from
   the docs is actually fine now, say so in a sub-note — the last reviewer's
   open items may already be closed.
3. **Doc/code drift** — where the docs claim something the code no longer
   does, or vice versa. Cite both sides.
4. **Tests that cannot fail** — name them and the missing assertion.
5. **What you did not check** — honest list, so I know the audit's edge.

Be adversarial about correctness and security, but **do not invent findings
to fill the report**. A clean area reported as clean, with the checks you ran,
is more valuable than a padded list. If you did not run a check, say so
rather than implying you did.
