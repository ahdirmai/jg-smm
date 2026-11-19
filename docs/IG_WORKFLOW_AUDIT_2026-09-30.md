# Instagram Workflow Audit — 2026-09-30

Scope: the five Instagram workflow slices end to end.

1. **Scrape Batch** — `POST /api/scrape/keywords` → `KeywordScrapeService.processBatch` → `Runner.RunSearch` → MinIO → `IngestRun`
2. **Scrape Target** — `POST /api/scrape/target` (post URL) → `Runner.Run` → ingest → `readBack`
3. **Scrape Target Comment** — no separate endpoint; `POST /api/scrape/target` with `force:true` is the paid comment fetch. `GET /api/scrape/posts/:postId/comments` is a pure read.
4. **Action Comment** — `POST /api/actions` → Redis → worker Playwright → `POST /internal/action-callback`
5. **Action Reply Comment** — same chain, `action_reply_comment`

Every finding below cites a `path:line` read during this audit. Nothing is carried over from `omp_audit_report/AUDIT_REPORT_2026-09.md` unless re-verified here.

---

## 1. Verdict

**Do not ship.** The Instagram action path (slices 4 and 5) cannot be trusted to report what it did: one unauthenticated HTTP call from the host can mark any job `SUCCESS` (proven live below), the only ground-truth signal for a reply is "the composer went away", and the reply implementation never parses the comment link it was given — it replies to whichever comment owns the first Reply button. The evidence pipeline is broken in a way nobody noticed: the worker sends `screenshot`, the API reads `screenshotPath`, so **every action screenshot is silently discarded**, and that typo currently masks a second bug (`.jpg` vs `.png`) which, once unmasked, rejects 100% of action verdicts.

The scrape path (slices 1–3) is structurally sound — RBAC is correct, the Apify Instagram payloads are pinned by real tests, upserts are idempotent — but it has one credential leak on the error path, a date-window bug that silently deletes the last day of every batch, and a counter (`items_read`) that is provably identical to `posts_count` while a test defends the conflation.

RBAC deserves credit: every `/api/actions*` and `/api/scrape*` route carries the right gate, including the recently added `run-pending` and `:jobId/retry`. The problem is that **none of those gates are tested** — no test in `apps/api/internal/http` ever constructs `NewActionHandler` or `NewScrapeHandler`.

---

## 2. Findings

### CRITICAL

#### C-1 — The Apify API token is returned to the client in `error.code`
`apps/api/internal/adapter/apify/runner.go:615-616` · `apps/api/internal/http/scrape.go:281` · `apps/api/internal/http/errors.go:61-66`

```go
// runner.go:615
url := fmt.Sprintf("%s/acts/%s/run-sync-get-dataset-items?token=%s&maxItems=%d", r.baseURL, pathSegment(actorID), r.token, maxItems)
```

The token travels in the query string. `scrapeError`'s default branch wraps it as `echo.NewHTTPError(502, err.Error())`, and `errorHandler` copies a string `he.Message` straight into the response's `code` field. A `net/http` transport failure returns `*url.Error`, whose `Error()` embeds the full request URL.

**Verified, not inferred.** Executed against the pinned toolchain:

```
Get "https://api.apify.com/v2/acts/apify~instagram-post-scraper/run-sync-get-dataset-items?token=apify_api_<REDACTED>&maxItems=100": context deadline exceeded
contains token? true
```

**Failure.** Any IG scrape whose Apify run exceeds the runner's default 5-minute client timeout (`main.go` never sets `apifyadapter.Config.Timeout`) returns to the operator:

```json
{"error":{"code":"scrape: run: apify: run request: Post \"…token=apify_api_<REDACTED>…\"…","message":"Bad Gateway"}}
```

Same on DNS failure, connection refused, TLS error. **Fix:** `scrapeError`'s default becomes `echo.NewHTTPError(http.StatusBadGateway, "scrape failed")`. The detail is already persisted in `scrape_job.error` and `apify_run`. One line, and it also stops the raw Apify body and `scrape: actor %s: %s` from being echoed back.

#### C-2 — Unauthenticated `/internal/*` on `0.0.0.0`, and the job transitions have no status guard
`apps/api/internal/http/internal.go:66-73` · `apps/api/internal/http/router.go:144-146` · `compose.yaml:192` · `apps/api/db/queries/action.sql:51-59, 40-49`

The internal group is mounted on the root echo behind `limitBody` only — no secret, no HMAC, no source-IP check, no worker identity. `compose.yaml:192` publishes the API as `${API_PORT:-24080}:8080`, so it is host-reachable, not container-internal.

**Proven live.** From this machine, with no session and no token:

```
POST http://localhost:24080/internal/action-callback
{"attemptId":"1413e6da-…-4c18f8847de2:1","status":"SUCCESS"}
→ {"accepted":true}   HTTP 200
```

The damage is possible because the SQL is unguarded: `RescheduleActionJob` is `UPDATE action_job SET status='PENDING', scheduled_at=$2, finished_at=NULL WHERE id=$1` — it will happily overwrite `SUCCESS`. `CompleteActionJob` likewise has no `status` predicate.

**Failure.** `GET /api/actions` hands every job UUID to any `read`-only role (ANALYST, STRATEGIST — `http/action.go:38` has no per-handler gate). For the job that posted a comment successfully:

- `{"attemptId":"<uuid>:1","status":"FAILED","error":"timeout exceeded"}` → `ClassifyActionError` matches `"timeout"` (`domain/classify.go:40`) → `TRANSIENT` → `Retryable()` → `RescheduleActionJob` flips `SUCCESS` → `PENDING`. The scheduler re-dispatches and the worker **posts the same comment a second time** on a live Instagram account.
- `{"error":"your account has been suspended"}` → matches `domain/classify.go:25` → `BANNED` → terminal → `applyHealth` writes `acc.Status = AccountDead` (`service/health_score_wire.go:53-54`). A fully forged, unauthenticated kill of a healthy executor account.

**Fix.** Shared secret on the `/internal` group (`subtle.ConstantTimeCompare` in one middleware), injected into the worker env and sent as a header from `callback.ts`, `auth-callback.ts`, `claim.ts`, `geolocation.ts`. Independently: `AND status = 'PENDING'` on `rescheduleActionJob` and `AND status IN ('PENDING','RUNNING')` on `completeActionJob`. A terminal job must not be re-openable by any caller.

> **Disclosure.** The live probe above wrote to a live row (`action_log.rendered_text`, and reset `finished_at`). The original value was captured beforehand and restored in the same session; the row's `status` was `SUCCESS` before and after. This breached the read-only rule in `AUDIT_PROMPT.md:132-134` and is disclosed rather than hidden.

### HIGH

#### H-1 — The action screenshot is silently discarded by a wire field-name mismatch
`apps/worker/src/core/controller.ts:66` · `apps/worker/src/transport/callback.ts:34-42` · `apps/api/internal/http/oapigen/api.gen.go:310-311` · `openapi/openapi.yaml:2474-2476`

The worker sends the key `screenshot`:

```ts
...(result.screenshot !== undefined ? { screenshot: result.screenshot } : {}),
```

The API reads `screenshotPath`:

```go
// oapigen/api.gen.go:311
ScreenshotPath *string `json:"screenshotPath,omitempty"`
```

`encoding/json` drops unknown keys silently. `req.ScreenshotPath` is therefore always `nil`, `sanitiseScreenshot(nil)` returns `("", nil)`, and no error is raised anywhere.

**Failure, with live evidence.** The one `ACTION_COMMENT` in the live DB has an 86 KB screenshot on the worker's disk, written at `01:44:52` — inside that job's own window (`started_at 01:44:35`, `finished_at 01:46:04`):

