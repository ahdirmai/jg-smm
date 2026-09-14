# Development Rule

> Aturan ini mengikat semua kontributor. Tujuannya: **kode mudah diubah, mudah dites, tidak bocor kredensial, dan dapat diaudit**. Kalau sebuah aturan menghalangi pengiriman nilai, ajukan ADR — jangan diam-diam melanggar.

## 0. Engineering Principles (Non-Negotiable)

Prinsip ini berlaku di semua bahasa/layer. Bila ragu, kembali ke sini.

### DRY (Don't Repeat Yourself) — dengan benar

- **Single Source of Truth (SSOT)** untuk setiap pengetahuan:
  - Kontrak API → OpenAPI spec (Go `oapi-codegen` generate handler types + `openapi-typescript` generate FE types). **Jangan** tulis DTO tangan dua kali.
  - Skema DB → SQL migration + `sqlc` (query). Model Go di-generate, bukan ditulis ulang.
  - Selector platform → satu objek `SEL` per platform (markup berubah = satu titik patch).
  - Konstanta (rate limit, enum, channel prefix) → `packages/shared` (TS) + `internal/domain/constants.go` (Go), **bukan** literal tersebar.
  - Sistem desain → token CSS variable (shadcn). **Jangan** hardcode hex/padding.
- **DRY ≠ abstraksi dini.** Duplikasi dua kali = biarkan. Duplikasi ketiga dengan pola sama = ekstrak. Abstraksi yang salah lebih mahal dari duplikasi.
- Aturan praktis: **rule of three**. Jangan bikin helper/abstraksi untuk 1-2 pemakaian. Untuk 3+, ekstrak dengan nama yang jelas.
- Duplikasi antar-boundary (BE↔FE) **diperbolehkan** jika memaksa kontrak eksplisit; disatukan lewat code-gen, bukan import lintas bahasa.

### SOLID (pragmatis, bukan dogma)

- **S — Single Responsibility.** Satu file/tipe/fungsi = satu alasan berubah.
  - Handler (parse → service → render) ≠ Service (business logic) ≠ Repository (query) ≠ Adapter (K8s/Redis/Apify). Layer wajib terpisah.
  - Fungsi ≤ 50 baris, satu tingkat abstraksi per fungsi.
  - File React: satu komponen utama per file; helper kecil boleh se-file bila privat.
- **O — Open/Closed.** Tambah platform baru = **tambah adapter**, bukan edit `switch` di mana-mana.
  - Setiap platform (IG/Threads) implement interface `PlatformAdapter` (BE action/scrape builder, Worker action handler). Registry `map[Platform]Adapter`. Kode inti tidak tahu platform konkret.
- **L — Liskov Substitution.** Semua implementasi interface harus bisa dipakai tukar-ganti tanpa kejutan. Kontrak nol-ambigu: dokumenkan precondition/postcondition/error yang diizinkan.
- **I — Interface Segregation.** Interface kecil & fokus (`Publisher`, `Clock`, `K8sClient`), bukan interface raksasa. Konsumen hanya bergantung pada method yang ia pakai.
- **D — Dependency Inversion.** Domain tidak bergantung pada detail (pgx/K8s/Redis); bergantung pada interface yang didefinisikan di sisi konsumen. Injeksi lewat **constructor**, bukan global/singleton.

### Best Practices (per baseline)

- **Fail loud, fail early.** Return error eksplisit; jangan `nil` diam-diam. Validasi di tepi sistem (HTTP/K8s/Redis boundary).
- **Idempotency by default.** Semua operasi yang bisa di-retry harus idempoten (UPSERT, `Idempotency-Key`, `generation` counter). Ini yang membuat sistem aman saat crash/retry.
- **Explicit over clever.** Kode dibaca 10× lebih sering daripada ditulis. Hindari meta-programming yang tak perlu.
- **Boundaries are typed.** Semua data yang lewat batas (HTTP, Redis, callback) divalidasi & di-type; tidak ada `any`/`interface{}` tanpa narrowing.
- **Testability is design.** Kalau susah dites, desainnya salah. `Clock`/`Rand`/`HTTPClient` di-inject sebagai interface agar deterministic.
- **Small PRs, reviewable.** ≤ 400 LOC, satu tujuan, satu tiket.
- **Convention over configuration.** Ikuti konvensi framework (Echo, Next.js, Playwright) alih-alih melawannya.
- **Boy Scout Rule.** Tinggalkan kode lebih bersih dari yang kamu temukan — dalam scope tiket.

