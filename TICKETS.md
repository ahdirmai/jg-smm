# Tickets — MVP-1-SMM

> Turunan dari `DEVELOPMENT_PHASE.md`. Tiap tiket punya: **ID**, **judul**, **scope**, **acceptance criteria (AC)**, **estimate** (S ≤ 1 hari, M 2-3 hari, L 4-5 hari), **depends on**, **labels**.
>
> Format ID: `<PHASE>-<N>` (mis. `P1-03`). Status default `TODO`. Prioritas: **P0** (wajib phase), **P1** (penting), **P2** (nice-to-have).
> Aturan: satu tiket = satu PR (≤ 400 LOC). Tiket dengan `depends on` belum boleh di-start sebelum dependency `DONE`.
>
> **Dokumen per tiket:** file ini adalah SSOT (ringkasan). Detail tiap tiket (context, scope, technical notes, AC, test plan, DoD) ada di `tickets/<phase>_<n>.md` — di-generate dengan `python3 scripts/gen_tickets.py`. Index: `tickets/README.md`.

---

## P0 — Foundation & Walking Skeleton

| ID    | Judul                 | Scope                                                                               | AC                                                              | Est | Dep   | Pri |
| ----- | --------------------- | ----------------------------------------------------------------------------------- | --------------------------------------------------------------- | --- | ----- | --- |
| P0-01 | Monorepo init         | `pnpm` workspaces + `go.work` + turbo + `packages/{shared,ui,tsconfig}`; build **arm64** | `pnpm i` & `go build ./...` jalan; turbo task terdaftar         | S   | –     | P0  |
| P0-02 | Compose stack (lokal) | `compose.yaml` arm64: Postgres+Timescale, Redis, MinIO, migrate, api, worker(dummy), web; `PROVISIONER_MODE=static`, `ACTION_DRY_RUN=true` | `docker compose up -d --scale worker=3` → semua sehat; `make up` idempoten | M   | P0-01 | P0  |
| P0-03 | BE skeleton Go        | Echo v4 + pgx + slog + `cmd/server` wiring + `/healthz` + graceful shutdown         | `/healthz` 200; SIGTERM drain; non-root distroless image        | M   | P0-01 | P0  |
| P0-04 | DB migration base     | golang-migrate: `TeamConfig`, `User`, `AuditLog` (up+down)                          | `make migrate` naik/turun bersih; CI migrate-dry hijau          | M   | P0-03 | P0  |
| P0-05 | sqlc setup            | `sqlc.yaml` + `db/queries/*.sql` contoh + generate ke `internal/repository/sqlcgen` | `sqlc generate` deterministik; query contoh terpakai di service | S   | P0-04 | P0  |
| P0-06 | Auth + RBAC 3 role    | Session cookie/JWT + middleware role (Strategist/Operator/Analyst)                  | Login/logout; endpoint terproteksi menolak role salah           | M   | P0-03 | P0  |
| P0-07 | OpenAPI SSOT          | `oapi-codegen` dari spec → handler types; `openapi-typescript` → `packages/shared`  | Spec sebagai sumber; FE type ter-generate di build              | M   | P0-03 | P0  |
| P0-08 | FE skeleton Next.js   | Next.js 15 App Router + shadcn/ui (dark, indigo) + layout dashboard + 1 halaman     | `pnpm dev` → halaman tampil; theme token shadcn                 | M   | P0-01 | P0  |
| P0-09 | Worker skeleton       | Node 22 + Playwright (arm64) + struktur `platforms/core/transport` + heartbeat dummy | Container boot; heartbeat ke BE tiap 30 dtk; log terstruktur   | M   | P0-02 | P0  |
| P0-10 | CI pipeline           | GitHub Actions: lint, typecheck, unit, integration, migrate-dry, build, Trivy       | PR hijau semua check; cache turbo/go/pnpm                       | M   | P0-01 | P0  |
| P0-11 | Pre-commit & Makefile | `gitleaks` hook + `make {up,test,migrate,seed,down}`                                | Commit dengan secret terblok; `make up` end-to-end              | S   | P0-02 | P0  |
| P0-12 | ADR bootstrap         | `docs/adr/` + ADR 0001–0012 tercatat (format MADR)                                  | Semua ADR existing ada file; template MADR tersedia             | S   | –     | P1  |

