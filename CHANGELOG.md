# Changelog

> Generated from conventional commits. `docs/PROGRESS.md` is the living
> per-ticket log; this file is the human-readable release view. Irreversible
> schema changes are traced here per `docs/DEVELOPMENT_RULE.md` §4.

## Unreleased
### Changed
- **Dashboard visual system replaced with a coastal-glass design.** The command
  centre now uses a floating, labelled navigation rail, a phone dock with the
  four primary destinations, a layered atmospheric canvas, and a quieter
  translucent card surface. The palette is shared by light and dark mode; the
  accent is reserved for primary actions and the active route. ReactBits
  Spotlight Card and Glare Hover provide pointer and gloss feedback without
  running on a loop. The shell adds a skip link, a focus-trapped mobile drawer,
  visible focus rings, and 44px navigation targets.
- **A worker-availability gate now protects the workspace.** After an hour of
  no interaction the shell blocks the dashboard and opens a modal explaining
  that boxd may have hibernated the fleet. `Start all workers` sends an
  authenticated keepalive to every worker, then polls `/api/workers/health`
  every two seconds and unlocks only when every worker answers. The background
  is inert while blocked; there is no dismiss path that bypasses the check.
- **Overview is now a decision page, not an analytics dashboard.** The
  reach/likes/comments KPI strip and the decorative trend sketch were removed.
  The page leads with a ranked attention list (failed actions, account
  sign-ins, offline workers), then fleet readiness, account health, platform
  mix, and the latest real queue entries. Every count comes from the live
  workers/accounts/actions hooks. "Export CSV" became "Open reports", and the
  dead Filter button was removed. Accounts keeps only the columns that fit a
  phone, with auth and status combined below the breakpoint.
- **Add account is an inline modal on Accounts.** The button no longer
  navigates to `/accounts/new`; it opens the same four-step funnel over the
  list: explicit location (Auto or a city), every platform including YouTube,
  credentials/tags, and review. The account list stays behind the dialog, and
  a successful add refreshes it without a navigation.
- **Actions is split into three sections instead of one long page.** Create
  action, Scraped posts, and Job queue are separate panels; the queue tab
  carries the failed-count badge. Queue controls appear only when relevant
  (Retry failed, Run queued, Cancel queued), status labels use operator
  language, and the action type stays visible on phone rows. The job detail
  dialog names the executing worker.
- **UI spacing and surfaces are now one system.** Cards use 20px internal
  padding, pages use a 20px vertical rhythm, and dialogs, selects, inputs, and
  native selects share the same radius, boundary, focus ring, and 44px field
  height. The Actions tabs collapse to a single three-column row at 390px, and
  audited pages keep zero horizontal overflow.
- **Job history now records the worker that executed it.** The pooled dispatcher
  writes `action_job.worker_id` after a worker accepts the push, and the callback
  resolves the worker's hostname to its row UUID before writing `action_log`.
  `ActionJob.workerId` is exposed to the client; the queue row and detail dialog
  resolve it to the worker name (or show a short id after a worker is removed).
### Infrastructure
- **Worker boxes downsized to 1 vCPU / 4 GiB.** All 10 pool workers and
  `smm-login-01` now use `1 vCPU · 4 GiB`; `smm-hub` stays `2 vCPU · 8 GiB`.
  Measured browser footprint after resize: ~0.8 GiB used of 3.8 GiB.
### Fixed
- **Release session now works even after the lease is already gone.** Closing
  a parked login with `worker_id = NULL` returned 204 without settling the
  account, so it stayed `NEEDS_INPUT` with no browser to submit a code into.
  The idempotent second click now marks the login `FAILED` with an explanation;
  the Accounts UI clears the panel and shows a success notice.
- **Boxd hub now serves the same-origin build on both hostnames.** The deployed
  web uses an empty `NEXT_PUBLIC_API_URL` and `/api/stream`, so
  `https://smm.ahdirmai.my.id` and `https://smm-hub.boxd.sh` both call the API
  on their own origin. The stale `smm-hub-01` worker row without an RPC endpoint
  was removed, leaving the health gate at 11/11 awake workers.
- **noVNC live view always receives a ticket.** Accounts no longer falls back to
  a worker's standing `novncUrl` (no ticket → nginx 403 and a blank iframe), and
  the Workers page mints a short-lived worker ticket through
  `POST /api/containers/:id/live-view` before opening the modal. This also
  surfaced the boxd `.env` drift: the hub was still in legacy/docker mode, so
  an account with no lease answered “account has no worker to view”. The hub
  now runs `WORKER_POOL_MODE=stateless` with 11/11 healthy workers.
### Fixed
- **Audit remediation: recovery, reports, and error boundaries.** `restore.sh`
  now accepts the absolute backup path that `drill.sh` passes (no doubled
  prefix), validates the dump/manifest before the drill wipes any volume, and
  the embedded session-count command is parseable by `/bin/sh`. The reports
  page binds each payload to the report kind that produced it, so a slow
  analytics request cannot render a targets payload; the analytics report
  looks the account up by id instead of the first 100-row page; nullable DTO
  mapping, CSV row mapping, URL query building, and SSE type/URL definitions
  each have one source; and an audit query failure no longer leaks its internal
  500 message. The duplicate worker-config test now exercises empty-env
  defaults.
### Removed
- **Monitoring and Templates are gone from the dashboard.** Their routes, nav
  entries, page titles, client hooks, API-client template methods, and e2e specs
  were removed; the backend endpoints remain for data/API compatibility.
### Added
- **YouTube comments now run end to end, on Shorts and watch pages.** The
  worker opens the video, waits for the "View N comments" control (Shorts does
  not render a comment box until it is clicked), scopes the composer to the
  newly created comment row, and verifies the typed text before submitting.
  A verification/challenge page reloads once instead of silently posting
  nothing; the run fails with the observed URL when the composer never
  appears. The API accepts YouTube as an action target and the Actions form
  exposes the URL shape. Only **top-level comments** are supported: the Apify
  actor returns video metadata, not comment bodies.