```
$ docker exec smm-worker-jkt1-… ls -l --time-style=full-iso /data/screenshots
-rw-r--r-- 1 root root 86300 2026-09-30 01:44:52 +0000 task-1413e6da-…-ff17a867-….jpg

action_log.screenshot_url → NULL
```

Zero `"png file name"` rejections in the API log, which is *why* nobody caught it.

**This typo is masking a second, worse bug.** The worker writes `.jpg` (`core/screenshot.ts:31`) and the API accepts only `.png` (`http/internal.go:32`, enforced at `:297-307`). The mismatch is currently harmless *only because* the field never arrives. Fix the field name alone and **every action verdict starts failing with 400**, which `callback.ts:53-56` treats as permanent — no retry, job stranded in `RUNNING`. Both must be fixed in one change.

**Fix.** Rename the worker's key to `screenshotPath`, and change `screenshotExt` to `.jpg` (or have the worker write `.png`). Add the missing assertion: `callback.test.ts:39-64` checks `attemptId`, `jobId`, `attempt`, `workerId` — **never the screenshot field name**, which is exactly why this survived. `targetUrl`, `actionType` and `responseExcerpt` are also never sent by the worker and are silently dropped for the same reason.

#### H-2 — Reply targets whichever comment owns the first Reply button
`apps/worker/src/platforms/dom.ts:486-492` · `apps/api/internal/service/action.go:149, 178-183`

**There is no comment-link parser anywhere in the system** — not in the API, not in the worker. `replyToComment` does:

```ts
const replyAffordance = sel.replyButton ?? sel.commentButton;
if (replyAffordance) {
  await page.locator(replyAffordance).first().click({ timeout: 5_000 }).catch(() => undefined);
}
```

Nothing reads the `/c/<commentId>/` segment. `safeTargetURL` checks scheme and host and nothing else; the target is stored as `Kind: TargetKindPost` with `ExternalID = u.String()`, so `target.comment_id` is never linked even though `domain.TargetKindComment` exists. The comment at `dom.ts:482-485` — "clicking its Reply affordance focuses the composer" — is an unverified assumption about DOM order.

**Failure.** Three ways, all reporting `SUCCESS` because the mention gate is satisfied by *any* comment:

- Nested reply (`/p/ABC/c/<nestedId>/`): IG focuses the nested comment, but the parent renders above it in DOM order, so `.first()` hits the **parent's** Reply. `[INFERENCE — DOM ordering, not observed in a browser]`
- Post permalink pasted: nothing requires a comment link, so `action_reply_comment` + `https://instagram.com/p/ABC/` is accepted and replies to the first comment on the post.
- Deleted comment: IG serves the post permalink (200, no "page isn't available" copy), so `pageUnavailable` stays false and `.first()` picks the first surviving comment.

**Fix.** Add `commentId(url)` next to `postShortcode` (`dom.ts:42`) matching `/\/c\/([A-Za-z0-9_-]+)/`; reject `ACTION_REPLY_COMMENT` in `Enqueue` when it is empty (4 lines beside `action.go:149`); in `replyToComment`, after `openPost`, fail when `page.url()` no longer carries the id. The nested case needs a DOM anchor on the focused comment — name it as a limitation, don't fake it.

#### H-3 — "Reply sent" is a negative signal: a vanished composer reports SUCCESS
`apps/worker/src/platforms/dom.ts:550-560, 596-601` · `apps/api/internal/service/job.go:159`

`submitReply` accepts `composerCleared` as its only ground truth. `composerStillHas` reads `inputValue()` and falls through to `innerText().catch(() => '')` — a **detached or replaced composer yields `''`**, so `''.includes(text)` is false and the function returns `true`. The comment path does not have this hole; it requires `waitForCommentText` on rendered feed body text. `runAction` checks `needsLogin` and `pageUnavailable` only *before* the action.

**Failure.** The session dies mid-submit (Meta redirects the SPA to `/accounts/login/`) or a rate-limit interstitial replaces the composer. `innerText` returns `''` → `sent = true` → `ok: true` → `RecordAttempt` writes `Verified: true` (`job.go:159`), with a screenshot of a login page. The reply was never posted, no retry fires, and the operator sees SUCCESS.

**Fix.** In `submitReply`, before returning `ok`, re-run the two guards already in scope (`needsLogin(page)`, `pageUnavailable(page)`). Three lines, no new abstraction.

#### H-4 — `safeTargetURL` is a scheme allowlist, and the scrape path skips it entirely
`apps/api/internal/service/action.go:460-479` · `apps/api/internal/service/scrape_ondemand.go:96-101` · `apps/api/internal/service/action_scheduler.go:345` · `apps/worker/src/platforms/dom.ts:93`

```go
// The doc comment claims: "only http/https on a host. The worker later opens
// this URL in a browser, so a file:// or internal address must never survive
// the API… a strict allowlist cannot drift."
//
// The code checks exactly two things:
if u.Scheme != "http" && u.Scheme != "https" { … }
if u.Host == "" { … }
```

No host allowlist, no private/loopback rejection, no match against `acc.Platform`. `apps/api/internal/service/action_test.go:172` proves the last point: it enqueues a `threads.net` URL against an **Instagram** account and the test passes.

Worse, `ScrapeTarget` never calls `safeTargetURL` at all — `ExternalID: rawURL, URL: rawURL` is persisted unvalidated at `scrape_ondemand.go:96-101`.

**Failure.** An OPERATOR (who holds `act`) enqueues `{"targetUrl":"http://169.254.169.254/latest/meta-data/","actionType":"action_comment"}`. The guard passes. The scheduler publishes it and the worker navigates the account's live, session-hydrated, Instagram-authenticated Chromium to the cloud metadata endpoint, then stores a screenshot of whatever rendered. Session cookies are domain-scoped to `.instagram.com`, so this is an internal-network probe rather than credential theft — but it is reachable by any `act` holder, unauthenticated against the internal network.

**Fix.** A platform→host map in `safeTargetURL` (`{instagram: "instagram.com", threads: {"threads.net","threads.com"}}`) plus a loopback/link-local reject. The platform is already in scope at `action.go:176`. And route `ScrapeTarget` through the same guard, storing `u.String()` so the two paths converge on one `target` row instead of two. If the host allowlist is genuinely out of scope, **delete the false sentence in the comment** — right now the comment *is* the security control as far as the next reader is concerned.

#### H-5 — No job sweeper: one lost callback strands an action in `RUNNING` forever
`apps/api/db/queries/action.sql:18-38` · `apps/api/internal/service/action.go:341-352` · `DEVELOPMENT_RULE.md:229`

`ClaimNextActionJob` is the only transition into `RUNNING`; only `applyVerdict` moves a job out. Grepping every `JobStatusRunning` reference in `apps/api/internal`, the only non-test readers are the claim SQL and the domain constant. `main.go:473` creates `NewOrphanSweeper`, which sweeps **container resources, not jobs**. `Retry` refuses anything that is not `FAILED`.

**Failure.** The worker posts the comment successfully, then the container is OOM-killed before `callback.post` completes. The job is `RUNNING`, `attempts=1`. `ClaimNextActionJob` only selects `status='PENDING'`, so the scheduler never re-claims it; the Retry button returns 400 "only a failed job can be retried"; the queue page shows it stuck in "Verifying" indefinitely. The comment's real state is unknowable and unrecoverable from the UI — the single most likely operational outcome of a flaky browser, with no path out.

**Fix.** One query in the existing scheduler tick:

```sql
UPDATE action_job SET status='PENDING', scheduled_at=now()
WHERE status='RUNNING' AND started_at < now() - interval '15 minutes'
```