## 1. Repo Layout

```
.
├── apps/
│   ├── api/          # Go 1.23 Echo BE
│   │   ├── cmd/server/            # main.go (wiring, DI root)
│   │   ├── internal/
│   │   │   ├── domain/            # entity, enum, constant, error sentinel (nol deps)
│   │   │   ├── port/              # interface (Publisher, Repository, K8sClient, Clock...)
│   │   │   ├── service/           # business logic (depends on port, bukan impl)
│   │   │   ├── repository/        # implementasi port pakai sqlc/pgx
│   │   │   ├── adapter/           # k8s, redis, apify, callback
│   │   │   ├── http/              # handler, middleware, router
│   │   │   ├── scheduler/         # rate limit, reconciler, sweeper
│   │   │   └── obs/               # slog, metrics, tracing
│   │   └── db/
│   │       ├── migrations/        # golang-migrate *.up.sql / *.down.sql
│   │       └── queries/           # sqlc *.sql per table
│   ├── web/          # Next.js 15 FE (App Router)
│   └── worker/       # Node.js 22 worker (Playwright + Apify client)
├── packages/
│   ├── shared/       # OpenAPI-generated TS types + constants (SSOT FE)
│   ├── ui/           # shadcn/ui components (FE-only)
│   └── tsconfig/     # base tsconfig (SSOT TS config)
├── infra/            # docker, k8s manifests/helm, terraform
├── docs/             # PRD, ERD, SYSTEM_DESIGN, DESIGN_SYSTEM, DEVELOPMENT_RULE, adr/
└── scripts/          # one-off ops script (idempoten, tidak destruktif tanpa --force)
```

- Monorepo: **pnpm workspaces** (FE + packages) + **go.work** (BE). Task orchestration: **turbo** (cache, dependency-aware).
- `apps/api/internal/domain` **wajib nol-dependency** (hanya stdlib). Ini menjaga dependency inversion.

## 2. Naming

- File: `kebab-case.ts` (route/util) / `PascalCase.tsx` (component) / `snake_case.go` (Go) / `*_test.go`.
- Variable/function: `camelCase` (TS); Go unexported `camelCase`, exported `PascalCase`.
- Type/Class/Component: `PascalCase`. Constant: `UPPER_SNAKE` (TS), `PascalCase` exported (Go).
- DB: kolom `snake_case`, model Prisma-style `PascalCase`.
- Go package: lowercase singular. Interface `-er` suffix (`Publisher`, `Clock`). Sentinel error `Err*`.
- Branch: `feat/`, `fix/`, `chore/`, `refactor/`, `docs/` + short-desc. Commit: Conventional Commits.

## 3. Bahasa

- Code, identifier, comment teknis, commit: **English**.
- UI copy default **English**, i18n-ready via `next-intl`.
- PRD/ERD/design doc: **English** (agar bisa dishare ke vendor/eng luar). Komunikasi tim sehari-hari boleh Bahasa Indonesia.

## 4. Konvensi Single-Team

- Tidak ada `workspaceId` di tabel. Single root config = `TeamConfig` (singleton).
- Migrasi ke multi-tenant: `TeamConfig` → `Workspace`, tambah `workspaceId` ke semua tabel root, ubah auth middleware. Estimasi 1-2 minggu. **ADR wajib** saat menyentuh tenancy.
- Semua migration irreversible di-trace di changelog.

## 5. Go (BE)