- **Bulk `Retry failed` in the Actions page.** It retries every FAILED action
  on the current page (not one row at a time), reusing the original text for
  24h, then the AI-generated text, and regenerating only when both expired.
  The button reports retried/skipped counts, and failed retries retain their
  reason.
- **Stored posts no longer trigger a paid re-scrape from the action picker.**
  Selecting an already-scraped post fills the target from the stored row;
  comments are still fetched when the target is a comment.
- **YouTube scrape actor switched to `streamers/youtube-scraper`** for both
  keyword search and on-demand post scrape. Its input uses
  `searchQueries`/`maxResults`/`dateFilter`; the parser normalizes the video
  row and keeps the exact window in the ingestor. The retired `apidojo`
  fields are not sent.
- **Elastic worker pool: the fleet is now sized for concurrency, not for the
  number of accounts.** A worker container holds a headful Chromium for as long
  as it runs (~85 MiB idle, up to ~460 MiB after a session, 4 GiB limit for the
  spike), so a host had to be big enough for every container at once. The pool
  keeps at most `ELASTIC_POOL_MAX_ACTIVE` AUTO workers RUNNING fleet-wide and
  pauses the rest once they have been quiet for `ELASTIC_POOL_IDLE_SECONDS`.
  A PAUSED container costs **0 bytes of RAM** (measured: `docker stats` reports
  `0B / 0B` for an Exited container) and resumes in **~3–5 s** (measured:
  `docker start` 0.39 s + entrypoint + node boot to `control subscriber ready`
  0.37 s), so a 4 vCPU / 8 GiB box can serve a fleet far larger than its memory
  would allow if everything ran at once.
  - **Policy is pure and testable.** `ElasticPool.Decide` is side-effect free:
    it steers only `source=AUTO` workers, treats an empty queue **plus** an
    expired idle window as idle (a worker is never killed the instant it
    finishes a job), ranks the rest busiest-first with a stable tie-break on
    worker id (an unstable order would make two workers trade the last slot
    every tick and flap both), and pauses the over-cap remainder while their
    jobs stay durable in Redis. Covered by 12 table tests.
  - **It writes `desired_state`, never `STOPPED`.** PAUSED keeps the session
    volume so a resume is a `docker start`, not an interactive `runLogin`;
    STOPPED would delete the volume and force a manual re-login. The existing
    reconciler performs the actual start/stop, so a driver outage cannot leave
    the pool half-applied.
  - **Off by default** (`ELASTIC_POOL_MAX_ACTIVE=0`), and requires
    `RECONCILE_INTERVAL_SECONDS > 0`: with the reconciler off nothing would
    carry out the intent. A nil driver also disables it, so local dev and tests
    keep working without a provisioner.
  - Docs: `docs/DEPLOYMENT_ELASTIC_POOL.md` (sizing, deploy, verification,
    rollback, troubleshooting); strategy and measured scale limits in
    `docs/SCALE_MINSPEC_STRATEGY.md`.
- **Keyword search now runs on a working actor, in two stages.** The publisher of
  `crawlerbros/instagram-keyword-search-scraper` was **removed from the Apify
  Store** (the actor and its whole namespace are gone), so every Instagram
  keyword batch failed with `403 insufficient-permissions`. Nothing configurable
  causes that — the actor no longer exists.
  - `APIFY_SEARCH_ACTOR_INSTAGRAM` is now
    `sasky/instagram-keyword-posts-urls-scraper`. It returns **post URLs only**,
    so each URL is enriched by `apify/instagram-post-scraper` (the same actor the
    single-post scrape uses) for its caption and metrics. Deduped by URL, because
    the same post can rank under two keywords and paying twice is the waste the
    pipeline exists to avoid.
  - The two stages are independently best-effort: an unenrichable post (deleted,
    private) is skipped rather than stored blank.
  - The search actor's `date`/`from`/`to` inputs **fail at runtime** ("Context:
    Parsing date..") in every form tried, so the date window is enforced by the
    ingestor (`inWindow`) — the same contract the previous actor had.
- **Posts are ranked by insight.** `GET /api/scrape/recent?ranked=true` scores
  every post and the batch page renders the order with a rank badge, the score,
  and the three engagement numbers, plus an Insight / Interaksi / Terbaru toggle.
  - Score = `log(1 + engagement) × per‑follower efficiency × freshness`. Log-scaled
    so one viral outlier cannot flatten the rest of the list to ties; per-follower
    as a multiplier so a small account can outrank a large one on equal absolute
    numbers; freshness halving every 72h. Ties break by engagement then id, so
    "the top post" does not move between identical requests.
  - The score is **shown**, not hidden, so an order can be explained.
- **Scraped-post preview shows captions, thumbnails and per-post metrics** instead
  of a thin `@handle · metrics` line, and each card's comment button now carries
  the sentiment distribution and the threaded comments (same reading model as the
  Actions card).

### Fixed
- **Boxd deployment: the login worker and live view were unreachable.** The
  pool bootstrapper appended `DefaultLoginEndpoint` even when
  `WORKER_POOL_ENDPOINTS` already named `smm-login-01`, overwriting the boxd
  `https://rpc…` URL with `http://smm-login-01:8090` (not resolvable off the
  compose network); it now only seeds a login worker when the name is absent.
  The noVNC URL also emitted `/websockify` (noVNC prepends `/`, producing
  `//websockify`, 404); it is relative and keeps the page directory, and the
  worker nginx copies `$arg_ticket` into a variable for `auth_request` because
  the directive does not interpolate variables in its URI. Verified live:
  accounts/sessions imported, RPC health on all 11 workers, ticketed
  WebSocket 101, and a browser noVNC connection.
- **YouTube comment actions failed on live Shorts and watch pages.** The
  "View N comments" selector was wrong, the editor lost focus after the first
  keystroke (leaving only a prefix), and the nav budget was shorter than the
  real page load. The worker now clicks the comments control, types in the
  focused contenteditable, asserts the full text, and allows 90s for
  navigation + 45s for the composer. A checkpoint reload is retried once.
