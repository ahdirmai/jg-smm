# Changelog

> Generated from conventional commits. `docs/PROGRESS.md` is the living
> per-ticket log; this file is the human-readable release view. Irreversible
> schema changes are traced here per `docs/DEVELOPMENT_RULE.md` §4.

## Unreleased
### Added
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

### Fixed
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