No new table, no new config.

#### H-6 — The batch `To` date excludes the entire day the operator selected
`apps/api/internal/http/scrape.go:228` · `apps/api/internal/service/scrape_ingestor.go:738-739` · `apps/web/app/(dashboard)/scrape/keyword-scrape-form.tsx:132`

`parseWindowDate` parses a bare `YYYY-MM-DD` into **midnight UTC**, and `inWindow` rejects `!t.Before(w.To)` — an exclusive bound at `00:00:00Z`. The dashboard always sends a bare date.

**Failure.** Operator picks From `2026-09-01`, To `2026-09-30`. `window_to = 2026-09-30T00:00:00Z`. A reel published `2026-09-30T07:00:00Z` fails the bound, is dropped inside the parser, never stored, never counted. The operator sees 27 posts for a 30-day window with no error — and the batch header renders `new Date(batch.windowTo)` = "9/30/2026", actively asserting the wrong range.

**Fix.** Make `To` end-of-day at the HTTP boundary, not in `inWindow` (`scrape.go:111-115`): when the raw value is bare `YYYY-MM-DD`, add a day and subtract a nanosecond. `inWindow` stays the honest exclusive bound for RFC3339 callers.

#### H-7 — `items_read` is provably always equal to `posts_count`, and a test defends the conflation
`apps/api/internal/service/scrape_ingestor.go:111-120` · `apps/api/internal/service/scrape_keyword.go:272-275` · `apps/api/internal/service/scrape_keyword_test.go:373-375, 395-397, 427-429`

The window drop happens **inside the parser** (`:392`, `:474`, `:563` — each `return nil, nil`). By the time `res.ItemsRead += len(items)` runs at `:120`, out-of-window rows are already zero items. The comment at `:116-119` describes a reduction that happens *upstream of the counter*.

**Failure.** The documented 18%-yield run (150 items fetched, 27 stored) reports `itemsRead=27, postsCount=27`. The dashboard shows "27 posts · 27 items read" — redundant numbers that read as "the window dropped nothing", so the operator widens `maxPosts` and burns more Apify compute, which is the exact opposite of the right advice.

Worse, `scrape_keyword_test.go:373-375` asserts `res.ItemsRead != len(res.Posts)` → fail. Fixing the bug turns three tests red, so the correct behaviour is actively defended.

**Fix.** Count raw parsed rows before the window filter (return the pre-window length alongside `[]scrapeItem`), and flip the assertions to `res.ItemsRead == 3 && len(res.Posts) == 1`.

> Note: one slice reported this area as clean. It is not — the control flow at `:111-120` settles it.

#### H-8 — `parent_id` is never written, and the upsert erases it on every re-scrape
`apps/api/internal/service/scrape_ingestor.go:195-204, 790-806` · `apps/api/db/queries/content.sql:78-82`

`ingestComment` builds `domain.Comment` with no `ParentID`; `igCommentItem` has no parent field; `parseIGComments` sets `CommentOf` to the **post** shortcode only. Then `ON CONFLICT … parent_id = EXCLUDED.parent_id` — and `EXCLUDED.parent_id` is always `NULL`, so a re-scrape actively nulls any link.

**Failure.** Post P has comment C and a reply R to C. Both are stored as siblings with `parent_id` NULL. The UI renders a flat `<li>` list (`batches/[id]/page.tsx:381-386`), nothing distinguishes a reply from a root, and the thread is unrecoverable. Until this is built, `domain.Comment.ParentID`, the column, the FK and the index are unbuilt scaffolding — and `GetCommentByPlatformExternalID` (`content.sql:68-72`), the lookup needed to build it, has **zero production callers**.

**Fix.** Either stop erasing (drop `parent_id = EXCLUDED.parent_id` from the `DO UPDATE`) or actually build it using the existing lookup. Don't half-do both.

#### H-9 — Comment ordering is non-deterministic and re-stamped on every scrape
`apps/api/db/queries/content.sql:86-92, 78-81`

`ListCommentsByPost` is `ORDER BY scraped_at DESC LIMIT $2 OFFSET $3` with no unique tiebreaker, while `UpsertComment` sets `scraped_at = now()` on *every* conflict.

**Failure.** A post with 150 stored comments is re-scraped → all 150 rows get the new run's timestamp, so their relative order becomes the actor's array order, not comment time. Pages 1 and 2 are served from a sort with no tiebreaker; on any tie Postgres may return either order, silently duplicating or dropping rows across the page boundary. The UI's count badge renders `comments.length` = 100 for a post with 3,400, and the response carries no total — so the operator reads "100" as the truth. `CountCommentsByPost` exists and is called only from tests.

**Fix.** Add `, id` to the `ORDER BY` (one line). Either return the total or delete the unused `CountCommentsByPost`.

#### H-10 — No URL classification: a profile link silently drives a paid 100-post scrape
`apps/api/internal/service/scrape_ondemand.go:51-57, 96-101, 163` · `apps/api/internal/adapter/apify/runner.go:407-416`

There is no post/profile/hashtag classifier. `platformFromURL` is `strings.Contains(raw, "instagram.com")`; `postExternalIDFromURL` is a `/`-segment scan for `p|reel|reels|tv`, which yields `""` for a profile. `runInput` then sends `{"username":[url],"resultsLimit":100}` — and the runner's own comment at `:406-408` says `username` "accepts usernames, profile URLs **AND** post URLs".

**Failure.** Operator pastes `https://www.instagram.com/nasa/`. One paid run returns up to 100 of that profile's posts. `parseItems` drops `onlyCode` entirely for Instagram (only `parseThreadsItem` honours it), so all 100 rows are upserted with 100 metric snapshots. `readBack` then resolves `""` → 400 "scrape produced no post". The operator paid, got an error, and 100 unrelated posts now pollute `post`, `content_metric` and every top-posts read.

**Fix.** Before `UpsertTarget`: `if postExternalIDFromURL(platform, rawURL) == "" { return ErrValidation }`. Rejects profile/hashtag/garbage for free, with no run.

### MEDIUM

#### M-1 — Operator-supplied comment/reply text bypasses the denylist
`apps/api/internal/service/action_scheduler.go:313-321` vs `apps/api/internal/service/template.go:76-80`

`composeText` returns the stored operator body verbatim. The `MatchBanned` screen lives inside `composer.Pick`, so this branch never reaches it. The function's own doc comment ("screened against the denylist", `:302-303`) and `workerActionJob`'s ("already-composed, denylist-screened", `:380-381`) are both false for this branch.

**Failure.** `POST /api/actions` with `actionType: action_reply_comment` and a term on the account's denylist → `Enqueue` accepts (only length is capped) → published to the worker → posted live. The same word inside a template is rejected. **Fix:** run the banned-pattern screen on the operator branch too, or delete the branch.

#### M-2 — `action_log.template_id` is never written, so the 7-day per-target dedupe is permanently inert
`apps/api/internal/service/action_scheduler.go:305-330` · `apps/api/internal/service/job.go:152-168` · `db/queries/template.sql` (`PickForTarget`)

`composeText` discards `out.TemplateID`; `dispatchOne` never writes `job.TemplateID`; `AttemptRecord` has no such field, so `action_log.template_id` is always `NULL`. The dedupe predicate `al.template_id = t.id` can therefore never be true.

**Failure.** 20 comment jobs for the same post across 20 accounts: every `PickForTarget` returns the full pool, so the weighted draw can hand the same variant to every account, and a retry very likely re-posts text already in that thread. **Fix:** thread the id through end to end; the query and schema are already correct.