- **Instagram comment/reply retries were not idempotent.** The Indonesian
  composer selectors (`Kirim`, `Balas`) and verification page were not
  matched, so a retry could navigate away from a comment that had already
  posted. Selectors now cover the Indonesian UI, the verification reload
  keeps the run on the same action, and a retry detects the existing comment
  before typing again.
- **Manual retry of a FAILED action 404'd.** The API wrote the human-readable
  status while the retry filter compared the enum spelling, and the wire
  values had drifted. Status casing is now normalized at the boundary
  (`FAILED`, `RUNNING`, …) and the UI renders labels without changing the
  contract; retry accepts the same FAILED rows the list shows.
- **A refused action no longer consumes a rate-limit token.** The limiter
  returned `allowed=false` but still recorded the hit, so repeated refusals
  could exhaust the window; the token is now returned when the action is not
  dispatched, and the retry path is covered by tests.
- **The worker live-view modal could block for 120s.** Opening the modal
  waited on a `/healthz` probe even after the iframe was ready; it now opens
  immediately and falls back to the probe only when the noVNC URL is absent.
- **AI model routing pointed at `nexa`, which is no longer available.** The
  comment generator now routes to the configured `rvr` model, so generated
  replies and the 503 path are exercised against a live model.
- **The worker fleet can no longer outgrow its host: a paused row no longer
  boots a container, and creates are hard-capped.** Live incident: 77 worker
  containers ran on a 16 GiB box until the kernel OOM-killed `postgres` and
  `minio` (`Exited 137`) and the API could not start, with load average 39.8 on
  8 CPUs. Four independent causes; all four are closed.
  - **`diff()` created a container for every PAUSED row.** `PAUSED + absent`
    returned `ActionCreate` "so a later resume has something to start". The
    packer seeds 454 accounts as `AUTO/PAUSED`, so the first tick after a
    restart materialised the entire fleet at once. Absent is now in sync: the
    container is built on demand, when the waker actually needs it.
  - **Nothing capped concurrent creates.** `ELASTIC_POOL_MAX_ACTIVE` only
    limited RUNNING *rows*, and `paused-but-absent` never consulted it. The
    reconciler now takes the same number as a hard ceiling on containers
    actually running: one exact `ListRunning` per tick, a slot spent per
    create/resume and returned on stop/delete. A failed count admits nothing.
  - **`StartWorker` now provisions when the container is absent**, so on-demand
    materialisation works: the first wake of a never-used account builds its
    container instead of failing `container not found`. Docker and k8s both
    (`k8s` also ensures the session PVC, since a paused row may own none).
  - **Restart policy was `unless-stopped`, handing the daemon the fleet's
    lifecycle.** Crashed workers restarted themselves indefinitely — 10 restarts
    each — with the API dead and nothing able to stop them; `docker stop` alone
    did not stick. Now `on-failure:3`: the reconciler and waker own start/stop,
    the daemon only retries a genuine crash.
  - `ListRunning` reports running containers only (a stopped one holds a row but
    spends no RAM, so counting it kept the cap permanently full) and reads the
    shared fleet snapshot instead of a fresh `/containers/json` per worker.
- **Threads login from the worker app works again, and a Threads reply now
  targets the right comment.** Both were live failures, each with a cause
  unrelated to the other.
  - **Login: the credential autofill raced the SPA.** `firstPresent` checked the
    login form once at `domcontentloaded`. Threads serves `/login` as a
    client-rendered SPA — at that moment the page is still the "Scan to get the
    app" shell, and the fields mount **~3–10s later** (measured live: 9.7s).
    Every dispatch logged `login form field not found` and fell back to the
    operator. The probe now polls up to 20s. Verified in the worker container:
    `fillMetaCredentials` succeeded after 20.5s and typed both values.
  - **A hydrated session no longer pays that wait.** On the scheduled-refresh
    path the stored session is already proof; the login page redirects it away,
    so the form never appears and the old code sat out the whole mount bound.
    `runLogin` now probes the hydrated context first and skips the fill when it
    is already proven.
- **Threads comment permalinks were the Instagram shape, which 404s.**
  Threads has no `/post/<post>/c/<comment>/` route (verified live against
  threads.com — 404, no composer). A Threads reply **is** a post with its own
  shortcode, so a comment's addressable permalink is `/post/<comment code>`
  (`comment_permalink.go`). `Enqueue` validates per platform via
  `replyTargetID`: IG still requires `/c/<id>/`, Threads accepts exactly
  `/post/<code>` and rejects the IG-shaped URL with a hint that says which shape
  is expected.
- **A Threads per-post scrape stored zero comments.** The live actor returns
  every reply row with `is_reply: true` and `parent_post_id: null` (verified on
  run `x2Ts33gUPTXBNd6GT`: 24 rows, all null) — the run's post-id map has
  nothing to resolve, so the reply branch fell through and the rows were
  dropped. In a single-post scrape (`onlyCode` set) every reply row can only
  belong to the post that was asked for, so the parent now falls back to that
  shortcode.
- **A reply can be aimed at a pasted comment link.** The Reply flow now accepts
  the platform's own "Copy link" output as the target — the shortest route, and
  the only one for a comment the scrape did not deliver. The picker clears a
  pasted link when a row is chosen (and vice versa) so what is visible is what
  ships; Queue stays disabled with no target at all.
- **Enriching one post failed with `max-total-charge-usd-below-minimum`.** The
  sync endpoint's `maxItems` is a CHARGE cap on a pay-per-event actor, and
  requesting 1 rounds below Apify's $0.005 minimum, so the run was rejected with a
  400. A batch would then find 15 URLs and store **0 posts**. `runSync` now floors
  `maxItems` at 10. Verified against the live API: `maxItems=1` → 400,
  `maxItems=10` → 201.
- **A negative metric ranked a post below zero.** The actor emits `likes: -1` when
  Instagram hides the count. Physics says a like count cannot be negative, so it
  now reads as "not reported" (0) rather than a small number — previously such a
  post sorted below every zero-engagement post and printed `Likes -1`.
