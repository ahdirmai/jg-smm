# Changelog

> Generated from conventional commits. `docs/PROGRESS.md` is the living
> per-ticket log; this file is the human-readable release view. Irreversible
> schema changes are traced here per `docs/DEVELOPMENT_RULE.md` §4.

## Unreleased
### Fixed
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