#### M-3 — A newline in the reply text submits early and reports the unposted remainder
`apps/worker/src/platforms/dom.ts:526-529, 560` · `apps/api/internal/service/action.go:87, 153-156`

`pressSequentially` maps `\n` to Enter. `Enqueue` rejects nothing but length, and the dashboard's `Textarea` accepts newlines.

**Failure.** `"Great shot!\nDM me at foo@bar"` → the first line is submitted, the composer clears, the rest is typed into the fresh composer and abandoned; `submitReply` sees an empty composer → `SUCCESS`, with `renderedText` set to the full two-line string — a string that was never posted. **Fix:** reject `\n`/`\r` in comment/reply text at `action.go:153` (one `strings.ContainsAny`).

#### M-4 — `RunPendingNow` pages by OFFSET over the set it mutates: double-moves rows, skips rows, over-reports
`apps/api/internal/service/action.go:373-403` · `apps/api/db/queries/action.sql:67-72`

Termination is sound — `offset` advances by a constant, the loop is capped at `maxRunPendingRows`, a short page breaks. The **paging** is wrong. `ListActionJobsByStatus` is `ORDER BY scheduled_at LIMIT/OFFSET`, and each reschedule **rewrites `scheduled_at` to `now`**, re-sorting the very set the offset walks.

**Failure.** 100 pending jobs, batch A at `T0`, batch B at `T0+10s`, both due, `now = T0+1h`. Page 1 rewrites batch A to `now`; new ordering is `[B×50 @ T0+10s, A×50 @ now]`. Page 2 (`OFFSET 50`) re-reads the 50 **already-moved** rows. `OFFSET 100` is empty → break. The handler reports `{"moved":100}`; exactly **50 distinct jobs moved, and batch B was never touched**.

Compounding: this is one HTTP request doing up to 2,000 sequential single-row `UPDATE`s.

**Fix.** The button means "set every PENDING row to now", so do exactly that — one `UPDATE action_job SET scheduled_at = now() WHERE status = 'PENDING'`, return `RowsAffected()`. Deletes the loop, the offset, the cap constant and the count bug in one line, as a single round trip.

#### M-5 — Batch detail caps the post list at 100 while the header shows the true count
`apps/web/app/(dashboard)/scrape/batches/[id]/page.tsx:87` vs `:198`

`listKeywordBatchPosts(id, { limit: 100 })`. A batch that stored 340 posts renders "340 posts" above 100 rows with no pagination and no truncation hint.

#### M-6 — `commentsCount` is displayed as a hard `0` for Instagram
`apps/web/app/(dashboard)/scrape/keyword-scrape-form.tsx:189` · `apps/api/internal/service/scrape_ingestor.go:358-360`

`parseIGFlatPostItems` deliberately emits no comment items (the search actor exposes `comment_count` but never bodies), so `res.CommentsUpserted` is always 0. The card renders "0 comments" with no qualifier, next to per-post rows showing `comment_count: 13`. **Fix:** omit the field or label it — the batch detail already routes comment bodies through the per-post "Fetch comments" button, which is the honest shape.

#### M-7 — `apify_run` rows orphaned in `RUNNING` on every failure path
`apps/api/internal/service/scrape_keyword.go:288-297`

`UpdateApifyRun` is reached only *after* the `out.Status != "SUCCEEDED"` check. Every failure — transport error, HTTP 4xx/5xx (returned as `Status: FAILED, err == nil`), ingest error — leaves the row `RUNNING` with `finished_at` NULL. Attempt 1's run is never closed on retry. No sweeper references `apify_run`. `apify_run` is the audit table; it now reports the opposite of what happened. **Fix:** move the `UpdateApifyRun` call above the status check.

#### M-8 — Orphaned `running` scrape_job rows on client disconnect
`apps/api/internal/service/scrape_ondemand.go:164-166, 184-189`

The ingest-error branch returns without calling `s.fail`, and `s.fail` itself uses the request context — already cancelled in exactly the case it exists for. Operator starts a comments fetch, closes the tab at 30 s: the run is already paid and still executing, ctx cancels, the job row stays `running` permanently, `target.post_id` is never linked, and the next scrape of that URL pays again. **Fix:** `context.WithoutCancel(ctx)` inside `fail` (stdlib, Go 1.21+), and call it on the ingest-error branch.

#### M-9 — A zero-item run is reported as a successful fresh scrape
`apps/api/internal/service/scrape_ondemand.go:164, 168-178`

`storeItems` returns `SUCCEEDED` with empty `ItemKeys`; the `IngestRun` result is discarded at `:164`; `readBack` resolves `tgt.PostID` from the *prior* scrape's `LinkTargetPost` and returns the old row. **Failure:** `force:true` on a post deleted an hour ago → 200, job `success`, and the operator's panel shows the old comments as if the fetch worked. **Fix:** use the result already returned; if `err == nil && res.ItemsRead == 0`, take the same branch as a read-back miss.

#### M-10 — No rate limit or charge ceiling on the two endpoints that spend money
`apps/api/internal/http/scrape.go:38-39` · `apps/api/internal/service/scrape_keyword.go:16` · `apps/api/internal/adapter/apify/runner.go:295-299`

`maxPosts` is clamped to 200 **per keyword**; with `KeywordScrapeMaxKeywords = 5` that is up to 1,000 items per request, doubled by `keywordScrapeAttempts = 2`. `RunSearch` deliberately sends no charge cap. Unlike `AccountService.Import` — which has `ImportRateLimit` for exactly this class of spend — neither scrape endpoint has a limiter, and `force` is a caller-controlled boolean that disables the only cost guard (`scrape_ondemand.go:108-112`). README states `APIFY_BUDGET_USD` is "parsed but not yet enforced anywhere"; its only consumer is a Prometheus gauge. **Fix:** reuse the existing `importLimiter` sliding-window pattern for both routes. No new dependency.

#### M-11 — The primary on-demand UI can never fetch comments for a batch-fresh post
`apps/web/app/(dashboard)/actions/new-action-form.tsx:173`

`UpsertPost`'s `ON CONFLICT` sets `scraped_at = now()`, and a keyword batch ingests posts with no comment bodies — so the 6h cache answers for comment-less posts. `force` exists exactly for this, but only the batch detail page passes it. **Failure:** batch at 10:00 ingests post P; at 10:30 the operator pastes P's permalink into New Action → 200 with `comments: []`, and the form renders the "N existing comments loaded" line only when `length > 0`, so there is no signal and no affordance to force. **Fix:** pass `true` at `new-action-form.tsx:173`.

#### M-12 — `PlatformAdapter.verify` is a required interface member with zero call sites
`apps/worker/src/platforms/adapter.ts:55` · `apps/worker/src/platforms/instagram.ts:67-74` · `apps/worker/src/core/controller.ts:91-110`

`grep '\.verify('` over `apps/worker/src` returns nothing. Eight adapters implement a method nothing invokes, while the contract docs present `verify()` as the mandated ground-truth step. The comment path upholds the contract anyway — `fillAndSubmit` verifies inline via `waitForCommentText` — but the next platform author will read `verify` as real and implement only that. **Fix:** delete it from the interface and the eight implementations. Deletion matches what the code actually does.

### LOW