- **The keyword search no longer dies on `context deadline exceeded`.** The search
  actor drives a real browser and takes ~40–60s PER KEYWORD, which does not fit in
  one `http.Client.Timeout` on the sync endpoint — the batch failed while the run
  was still healthy on Apify, paying for a run it then discarded. The search path
  now starts the run asynchronously and polls, with its own 15-minute budget
  (`searchPollBudget`), separate from the Threads scrape's 4 minutes.
- **The New-Action account list is grouped by worker location.**
- **Scraped posts and comments now carry an LLM sentiment label.** Every comment
  is labelled POSITIVE / NEUTRAL / NEGATIVE at ingest (migration
  `000019_sentiment`, additive + reversible), and the dashboard renders a
  distribution plus a per-comment badge, so an operator can see the mood of a
  thread instead of reading every line.
  - **One LLM call per batch, not per comment** (cap 40, `MaxSentimentBatch`),
    so a post with hundreds of comments costs one round trip, not hundreds.
    Comments already labelled are skipped, so a re-scrape is free.
  - **Unanalysed is a real state, not a default.** A comment with no verdict
    stores NULL and renders no badge; the distribution reports
    "`N` of `M` analysed". Treating a missing label as NEUTRAL would tell the
    operator a machine read the comment calmly when nothing read it.
  - **Bad model output is dropped, never coerced.** An unknown label is
    discarded rather than folded into NEUTRAL, and a score outside 0..1 is
    dropped while its label survives. An absent `score` stays absent — decoding
    it into Go's zero float would have stored a fabricated 0.0 confidence
    (found by test, `TestSentimentDropsOutOfRangeScoreKeepsLabel`).
  - Analysis is best-effort end to end: an unconfigured or failing LLM leaves
    rows unlabelled and never fails a scrape the operator already paid for.
- **Comments are threaded in the dashboard.** A reply renders nested under the
  comment it answers, in both the scrape card and the Reply picker. The tree is
  built from `parentId` at render time — one source of truth, nothing
  denormalised. A reply whose parent fell outside the page limit is shown
  top-level rather than hidden (it is real content), and the walk is
  depth-bounded so a malformed row cannot hang the render.
- **Nested replies can now be ingested** (`parseIGComments` recurses). The actor
  we use today returns `replies: null` on the free plan, so this is inert until
  Apify is upgraded — but it is correct and tested now, so the upgrade needs no
  code change. `comment.parent_id` is now written via COALESCE, which keeps an
  existing link when a payload has none instead of erasing it.
- **Reply to a specific comment from the dashboard.** The New-Action form
  gains a third flow beside Comment and Like. Pick Reply, choose one of the
  scraped comments, and the job targets that comment's own permalink
  (`/p/<code>/c/<id>/`) instead of the post — which is the only input the API
  accepts for a reply (`action.go` rejects a bare post URL, and the worker's
  `replyToComment` refuses when the page did not land on that comment).
  - `GET /api/scrape/posts/{id}/comments` and the scrape card now return a
    derived `url` per comment. It is **not stored**: `comment.external_id`
    already holds the id Instagram puts in the `/c/<id>/` segment, and
    `post.external_id` is the shortcode, so the permalink is pure derivation.
    A comment whose post code is unknown yields an empty `url`, and the form
    then offers no Reply at all rather than a URL the server would reject.
  - On Instagram the reply box pre-fills an `@username` mention and the
    worker **preserves** it, so the reply tags the person it answers. Verified
    live: the commit echoed `"@rackytampubolon setuju, roadmap dulu baru janji"`.
  - **Two defects found and fixed after live failures:**
    1. **Meta's "Your Meta Account is here" modal blocked every action.** Seen
       on two separate accounts/workers: the full-screen consolidation dialog
       renders on an ordinary post load and its scrim swallows every click, so
       the reply flow opened no composer and failed as *"no @mention
       prefilled"* — blaming the selector for an unclickable page. `openPost`
       now dismisses it on every exit path (`dismissMetaAccountModal`),
       best-effort: absent modal → no-op, and it can never fail a job.
    2. **The mention check accepted any `@handle`, so the wrong tag passed.**
       A *top-level* composer pre-fills the **post author**; a threaded reply
       box pre-fills the **comment author**. Asking only "does it start with
       @" meant a reply could tag the post author and still be recorded as
       threaded. The BE now resolves the target comment's author and sends
       `expectedMention`; the worker requires the prefill to name that person
       (case-insensitive, `@`-tolerant). An empty expectation still passes, so
       a rolling deploy cannot refuse every reply.
  - The mount bound was **6s, measured from one sample**. Polled again live and
    the affordance took **~9.8s**, so the 6s bound still failed every reply.
    It is now 12s, and expiry is not a failure. A bound drawn from a single
    measurement is a guess.
  - Spam posture is unchanged and inherits every existing gate, because a
    reply rides the same enqueue → cooldown → rate-limit → pace pipeline as a
    like or a comment: per-(account,target) 60s cooldown, per-platform hourly
    budget (IG 30/hr) and the per-account pace (`ACTION_ACCOUNT_PACE_SECONDS`,
    480s). None of these are new or relaxed for replies.
  - **Deployed to the worker fleet.** `smm-worker:latest` was rebuilt (the
    running containers were from 2026-10-05, before the reply work) and all 15
    workers recreated. The deploy is a `worker.generation` bump, not a
    `docker restart`: `CreateWorker` binds the session volume by worker ID
    (`smm-session-<id>`), so the reconciler tears the container down and
    brings it back on the new image **with the stored Instagram session
    intact** — verified: all 10 account-holding workers kept a live
    `sessionid`. A pre-deploy backup of all 22 session volumes plus the DB
    was taken first (`infra/backup/pre-worker-deploy-*`, gitignored).
- **The New-Action account list is grouped by worker location.** Picking
  accounts for a batch now shows one section per location the accounts run
  from (city, falling back to region; unassigned last), so an operator can
  see "Jakarta (3) / Bandung (2)" instead of one flat stack. The grouping
  comes from the existing `GET /api/accounts/by-region` endpoint — no new
  API surface; a fetch failure keeps the flat list, so grouping can never
  block enqueueing.
