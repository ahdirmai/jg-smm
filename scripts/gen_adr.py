#!/usr/bin/env python3
"""Generate the P0-12 ADR set (docs/adr/0001..0012) in MADR format.

The narratives mirror DEVELOPMENT_RULE.md §17.1 so the rule and the ADRs stay
consistent. Re-running overwrites the files; the template (0000) is hand-written
and never touched here.
"""

from pathlib import Path

OUT = Path(__file__).resolve().parent.parent / "docs" / "adr"

COMMON_DRIVERS = [
    "Local build target = Mac M2 16 GB, docker-compose on colima (arm64).",
    "Must be cheap and fully self-hosted for the MVP.",
    "Minimise ban/blast radius across hundreds of accounts.",
]

ADRS = [
    {
        "num": "0001",
        "slug": "container-per-device",
        "title": "One container per device hosts N accounts (max one per platform)",
        "tags": "infra, worker, provisioning",
        "context": (
            "We manage hundreds of social accounts. A naive model of one pod per "
            "account multiplies resource cost and reconnect churn. A real device, "
            "however, is naturally one session per platform. We need an isolation "
            "unit that matches how the platforms see us while staying cheap."
        ),
        "drivers": [
            "Resource efficiency on a 16 GB machine.",
            "Match the platform's mental model (one device = one session per platform).",
            "Keep an enforceable uniqueness invariant.",
        ],
        "options": [
            "One container per account (max isolation, max cost).",
            "One container per device hosting N accounts, at most one per platform.",
            "One container per platform pooled across accounts.",
        ],
        "chosen": 1,
        "justification": "it halves the blast-radius/cost trade-off: one real device maps to one container, and the platform-per-container rule is a database invariant.",
        "good": "Cost scales with devices, not accounts; matches how platforms fingerprint.",
        "bad": "Blast radius = all N accounts on the device if the pod dies; mitigated by `MAX_ACCOUNTS_PER_CONTAINER`.",
        "neutral": "PVC `smm-session-<workerId>` holds all sessions on the device.",
        "confirm": "Enforced by `@@unique([workerId, platform])` on `Account`; bounded by `MAX_ACCOUNTS_PER_CONTAINER`.",
        "moreinfo": "PRD F2/F3; ERD `Account`/`Worker`; SYSTEM_DESIGN provisioning; P1-01, P1-05.",
    },
    {
        "num": "0002",
        "slug": "playwright-vs-apify-action",
        "title": "Actions use Playwright; scraping uses Apify",
        "tags": "worker, scraping, action",
        "context": (
            "We both read (scrape) and write (comment/like/report) on seven "
            "platforms. Scraping at scale needs rotating infrastructure and "
            "platform-specific actors; performing an authenticated action needs a "
            "real browser session tied to a specific account."
        ),
        "drivers": [
            "Scrape must scale out without us running huge infra.",
            "Actions must run from the account's real session/fingerprint.",
            "Keep one tool per concern (DRY, clear ownership).",
        ],
        "options": [
            "Use Apify for both scraping and actions.",
            "Use Playwright for both.",
            "Apify for scraping, Playwright for actions.",
        ],
        "chosen": 2,
        "justification": "each tool does what it is best at: Apify for cloud-scale read, Playwright for authenticated, fingerprinted, headful write.",
        "good": "Scrape scales cheaply; actions run headful on Xvfb with the account's own session.",
        "bad": "Two runtimes/toolchains to maintain and two sets of credentials paths.",
        "neutral": "Scrape credentials and action sessions are provisioned separately.",
        "confirm": "Worker has no scrape code; scrapers never hold action sessions.",
        "moreinfo": "SYSTEM_DESIGN §Components; PLATFORM_MATRIX; P2-03 vs P3-*.",
    },
    {
        "num": "0003",
        "slug": "sequential-batch-action",
        "title": "Action concurrency is 1 per container (batch sequential)",
        "tags": "worker, action",
        "context": (
            "A single browser session cannot safely perform two actions at once "
            "without looking robotic and racing on the same cookies. Yet we want "
            "throughput across the fleet."
        ),
        "drivers": [
            "Avoid detection from parallel same-session activity.",
            "Bound CPU/RAM per container (headful Chromium is heavy).",
            "Minimise wall-clock across the fleet.",
        ],
        "options": [
            "Run multiple actions in parallel inside one container.",
            "One action at a time per container; parallelism across containers.",
        ],
        "chosen": 1,
        "justification": "sequential within a container keeps the session coherent and the resource profile flat; the fleet provides parallelism.",
        "good": "Predictable resource use; no in-session races.",
        "bad": "Per-account throughput is bounded by action latency.",
        "neutral": "`ACTION_BATCH_PARALLELISM` controls how many containers act in a batch (default 4, local 2).",
        "confirm": "Worker loop processes exactly one dequeued job at a time (BLPOP → run → ACK).",
        "moreinfo": "DEVELOPMENT_RULE §concurrency; P1-09; P3-*.",
    },
    {
        "num": "0004",
        "slug": "single-team-no-workspace",
        "title": "Single team, no multi-tenancy in the MVP",
        "tags": "backend, data-model, auth",
        "context": (
            "The MVP serves one social-media team. Multi-tenancy would force a "
            "workspace column on every root table and complicate auth, RBAC and "
            "queries from day one."
        ),
        "drivers": [
            "Ship the core value fast (scrape + action).",
            "Avoid premature complexity (YAGNI).",
            "Keep a clear, cheap migration path if tenancy is needed later.",
        ],
        "options": [
            "Full multi-tenant (workspaceId everywhere) now.",
            "Single team now; a documented migration path to workspaces later.",
        ],
        "chosen": 1,
        "justification": "tenancy is not needed for MVP value and the migration is bounded (~1-2 weeks per DEVELOPMENT_RULE §tenancy).",
        "good": "Simpler schema, auth and queries; faster delivery.",
        "bad": "A later tenancy move is a breaking migration (requires an ADR).",
        "neutral": "Any change touching tenancy mandates a new ADR.",
        "confirm": "`TeamConfig` is a singleton row; no `workspaceId` anywhere.",
        "moreinfo": "PRD scope; ERD `TeamConfig`; DEVELOPMENT_RULE §tenancy.",
    },
    {
        "num": "0005",
        "slug": "timescaledb-for-metrics",
        "title": "Time-series metrics use TimescaleDB",
        "tags": "database, metrics, monitoring",
        "context": (
            "We store high-cardinality metric snapshots (reach/views/mentions) at "
            "30-minute cadence across posts and official accounts, with retention "
            "rules. Plain Postgres tables grow and slow down for range queries."
        ),
        "drivers": [
            "Fast time-range aggregation for dashboards.",
            "Automated retention (hot 90 days, cold 1 year).",
            "Stay on Postgres (one engine, one ops surface).",
        ],
        "options": [
            "Plain Postgres tables with manual partitioning.",
            "A dedicated TSDB (e.g. Prometheus/InfluxDB).",
            "TimescaleDB extension on Postgres.",
        ],
        "chosen": 2,
        "justification": "TimescaleDB keeps everything in Postgres while adding hypertables, compression and retention policies.",
        "good": "One database to run/back up; fast time-range queries; native retention.",
        "bad": "Ties us to a Postgres extension image (`timescale/timescaledb`).",
        "neutral": "Hypertables: `MetricSnapshot`, `AnalyticsSnapshot`.",
        "confirm": "Retention policy active; dashboards hit hypertables via time_bucket.",
        "moreinfo": "ERD metrics; SYSTEM_DESIGN storage; P2-01, P2-05, P2-10.",
    },
    {
        "num": "0006",
        "slug": "redis-list-durable-action",
        "title": "Durable action queue via Redis List; control via Pub/Sub",
        "tags": "backend, worker, transport",
        "context": (
            "Actions are money: losing a queued comment silently is unacceptable. "
            "Control messages (login, OTP) are ephemeral and only matter to a live "
            "worker. We need durable delivery for one and fire-and-forget for the "
            "other, without running a heavy broker."
        ),
        "drivers": [
            "At-least-once delivery for actions.",
            "Ephemeral, low-latency control channel.",
            "Minimal moving parts (no RabbitMQ/Kafka).",
        ],
        "options": [
            "BullMQ over Redis (managed job semantics).",
            "Redis List (`LPUSH`/`BLPOP`) with explicit ACK for actions; Pub/Sub for control.",
            "Kafka/RabbitMQ.",
        ],
        "chosen": 1,
        "justification": "a List is a durable, inspectable queue with blocking pop and explicit ACK; Pub/Sub fits ephemeral control without persistence overhead.",
        "good": "Actions survive restarts; the queue is trivially observable.",
        "bad": "We implement retry/ACK semantics ourselves.",
        "neutral": "Queues keyed `queue:action:<workerId>`, control channel `control-<workerId>`.",
        "confirm": "DB commit happens before LPUSH; worker ACKs only after a verdict.",
        "moreinfo": "SYSTEM_DESIGN transport table; P1-08, P1-09, P1-11.",
    },
    {
        "num": "0007",
        "slug": "shadcn-ui-design-system",
        "title": "UI built on shadcn/ui",
        "tags": "frontend, design-system",
        "context": (
            "The operator dashboard must feel like an internal tool a social-media "
            "specialist uses for hours: accessible, themeable (light/dark), "
            "minimalist but professional. We do not want to hand-roll primitives "
            "nor adopt a heavy component kit with its own visual identity."
        ),
        "drivers": [
            "Own the code (copy-in primitives, no black-box upgrades).",
            "First-class Tailwind + Radix accessibility.",
            "Support light/dark tokens with an indigo accent.",
        ],
        "options": [
            "MUI / Chakra / Mantine.",
            "Headless Radix + custom CSS from scratch.",
            "shadcn/ui (Radix + Tailwind, copied in).",
        ],
        "chosen": 2,
        "justification": "shadcn/ui gives accessible primitives as source we own, styled with our Tailwind tokens.",
        "good": "Full control, no version lock, consistent tokens across the app.",
        "bad": "We maintain the components ourselves.",
        "neutral": "Design tokens live in `apps/web`; shared primitives in `@smm/ui`.",
        "confirm": "Every screen uses tokenised classes; light/dark verified by the prototype browser check.",
        "moreinfo": "DESIGN_SYSTEM; P0-08; prototype `docs/prototype/`.",
    },
    {
        "num": "0008",
        "slug": "go-be-sqlc-pgx",
        "title": "Backend in Go with sqlc + pgx + Echo + slog",
        "tags": "backend, stack",
        "context": (
            "The API must serve REST + SSE + a scheduler in one process, be cheap "
            "to run, and be easy to reason about by a small team. We want compile-time "
            "safety between Go and SQL."
        ),
        "drivers": [
            "Statically typed SQL (no ORM magic).",
            "Fast cold start and a single deployable binary.",
            "One process to host REST, SSE and the scrape scheduler.",
        ],
        "options": [
            "Node/TypeScript API (same language as FE/worker).",
            "Go + Echo + pgx + sqlc + slog.",
            "Go + a full ORM (GORM).",
        ],
        "chosen": 1,
        "justification": "sqlc generates typed Go from SQL keeping queries explicit; Echo + slog keep the surface small; a single binary is trivial to containerise.",
        "good": "Type-safe queries, small footprint, simple deployment.",
        "bad": "A second language/toolchain alongside TS.",
        "neutral": "Migrations via golang-migrate; generated code in `internal/repository/sqlcgen`.",
        "confirm": "CI runs gofmt/vet/build/test and fails on sqlc/OpenAPI drift.",
        "moreinfo": "DEVELOPMENT_RULE §backend; P0-03, P0-05; SYSTEM_DESIGN.",
    },
    {
        "num": "0009",
        "slug": "all-containerized-no-managed-services",
        "title": "All services self-hosted containers (no managed cloud)",
        "tags": "infra, devops",
        "context": (
            "The MVP targets a cheap self-hosted deployment and a one-command local "
            "dev experience. Managed services (RDS/ElastiCache/S3) add cost, "
            "vendor coupling and a dev/prod parity gap."
        ),
        "drivers": [
            "Low cost and full control for the MVP.",
            "Dev/prod parity (same compose topology).",
            "Avoid cloud lock-in while the product is unproven.",
        ],
        "options": [
            "Managed Postgres/Redis/S3.",
            "Self-hosted Postgres(Timescale)+Redis+MinIO as containers.",
        ],
        "chosen": 1,
        "justification": "self-hosting keeps MVP cost near zero and makes local = prod topology, at the price of owning DB/Redis ops.",
        "good": "Cheap, portable, identical local and server topology.",
        "bad": "Backups, upgrades and HA are on us (runbooks required).",
        "neutral": "MinIO provides the S3 API, so the storage client is portable.",
        "confirm": "compose.yaml is the single topology; no managed endpoints in config.",
        "moreinfo": "INFRA_ANALYST; P0-02; SYSTEM_DESIGN storage.",
    },
    {
        "num": "0010",
        "slug": "sse-over-websocket",
        "title": "Real-time uses SSE, not WebSocket",
        "tags": "frontend, backend, realtime",
        "context": (
            "The dashboard needs live updates (worker health, provisioning, job "
            "verdicts, analytics). Traffic is one-way server→browser; commands go "
            "over REST. WebSocket would add a bidirectional connection we do not need."
        ),
        "drivers": [
            "Simplest transport that fits one-way push.",
            "Work with plain HTTP infra (proxies, auth headers).",
            "Easy client consumption and reconnect semantics.",
        ],
        "options": [
            "WebSocket / socket.io (bidirectional).",
            "SSE via `EventSource`; commands over REST.",
            "Long-polling.",
        ],
        "chosen": 1,
        "justification": "SSE is one-way, HTTP-native, auto-reconnects, and needs no extra protocol; REST already covers commands.",
        "good": "Trivial server implementation, works through standard proxies.",
        "bad": "No server→client binary frames; one connection per open tab.",
        "neutral": "Frames carry full entities so the client can reconcile without a separate fetch.",
        "confirm": "Client reconnects and refetches on drop; frames validated by schema.",
        "moreinfo": "SYSTEM_DESIGN realtime; P1-17, P2-14.",
    },
    {
        "num": "0011",
        "slug": "worker-redis-pubsub-callback-contract",
        "title": "Worker contract: subscribe control + POST callbacks (never touches DB)",
        "tags": "worker, backend, contract",
        "context": (
            "Workers run untrusted platform sessions and may be re-created often. "
            "They must not hold database credentials. We need a contract that "
            "keeps the DB a backend-only concern while still reporting per-attempt "
            "outcomes."
        ),
        "drivers": [
            "Workers never hold DB credentials.",
            "Per-attempt verdicts are recorded durably (audit).",
            "Mirror the proven pattern from the reference project `JG/automation`.",
        ],
        "options": [
            "Workers write directly to Postgres.",
            "Workers only SUBSCRIBE (control) and POST callbacks (verdict); BE owns the DB.",
        ],
        "chosen": 1,
        "justification": "isolating DB access to the backend keeps credentials safe and centralises validation; each verdict becomes an upserted row.",
        "good": "Small blast radius if a worker is compromised; single validation point.",
        "bad": "Pub/Sub has no delivery guarantee; callback authorization must be enforced beyond loopback.",
        "neutral": "Verdict per attempt = a row (upsert on attempt id).",
        "confirm": "Worker config contains no DATABASE_URL; callbacks validated + size-limited.",
        "moreinfo": "SYSTEM_DESIGN worker contract; P1-13; reference `JG/automation`.",
    },
    {
        "num": "0012",
        "slug": "desired-state-worker-provisioning",
        "title": "Dynamic workers via a desired-state reconciler (empty fleet by default)",
        "tags": "infra, provisioning, worker",
        "context": (
            "The number of worker containers should be dynamic and manageable from "
            "the dashboard. A static list of `WORKER_IDS` cannot express create/"
            "delete/pause and drifts from reality. We also want the fleet to start "
            "empty rather than pre-provisioning idle containers."
        ),
        "drivers": [
            "Operators add/remove containers from the UI, no server access.",
            "Reconciliation must be idempotent and auditable.",
            "Do not run (and pay for) containers with no work.",
        ],
        "options": [
            "Static `WORKER_IDS` env list.",
            "Desired-state reconciler with `Worker.desiredState` + `generation`; fleet empty by default; auto-create fallback on add-account.",
        ],
        "chosen": 1,
        "justification": "desired-state reconciliation expresses intent declaratively, is idempotent, and lets the dashboard drive the fleet; an empty default avoids idle cost.",
        "good": "UI-driven fleet, idempotent reconcile, full audit trail.",
        "bad": "More moving parts: reconciler, generation labels, orphan sweeper.",
        "neutral": "Two triggers: events + a 60 s cron; every op in `ProvisionLog`.",
        "confirm": "Idempotency via `Worker.generation` + pod label; empty containers auto-delete only for `source=AUTO` (`MANUAL` containers persist).",
        "moreinfo": "INFRA_ANALYST §15; P1-04, P1-05, P1-19; SYSTEM_DESIGN provisioning.",
    },
]