- Go 1.23+. Module path `github.com/<org>/smm/apps/api`.
- `gofmt` + `goimports` (non-negotiable, di CI). Linter: `golangci-lint` (`default + gocritic + govet + staticcheck + errcheck + gosec + revive`).
- **Error**: wrap `fmt.Errorf("layer.op: %w", err)`; sentinel di `internal/domain/errors.go` (`ErrNotFound`, `ErrConflict`, `ErrUnauthorized`, `ErrRateLimited`, `ErrK8sQuota`...). **Jangan** `panic` di production path.
- **Context**: `context.Context` parameter pertama, selalu; cancellation dipropagasi ke semua goroutine/IO.
- **Concurrency**: `errgroup.Group` untuk fan-out; `errgroup.WithContext`. Tanpa goroutine bocor (selalu drain channel).
- **Dependency injection**: constructor manual di `cmd/server` (wiring root). Service bergantung pada interface di `internal/port`, bukan impl konkret. **Tidak ada** global mutable state / `init()` side effect.
- **Validator** (`go-playground/validator`) di bind struct; invariant bisnis di service (2 lapis).
- **DB**: `pgx/v5` + `sqlc`. Query **eksplisit** kolom (jangan `SELECT *`). Transaksi via helper `WithTx(ctx, fn)`. Query berat wajib `EXPLAIN ANALYZE` di deskripsi PR. **Tidak ada ORM runtime reflection.**
- **HTTP**: Echo v4. Handler tipis (parse → service → render). Export error via middleware: sentinel → `{error:{code,message,traceId}}`; **jangan** bocorkan `err.Error()` mentah.
- **Logger**: `log/slog`. Setiap log request: `traceId`, `route`, `userId` (bila ada). PII rule: handle/`accountId` boleh; cookie/token/password **dilarang** (CI grep gate).
- **K8s client** (`k8s.io/client-go`): scope namespace `smm`, RBAC minimal. Rate limit `10 create/menit` (burst 20); bulk via Job queue.
- **Idempotency-Key** wajib untuk POST mutasi (create-account, kill-worker, bulk-import); simpan Redis 24 jam.
- **Test**: tabel-driven (`t.Run`), `testcontainers-go` + `envtest` (K8s). Coverage target **70%**. `govulncheck ./...` di CI per PR.
- **OpenAPI**: generate via `oapi-codegen`; spec = SSOT. FE type-sync via `packages/shared`.

### 5.1 Layer Dependency Rule (enforced)

```
http ─┐
scheduler ─┤─► service ─► port ◄─ adapter (k8s/redis/apify)
repository ─┘        ▲                    │
                     └── domain (nol deps) ◄┘
```

- Arah dependency **selalu** ke dalam (`domain` paling dalam, nol import internal).
- `service` hanya tahu `port`, **tidak pernah** import `repository`/`adapter` konkret.
- Import cycle = build gagal. Enforce via `internal/arch` test (template test arch-go / `depguard` di golangci).

## 6. TypeScript / Frontend (Next.js 15)

- `strict: true`, `noUncheckedIndexedAccess: true`, `exactOptionalPropertyTypes: true`. **Larang `any`** (pakai `unknown` + narrowing / generic).
- Hindari `enum` runtime; pakai `as const` + union type.
- **Zod = source of truth** kontrak FE (form + boundary parsing). Infer via `z.infer`.
- **Server Component default**; `"use client"` hanya untuk interaksi/state/SSE.
- **Data fetching**: TanStack Query. **Dilarang** `fetch` langsung di client component — selalu lewat query hook/service.
- **State**: server state → TanStack Query; UI lokal → Zustand. SSE frame = objek penuh → `setQueryData` invalidation, **jangan** mirror state.
- **Form**: `react-hook-form` + `@hookform/resolvers/zod`.
- **A11y**: semua elemen interaktif keyboard-reachable; axe-core di CI.
- **Bundle budget**: route ≤ 200 KB gzip first-load (delta > 20 KB → block).
- **Import order**: builtin → external → internal(`@smm/*`) → relative. Boundary rule via `eslint-plugin-import`.
- **Function ≤ 50 baris**; ekstrak sebelum perlu scroll.

### 6.1 shadcn/ui Rules

- Base design system = **shadcn/ui** (Radix + Tailwind). Konvensi detail: `DESIGN_SYSTEM.md`.
- `packages/ui/src/components/ui/*` = output `shadcn add`. **Dilarang edit langsung** — patch via wrapper di `apps/web/components/` atau PR upstream.
- Custom komponen di `packages/ui/src/components/` (di luar `ui/`), dibangun di atas primitive shadcn.
- Theme token di `packages/ui/src/styles/globals.css` — HSL CSS variables (shadcn convention). **Jangan** hardcode hex.
- Variant via `class-variance-authority`, satu file dengan komponen; re-export tipe variant.
- Icon dari `lucide-react`; brand icon (IG/Threads) = custom SVG component.
- Tambah komponen: `pnpm dlx shadcn@latest add <name>` → commit lockfile.
- **Storybook wajib** untuk setiap custom komponen (+ `@storybook/addon-a11y`).