- **AI comment drafts have an intensity dial.** The "AI generate" button
  gains a three-level selector — soft / medium / bold — orthogonal to the
  support/counter stance: the same stance can be written safely or pointed.
  `POST /api/scrape/comments/generate` accepts `intensity` (empty = medium,
  unknown = 400); the level only steers the LLM prompt, and the denylist
  screen and length cap apply to every level, so a bold draft can never ship
  a banned word. Medium is byte-identical to the pre-intensity prompt, so
  callers that do not pick a level get exactly the old behaviour.
- **Keyword batches now run on seven platforms: YouTube, LinkedIn, X and
  Facebook join Instagram, Threads and TikTok.** One batch per platform per
  keyword set, four new Apify actors chosen by measured cost and verified
  against live rows before a line of parser was written:
  `apidojo/youtube-scraper-api`, `harvestapi/linkedin-post-search`,
  `apidojo/twitter-scraper-lite`, `danek/facebook-search-ppr`.
  Two cheaper-looking actors were rejected on their real input schema:
  `worldunboxer/rapid-linkedin-scraper` ($0.0005, the cheapest LinkedIn actor
  in the store) scrapes **job boards** — its inputs are `jobs_titles`,
  `cities` and `employment_level`, and it has no post search at all; and
  `apify/facebook-posts-scraper` (115k users, the most proven actor on
  Facebook) takes page and profile **URLs**, not keywords, so it cannot serve
  a keyword batch. `docs/platforms/*.md` §4 records both.
  ⚠ **YouTube and X also bill ~$0.015–0.016 per query** on top of per-result
  pricing (measured: a 9-row YouTube run $0.0185, a 10-row X run $0.020). That
  fee is invisible on the store page, which advertises the per-result rate
  only, so a 5-keyword batch on either platform costs ~$0.08 before a single
  row arrives. **X has no result cap and no date filter in its schema at all**,
  which makes it the most expensive platform per keyword set rather than one of
  the cheapest — its window is enforced entirely in the ingestor. LinkedIn's
  `maxPosts` must be sent, and as `maxPosts`: sent as `maxItems` the key is
  ignored, and one measured run returned 97 rows for $0.096 against a request
  for 5.
- **A platform that cannot serve a keyword batch says so from the platform
  list, not from a hand-typed string.** `scrape_keyword.go` had a literal
  "supports instagram, threads and tiktok only" that went stale on every add.
  `runner.go`'s `searchInput` likewise became a `switch` that returns `nil` for
  an unknown platform: the old if-chain silently sent the Instagram payload to
  any platform missing from it, and an unknown key is answered 200 with the
  actor's own default — invisible, where a missing branch is not.
- **`/monitoring` reads real Instagram metrics through Composio.** The
  analytics provider was a deterministic stub, so the page had never shown a
  figure a social media team could act on. `apps/api/internal/adapter/
  analytics/composio.go` implements the existing `port.AnalyticsProvider` seam
  against Composio's v3.1 `tools/execute` endpoint — the stub's own doc comment
  said this swap would touch that package only, and it did. One
  `INSTAGRAM_GET_USER_INSIGHTS` call returns reach / views / profile_views /
  accounts_engaged / total_interactions plus likes, comments, saves and shares;
  followers come from `INSTAGRAM_GET_USER_INFO`, because Instagram silently
  drops `follower_count` from the insights response. A metric Instagram does
  not report stays `NULL` and renders `—`; it never becomes `0`.
  Selected with `ANALYTICS_PROVIDER=composio` + `COMPOSIO_API_KEY`. A monitored
  account binds to a Composio connection through the new
  `official_account.provider_ref` (`ca_...`, exposed as `providerRef` on
  `POST /api/official-accounts`). **Instagram only**: Facebook follows
  (`FACEBOOK_GET_PAGE_INSIGHTS` exists), but **TikTok publishes no insights
  tool in Composio at all** — only `GET_USER_STATS` (followers, likes, video
  count) — so TikTok reach cannot come from this connector.
- **The `/monitoring/:platform` trend reports a period-over-period change.**
  "Reach naik atau turun?" is the question the page exists to answer, and it
  could not. The page now requests twice the window it charts, so the
  preceding period arrives in the same response, and shows the total plus the
  percentage change against it. A previous period that sums to zero renders as
  "no earlier period to compare against yet" rather than a fabricated +100%.
- **`/monitoring/:platform` now breaks interactions down, and lists posts.**
  Two gaps, one cause: the adapter fetched more than the page could show. The
  KPI strip could only say "14 interactions" — one opaque number — while
  `likes`, `comments`, `saves`, `shares` and `accounts_engaged` sat unread in
  the snapshot's `metrics` blob, and the per-post table had been deleted as
  dead UI. `GET /api/analytics/{platform}` now returns a `breakdown` array
  parsed from that blob (plus an interactions-per-reach rate, computed, and
  null when reach is unknown — a post nobody saw has no rate, and 0% would
  read as "published and ignored"). Those parts are promoted out of JSON
  rather than into columns: a provider reporting one more interaction type
  would otherwise need a migration.
- **Per-post metrics, Instagram only.**
  `GET /api/analytics/{platform}/content` returns one row per post — caption,
  permalink, content type, publish date, reach / views / interactions /
  likes / comments / saves / shares and an engagement rate — fed by a new
  `analytics_content_metric` table written by every ingest (10 posts per
  account per pass, one Composio call each). Backed by
  `port.AnalyticsProvider.FetchContentMetrics`; the stub deliberately returns
  nothing rather than inventing captions, because a per-post table of
  fabricated posts is indistinguishable from a real one. Media insights are
  **lifetime totals**, so the table is a gauge refreshed in place, not a time
  series — hence an upsert on `(account, media_id, provider)` and no
  hypertable. One metric set serves feed posts and reels alike:
  `profile_visits`/`follows` are account-scoped and asking a REELS for them
  fails the whole call, `impressions` is deprecated by Instagram and
  `replies` is rejected by carousels. A content-fetch failure fails the
  account run rather than going quiet — a table that silently stops
  refreshing looks exactly like one where every post is dead. **TikTok is
  doubly blocked here**: no per-post insights tool at all, and the reach it
  would need is unobtainable too.