def render(a, drivers):
    opts_md = "\n".join(f"{i + 1}. {o}" for i, o in enumerate(a["options"]))
    drv_md = "\n".join(f"- {d}" for d in a["drivers"])
    pros_cons = "\n\n".join(
        f"### {o}\n\nRejected in favour of the chosen option. Its benefits were outweighed by the drivers above; its drawbacks are the inverse of the chosen option's strengths."
        for o in a["options"]
        if o != a["options"][a["chosen"]]
    )
    chosen_opt = a["options"][a["chosen"]]
    pros_cons = (
        f"### {chosen_opt} *(chosen)*\n\n"
        f"- **Good**: {a['good']}\n- **Bad**: {a['bad']}\n\n"
        "### Alternatives\n\n"
        + "\n\n".join(
            f"- **{o}** — rejected; its trade-offs did not beat the chosen option against the decision drivers."
            for o in a["options"]
            if o != chosen_opt
        )
    )
    return f"""# {a["num"]}. {a["title"]}

- **Status**: accepted
- **Date**: 2026-09
- **Tags**: {a["tags"]}

## Context and Problem Statement

{a["context"]}

## Decision Drivers

{drv_md}

## Considered Options

{opts_md}

## Decision Outcome

Chosen option: **"{a["options"][a["chosen"]]}"** because {a["justification"].rstrip(".")}.

### Consequences

- **Good**: {a["good"]}
- **Bad**: {a["bad"]}
- **Neutral**: {a["neutral"]}

### Confirmation

{a["confirm"]}

## Pros and Cons of the Options

{pros_cons}

## More Information

- {a["moreinfo"]}
- See also: `DEVELOPMENT_RULE.md` §17.1, `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`.
"""


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for a in ADRS:
        path = OUT / f"{a['num']}-{a['slug']}.md"
        path.write_text(render(a, COMMON_DRIVERS), encoding="utf-8")
        print("wrote", path.name)


if __name__ == "__main__":
    main()