## 7. Worker (Node.js 22 + Playwright)

> Contract inti diadopsi dari `JG/automation` (terbukti jalan): worker **hanya** subscribe Redis, **hanya** POST callback ke BE, **tidak pernah** sentuh DB.

### 7.1 Topologi

- **1 Worker = 1 container = 1 "device"**, meng-host **N akun, maks 1 per platform** (`@@unique([workerId, platform])`). `WORKER_ID` = id container, **bukan** id akun; tiap job/instruksi bawa `accountId`. BE = satu-satunya publisher; worker tidak pernah `PUBLISH`.
- **2 channel masuk**:
  - `queue:action:<workerId>` (Redis **List durable**) — `BLPOP` 1 job, concurrency = 1. Job bawa `accountId`; action lintas akun tetap sequential.
  - `control-<workerId>` (Redis **Pub/Sub**) — instruksi privat container: `auth-login`, `auth-input`, `auth-clear` (payload bawa `accountId`). Kredensial **tidak pernah** ke channel broadcast.
- **2 Redis client terpisah**: mode subscribe tidak bisa jalankan perintah → `sub` (subscribe) + `cmd` (SET/GET/BLPOP). Wajib dua koneksi.
- Worker **stateful**: 1 browser/container, 1 `context` per akun, `storageState()` persist ke `/data/sessions/session-<platform>.json` di **PVC per container** (bukan emptyDir). Restart → session terbaca ulang → tidak login ulang.

> Kapabilitas per platform (action/scrape didukung) & kontrak adapter tunggal: lihat `PLATFORM_MATRIX.md`. Brief alur+selector tiap platform: `platforms/<slug>.md`.

### 7.2 Struktur Kode Worker (SOLID)

```
apps/worker/src/
├── index.js            # bootstrap: wiring, BLPOP loop, subscribe (composition root)
├── platforms/
│   ├── registry.js     # map[Platform]Adapter  (Open/Closed)
│   ├── instagram.js    # adapter: selectors + actions (like/comment/verify) + login
│   └── threads.js
├── core/
│   ├── browser.js      # launch headful/Xvfb, newContext(storageState), CDP screenshot
│   ├── session.js      # read/write session-<platform>.json
│   ├── controller.js   # action orchestration (sequential, jitter, cooldown)
│   └── auth.js         # login flow, waitForLoginOutcome, authContexts Map
├── transport/
│   ├── queue.js        # BLPOP + ACK
│   ├── control.js      # subscribe + dispatch
│   └── callback.js     # POST ke BE, retry, timeout, never-throw
└── sel/                # SEL selector objects (per platform, satu titik patch)
```

- Setiap platform mengimplementasikan interface yang sama (**LSP**): `login(ctx)`, `like(ctx, job)`, `comment(ctx, job)`, `verify(ctx, job)`. Registry memetakan `Platform → Adapter`. Menambah platform = **tambah file + daftar di registry**, tanpa menyentuh `controller.js` (**OCP**).
- `controller.js` hanya tahu interface adapter, bukan detail DOM tiap platform (**DIP**).
- Transport (`queue`/`control`/`callback`) terpisah dari aksi platform (**SRP**).

### 7.3 Aturan Eksekusi

- **Concurrency = 1 per container** untuk action. **Sequential batch lintas akun**: 1 job = 1 `context` (per akun, isolasi), selesai → `context.close()` di `finally` → jitter acak 30-90 dtk → job berikutnya. Tidak ada tab paralel.
  - Pengecualian: handler **jalur control tidak di-`await`** (`execute().catch()`) — `auth-login`/`auth-input` jalan konkuren agar worker tetap bisa terima instruksi saat idle. Action tetap sequential.