- **The operator's "Log in" now fills the credential form automatically.**
  The `auth-login` dispatch unseals the account's stored password once
  (`account.password_enc` gains its single narrow reader,
  `GetAccountPasswordEnc`) and rides it on the private per-worker control
  channel — the same wire that already carries 2FA codes — so the worker's
  headful browser autofills the login form before the cookie poll runs. A
  fill failure (checkpoint wall, markup change) never aborts the login: the
  operator can still type in the noVNC view, and the cookie poll remains the
  only verdict. The plaintext crosses exactly one hop and lives in the
  worker's memory for the login only; no API log, control log line, read
  model or response ever carries it. The P2-06 session refresher reuses the
  same delivery, so a dead session re-logs in with no human in the loop
  instead of parking at `needs_input` until an operator sits in the live
  view. The Meta adapters resolve the identifier field by candidate
  selectors (`username` → `email` → `input[type=text]`) because Meta
  re-names the attribute between rollouts.
- **"Open profile" opens in the account's worker browser, not the
  operator's tab.** The row and panel buttons now dispatch
  `POST /api/containers/{id}/browser` (documented in the OpenAPI contract as
  `openContainerBrowser`): the API SSRF-guards the URL to the account's
  platform hosts, publishes `browser-open` on the control channel, and the
  worker navigates its live, session-hydrated Chromium on the Xvfb display —
  the operator watches the logged-in profile in the noVNC live view instead
  of a logged-out tab on their own machine. The worker keeps one live-view
  context across calls, so repeated opens navigate one window rather than
  stacking Chrome windows. An account without a worker or live view falls
  back to the operator tab; a failed dispatch does the same.
### Changed
- **TikTok scraping moved to `xmolodtsov/tiktok-search-scraper` — 83× cheaper
  per post that is actually stored.** The old actor billed **$0.0037 per
  result on the free tier**, not the "$1.70 / 1000" its store page advertises
  (that is the GOLD-tier price, and this account is on FREE). A live run
  returned a 50-row page and billed all of it at **$0.186**, of which the
  ingestor kept **3** rows. Cost per usable post: **$0.062**.
  - The reason for the 94% waste is the actual subject of this change: the
    payload sent `oldestPostDateUnified` and `newestPostDate` on the strength
    of a code comment asserting this actor "CAN filter server-side … under
    `profileSorting: latest`". **That actor has no date field of any kind in
    its input schema.** Both fields were accepted and discarded, silently, and
    a full page was billed against a window nothing narrowed. A payload test
    proves what we sent; it cannot prove the actor obeyed it, and a comment
    asserting an actor honours a field is not evidence that it does.
  - The replacement bills **$0.0003 per result** and returns a 10-row page for
    the same keyword, so a real batch cost **$0.003 for 4 stored posts** —
    **$0.00075 each**. `maxItems` is a genuine cap there, so it is the ceiling
    on what a run can cost; the old `resultsPerPage` was a page size and
    bounded nothing.
  - The parser is rewritten for the new row shape, and the swap is a net
    deletion: `errorCode` handling, the `mediaUrls` loop, the `anyToString`
    round-trip on the author id and the three dead date fields are all gone.
    `bookmarks` is stored under the existing `collects` key so saved posts keep
    their value. Cover and thumbnail now arrive on every row with no billed
    flag, so the media problem fixed in the previous commit cannot recur.
  - The test fixture is a **verbatim row captured from a live run**, because
    the last TikTok parser was written against a hand-invented row and shipped
    a bug for months: the fixture asserted a `mediaUrls` entry the actor only
    emits behind a billed flag, so the suite agreed with the bug.
  - `sortType: DATE_POSTED` is documented as sorting newest-first but was
    measured returning the same unsorted pool as the default, so it is not
    sent. A field that reads like a filter and is not one is the same
    silent-wrong-answer shape this fix is about.
  - ⚠ **This actor is less battle-tested than the one it replaced**: 30,517
    users and **1 review**, against 294,682 users and 370. If a keyword batch
    looks thin, re-run it before trusting the count.

### Fixed
- **AI comment drafts no longer come back with em dashes.** The model read them
  as machine-written. The comment prompt now forbids em and en dashes in both
  the user and the system prompt, and the parser rewrites any that still
  arrive: a dash between digits becomes a hyphen (`2019—2021` → `2019-2021`),
  every other dash becomes a comma (`bagus — serius` → `bagus, serius`). The
  third guard means a later prompt edit cannot silently let them back into
  shipped comments.
- **The single-post "Scrape" button no longer pays for a run when the post is
  already scraped and fresh.** It always sent `force: true`, so re-scraping a
  post within the 6h window re-bought the same data from Apify every time. The
  button now defaults to `force: false` and the 6h freshness cache is actually
  used: a post that is fresh *and* whose comments were fetched is served from
  storage for free, and the card says "served from storage" with a separate
  **Re-scrape** button for an intentional paid run.
  - The cache had a hole that forced the old `force: true` hack: a keyword
    batch stamps `post.scraped_at = now()` for every post it ingests, but the
    search actor returns no comment bodies — so a just-scraped post looked
    "fresh" and the cache would answer "no comments" as if it were a real
    answer for six hours. A new `post.comments_fetched` column (migration
    000018, additive + down-migration) separates "we looked at this post"
    (`scraped_at`) from "we fetched its comments". A permalink scrape always
    fetches comments and marks the post `comments_fetched = true`; a
    keyword-batch ingest never does, so its rows stay `false` and the cache
    correctly declines to serve them. Rows that already have stored comments
    are backfilled to `true`.