**Exit P0:** `git clone && make up` (lokal Mac M2 16 GB) → dashboard kosong + login + `/healthz` OK + CI hijau.

> Lingkungan P0–P4 = **lokal Mac M2 16 GB** (docker-compose, ARM64, tanpa K8s). K8s + reconciler = jalur produksi (P1 khusus prod, diuji via envtest opsional). Lihat `INFRA_ANALYST.md` §15.1/§15.2.

---

## P1 — Account Lifecycle & Provisioning

| ID    | Judul                             | Scope                                                                                                     | AC                                                             | Est | Dep          | Pri |
| ----- | --------------------------------- | --------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | --- | ------------ | --- |
| P1-01 | ERD → migrations                  | Tabel `Account`, `Worker`, `ProxyGroup`, `ProvisionLog`, `Heartbeat` + enum                               | Migration up/down bersih; `@@unique([workerId, platform])` ada | M   | P0-04        | P0  |
| P1-02 | Domain & port                     | `internal/domain` entity/enum/error + `internal/port` interface (K8sClient, Publisher, Repository, Clock) | `domain` nol import internal; arch test lolos                  | M   | P1-01        | P0  |
| P1-03 | K8s provisioner                   | `internal/adapter/k8s` create/delete pod+PVC+service; rate limit 10 create/menit                          | Pod+PVC `smm-session-<workerId>` terbuat di envtest            | L   | P1-02        | P0  |
| P1-04 | Desired-state reconciler          | `diff(desired, actual) → actions`; `Worker.desiredState` + `generation` label                             | Reconciler idempotent; retry aman; pod gen usang dihapus       | L   | P1-03        | P0  |
| P1-05 | Bin-packing service               | Cari container slot platform kosong / auto-create; `MAX_ACCOUNTS_PER_CONTAINER`                           | Add akun → ter-pack; container 0 akun → auto-delete            | M   | P1-04        | P0  |
| P1-06 | ProvisionLog + orphan sweeper     | Catat CREATE/DELETE; sweep pod orphan; safety-net 60 dtk                                                  | Setiap op tercatat; orphan terhapus; test sweeper              | M   | P1-04        | P0  |
| P1-07 | Credential crypto                 | AES-256-GCM encrypt/decrypt `passwordEnc`; write-only, tak pernah select                                  | Round-trip test; CI grep blokir plaintext credential           | S   | P1-02        | P0  |
| P1-08 | Redis transport BE                | Publisher: `LPUSH queue:action:<workerId>`, `PUBLISH control-<workerId>`; receivers ack                   | Commit DB sebelum LPUSH (test order); receivers dilaporkan     | M   | P1-02        | P0  |
| P1-09 | Worker BLPOP loop                 | `queue:action:<workerId>` BLPOP + ACK; 2 Redis client (sub/cmd)                                           | Job terambil sequential; malformed di-drop + log               | M   | P0-09        | P0  |
| P1-10 | Worker login headful              | Xvfb+x11vnc+noVNC; `runLogin`/`waitForLoginOutcome`; persist `storageState` per platform                  | Login verified → session tersimpan; bukti = auth cookie        | L   | P1-09        | P0  |
| P1-11 | Control channel worker            | Subscribe `control-<workerId>`: `auth-login`/`auth-input`/`auth-clear` (payload accountId)                | Instruksi bertarget; jalur control tidak di-await              | M   | P1-09        | P0  |
| P1-12 | 2FA/checkpoint flow               | Status `NEEDS_INPUT`; context hidup di Map; screenshot; report callback                                   | Kode OTP sambung context sama; bisa berulang                   | L   | P1-10        | P0  |
| P1-13 | Callback endpoints BE             | `POST /internal/{action,account}-callback`, `/heartbeat` + sanitasi + MaxBytes                            | Status enum tervalidasi; `path.Base` screenshot; truncate      | M   | P1-08        | P0  |
| P1-14 | Proxy binding                     | `ProxyGroup` CRUD + assign region-matched; proxy context-level worker                                     | Akun terikat group; proxy dipakai saat action                  | M   | P1-01        | P1  |
| P1-15 | Add Account UI                    | Modal multi-step (platform+cred, proxy) + stepper SSE                                                     | Submit → provisioning tampil real-time                         | M   | P1-05, P1-07 | P0  |
| P1-16 | Account ops (pause/resume/remove) | `Worker.desiredState` toggle; remove akun → cleanup pod+PVC+service                                       | Pause→PVC ditahan; Resume→ready tanpa login; remove bersih     | M   | P1-04, P1-15 | P0  |
| P1-17 | SSE stream BE                     | `GET /api/stream`: `account-updated`, `worker-health`, `provision-updated`                                | Frame entitas penuh; reconnect + refetch                       | M   | P0-07        | P0  |
| P1-18 | Account list page FE              | Tabel/grid akun + status + `AuthStatusBadge` + filter                                                     | Data akun live; filter platform/status/region                  | M   | P1-17        | P1  |

