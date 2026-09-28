# STATUS_SNAPSHOT — 2026-09-29

> Point-in-time recap of what shipped, what it fixed, and what is still open.
> Living log remains `PROGRESS.md`; this file is the read-out, not the log.

## Shipped this window (commits `e0207d3`..`3b69b7f`)

Nine commits on `main`. The throughline: a keyword scrape reported **31 items
from a 50-item dataset**, and finding out why surfaced four separate defects.

### `e0207d3` fix(scrape): per-post Threads actor + three ingest bugs

The per-post Threads scrape moved to `themineworks/threads-scraper` (full
Apify id). Both predecessors were broken: `futurizerush/meta-threads-scraper`
404s (user-posts-only build, no per-post mode) and `pro100chok/threads-scraper-usage`
runs async-only. `includeReplies: true` is load-bearing — replies are excluded
by default and are the only source of Threads comments.

The three bugs, each reproduced by replaying the raw dataset out of MinIO:

1. **19 of 50 rows dropped.** The actor flags `is_reply` on a row whose parent
   it never delivers — keyword mode marks a reply without fetching the parent,
   and `replied_to_post_code` arrives empty. The parser dropped those rows as
   orphans, which read on the dashboard as the batch having fewer posts than it
   did. A row with no resolvable parent now falls back to a top-level post: the
   parent link is unavailable, the post and its metrics are not.
2. **Context rows stored as posts.** Post mode returns the share/quote rows
   around the requested post (`Dengan <name> – Saya baru saja diakui…`), with no
   field separating them from the requested post. The ingestor now takes the
   shortcode from the requested permalink and drops everything else.
3. **`items_read` reported the stored count.** `keyword_batch.items_read` and
   `posts_count` were both written from `PostsUpserted`, so every window drop was
   invisible. `items_read` is now the parser output; `posts_count` what survived.

**Verified against the live 50-item batch: 50/50 kept** with the full-month
window (before the fix: 31).

### `d83f2e8` feat(api): session refresher wiring (P2-06)

`SessionRefresher` is now wired in `cmd/server`, opt-in via
`SESSION_REFRESH_INTERVAL_SECONDS` (lead `SESSION_REFRESH_LEAD_HOURS`, default
168h). Gated on Redis — the refresh instruction rides the control channel.
Repeated login failure sets `QUARANTINED`, keeping the account out of the
packable pool until an operator resumes it.

### `7f34425` fix(scrape): persist `apify_run.run_id`

`UpdateApifyRun` never wrote the run id despite both callers passing it —
**all 25 rows in the running stack were blank**, so an in-flight run was
unfindable on the Apify side. `COALESCE`/`NULLIF` keep the write-ordering safe.

### `c44d187` feat(actions): operator queue controls + LLM sizing

Retry a FAILED row, pull the backoff queue forward, `action-updated` SSE frames.

**`RunPendingNow` swept instead of capped.** A cap at `MaxActionBatch` (50)
would move the first 50 jobs and report `moved: 50` while the tail kept waiting
on its timer — a 60-job queue silently lost 10. It now pages forward, offset
advancing by rows read so a permanently bad row cannot stall the sweep.

LLM client sized for reasoning models (timeout 60→180s, max_tokens 300→1500).
`TRANSIENT` now covers "not found"/"did not change", so a deleted post stops
retrying forever.

### `1f4d4e9` feat(web): accounts redesign + queue triage

The queue header now shows the backoff timer when jobs are parked: a settled
queue can still hold PENDING rows waiting out a backoff, and the header
previously implied the queue was done.

Also: the web healthcheck. Docker sets `HOSTNAME` to the container id, the
standalone server then binds only eth0, and the image's own 127.0.0.1 probe
fails from inside while the published port works. Pinned to `0.0.0.0`.

### `3c3f60e` fix(worker): session hydration + robustness

`runLogin` hydrates the persisted session — without it the refresher parked
every account on `needs_input`. Image mirror falls back to a local registry; X
probe checks the unix socket instead of a process name.

### `b75710f` chore(security): backup output ignored

A logical dump carries `COPY` blocks for `app_user` (password hashes),
`auth_session` (live session tokens) and `account` (platform credentials).
Committing that publishes secrets into history, where removal needs a rewrite.
The scripts stay tracked; only their output is ignored.

## Live state (2026-09-29)

| Thing | Value |
| --- | --- |
| `action_job` | 18 SUCCESS · 1 FAILED · 0 PENDING |
| The one FAILED | `ACTION_LIKE` — "like button not found on the post" (3 attempts) |
| Threads posts stored | 52 |
| Latest keyword batch | `items_read` 31, `posts_count` 31 (ingested before the fix) |
| api / web | both rebuilt and healthy |

The `items_read: 31` row is pre-fix data — it will not correct itself, and
re-scraping the keyword would. The 19 dropped posts are still absent from the
post table; they come back on the next scrape of the same keyword, since the
ingest is idempotent by `(platform, external_id)`.

## Still open

- **0 comments on the Threads post scrape.** `includeReplies: true` was sent
  and the actor reported `delivered: 6`, but delivered no reply rows. Actor-side
  behavior, not a parser bug — a second actor swap or a support ticket.
- **`Column4`-style positional sqlc fields** are gone for `UpdateApifyRun`
  (`sqlc.arg('run_id')`), but the pattern is worth checking across the other
  queries with expressions.
- **Velocity gate is per-account, not per-platform.** A Threads account was
  suspended after rapid like+comment+reply on one post while IG stayed healthy.
  The gate that would have prevented it does not exist yet.
- **`docs/local-worker/shot-*.mjs` stay untracked** by their own README's
  instruction ("Not part of the pnpm workspace. Do not commit.")
- **Zero comments ingested from keyword batches**, every batch so far reports
  `comments_count: 0` — the search actors expose a count but never the bodies.
  Fetching comments needs the post-scraper, which is what "Fetch comments" runs.

## Verified

- API: `go build ./...` clean, full suite green.
- Threads ingest: 50/50 rows kept on the replayed live dataset (probe test,
  removed after).
- Queue: `RunPendingNow` sweeps past the 50-job cap — `TestRunPendingNowSweepsPastTheBatchCap`.
- Context filter: 4 share/quote rows dropped, requested post kept, and a
  no-code filter keeps them all (the keyword path).
- Stack: `api` + `web` rebuilt, both healthy.