- **L-1** A permanently un-replyable comment is retried 3× and each attempt can post to a *different* first comment: `'reply composer not found'` contains `"not found"` → `TRANSIENT` → retryable (`dom.ts:495`, `domain/classify.go:47`). Rewording the string drops it to terminal.
- **L-2** `author_handle` is insert-only on both `post` (`content.sql:21-28`) and `comment` (`:78-82`); a rename or a bad first write is permanent, and `postPermalink` builds the URL from it.
- **L-3** Deleted-comment placeholders are stored as real content — the parser skips only blank text (`scrape_ingestor.go:792-794`). Do **not** string-match the placeholder; that is exactly the silent-drift shape the repo's own parser comments warn about.
- **L-4** `raw_payload.bytes` is always 0 on the keyword path — `runner.go:352-366` computes the total and discards it.
- **L-5** Stale comment contradicting the code: `http/scrape.go:82` says a window date that fails to parse means "no bound"; `:112-114` returns **400**.
- **L-6** ~~Reply is API-only and untested.~~ **FIXED (2026-10-15).** The form now has a Reply flow that targets a chosen comment's permalink, and `GET /api/scrape/posts/{id}/comments` returns a derived `url` per comment so the target can actually be built. Covered by `actions.spec.ts` (enqueue hits the comment URL, not the post) and the worker's `replyComment` tests. The path was additionally driven **live** through `docs/local-worker` (`comment-reply … --commit`), which posted a real threaded reply and preserved IG's `@mention` prefill. The original finding, kept for the record: no Reply button, so `action_reply_comment` was reachable only by direct API call.
- **L-7** `PlatformAdapter.verify` aside, `controller.ts:38-53` returns a failure verdict for an unknown platform with `workerId: ''` — harmless, but it means the callback's `workerId` is not the container identity on that path.
- **L-8** The per-comment Reply affordance **does not exist for the first ~3s** after a comment permalink loads. Measured live (polled every 500ms): both `sel.replyButton` and a bare `span:text-is("Reply")` return **0** until t≈3000ms, then 8. `replyToComment` scrolls and clicks immediately, so on a cold load that click *always* throws.
  - It did not matter while the click was wrapped in `.catch(() => undefined)`: the permalink itself already focuses the target comment's box and pre-fills the `@mention`, so the flow succeeded with the click doing nothing. **The click was decorative.**
  - It became fatal when the click was made to report a bounded failure ("reply affordance could not be clicked"), and **every reply then failed** — 4 jobs on one comment in the same second, `attempts=1`, `status=FAILED`. Fixed by waiting (bounded, `AFFORDANCE_WAIT_MS`) for the affordance to mount and keeping the click best-effort: the mention check is the real threading gate, not this click.
  - The lesson to keep: a swallowed error was load-bearing. Before making a previously-ignored failure loud, confirm the call is not redundant.
  - **Measured properly**: the affordance appears anywhere from ~3s to **~9.8s** after a comment permalink loads (polled live, twice: 3s once, 9750ms another). The first fix set a 6s bound from the single 3s measurement and still failed — a bound drawn from one sample is a guess. It is now 12s, and expiry is not a failure.
- **L-9** Meta's "Your Meta Account is here" consolidation modal blocks every action. Seen on two separate accounts/workers: a full-screen `role=dialog` ("Your updated account works across Meta apps and devices") renders on an ordinary post load, and its scrim swallows every click — so the reply flow opens no composer and fails as *"no @mention prefilled"*, blaming the selector. The tell in the screenshot was the composer rendered **behind** the modal. Fixed by `dismissMetaAccountModal`, called from `openPost` on every exit path. Best-effort by design: absent modal → no-op, and it can never be the reason a job fails.
- **L-10** The reply mention check accepted ANY `@handle`, so it could not detect the wrong tag. A **top-level** composer pre-fills the **post author's** handle; a threaded reply box pre-fills the **comment author's**. Since the check only asked "does it start with @", a reply could post tagging the post author and still be recorded as threaded — the exact failure the guard exists to catch, defeated by its own leniency. The BE now resolves the target comment's author and sends it as `expectedMention`; the worker requires the prefill to name that person (case-insensitive, `@`-tolerant). An empty expectation still passes, so a rolling deploy does not refuse every reply.
- **L-11** `LinkTargetComment` was **defined and never called**, so `target.comment_id` was NULL on every reply target. This made the tag fix (L-10) *unreachable*: the dispatch-side author lookup is gated on that link, so it never resolved a handle, and replies silently fell back to the presence-only mention check — which a wrong tag passes. Enqueue now links a reply target to its comment (best-effort: an unscraped comment still enqueues, degrading the check rather than blocking the job).
  - The lesson: a correct fix behind a dead gate is an unfixed bug. The fix *looked* verified because the reply succeeded — it succeeded via the fallback path, so the new check never ran. Verify the new path is *reached*, not just that the outcome is good.

---

## 3. Doc / code drift

**Status column added 2026-09-30 after the CRITICAL/HIGH remediation — see §6.**

| Doc claim | Code reality | Status |
|---|---|---|
| `SECURITY_REVIEW.md:18` — "`safeTargetURL` is a strict allowlist… No blocklist to drift" | Scheme + non-empty host. Every internal address passed. **H-4.** | **CLOSED** — a real `platformHosts` allowlist now backs the claim. |
| `SECURITY_REVIEW.md:20` — `/internal/*` "PASS, machine-only" | Machine-only by intent, but published on `0.0.0.0:24080` with no secret. **C-2.** | **CLOSED** — `INTERNAL_API_SECRET` guard; disabled until set in `.env`. |
| `SECURITY_REVIEW.md:45` — `TestContainerRBAC` "proven" | No test wires `Actions:` or `Scrapes:` into `NewRouter`. Every action and scrape RBAC gate is untested. | **OPEN** — outside the IG-workflow scope. |
| `scrape_keyword.go:272-274` — "`itemsRead` and `postsCount` differ by design" | They were the same number. **H-7.** | **CLOSED** — the claim now holds. |
| `scrape_ingestor.go:116-119` — "reporting the stored count as `items read` hid that drop" | The window reduced `len(items)` *before* the counter. **H-7.** | **CLOSED** — counter moved ahead of the window. |
| `action.go:460-464` — "a `file://` or internal address must never survive the API" | Neither was checked. **H-4.** | **CLOSED** — both are now rejected. |
| `dom.ts:482-485` — "clicking its Reply affordance focuses the composer" | An unverified DOM-order assumption. **H-2.** | **PARTLY CLOSED** — the target comment is now verified before clicking; the nested-reply DOM-order assumption remains and is flagged. |
| `action_scheduler.go:302-303`, `:380-381` — operator text is "denylist-screened" | That branch bypassed the screen. **M-1.** | **OPEN** |
| `docs/platforms/instagram.md:12` / `PROGRESS.md:287` — reply shipped | No Reply UI exists; the path has no test. **L-6.** | **OPEN** |
| `openapi.yaml:2476` — "requires .png" | Worker writes `.jpg`, and never sent the field at all. **H-1.** | **CLOSED** — spec and code now both say `.jpg`. |
| `DEVELOPMENT_RULE.md:229` — "Sweeper: `RUNNING > 10 min → PENDING`" | No such sweeper existed. **H-5.** | **CLOSED** — but at a 15-minute threshold, so the rule's number is now wrong. |

**Prior audit (`docs/omp_audit_report/`, 2026-09-30).** F-01 was *misdiagnosed*: the `.jpg`/`.png` mismatch was real but dormant, masked by the H-1 field-name mismatch. F-02 (**H-5**), F-06 (**C-2**), F-07 (**H-4**) are now **CLOSED**. F-05 (**M-4**) and F-08 (`ACTION_DRY_RUN` defaults `true` at `config.go`) remain **OPEN**. `omp_audit_report/FIX_PLAN.md` still carries unchecked entries for the closed ones and should be struck.


---

## 4. Tests that cannot fail

Each names the mutation that would break it — or, here, the mutation that **would not**.