**Exit P1:** Add akun IG+Threads dari UI → 1 container host keduanya → `authenticated`; restart/pause/resume/sweep teruji.

---

## P2 — Scrape (IG + Threads) & Monitoring

| ID    | Judul                      | Scope                                                                       | AC                                                                | Est | Dep          | Pri |
| ----- | -------------------------- | --------------------------------------------------------------------------- | ----------------------------------------------------------------- | --- | ------------ | --- |
| P2-01 | ERD scrape                 | `ScrapeJob`, `Post`, `Comment`, `MetricSnapshot` (+Timescale hypertable)    | Migration bersih; hypertable + retention aktif                    | M   | P1-01        | P0  |
| P2-02 | Scrape scheduler           | FIFO per akun + jitter 5-15 dtk + backoff rate limit                        | Urutan terjaga; backoff saat rate limit                           | M   | P2-01        | P0  |
| P2-03 | Apify adapter              | Actor per platform (post URL, profile, hashtag, mention); K8s Job ephemeral | Job jalan; hasil ter-normalisasi; kredensial dari BE by accountId | L   | P1-03, P2-01 | P0  |
| P2-04 | Ingest pipeline            | Raw → MinIO; normalisasi idempotent → Postgres                              | Re-run ingest idempoten; raw tersimpan                            | M   | P2-03        | P0  |
| P2-05 | Metric aggregation         | `MetricSnapshot` tiap 30 menit top-100 post; reach/views reels/mention      | Snapshot tersimpan; query cepat via Timescale                     | M   | P2-04        | P0  |
| P2-06 | Session refresh job        | `SESSION_REFRESH` 7 hari sebelum expiry → re-login via control              | Job trigger tepat waktu; gagal 3x → quarantined                   | M   | P1-10, P2-02 | P1  |
| P2-07 | Alert engine               | Aturan views drop >50%/24j, mention spike >3x → notif                       | Alert firing di staging; tidak duplikat                           | M   | P2-05        | P1  |
| P2-08 | Monitoring dashboard FE    | Chart metric per akun/post + sparkline + filter waktu                       | Data live; p95 load < 1.5 dtk                                     | M   | P2-05, P1-17 | P1  |
| P2-09 | Live monitoring (deferred) | Live TikTok/IG views bila sumber tersedia                                   | Spike ADR bila API tak ada; tandai deferral                       | S   | P2-03        | P2  |

**Exit P2:** Scrape IG+Threads end-to-end → Post+Comment masuk DB → tampil dashboard + metric + alert.

---

## P3 — Action Engine (Playwright)