- **AI comment generation 502'd on any batch of ~3+ drafts.** The LLM client
  sent a flat `max_tokens: 1500`, but the configured gateway fronts a
  REASONING model that spends its thinking tokens before the answer — and the
  thinking grows with how many variants are requested (measured: ~300 for one,
  up to ~2000 for ten). For `count >= 3` the flat budget ran out inside the
  thinking, the completion came back `content: null` with
  `finish_reason: "length"`, and the request surfaced as a 502 "scrape failed"
  (or, when a sliver of answer survived, a 200 with a truncated comment). The
  budget is now sized per request in the service — `500 + 300 × count`,
  clamped to 16000 — and passed through the `LLMCompleter` port; the adapter
  keeps a hard cap (16384, the gateway's largest accepted value) as a
  caller-bug guard only. A ceiling costs nothing for small counts: the model
  stops early when it is done.
- **AI comment generation switched to `nexa/claude-haiku-4.5` — ~8s instead of ~30s.**
  The previous model (`nexa/glm-5.3-flash`) still spent a reasoning pass before
  answering, so a 10-draft generation took ~30s. The Claude Code-style model
  answers directly, in ~8s, with equivalent output. The gateway delivers it as
  an SSE event stream even for a non-streaming request, so the LLM client now
  reassembles the delta content across events (and strips inline
  `<thinking>…</thinking>` blocks a reasoning model can leak into the answer).
  Both shapes are covered by tests; the single-JSON path is unchanged.
- **AI comment drafts gained a length picker and a 5-step intensity slider.**
  The dashboard's "AI generate" row now has a Short / Medium / Long select and
  a 1-5 slider (Gentle → Sharp, 3 = balanced) instead of the old three-button
  soft/medium/bold control. `POST /api/comments/generate` accepts `intensity`
  as an int (0-5, 0 = default 3; out of range = 400) and `length`
  (short/medium/long; empty = medium; unknown = 400). The prompt also now asks
  for human-like phrasing: varied openings so no two candidates start the
  same way, and everyday Indonesian abbreviations and light slang ("nggak",
  "bgt", "sih") where they fit.
- **The noVNC live view clipped the remote screen.** noVNC defaults to
  `resize=off`, so the 1280×800 Xvfb framebuffer rendered at native size and
  got cut off at the iframe edge. The live-view URL now sets `resize=scale`,
  which fits the whole remote display into the frame (aspect-preserved, no
  letterboxing — the display and the frame share the 16:10 ratio).
- **No scraped TikTok post ever stored its media.** The parser read
  `mediaUrls`, which the actor documents as an **empty array** unless a
  `shouldDownload*` flag is on — and those flags are separately billed events
  this integration deliberately does not pay for. The free media, the
  thumbnail, was in `videoMeta.coverUrl` in the very same row, unread. Every
  TikTok post stored an empty list and the detail dialog rendered nothing at
  all. The parser now falls back to the cover, preferring `originalCoverUrl`.
  The test fixture is why this shipped: it asserted a `mediaUrls` entry the
  actor never emits on a default run, so the suite agreed with the bug. It
  now matches the actor's published output sample, and covers both paths —
  a downloaded run keeps its downloads, a row with no `videoMeta` survives
  with no media rather than being dropped.
- **The post detail dialog said nothing when media was missing.** A post with
  no media and a post whose media the provider never returned looked
  identical, which is how the above went unnoticed. It now says which one
  it is.
- **Deleting an official account did not remove it from `/monitoring`.**
  `ArchiveOfficialAccount` sets `status = 'ARCHIVED'` — a soft delete, which is
  the right call for an entity whose snapshots stay queryable as history. But
  `ListOfficialAccounts`, `ListOfficialAccountsByPlatform` and
  `CountOfficialAccountsByPlatform` had no status filter at all, so an archived
  account kept rendering in the monitored list, kept being counted per
  platform, and could never be re-added: the unique index on
  `(platform, handle)` plus the `GetOfficialAccountByHandle` pre-check made
  every subsequent `POST` for that handle a 409. The three queries now exclude
  `ARCHIVED`, matching what `ListOfficialAccountsForIngest` already did.
  `PAUSED` stays listed — paused means "monitored but not ingesting", and
  hiding it would make the account impossible to find and resume.
- **The e2e suite leaked official accounts into the database.** Its global
  teardown swept worker accounts, users and templates, but nothing on the
  official-account entity, so every monitoring run left rows behind. The sweep
  now covers them, and the spec's handle prefix matches the teardown's
  `e2e_` prefix — the two had drifted apart, so even a matching sweep would
  have found nothing.
- **The daily trend summed a gauge across time as well as across accounts.**
  `AnalyticsTrendByPlatform` grouped by day and `sum()`-ed the metric, so a
  provider reporting a *value for the period* (rather than an increment) had
  one day multiplied by the number of ingests in it. A 30-minute cron wrote 48
  copies of the same daily reach and the chart showed 48× the real figure. The
  per-account step now takes `max` — one value per account per day, its latest
  reading — and the outer query sums those across accounts, which is the only
  axis that should add up. `AnalyticsTrendByAccount` takes the same guard.
  `AnalyticsOverviewByPlatform` already used `last()` and was unaffected.
- **The Composio snapshot's `ts` is truncated to the UTC day.** Instagram's
  `total_value` for `period: day` is a running total for the current day, not
  an increment. Storing one row per fetch would write the same day repeatedly;
  day-truncated `ts` lets the existing `(official_account_id, ts, provider)`
  upsert collapse each day's ingests into one row that keeps refreshing, which
  is exactly what the daily query sums. The trade-off is stated in the adapter:
  the current day's point is a partial day, so the last point of the trend is
  always lower than the ones before it.
- **`/monitoring` "Total followers" added up every metric.** The API returns
  one KPI row per (account, metric), and the card reduced over all of them, so
  followers + reach + views + engagements + profile_views were summed and
  labelled "followers". Each card now totals its own metric.
- **The `/monitoring/:platform` KPI strip was always empty.** The account
  filter built `new Set(null)` for the default "All official accounts" — an
  empty Set, which is truthy, so the `accountIds ? filter : keep-all` ternary
  always took the filter branch and dropped every KPI. The default view now
  shows the platform's accounts.
- **The trend chart's legend named a second series that was never drawn.**
  `TrendChart` supported a `compare` prop, but no caller passed it, so the
  legend advertised a line the chart did not contain. The metric is now a real
  control — one series per request, chosen by the reader — and the unused
  `compare` path, `seriesB`, and `postKindLabel` are gone.
- **Four of six Instagram KPI cards could only ever render `—`.** The
  per-platform config invented metric keys (`reels_views`, `saves`,
  `comments`, `profile_visits`) that no provider populates and the trend query
  cannot even select. The keys are now the six scalars the snapshot table
  actually stores, and only the labels and order vary per platform.
- **The "Top posts" table was permanently "Not yet enabled in the MVP".** A
  table with column headers and no data source. Removed; per-post insights
  need a media-id round trip that does not exist yet.
- **The per-account "Followers" column showed whatever metric came first.**
  `kpis.find((k) => k.officialAccountId === a.id)` had no metric predicate, so
  the column rendered reach whenever followers was absent. The overview table
  now shows reach, views and followers, each looked up by metric.
- **`post` now stores the date a post was published (migration
  `000016_post_published_at`).** `post` carried exactly one timestamp,
  `scraped_at`, which is `now()`. The actor payload's publish date
  (`pub_date`, `timestamp`, `posted_at`, `taken_at`, `created_at`) was
  read, used to evaluate the operator's window, and then discarded —
  `scrapeItem` had no field to carry it and no column to land it in.

  The symptom was not a wrong number but a plausible one: the batch
  detail page rendered `scrapedAt` with no label, so every post in a
  batch read as the day the batch ran. A batch scraped on 2026-09-29
  with a 2026-09-01 → 2026-09-29 window showed 27 posts all dated
  29 September, while the actor's own dataset showed a real spread of
  4–28 September. It made recency unsortable, made "posts from the last
  N days" unanswerable from the database, and made the window filter's
  own result unauditable without re-reading the raw payload.

  `published_at` is nullable on purpose: existing rows are
  unbackfillable — the dates are gone, not hidden. `scraped_at` remains
  the fallback and is now labelled `scrape …` in the UI rather than
  silently standing in for a date we never had. `ListPosts` and
  `ListRecentPostsByPlatform` order by
  `published_at DESC NULLS LAST, scraped_at DESC`.

### Scrape

- **Threads per-post actor swapped to `themineworks/threads-scraper`.** Both
  predecessors were broken: `futurizerush/meta-threads-scraper` 404s (a
  user-posts-only build with no per-post mode) and `pro100chok/threads-scraper-usage`
  runs async-only. `includeReplies: true` is load-bearing — replies are
  excluded by default and are the only source of Threads comments.
- **Fixed the loss of 19 of 50 posts in a keyword batch.** The actor flags
  `is_reply` on a row whose parent it never delivers (keyword mode marks a
  reply without fetching the parent, and `replied_to_post_code` arrives
  empty). The parser dropped those rows as orphans; they now fall back to
  top-level posts. Verified by replaying the live dataset: 50/50 kept.
- **Share/quote context rows are no longer stored as posts.** Post mode
  returns `Dengan <name> – Saya baru saja diakui…` rows around the requested
  post with no field separating them from it. The ingestor now keeps only
  the shortcode named by the requested permalink.
- **`items_read` no longer reports the stored count.** It and `posts_count`
  were both written from the upsert count, so every window drop was
  invisible on the batch dashboard.
- **`apify_run.run_id` is now persisted.** `UpdateApifyRun` never wrote it
  despite both callers passing it — every row in the running stack was
  blank, so an in-flight run was unfindable on the Apify side.

### Actions

- **Operator queue controls:** retry a FAILED row, pull the backoff queue
  forward (`/actions/run-pending`), and `action-updated` SSE frames.
- **`RunPendingNow` sweeps the queue instead of capping at 50.** A cap would
  move the first 50 jobs and report "moved: 50" while the tail kept waiting
  on its timer.
- **LLM client sized for reasoning models** (timeout 60→180s, max_tokens
  300→1500); tolerates SSE-shaped bodies from OpenAI-compatible gateways.
- **`TRANSIENT` covers "not found" and "did not change"**, so a deleted post
  stops being retried forever.

### Accounts / fleet

- **Scheduled session refresh (P2-06).** Opt-in via
  `SESSION_REFRESH_INTERVAL_SECONDS`; three consecutive login failures set
  `QUARANTINED`, keeping the account out of the packable pool.
- **Accounts page redesigned for fleet readiness**, three interaction bugs
  fixed, per-platform metric vocabulary, batch detail dialog with
  stored/paid comments, scroll-snap carousel.
- **The queue header now shows the backoff timer** when jobs are parked — a
  settled queue can still hold PENDING rows waiting one out.

### Worker

- `runLogin` hydrates the persisted session (the refresher otherwise parked
  every account on `needs_input`).
- Image mirror falls back to a local registry; X probe checks the unix socket
  instead of a process name; healthcheck covers the image too.

### Infra / security

- **Backup output is gitignored.** A logical dump carries `COPY` blocks for
  `app_user` (password hashes), `auth_session` (live session tokens) and
  `account` (platform credentials); the bundles carry the worker sessions
  volume as a tar. The scripts stay tracked, only their output is ignored.

### Schema (irreversible — traced here per rule §4)

- **000015 `session_refresh`** — `account.cookie_expiry_at` (nullable
  timestamptz) gains a writer and a failure streak; 3 failures set
  `account.status = QUARANTINED`. Down migration drops the column and resets
  quarantined accounts to their prior status.
- **000014 `keyword_batch`** — the batch table for async keyword scrape.
- **000013 `keyword_scrape_apify_nullable`** — `apify_run.run_id` nullable:
  the row is created before the Apify-side id exists.

### Documentation

- `docs/platforms/threads.md` and `instagram.md` rebriefed to the current
  actors; `docs/BATCH_SCRAPE.md` actor table corrected. This README gained an
  Apify-actor table.
- `docs/STATUS_SNAPSHOT.md` — periodic read-out of what shipped, live state,
  and what is still open.