- **Headful** (`headless:false`) di atas Xvfb :99 (login Meta & anti-bot menolak headless). x11vnc + noVNC stream display; service noVNC internal ClusterIP, **tidak pernah** publik.
- **UA + locale + viewport fixed per container.** Identitas fingerprint konsisten antara sesi login & action; tiap akun punya proxy context-level sendiri. Identitas berubah = signal risiko Meta.
- **Screenshot via CDP** `Page.captureScreenshot` (bukan `page.screenshot` — Playwright menunggu font settle, Meta tak pernah settle → timeout). Nama deterministik `task-{id}-{worker}.jpg`, return basename saja.
- **Selector terpusat di `SEL`**. Pin versi Playwright (jangan `latest`); update via PR + test.

### 7.4 Kontrak & Invarian (BE & Worker)

- **Verdict = HTTP callback** (`POST /internal/action-callback`, `/internal/account-callback`). **Bukan** tulis DB. Worker tidak punya driver SQL/kredensial DB/pool.
- **Callback retry**: task = 3×, backoff `attempt × 500ms`, timeout 8s (`AbortSignal.timeout`), **4xx stop**, inject `worker_id` otomatis, **tidak pernah throw**. Akun callback = fire-and-forget (state browser tak boleh nunggu network).
- **Idempoten**: BE upsert `ActionLog` UNIQUE `(actionJobId, attempt)`; `screenshotUrl` pakai `COALESCE` (callback `running` tanpa shot jangan hapus shot sebelumnya).
- **Pesan cacat di-drop + log, jangan throw.** Validasi minimal `if (!id) return`. Worker **bukan** validator URL — SSRF guard (`normalizeTarget`) tugas BE sebelum publish.
- **Heartbeat** 30 dtk ke `/internal/heartbeat` (`browserStatus`, `queueDepth`, `lastActionAt`). Gagal 3× → self-exit (K8s restart).
- **Cooldown gate**: sebelum comment, `cmd.SET cooldown:comment:<id>:<sha256(url)> PX 60000 NX`. Sudah ada → report `FAILED` + pesan cooldown (terlihat operator, bukan silent no-op).
- **Verifikasi ground truth**: comment → teks muncul di feed (`[role=feed]` includes, poll ≤15s); like → state tombol berubah. Submit `Ctrl+Enter` → fallback klik → verifikasi. Gagal = `FAILED`.
- **Login/2FA**: `runLogin` → `waitForLoginOutcome` poll (auth cookie = bukti, bukan URL). Outcome `verified` → persist session + report handle; `needs_input` → context dibiarkan hidup di Map `authContexts` + screenshot + report; operator kirim kode → `auth-input` sambung context sama; `rejected`/`failed` → close context. Bisa berulang. Context hidup hilang saat restart = akun macet `needs_input` (diterima; remediasi = re-enroll).
- **Login context tanpa `ignoreHTTPSErrors`** (cert buruk di jalur kredensial = kebocoran password). Task context boleh `ignoreHTTPSErrors:true` (cert buruk = temuan).

### 7.5 BE Invarian (Scheduler/Provisioner — dari referensi, wajib)

- **Desired-state reconciler, bukan cron buta.** Sumber keinginan = `Worker.desiredState` (level container/device; `Account.status` untuk per-akun); actual = `Worker.containerId` + pod label `smm.generation`. Pemicu: **event** API (add/remove/pause/resume, latensi detik) + **safety-net** 60 dtk. Reconciler = fungsi murni `diff(desired, actual) → actions`, idempotent.
- **Idempotensi provisioning lewat `generation`**, bukan `SETNX`/lock. Pod dilabeli `smm.generation=<n>`; apply menaikkan generation; pod gen usang/ganda = orphan → delete. Retry & BE restart aman.
- **Commit DB (insert `ActionJob` PENDING) SEBELUM `LPUSH`.** Dibalik → worker callback `jobId` tak dikenal → verdict hilang.
- **Kirim `receivers`** (ack PUBLISH/`LPUSH`) di response create ke FE. `receivers < expected` = ada worker tak dengar → baris `PENDING` selamanya → tampilkan warning operator.
- **`PENDING` ≠ `FAILED`** (jangan sweep). Sweeper hanya `RUNNING > 10 menit tanpa heartbeat → reset PENDING (attempt++)` max 3×.
- **Validasi + sanitasi callback** di BE: status ∈ enum, `worker_id` ≤ 64, `path.Base` screenshot (tolak `..`/`/`), truncate (`renderedText` 2000, error 500), `MaxBytesReader`.
- **Orphan job + backoff** `min(2^attempt × 30s, 30m)`.
- **Rate limit per akun dipaksa scheduler (Go BE)**, bukan di dalam job.
- **Bin-packing** akun → container: pilih container region-matched ber-slot platform kosong (`MAX_ACCOUNTS_PER_CONTAINER`), else auto-create; container 0 akun → auto-delete.