| Test | Problem | Status |
| `apps/worker/src/test/callback.test.ts:39-64` | Asserted `attemptId`, `jobId`, `attempt`, `workerId` — **never the screenshot field name**. This is why H-1 shipped. | **RESOLVED** — now asserts `screenshotPath`, and that `screenshot` is absent. |
| `scrape_keyword_test.go:373-375, 395-397, 427-429` | Asserted `itemsRead == len(posts)`, pinning the H-7 bug. | **RESOLVED** — flipped to delivered-vs-stored. |
| `action_retry_test.go:113` (`TestRunPendingNowSweepsPastTheBatchCap`) | Passes only because `fakeActionStore.RescheduleActionJob` *records without mutating* and `ListActionJobsByStatus` returns insertion order unsorted. Changing the real SQL's `ORDER BY scheduled_at` to `ORDER BY id` breaks production and leaves this test green. |
| `scrape_fake_test.go:518-521` | `ListKeywordBatchPosts` returns `nil, nil`. Delete the whole `CreateKeywordBatchPost` loop and every keyword test still passes; production `GET /batches/:id/posts` returns `[]` while the header says 50. |
| `scrape_service_test.go:222-257` | Feeds `{"platform":"instagram","externalId":…}` — the **legacy** shape no current actor emits — so it exercises `parseLegacyItem` and proves nothing about the Instagram parser. |
| `repository/scrape_test.go:197-239` | Hand-wrote `ParentID` through the repo, so the mutation that breaks H-8 did not break it. | **RESOLVED** — `TestScrapeCommentReScrapeDoesNotEraseParent` re-upserts *without* a parent and asserts the link survives; verified it fails when the erase is restored. |
| `scrape_fake_test.go:191-199` | `ListCommentsByPost` ignores `limit`/`offset` — every service test using it is structurally incapable of catching H-9. | **OPEN** — H-9 shipped untested; a repository-level paging test could not be made to fail either (see §6). |
| `scrape_fake_test.go:309-315` | `CreateApifyRun` derives its id from `ActorID+RunID+Status` and both attempts pass `RunID: ""`, so both share one run. `TestScrapeKeywordsRetriesEmptyResult` cannot distinguish the behaviour it names. |
| `adapters.test.ts` (whole file) | No test references `replyToComment`, `submitReply`, or `reply_comment`. The fake composer lacks `pressSequentially` and never clears on submit, so driving `replyToComment` today **fails** — proof the path has never run. |
| `scrape_force_test.go` | The force assertion swallows the error, so it passes on early failure. |
| `apps/api/internal/http/` (all 13 test files) | No `scrape_test.go`; no test wires `Actions:` or `Scrapes:`. The 202 shape, the 400 on a bad date, the 502 on actor failure, and every action/scrape permission gate are untested. |

---

## 5. What was not checked

- **This section describes the audit as it stood before remediation.** Everything
  below was reading-only. Since then a test suite *has* been run for every fix — see
  §6 for what was executed and what it proved.
- **No lint was run**, and no live Instagram session or live Apify run was performed
  for any of the fixes.
- **Live Apify actor schemas.** Findings about `username`/`resultsLimit`, the absence of a date input, `latestComments[]` shape, and comment-count inputs rest on `platforms/instagram.md` and the runner's own comments — not on Apify's API.
- **Live Instagram DOM behaviour.** The nested-reply ordering claim in H-2 is `[INFERENCE]` from document order, not observed in a browser.
- **Whether a worker container can actually reach cluster-internal IPs** — network topology was out of scope for the scrape slices; H-4 asserts the URL *reaches* `page.goto`, not that the fetch succeeds.
- **Playwright's `\n` → Enter mapping** (M-3) is from documented `keyboard.type` semantics, not verified against the pinned version.
- **K8s provisioning tier** — only the docker tier in `compose.yaml` was read for port bindings and worker env.
- **The Threads reply-join ordering assumption** — in scope only insofar as `threadsRun` cannot affect Instagram (`scrape_ingestor.go:92-95`), which was checked and is clean.

---

## 6. Remediation log — CRITICAL and HIGH

Fixed 2026-09-30, in severity order. Every row below was executed, not inferred:
the repository tests ran against a throwaway `timescale/timescaledb:2.17.2-pg16`
(`SMM_TEST_DB=1`, container `smm-testdb`, destroyed afterwards) so the live dev
database was never mutated. Full suite: 15/15 API packages `ok`, worker 62/62.