| ID    | Judul                             | Scope                                                                          | AC                                                    | Est | Dep          | Pri |
| ----- | --------------------------------- | ------------------------------------------------------------------------------ | ----------------------------------------------------- | --- | ------------ | --- |
| P3-01 | ERD action                        | `ActionJob`, `ActionLog` (UNIQUE `(actionJobId,attempt)`, screenshot COALESCE) | Migration bersih; constraint ada                      | M   | P1-01        | P0  |
| P3-02 | Template engine                   | Template pool + var `{topic}{product}{handle}` + random + dedupe 7 hari        | Pick random; dedupe per target 7 hari                 | M   | P3-01        | P0  |
| P3-03 | Ban-word detector                 | Regex list editable + validasi inline                                          | Komentar banned ditolak sebelum queue                 | S   | P3-02        | P1  |
| P3-04 | Worker platform adapter (IG)      | `PlatformAdapter`: login/like/comment/verify + `SEL`                           | Like & comment IG jalan di akun dummy                 | L   | P1-10        | P0  |
| P3-05 | Worker platform adapter (Threads) | Implementasi sama untuk Threads                                                | Like & comment Threads jalan                          | L   | P3-04        | P0  |
| P3-06 | Platform registry (OCP)           | `map[Platform]Adapter`; tambah platform tanpa edit controller                  | Registry teruji; controller tak tahu platform konkret | S   | P3-04        | P0  |
| P3-07 | Sequential controller             | `context` per akun + jitter 30-90 dtk + max 1 tab + close di finally           | Concurrency=1; isolasi cookie antar task              | M   | P3-06        | P0  |
| P3-08 | Ground-truth verify               | Comment di feed (poll ≤15s), like state; gagal→FAILED                          | Tidak ada `SUCCESS` tanpa verifikasi                  | M   | P3-07        | P0  |
| P3-09 | Cooldown gate                     | `SET cooldown:comment:<id>:<sha256(url)> PX 60000 NX`                          | Double-action dicegah; report FAILED + pesan          | S   | P3-07        | P0  |
| P3-10 | Rate limit scheduler              | IG 30/jam, Threads 15/jam, dipaksa BE (bukan job)                              | Kuota tidak terlewati; backoff                        | M   | P3-07        | P0  |
| P3-11 | Action callback + retry           | `action-callback` retry 3x backoff, 4xx stop, never-throw; Idempotency-Key     | Retry teruji; update idempoten                        | M   | P1-13, P3-07 | P0  |
| P3-12 | Error classification              | `TRANSIENT`/`AUTH`/`RATE_LIMIT`/`BANNED` → `ActionLog.error_class`             | Tiap fail terkategori; tampil di dashboard            | M   | P3-11        | P0  |
| P3-13 | Action UI                         | Queue 50 action + template composer + live status                              | Enqueue dari UI; status real-time                     | M   | P3-02, P1-17 | P0  |
| P3-14 | CDP screenshot                    | Screenshot via `Page.captureScreenshot`; nama deterministik                    | Screenshot ada; tidak timeout                         | S   | P3-04        | P0  |

**Exit P3:** 100 like/comment tereksekusi, success ≥85%, comment terverifikasi di feed, cooldown & rate limit teruji.

---

## P4 — Dashboard Productization & Operator UX

| ID    | Judul                                | Scope                                                                     | AC                                                            | Est | Dep   | Pri |
| ----- | ------------------------------------ | ------------------------------------------------------------------------- | ------------------------------------------------------------- | --- | ----- | --- |
| P4-01 | ContainerCard (container + accounts) | Grid container dgn child account rows; ops Pause/Resume/Remove            | Layout sesuai DESIGN_SYSTEM; ops jalan                        | M   | P1-18 | P0  |
| P4-02 | Job Queue virtualized                | `@tanstack/react-virtual`, group per akun, drawer attempt+screenshot      | >1000 baris lancar; drawer lengkap                            | M   | P3-13 | P0  |
| P4-03 | SSE realtime lengkap                 | `action-updated`, `account-updated`, `worker-health`, `provision-updated` | Latensi < 2 dtk; reconnect refetch                            | M   | P1-17 | P0  |
| P4-04 | Report builder                       | Pilih akun/post × rentang × metrik; render tabel+chart                    | Query benar; hasil konsisten                                  | M   | P2-08 | P1  |
| P4-05 | Export CSV/JSON                      | Export dari report builder                                                | File valid; besar data ditangani streaming                    | S   | P4-04 | P1  |
| P4-06 | Scheduled report email               | Job mingguan → email                                                      | Email terkirim; template rapi                                 | M   | P4-05 | P2  |
| P4-07 | Bulk import CSV                      | Upload maks 100 baris + validasi + progress SSE + antrian challenge       | Response `{queued,invalid,rate_limited}`; rate limit 10/menit | M   | P1-15 | P0  |
| P4-08 | Live ticker + LiveBrowserModal       | Stream event + iframe noVNC                                               | Ticker live; modal noVNC jalan                                | M   | P4-03 | P1  |
| P4-09 | RBAC UI per role                     | Sembunyikan/disable aksi sesuai role                                      | Role salah tidak lihat aksi                                   | S   | P0-06 | P1  |
| P4-10 | Storybook + a11y                     | Story semua custom komponen + `addon-a11y` + axe CI                       | Semua komponen punya story; axe lolos                         | M   | P4-01 | P1  |