### 7.6 Logging Worker

- Setiap job: `jobId`, `accountId`, `containerId`, `attempt`, `durationMs`. **Tanpa** credential/cookie/plaintext password (CI grep gate).

## 8. Container Rule

Semua service (BE, FE, Worker, Postgres+TimescaleDB, Redis, MinIO) berjalan dalam container. **Tidak ada managed service eksternal** di MVP.

### 8.1 Dockerfile (multi-stage)

- **BE (Go)**: stage `build` (`golang:1.23-alpine` + cache `go mod`); runtime `gcr.io/distroless/static-debian12:nonroot`. Static binary, `CGO_ENABLED=0`.
- **FE (Next.js)**: `deps` (`pnpm install --frozen-lockfile`) → `build` (`pnpm build`) → `runtime` (`node:22-alpine` + `output: standalone`).
- **Worker (Node)**: `deps` → `runtime` (`node:22-bookworm-slim` + Playwright Chromium + system deps, Xvfb/x11vnc/noVNC). Size ~1.2 GB (diterima).
- **Postgres**: `timescale/timescaledb:2.14.2-pg16` (pin digest) + init ConfigMap (`timescale-tune`).
- **Redis**: `redis:7-alpine` + config (AOF on, RDB per jam, `maxmemory 512mb` + `allkeys-lru`).
- **MinIO**: `minio/minio` (pin tag) + healthcheck.

### 8.2 Standar Container

- **Multi-stage** wajib; image runtime tanpa toolchain.
- **Non-root** UID 10001 (runtime); Postgres default UID 999.
- **Read-only rootfs** bila memungkinkan; EmptyDir untuk `/tmp` writable.
- **Drop ALL capabilities** + `seccompProfile: RuntimeDefault`.
- **HEALTHCHECK** wajib (kecuali Postgres/Redis built-in).
- **Graceful shutdown**: handle SIGTERM (Go `signal.NotifyContext`; Node `process.on('SIGTERM')` + drain).
- **Resource request+limit** wajib:
  - BE 256m/256Mi → 500m/512Mi.
  - FE 100m/128Mi → 200m/256Mi.
  - Worker 750m/1Gi → 2 CPU/4Gi.
  - Postgres 1/2Gi → 2/4Gi.
  - Redis 250m/256Mi → 500m/512Mi.
- **Image scan**: Trivy per push; CVE high/critical block deploy.
- **Image label**: `org.opencontainers.image.{source,revision,created}`.
- **Pin by digest** (`FROM image@sha256:...`) di Dockerfile produksi untuk reproducibility.

### 8.3 Init Container & Secrets

- BE Deployment pakai init container `migrate` (`golang-migrate up`) sebelum app start. Failure → CrashLoopBackOff → alert. Alternatif: ArgoCD hook / one-shot Job per release tag.
- **Tidak ada secret di env/Dockerfile.** Inject runtime dari K8s Secret (BE) atau BE→Worker via secure channel saat pod ready. Dev: `compose/.env` (gitignored) + secrets file mount.
- **Tier config (non-secret) via env `Setting`:** `PROVISIONER_MODE={static|k8s}` (lokal=static), `ACTION_BATCH_PARALLELISM` (lokal=2, prod=4), `ACTION_DRY_RUN` (lokal=true, prod=false), `MAX_ACCOUNTS_PER_CONTAINER`. Lihat `INFRA_ANALYST.md` §15.2.

### 8.4 Dev Container

- `compose.yaml` root: Postgres + Redis + MinIO + migrate + api + worker (1 replica) + web.
- Hot reload: BE `air`, FE `next dev`, Worker source mount + `tsx watch`.
- First-run: `make up` → tunggu migrate → seed dummy → `localhost:3000`.
- Test: `make test` (go test, vitest, playwright e2e).