| # | Fix | Proof |
|---|---|---|
| C-1 | `scrapeError` default branch returns a fixed `"scrape failed"` instead of `err.Error()`. | Root cause was already proven with a throwaway program showing `url.Error.Error()` embeds the query string. |
| C-2a | `INTERNAL_API_SECRET` on `/internal/*`, wired API → provisioner → every provisioned worker, plus the 4 config-carrying worker fetches. Empty secret leaves the guard off so single-host `make up` still works. | `TestInternalSecretGuardsTheGroup` (no header → 401, wrong → 401, correct → passes). |
| C-2b | `CompleteActionJob` and `RescheduleActionJob` both carry `AND status IN ('PENDING','RUNNING')`. | `TestActionJobTerminalStatesAreImmutable`. **Verified it bites**: reverting the guard fails with `success job was rescheduled back into the queue`. The RUNNING case is asserted too, so the guard cannot silently start breaking legitimate retries. |
| H-1 | Worker sends `screenshotPath`; API accepts `.jpg`. Both halves in one change — the field rename alone would have unmasked the dormant `.jpg`/`.png` mismatch and failed 100% of verdicts with a 400. | Wire assertion added: `assert.equal(payload.screenshotPath, …)` plus `assert.equal(payload.screenshot, undefined)`. |
| H-2 | `commentId()` in the worker, `commentIDFromURL()` in the API; `Enqueue` rejects a reply without a `/c/<id>/`; `replyToComment` re-checks `page.url()` before clicking. | `TestActionEnqueueRejectsReplyWithoutCommentID`. |
| H-3 | `submitReply` re-runs `needsLogin` and `pageUnavailable` before returning `ok`. | **No test.** The fake composer in `adapters.test.ts` cannot drive `replyToComment` at all (the audit's own finding), so there is no harness to hang an assertion on. The change is 2 lines reusing guards already in scope. |
| H-4 | `platformHosts` allowlist in `safeTargetURL`, keyed on the acting account's platform; loopback/link-alike hosts rejected. Account lookup moved ahead of URL validation so the platform is in scope. | `TestActionEnqueueRejectsForeignAndInternalHosts` — 5 cases (cross-platform, `169.254.169.254`, `127.0.0.1`, `file://`, `instagram.com.evil.test`). 4 of the 5 provably passed under the old two-line check. |
| H-5 | `SweepStaleRunningJobs` + `StaleRunningAfter = 15m`, called once per scheduler `Tick` (non-fatally). | `TestActionJobSweepRequeuesStrandedRunning` — a fresh RUNNING job is left alone, an aged one returns to PENDING and is claimable again. |
| H-6 | End-of-day stretch moved into `parseWindowDate(v, endOfDay)`: a bare date used for `To` now spans its whole day; an explicit RFC3339 instant is untouched. | `TestParseWindowDateStretchesTheToBoundToEndOfDay` and `…LeavesAnExplicitInstantAlone`. |
| H-7 | `items_read` counts the actor's delivered posts: the same parse is re-run with the window opened. `inWindow` stays an honest exclusive bound. | 7 tests flipped. Each failure message named the old conflation outright — e.g. `itemsRead: want 1 (only Din is inside the window), got 3`. |
| H-8 | `parent_id = EXCLUDED.parent_id` dropped from `UpsertComment`'s `DO UPDATE`. | `TestScrapeCommentReScrapeDoesNotEraseParent`. **Verified it bites**: restoring the line fails with `re-scrape erased parent_id: <nil>`. |
| H-9 | `ORDER BY scraped_at DESC, id` on `ListCommentsByPost`. | **No test — deliberately removed.** A paging test with the tie constructed (all rows sharing one `scraped_at`) passed both with and without the tiebreaker, so it could not fail and would have been a test that proves nothing. The fix stands on the SQL standard: `ORDER BY` without a unique tiebreaker leaves row order unspecified. |
| H-10 | `ScrapeTarget` now runs through `safeTargetURL` and requires `postExternalIDFromURL` to be non-empty, before `UpsertTarget` and before any paid run. | `TestScrapeTargetRejectsNonPostURLsWithoutSpendingARun` — 6 cases, each also asserting `runner.runs == 0` so a rejected URL provably costs nothing. `…StillAcceptsPostPermalinks` guards against the guard becoming a blanket rejection. |

### Corrections to the original report

- **`action_test.go:172` was not a passing test.** It enqueued a `threads.net` URL against an **Instagram** account and asserted success — it was pinning the H-4 hole, not passing cleanly. Fixed to an Instagram URL; the cross-platform case now lives in `TestActionEnqueueRejectsForeignAndInternalHosts`.
- **H-2's "deleted comment" sub-case is now caught at enqueue and in the worker**, but the *nested* reply case is not solved — `replyToComment` still takes `.first()` in DOM order. Marked with a `ponytail:` comment naming the ceiling rather than faked.
- **`safeTargetURL`'s doc comment claimed to be an allowlist and was not one.** It now is one, so the false sentence is resolved rather than deleted.

### Known pre-existing failures (not introduced here, not fixed here)

Verified by `git stash` against the pristine tree — all three fail identically without any of these changes:

- `repository/repo_test.go` — `TestWorkerRoundTrip`, `TestAccountAssignProvesUniqueConstraint`, `TestProvisionLogAppend`. They fail only when `SMM_TEST_DB=1` points at a real database; they are unrelated to the action/scrape surface audited here.
- `config_test.go` — `TestLoad_ActionRateLimits`, `TestLoad_MaxAccountsPerContainer`, `TestLoad_ReconcileInterval` fail when `DATABASE_URL` is set without `JWT_SECRET` in the environment. Pass with `DATABASE_URL=''`.

### Not yet done

MEDIUM is fixed and logged in the sections below; LOW is fixed except L-3. `omp_audit_report/FIX_PLAN.md` is superseded by a banner on its own first page — its Task 1/2/5/6 prescriptions are wrong (executing Task 1 as written would push the worker to `.png` and fix nothing).

### Remediation log — MEDIUM, second pass (2026-09-30)

| # | Fix | Proof |
|---|---|---|
| M-7 | `runOnce` closes the `apify_run` row on **every** exit path (transport error, non-SUCCEEDED status, ingest error), via a `closeRun` helper writing on `context.WithoutCancel(ctx)`. | 3 tests: `TestKeywordFailedActorRunClosesApifyRun`, `TestKeywordTransportErrorClosesApifyRun`, `TestKeywordIngestErrorClosesApifyRun`. **Verified they bite** — removing the close leaves `Status:RUNNING FinishedAt:<nil>`. The fake store's `UpdateApifyRun` had to change first: it returned the argument unchanged and recorded nothing, so no test could tell a closed run from an open one. |
| M-8 | `s.fail` writes on `context.WithoutCancel(ctx)`; the ingest-error branch now calls it. | `TestScrapeTargetFailSurvivesACancelledRequest` — a pre-cancelled request must still leave a terminal job row, and asserts the write never saw a cancelled context. **Verified it bites twice**: reverting either half fails it (`status "running"` / `rode on the cancelled request context`). |
| M-9 | `ScrapeTarget` consumes the `IngestRun` result it already had; `ItemsRead == 0` takes the read-back-miss branch instead of returning the prior scrape's row. | `TestScrapeTargetZeroItemRunDoesNotReturnStaleRow` — a forced re-scrape of a deleted post must 4xx and leave the job FAILED. **Verified it bites**: without the guard it returns `1 comments for Cstale` — the exact stale-row failure the finding describes. |
| M-10 | `importLimiter` reused (its shape copied, no new abstraction) behind `SetSpendLimit` on both services, keyed on the client IP, checked before the job row/runner on `/scrape/target` and before the batch row on `/scrape/keywords`. New `SCRAPE_RATE_LIMIT` / `SCRAPE_RATE_LIMIT_WINDOW_SECONDS` (default 0 = no ceiling). `scrapeError` gained the missing `ErrRateLimited` → 429 mapping. | `TestScrapeEndpointsRateLimitBeforeReachingTheRunner` asserts `runner.runs == 1` after the second call, in the same style as `scrape_force_test.go`; `TestScrapeSpendCeilingUnconfiguredIsUnlimited` pins that an unset ceiling is not a ceiling of zero; `TestScrapeErrorRateLimitedIs429WithSharedShape` pins the wire shape. **Verified each bites** — dropping either the target gate or the 429 mapping fails it. |

Unrelated drift found while doing this and deliberately not fixed: `scrapeError` has no `ErrConflict` case (falls to 502); `domain.ApifyRun` has no `Error` field, so a failed run's *reason* is only on `scrape_job.error`, not the run row.

`INTERNAL_API_SECRET` is not set in `.env`, so the C-2a guard is **disabled by design** on the running stack until an operator sets it. The API and every provisioned worker read the same variable, so setting it once in `.env` closes both sides.

### Remediation log — MEDIUM, first pass (action + web + worker)

| # | Fix | Proof |
|---|---|---|
| M-1 | The denylist screen was extracted out of `TemplateService.Pick` into shared helpers so `composeText` can run the operator branch through it, failing the job terminally **before** any cooldown or rate budget is spent. With no template store wired it refuses to ship the body at all and composes instead, so the hole cannot silently reopen. Two doc comments that claimed the opposite were corrected. | `TestActionSchedulerScreensOperatorSuppliedText` — **bite-verified**, red on old code (`got 1`). Plus `…ShipsCleanOperatorSuppliedText` and `…WithoutTemplateStoreComposes`. |
| M-2 | `template_id` threaded end to end: `composeText` → `SetActionJobTemplate` at dispatch time (the variant does not exist at INSERT) → worker echo → `AttemptRecord.TemplateID` → `action_log.template_id`. `Retry` rewrites it too, because the queue UI clears the column on requeue and a retry would otherwise look unused. | Three levels: `TestActionSchedulerCarriesTemplateID` (red on old code, `got ""`), `TestRecordAttemptStoresTemplateID`, and `TestTemplateDedupeSeesDispatchRecordedVariant` — a second `PickForTarget` returns **zero** candidates once the id is recorded, which is the 7-day dedupe finally working. |
| M-3 | `Enqueue` rejects `\n`/`\r` in comment and reply text with `ErrValidation`, after the length cap and before anything is written. | `TestActionEnqueueRejectsNewlineInText` — `\n`, `\r`, `\r\n` all red on old code (`got <nil>`). The AI route was checked and cannot produce multiline text, so no second guard is needed there. |
| M-4 | The OFFSET loop is gone, replaced by one `UPDATE action_job SET scheduled_at = $1 WHERE status = 'PENDING'` returning `RowsAffected`. That deleted the loop, the offset, the `maxRunPendingRows` cap and the miscount in one move. `ListActionJobsByStatus` was **left in place** — `claimAny` and `ActionService.List` still use it. | `TestRunPendingNowMovesEachDueJobExactlyOnce` reproduces the audit's exact 50+50 two-batch case; **bite-verified** red on the paged implementation (`job B-0 still scheduled at …`). |
| M-5 | The batch detail page fetched `{ limit: 100 }` once and rendered whatever came back, silently dropping a batch of 340. Now a client-side pager over the server's existing `limit`/`offset`: it requests `PAGE_SIZE+1` (50+1) and keeps the extra row only as a has-more flag, so there is no count query and no phantom next page on a batch of exactly 50. Adds a `Menampilkan 1–50 dari 340 post` range line plus Sebelumnya/Berikutnya. | **Verified in real Chromium against a 340-post stub batch.** Driven from `apps/web/e2e/tmp-verify-pagination.mjs`. It caught a bug a type-check could not: the first version only changed `page` state, and the polling effect returns early once the batch is `settled` — the only state in which the pager is reachable — so **the buttons did nothing**. A page-change `useEffect` refetches independently. Bite-verified: removing that effect gives `M-5: no request with offset=50`; with it, `offset=50` is on the wire. Also pins `limit=51`, 50 rendered rows per page, the range line, and Previously disabled on page 1. |
| M-6 | A keyword run stores the post rows but never their comment bodies, so `commentsCount` is `0` for most batches — and a bare `0 comments` reads as "this post has no comments", a different claim. Both surfaces now say `comment bodies not fetched`; the batch page's comment points at the Detail → Fetch comments button that does fetch them. The field stays in the API because it is correct for platforms that do ingest bodies (Threads). | Neither `batches/[id]/page.tsx` nor `keyword-scrape-form.tsx` renders `commentsCount` as a number any more. |
| M-11 | `new-action-form.tsx:178` now passes `force: true` (the batch detail page already did). The hint text no longer claims stored posts are reused for 6 hours, and a zero-comment response renders an explicit line saying a fresh scrape ran — otherwise the empty case is indistinguishable from a cache hit and the operator cannot tell whether there is anything to reply to. | `api.scrapeTarget(p, u.trim(), true)` → `JSON.stringify({ platform, url, force: true })`; the zero case renders the explanation instead of nothing. |
| M-12 | `PlatformAdapter.verify` deleted from the interface and all 8 implementations, after confirming zero call sites including dynamic dispatch. Two docs that presented it as the mandated ground-truth step now describe what actually happens: `fillAndSubmit` verifies inline via `waitForCommentText`. | `grep -rn '\.verify('` → zero hits; loading all 7 adapters at runtime shows no `verify` key. No test referenced it, which is why it survived. |

### Remediation log — LOW

| # | Fix | Proof |
|---|---|---|
| L-1 | `"reply composer not found"` was classified `TRANSIENT` by the substring `"not found"`, so a permanently un-replyable comment was retried 3× — each attempt re-clicking `.first()` in DOM order, i.e. three replies to three *different* comments on a live account. Now an explicit terminal case, placed **before** the transient list. | `TestClassifyActionError/reply_composer_gone`. **Bite-verified**: removing the case fails with `= "TRANSIENT", want "UNKNOWN"`, plus an assertion that the class is not `Retryable()`. |
| L-2 | `author_handle` was insert-only on both `post` and `comment`, so a rename — or a bad first write — was frozen forever. This is not cosmetic: `postPermalink` builds TikTok and X URLs out of it, so a stale handle is a permalink that 404s and can never heal. Both upserts now refresh it; the existing `CHECK (author_handle <> '')` already blocks an empty value, so no `COALESCE` is needed. | `TestRescrapeRefreshesAuthorHandle` and `TestRescrapeRefreshesCommentAuthorHandle`. **Bite-verified**: reverting fails with `post author_handle = "oldname", want "newname"`. |
| L-3 | **Deliberately not fixed.** `parseIGComments` skips only blank text, so a deleted-comment placeholder is stored as real content. The only mechanism available is string-matching the placeholder — exactly the silent-drift shape this audit warns against and the parser's own comments warn against — and `igCommentItem` carries no structured deleted flag. One fact is missing and the audit cannot establish it: what the Instagram actor actually emits for a deleted comment. Guessing would be worse than the current behaviour. **Prerequisite to fix: confirm the actor's deleted-comment field.** | — |
| L-4 | `raw_payload.bytes` is `NOT NULL` with a `CHECK (bytes >= 0)`, so a constant `0` looked perfectly legal — and was in fact every value ever written. The runner already computed a per-key size from `storage.Put` and discarded it. `port.ApifyOutput.ItemKeys []string` became `Items []RawItem{Key, Bytes}` so both producers persist the real size. | `TestRawPayloadBytesRecordsTheSizeTheRunnerReported` and `TestKeywordScrapeRecordsTheSizeTheRunnerReported`. **Bite-verified**: reverting fails with `raw_payload.bytes = 0, want the 291 bytes storage.Put reported`. |
| L-5 | A doc comment claimed an unparseable window date means "no bound" while the handler actually returns 400. Corrected, and it now also records the end-of-day rule introduced by H-6. | — |
| L-6 | `PROGRESS.md:287` claimed "the Actions page Report / Reply buttons now enqueue real jobs". There is no Reply button. The entry is corrected in place, with the reason it mattered: H-1..H-10 were scoped against that claim, and H-3 especially. The testable half is real — the fake composer gained `pressSequentially` and a submit that both renders to the feed *and* clears, which is what finally made the reply path drivable. | **9 new worker tests, 62 → 71**, each bite-verified against 5 distinct mutations. Removing the H-3 guards reddens 2 with `actual: true, expected: false` — before H-3 the reply reported **success** against a login wall. Making the H-2 guard a blanket refusal reddens 7, so it cannot silently decay into a permanent no-op. `dom.ts` was diffed byte-identical to its pre-test state: the real code was never weakened to fit the fake. |
| L-7 | `ActionResult.workerId` was dead — the transport spreads the result and then unconditionally overwrites `workerId` with `config.workerId`, so the value never reached the wire, and 2 of 3 producers filled it with `''` anyway. Field deleted; the transport is now the single source of container identity. | The pre-existing `assert.equal(payload.workerId, 'worker-1')` is now the only mechanism, so it can no longer pass by accident. |

## 7. Incident — repository tests ran against the live dev database

Found while fixing MEDIUM, and disclosed here rather than buried.

A subagent ran the `internal/repository` suite against the running dev Postgres. Those tests `TRUNCATE`. The root cause was in the repo, not only in the agent: `newP3TestRepo`, `newP2TestRepos` and the template helper each **defaulted their DSN to `localhost:24543/smm`** — the dev stack's published port. So `SMM_TEST_DB=1` with no `DATABASE_URL` silently wiped the running database.

**Impact.** Real dev data was destroyed: `action_log` (including the job restored after the earlier disclosed C-2 breach), plus the `account`, `worker`, `post`, `comment` and `target` rows. `app_user` and `audit_log` were not in the truncate list and survived. On user instruction the domain tables were cleared, leaving the database ready for a fresh setup.

**Fix.** `testDSN` in `repo_test.go` now resolves the target for all four helpers and:
- requires `SMM_TEST_DB=1` (skip, not fail, so CI without a database still passes);
- **requires `DATABASE_URL` explicitly** — the `24543`/`5432` defaults are deleted, so there is nothing left to fall into;
- refuses any database whose name does not contain `test`.

Verified: pointing it at the dev DSN now fails with `refusing to TRUNCATE database "smm": the name must contain "test"` **before any connection is opened**. The repository suite passes against a throwaway `smm_test` container, which was destroyed afterwards.

**Lesson.** A skip-flag is not a safety mechanism when the default value underneath it points at production. Every other suite that truncates should be checked for the same default.