**Exit P4:** Operator kelola 100 akun (add/pause/remove/bulk) dari UI tanpa redeploy; report + schedule; Storybook lengkap.

---

## P5 — Hardening, Scale & GA

| ID    | Judul                     | Scope                                                                   | AC                                              | Est | Dep          | Pri |
| ----- | ------------------------- | ----------------------------------------------------------------------- | ----------------------------------------------- | --- | ------------ | --- |
| P5-01 | Scale test ~50 container  | 100 akun ter-pack ~50 container; bin-packing + quota 500 pod            | Stabil; resource sesuai; quota terjaga          | L   | P1-05        | P0  |
| P5-02 | Health score + quarantine | Score 0-100; auto-quarantine <30; anti-flapping 3 restart/10m           | Quarantine otomatis; manual reset ada           | M   | P3-12        | P0  |
| P5-03 | Observability lengkap     | Prometheus metric + Grafana + Loki + alert Slack                        | Semua metric ter-export; alert teruji           | M   | P0-10        | P0  |
| P5-04 | Security review           | RBAC minimal, secret rotation, gitleaks, Trivy, govulncheck             | Sign-off; nol temuan high/critical              | M   | P0-10        | P0  |
| P5-05 | Backup & restore          | Postgres WAL, MinIO, PVC session + DR drill                             | Restore < 30 menit (drill)                      | M   | P0-02        | P0  |
| P5-06 | Runbook + severity matrix | `infra/runbooks/*` per alert + `docs/SEVERITY.md` + on-call             | Semua alert punya runbook; MTTR < 5 menit       | M   | P5-03        | P0  |
| P5-07 | Load test FE/BE           | Dashboard p95 <1.5 dtk; BE throughput                                   | Target terpenuhi; no memory leak                | M   | P4-03        | P1  |
| P5-08 | Cost monitoring           | `proxy_bytes_used_total`, `apify_run_cost_usd_total` + alert 90% budget | Metric export; alert budget                     | S   | P5-03        | P1  |
| P5-09 | Docs final sync           | PRD/ERD/SYSTEM_DESIGN/DESIGN_SYSTEM/DEVELOPMENT_RULE/ADR sync           | Review konsistensi lintas dokumen lolos         | M   | –            | P0  |
| P5-10 | GA sign-off               | Uji 30 hari + acceptance v1.0                                           | 100+ akun stabil; nol ban; acceptance PRD lolos | L   | P5-01..P5-09 | P0  |

**Exit P5:** 100+ akun stabil 30 hari, uptime ≥98%, action success ≥90%, nol ban, DR drill sukses, runbook lengkap → **GA**.

---

## Ringkasan Beban

| Phase     | Jumlah tiket | Estimasi (S/M/L)               |
| --------- | ------------ | ------------------------------ |
| P0        | 12           | 6S 5M 0L 1S                    |
| P1        | 18           | 3S 12M 3L                      |
| P2        | 9            | 1S 7M 1L                       |
| P3        | 14           | 4S 8M 2L                       |
| P4        | 10           | 2S 7M 1L                       |
| P5        | 10           | 2S 6M 2L                       |
| **Total** | **73 tiket** | **≈ 14 minggu (2–3 engineer)** |

## Aturan Tiket

- Satu tiket = satu PR (≤ 400 LOC). Tiket L dipecah bila PR membengkak.
- `depends on` harus `DONE` sebelum start. Blocked → naikkan ke PM.
- Setiap PR menyertakan link tiket + AC yang dipenuhi di deskripsi.
- Tiket tidak selesai bila AC belum semua ✅ (lihat Definition of Done di `DEVELOPMENT_RULE.md`).