## 9. Testing

- **Test pyramid**: banyak unit, cukup integration, sedikit e2e.
- BE unit: business logic murni (validation, scheduling, parsing, diff reconciler).
- BE integration: setiap HTTP handler + query sqlc non-trivial (`testcontainers-go` + `envtest`).
- Worker unit: adapter platform (mock DOM/PW), transport retry logic, verifikasi.
- FE unit: hook kompleks, parser, util (vitest).
- **E2E wajib**: 3 flow kritis (login + scrape trigger + action execute).
- Coverage: **70% BE**, **60% FE** (delta drop > 2% → block).
- Test **deterministik**: freeze waktu & rand via interface (`Clock`, `Rand`); `vitest.useFakeTimers()`.
- File colocated: `*_test.go`, `*.spec.ts(x)`.
- **Contract test** worker↔BE callback: fixture payload dari `docs/worker-contract` di-replay ke handler → assert skema.

## 10. CI Pipeline (GitHub Actions)

```
lint (eslint + golangci) → typecheck (FE) + go vet → unit (BE+FE+worker)
→ integration (testcontainers) → db-migrate-dry → build (multi-stage)
→ image scan (Trivy) → e2e (PR only) → preview deploy
```

- PR wajib hijau semua check sebelum merge.
- Coverage drop > 2% → block.
- Migration dry-run (golang-migrate up ke Postgres ephemeral → rollback).
- Image build BE (distroless) + FE (standalone) + Worker; Trivy scan (CVE high/critical block).
- `govulncheck` BE per PR. Bundle-size check FE (delta > 20 KB block).
- `docker compose up -d` smoke + e2e + `docker compose down`.
- **CI cache**: turbo remote cache + Go build cache + pnpm store.

## 11. Git Workflow

- **Trunk-based**: `main` selalu deployable.
- Branch hidup max 3 hari; lebih → pecah.
- PR ≤ 400 LOC diff; lebih → pecah.
- Squash merge ke `main`; commit message dari PR title.
- Release tag `v0.x.y` per sprint; changelog auto-generate dari conventional commits.

## 12. Definition of Done

- [ ] Merged via PR, review ≥ 1 approver.
- [ ] Lint + typecheck + test hijau.
- [ ] Migration DB ditulis + tested (CI migrate-dry).
- [ ] Audit log entry untuk aksi sensitif.
- [ ] Feature flag bila belum 100%.
- [ ] Docs updated (API: OpenAPI; FE: Storybook; runbook untuk fitur operasional).
- [ ] Demo recorded untuk Strategist/Operator/Analyst.
- [ ] **Worker/container**: cookie encryption verified, browser context cleared on shutdown, no cookie di log.
- [ ] **Action flow**: manual smoke test di staging (1 like + 1 comment end-to-end akun dummy).

## 13. Security Baseline

- Secret hanya via env/secret manager; tidak pernah di-commit.
- Dependency: `pnpm audit` mingguan (FE); `govulncheck ./...` mingguan (BE); Dependabot auto-PR patch.
- Container: distroless (BE) + Playwright image (worker); Trivy mingguan.
- K8s RBAC: `ServiceAccount smm-provisioner` izin minimal `pods create,delete,get,list` di namespace `smm`. **Tidak boleh** izin node/RBAC/secret lain.
- Pre-commit hook: `gitleaks`.
- Tidak ada production data di seed/staging.

## 14. Observability Baseline

- Metric tiap service: `http_requests_total`, `http_request_duration_seconds`, `jobs_processed_total{status}`, `worker_heartbeat_age_seconds`.
- Setiap error log wajib `traceId`.
- Alert route ke Slack `#smm-oncall`.
- Cost monitor: `proxy_bytes_used_total`, `apify_run_cost_usd_total`; alert daily spend > 90% budget.
- Retention: app log 30 hari (Loki); audit log 1 tahun (Postgres + S3 archive).

## 15. Performance Budget

| Halaman        | TTI     | LCP     | JS gzip  |
| -------------- | ------- | ------- | -------- |
| Dashboard      | 1.5 dtk | 2.0 dtk | ≤ 200 KB |
| Worker Console | 1.5 dtk | 2.0 dtk | ≤ 250 KB |
| Job Queue      | 1.2 dtk | 1.5 dtk | ≤ 300 KB |

## 16. Pelarangan

- ❌ Commit langsung ke `main`. ❌ Merge PR sendiri tanpa reviewer.
- ❌ `console.log` di production (FE pakai logger; BE pakai `slog`).
- ❌ `// @ts-ignore` / `// nolint:` tanpa komentar alasan + tiket.
- ❌ Disable ESLint/golangci rule tanpa diskusi.
- ❌ Tambah dependency baru tanpa ADR singkat di PR.
- ❌ Hardcode URL/kredensial/warna di source.
- ❌ Bypass rate limit scheduler di kode.
- ❌ Log cookie/token/password (CI grep gate).
- ❌ Pin dependency sebagai `latest`.
- ❌ ORM runtime reflection di Go (pakai sqlc + raw SQL).
- ❌ Import melintasi layer terlarang (arch test akan gagal).

## 17. ADRs

Setiap keputusan arsitektur signifikan → `docs/adr/NNNN-slug.md` (format MADR: Context, Decision, Consequences, Alternatives). Wajib ADR bila:

- Ganti DB / queue / auth provider.
- Menambah platform baru (adaptor) dengan trade-off baru.
- Pattern scaling / provisioning baru.
- Dependency runtime baru yang berat (pengaruh bundle/image).

### 17.1 ADR Existing

- `0001-container-per-device.md` — kenapa 1 container = 1 "device" meng-host N akun (maks 1 per platform): satu device nyata memang 1 sesi/platform, hemat resource vs 1-pod-per-akun, invarian dijaga `@@unique([workerId, platform])`. Trade-off: blast radius = N akun saat pod mati (dibatasi `MAX_ACCOUNTS_PER_CONTAINER`).
- `0002-playwright-vs-apify-action.md` — action pakai Playwright (Node), scrape pakai Apify.
- `0003-sequential-batch-action.md` — concurrency = 1 per container.
- `0004-single-team-no-workspace.md` — tanpa multi-tenant di MVP.
- `0005-timescaledb-for-metrics.md` — time-series pakai TimescaleDB.
- `0006-redis-list-durable-action.md` — action Redis List durable (`BLPOP`), bukan BullMQ; kontrol login Redis Pub/Sub (`control-<id>`).
- `0007-shadcn-ui-design-system.md` — kenapa shadcn/ui.
- `0008-go-be-sqlc-pgx.md` — BE Go + sqlc + pgx: statically typed, cold start cepat, single binary, Echo + slog cukup untuk REST + SSE + scheduler dalam 1 proses.
- `0009-all-containerized-no-managed-services.md` — Postgres+Redis+MinIO self-hosted container (bukan RDS/ElastiCache/S3): murah di MVP, kontrol penuh, dev parity. Trade-off: ops DB/Redis ditanggung sendiri.
- `0010-sse-over-websocket.md` — real-time pakai SSE (`EventSource`), bukan socket.io; satu arah server→browser, command via REST. Frame = entitas penuh.
- `0011-worker-redis-pubsub-callback-contract.md` — kontrak worker dari `JG/automation`: worker hanya SUBSCRIBE + POST callback, tidak sentuh DB. Verdict per attempt = baris (upsert). Trade-off: Pub/Sub tanpa delivery guarantee; gap otorisasi callback ditutup setelah keluar loopback.
- `0012-desired-state-worker-provisioning.md` — worker dinamis pakai desired-state reconciler (`Worker.desiredState`), bukan daftar `WORKER_IDS` statis. Add akun → bin-pack slot/platform atau auto-create; container kosong → auto-delete. Idempotensi via `Worker.generation` + pod label. Dua pemicu: event + cron 60 dtk. Semua op di `ProvisionLog`.

## 18. On-call

- Runbook di `infra/runbooks/` per alert.
- Severity matrix di `docs/SEVERITY.md`.
- Handover: tulis di `#smm-oncall` setiap shift.
- **Blameless postmortem** untuk setiap incident P1/P2 dalam 3 hari kerja; action item ber-tiket.
