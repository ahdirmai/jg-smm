# PRD — Social Media Management (Apify + Playwright)

> Status: Approved v1.0. Konfirmasi closed (§1).

## 1. Konfirmasi (sudah disetujui)

| #   | Keputusan                   | Nilai                                                                                                                                                        |
| --- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1   | Platform MVP                | **Instagram + Threads**; target akhir **7 platform** (Threads, Facebook, Instagram, LinkedIn, X, YouTube, TikTok) — lihat `PLATFORM_MATRIX.md`               |
| 2   | Comment generation          | Template pool                                                                                                                                                |
| 3   | Action                      | Comment / Like ke post/comment target                                                                                                                        |
| 4   | Tenant                      | Single team (no multi-tenant)                                                                                                                                |
| 5   | Akun                        | Sudah tersedia, ratusan                                                                                                                                      |
| 6   | Akun worker vs akun resmi   | **Dua konsep terpisah.** Akun worker = eksekutor action (F2/F6). **Official Account** = akun brand/client yang **dipantau** (read-only, tanpa login/action). |
| 7   | Sumber analytics akun resmi | **3rd-party provider** (provider-agnostic; bukan self-scrape)                                                                                                |

Implikasi:

- Skema DB tanpa `Workspace` (single tenant) → nanti migrasi kalau jadi SaaS.
- Threads = Meta, scraper Apify actor berbeda; rate limit lebih ketat dari IG. Action tetap via Playwright (browser automation), bukan Meta API.
- 100+ akun = kesehatan akun kritis → health score + auto-quarantine wajib jalan hari-1.
- 100+ akun = proxy budget besar → geo-grouping wajib, cap per region.
- **Pemisahan dua domain akun** (worker vs official) berarti analitik platform resmi **tidak** dihitung dari akun worker. Akun worker murni eksekutor; metrik performa brand datang dari **Official Account** via 3rd-party. Lihat F5.

## 2. Latar Belakang

Tim Social Media Ops butuh satu panel untuk:

- Memantau performa akun/post lintas platform (monitoring).
- Mengelola kontainer scraper terdistribusi (worker orchestration).
- Mengeksekusi action otomatis (like/comment) pada target yang dikurasi manusia.

## 3. Persona

- **Strategist** — baca KPI, tren, laporan. Read-mostly. Pemakai utama halaman Monitoring/Official Accounts.
- **Operator** — kelola worker, antri action, pantau kegagalan. Action-heavy.
- **Analyst** — bangun report custom, export (termasuk analytics akun resmi). Query-heavy.

## 4. Scope

**In (MVP):**

- Scrape Instagram + Threads: post, comment, like, share, repost, like-on-comment, reply-comment, views.
- **Monitoring akun resmi (Official Accounts):** reach, reels/video views, mention, status live, per platform — bersumber dari 3rd-party provider.
- Action: comment-on-post, like-on-post, comment-on-comment, like-on-comment, report-on-post, report-on-comment.
- Worker orchestration: container spawn/terminate, IP geo grouping, healthcheck.
- Comment generation: template pool + variabel injection.
- Login akun interaktif: worker headful (Playwright + Xvfb), operator selesaikan 2FA/checkpoint via noVNC atau input kode OTP; session `storageState` persist per akun (PVC).
- Dashboard 3-role (single team), real-time via SSE.
- Internal performance report (CSV/JSON export).
- Health score per akun + auto-quarantine.

**Out (MVP):**

- 5 platform lain (Facebook, LinkedIn, X, YouTube, TikTok) — slot adapter siap, brief diisi bertahap (`PLATFORM_MATRIX.md` + `platforms/`). Halaman analytics-nya tetap dirender (struktur per-platform) walau datanya kosong sampai adapter/platform aktif.
- Live IG/Threads streaming viewer.
- LLM-generated comments.
- Abuse-report button ke platform.
- Auto-reply chain (comment → reply → like).
- SSO/SAML.
- Mobile app.
- Multi-tenant workspace.

## 5. Functional Requirements

### F1 — Auth & User

- F1.1 Single team; user invite via email + invite link.
- F1.2 Role: owner / strategist / operator / analyst (1 user = 1 role).
- F1.3 Session 24 jam, refresh token 30 hari.
- F1.4 RBAC middleware: strategist (read), operator (action + worker), analyst (read + export), owner (semua).

### F2 — Account & Proxy

- F2.1 Connect akun via **login interaktif**: operator isi username+password → BE encrypt AES-256-GCM → publish ke `control-<workerId>` (channel per container; payload bawa `accountId`) → worker headful login → tunggu outcome. Akun di-assign ke container ber-slot-kosong (lihat F3.9); bila fleet kosong, user **buat container dulu** (F3.1a) atau andalkan fallback auto-create. Fallback legacy: paste cookie (jalur `credentials` existing).
- F2.1a Outcome 2FA/checkpoint → status `NEEDS_INPUT`: worker tahan context browser hidup + screenshot → operator masuk kode via dialog `auth-input` ATAU klik langsung lewat noVNC (iframe di dashboard). Bisa berulang (checkpoint kedua).
- F2.1b Bukti login = **auth cookie** (`sessionid`, `ds_user_id`), bukan redirect URL. Sukses → `storageState()` persist ke PVC session + `handle` diverifikasi dari halaman profil.
- F2.2 Bind akun ke ProxyGroup (region, provider).
- F2.3 Health check akun: login validity, rate limit headroom, ban signal (cek dari response IG/Threads).
- F2.4 Health score 0-100; auto-quarantine < 30.

### F3 — Worker Orchestration (desired-state, dinamis)

- F3.1 Spawn container (K8s pod) 1:1 dengan **device**, bukan akun: **1 container meng-host N akun, maks 1 akun per platform** (`@@unique([workerId, platform])`). Label `smm.worker=<workerId>`, `smm.generation=<n>`.
- F3.1a **Default tanpa container.** Saat sistem kosong, fleet worker = **nol**. Container **dibuat manual oleh user dari dashboard** (pilih platform → provision 1 `/dev/slot`). Tidak ada container yang di-spawn otomatis hanya karena data kosong.
- F3.2 Container = unit provisioning: bind ke N `Account` (maks 1/platform) + 1 `ProxyGroup` (region-matched). Fingerprint (UA/locale/viewport) fixed per container; tiap akun punya `context` + `storageState` sendiri.
- F3.3 **Desired-state reconciler**: `Worker.desiredState` (`RUNNING|STOPPED`) = keinginan (di level container/device); actual = `Worker.containerId` + pod K8s. Reconciler menyelaraskan keduanya. Pemicu: **event** (add/remove akun, pause/resume dari UI, latensi detik) + **periodic safety-net** (60 dtk). Pause/Resume berlaku ke **seluruh container** (semua akunnya).
- F3.4 **Idempotent & anti-spawn-ganda**: `Worker.generation` naik tiap apply, pod dilabeli generation; pod generation usang / ganda = orphan → dibunuh. Retry event aman.
- F3.5 Heartbeat tiap 30 dtk; tanpa heartbeat > 90 dtk → `DEAD` + respawn (gen++). Anti-flapping: max 3 restart / 10 menit → container `quarantined` (semua akun di dalamnya), butuh reset manual.
- F3.9 **Assign akun → container (bin-packing)**: default akun di-assign ke container yang sudah dibuat user dan punya slot platform kosong (`MAX_ACCOUNTS_PER_CONTAINER`, default = jumlah platform = 2 di MVP), region-matched. Bila **tidak ada** slot: (a) fallback **auto-create** container baru (default, agar operator tidak klik manual 25× saat 50 akun), atau (b) reject dengan hint "buat container dulu" bila auto-create dimatikan (`PROVISION_AUTO_CREATE=false`). Container dengan **0 akun** → auto-delete. 100 akun → **~50 container**.
- F3.6 **Pause** (operator, level container): drain 60 dtk → delete pod → PVC session ditahan. **Resume**: reconciler create pod baru → semua session akun terbaca → ready tanpa login ulang.
- F3.7 Setiap op CREATE/DELETE dicatat di `ProvisionLog` (generation, hasil, error) untuk trace & troubleshooting.
- F3.8 Rate limit K8s API `10 create/menit` (burst 20); burst create di-queue Redis.

### F4 — Scrape Job

- F4.1 Antrian via Redis; FIFO per akun, jitter 5-15 dtk.
- F4.2 Backoff eksponensial saat rate limit.
- F4.3 Raw payload → S3 (atau MinIO); normalisasi → Postgres.
- F4.4 Scrape target: post URL, profile URL, hashtag, mention.

### F5 — Monitoring Akun Resmi (Official Accounts)

> **Dua domain akun yang tidak boleh dicampur.**
>
> - **Worker Account** (`Account`) — eksekutor action. Login + Playwright. **Tidak** masuk analitik performa.
> - **Official Account** (`OfficialAccount`) — akun brand/client yang **dipantau**. **Read-only**: tanpa kredensial, tanpa login, tanpa action. Ini **subjek** analitik.

- F5.1 **Daftar Official Account**: user menambahkan `platform + handle` (+ URL profil opsional). Tidak ada kredensial; verifikasi kepemilikan (opsional) lewat metadata provider.
- F5.2 **Ingest metrik via 3rd-party provider** (provider-agnostic). BE menarik/menerima snapshot per akun per platform secara **terjadwal** (default tiap 30 menit) dan **on-demand**. Jalur ingest **terpisah** dari jalur action worker (lihat `SYSTEM_DESIGN.md` → Data Flow → Analytics).
- F5.3 **Snapshot time-series**: setiap tarikan menulis baris `AnalyticsSnapshot` (`officialAccountId`, `ts`, `metrics` JSONB + kolom query cepat `reach`, `views`, `mentions`, `followers`). Query tren = `(officialAccountId, ts)`.
- F5.4 **Metrik per platform berbeda** (definisi & ketersediaan per platform ada di `PLATFORM_MATRIX.md` §2.3). Contoh IG/Threads: reach, profile views, follower delta, mention, top posts. Contoh YouTube: views, watch time, subs. Contoh TikTok: views, live viewers.
- F5.5 **Halaman analytics per platform** — 7 halaman, satu per platform, layout konsisten, KPI berbeda per platform. Plus **Overview** yang mengagregasi seluruh akun resmi.
- F5.6 **Live monitoring**: status live TikTok / live IG (viewer count, durasi) — sumber tetap provider; ditandai `LIVE`/`OFFLINE`.
- F5.7 **Mention tracking**: daftar mention terhadap akun resmi (author, teks, url, sentiment-opsional dari provider).
- F5.8 **Alert**: penurunan views/reach >50% dalam 24 jam, lonjakan mention >3x baseline, akun resmi berhenti ter-ingest (`AnalyticsIngestRun` gagal berulang).
- F5.9 **Data provenance**: tiap snapshot menyimpan `provider` + `providerRunId` + `fetchedAt`, agar bisa diaudit & provider bisa diganti tanpa kehilangan histori.
- F5.10 **Degradasi anggun**: provider down ≠ blocking dashboard; UI menampilkan snapshot terakhir + badge `stale` (umur data) dan status `AnalyticsIngestRun` terakhir.
- F5.11 **Batas MVP**: akun resmi tidak melakukan action apa pun ke platform (read-only). Bila nanti ada auto-engage ke akun resmi, dibuat requirement terpisah.

### F6 — Action

- F6.1 Comment-on-post: pilih template → schedule atau now → assign worker.
- F6.2 Like-on-post: idempotent (skip jika sudah like).
- F6.3 Comment/like pada komentar: butuh parent comment ID.
- F6.4 Rate limit per akun: IG 30/jam, Threads 15/jam.
- F6.5 Cool-down gagal 1 jam.
- F6.6 **Eksekusi sequential dalam 1 container** — 1 browser tab, antrian lintas-akun di dalam container. Concurrency = 1 per container (satu akun pada satu waktu). Tidak ada paralel tab. Jitter 30-90 dtk antar action.
  - F6.6a **Batch paralel antar container**: 1 batch disebar ke maks `ACTION_BATCH_PARALLELISM` container (default 4) — N worker bersamaan, tiap container tetap sequential. Guardrail rate-limit/cooldown per akun tidak dilonggarkan oleh batch.
- F6.7 **Verifikasi ground truth** pasca-action: comment → teks harus benar-benar muncul di feed (bukan sekadar composer clear); like → state tombol berubah. Gagal verifikasi = `FAILED`, bukan `SUCCESS`.
- F6.8 **Cooldown gate per (akun, target)**: Redis `SET` + PX TTL 60 dtk sebelum eksekusi komentar — cegah dua action ke post sama dalam window sempit. Dedeny → `FAILED` + pesan cooldown di `ActionLog` (terlihat operator, bukan silent no-op).

### F7 — Comment Template

- F7.1 Template: text + variabel `{topic}` `{product}` `{handle}`.
- F7.2 Random pick per execution, dedupe 7 hari per target.
- F7.3 Ban-word detector inline (regex list editable).

### F8 — Report

- F8.1 Builder: pilih akun/post × rentang waktu × metrik.
- F8.2 Export CSV/JSON.
- F8.3 Schedule report mingguan ke email.

### F9 — Container = 1 Device (N Account)

- **1 container = 1 "device" = N akun, maks 1 akun per platform** (`@@unique([workerId, platform])`). MVP platform = IG + Threads → maks 2 akun/container. 100 akun → **~50 container**.
- Container image: Node.js + Playwright + Apify SDK + stealth plugin.
- **Action path**: Playwright (persistent browser context, sequential batch).
- **Scrape path**: Apify actor (K8s Job ephemeral, reuse kredensial akun via BE by `accountId`).
- Container long-running (stateful), scraper ephemeral.
- Jadwal action: sequential lintas akun dalam container, jitter 30-90 dtk, max 1 tab.
- PVC session per container: `session-<platform>.json` per akun (isolasi cookie). Container security: runAs non-root, read-only rootfs, drop ALL capabilities, seccomp profile `runtime/default`.

### F10 — Session & Cookie Lifecycle

- Sumber kebenaran sesi = `storageState()` Playwright di PVC per-akun (`/data/sessions/session-<platform>.json`), hasil login F2.1. Fallback legacy: cookie blob encrypted (`Account.credentials`).
- `Account.cookieExpiryAt` diestimasi dari umur cookie saat login.
- 7 hari sebelum expiry → scheduler trigger `ScrapeJob` type `SESSION_REFRESH`: worker re-login via `control-<workerId>` `auth-login` (payload `accountId`; username+password terenkripsi di DB) atau pakai cookie masih hidup.
- Gagal refresh 3x → akun `quarantined` + notif owner.
- UA + locale + viewport **fixed per worker** — identitas fingerprint harus sama antara sesi login & sesi action; beda fingerprint = trigger challenge Meta.

### F11 — Telemetri & Audit

- Semua action tulis `ActionLog` (jobId, attempt, payload template, rendered text, response excerpt, screenshot URL).
- Semua mutasi tulis `AuditLog` (kill container, edit template, hapus akun, manual action, login).
- Sampling error 100% ke Loki; info 10%.

### F12 — Error Handling Action

- Action fail diklasifikasikan: `TRANSIENT` (network, 5xx) → retry. `AUTH` (cookie invalid) → quarantine + alert. `RATE_LIMIT` (429) → backoff panjang. `BANNED` (response "account suspended") → permanent quarantine.
- Semua fail masuk `ActionLog.error_class` untuk dashboard.

### F13 — On-demand Worker Provisioning via Dashboard

- Client (Operator/Owner) tambah akun dari UI: isi platform + username + password, pilih proxy group → submit.
- BE Go flow: encrypt kredensial → **assign** akun ke container (F3.9): cari `Worker` region-matched dengan slot platform kosong; bila tak ada, auto-create `Worker` baru (`desiredState=RUNNING`, `status=PENDING`) bila `PROVISION_AUTO_CREATE=true`, else reject. Insert `Account` (`status=PENDING`, `authStatus=AUTHENTICATING`, `workerId`) → `enqueue reconcile` → provisioner create pod `smm-worker-{workerId}` (label `smm.worker=<workerId>`, `smm.generation=<n>`) + PVC `smm-session-{workerId}` + ClusterIP service noVNC.
- Pod boot: pull image → mount PVC session → headful Chromium launch (Xvfb) → `BLPOP` queue → heartbeat pertama → status `READY`. Timeout boot 90 dtk → `ERROR` + alert. Container meng-host semua akunnya (maks 1/platform).
- Login: BE `PUBLISH control-<workerId>` `auth-login` (payload `accountId`) → worker login → callback `authStatus`. (Detail: F2.1.)
- Progress UI: stepper `Creating pod → Booting browser → Waiting login → Ready` (+ cabang `Needs input` saat 2FA) via SSE, ETA per step.
- **Pause container** dari UI: `Worker.desiredState=STOPPED` → reconciler drain → delete pod → **PVC ditahan**. Resume: `desiredState=RUNNING` → pod baru → semua session akun terbaca → ready tanpa login ulang.
- **Remove akun** dari UI: `Account.status=ARCHIVED`; jika akun terakhir di container → reconciler delete pod + PVC + service + `Worker` row (soft delete, data audit tetap).
- Jumlah worker **dinamis** — dikendalikan desired-state dari dashboard tanpa redeploy BE/FE dan tanpa daftar worker statis.
- **Idempotent provisioning**: `Worker.generation` + label pod = guard anti-spawn-ganda; tiap op dicatat di `ProvisionLog`.
- Burst create di-rate-limit `10 create/menit` ke K8s API; antrian di Redis. Quota namespace 500 pod.
- HPA **tidak** dipakai untuk worker (replicas per-container via desired-state). HPA hanya untuk BE/FE.

## 6. Non-Functional Requirements

| Aspek              | Target                                  |
| ------------------ | --------------------------------------- |
| Availability       | 99% (single region MVP)                 |
| Scrape latency     | target → data tampil < 5 menit          |
| Action latency     | schedule → eksekusi < 2 menit           |
| Containers         | 50 (MVP, ~100 akun), 250+ (v1, dinamis) |
| Data retention     | 90 hari hot, 1 tahun cold               |
| p95 dashboard load | < 1.5 dtk                               |
| A11y               | WCAG 2.1 AA                             |

## 7. KPI

- Scraping success rate ≥ 95% per akun per hari.
- Action success rate ≥ 90%.
- Worker uptime ≥ 98%.
- MTTR worker failure < 5 menit.
- **Analytics freshness**: ≥ 90% Official Account punya snapshot < 60 menit pada jam operasional.
- **Analytics ingest success**: ≥ 98% `AnalyticsIngestRun` sukses per hari.

## 8. Release Plan

| Versi | Isi                                                                                                        | ETA       |
| ----- | ---------------------------------------------------------------------------------------------------------- | --------- |
| v0.1  | IG scrape read-only + auth single-team                                                                     | 3 minggu  |
| v0.2  | Container-per-device orchestrator + bin-packing + proxy binding + Threads adapter + Add-account UI         | 5 minggu  |
| v0.3  | Monitoring akun resmi (Official Accounts) + ingest 3rd-party + halaman per-platform + health score + alert | 7 minggu  |
| v0.4  | Action Playwright (like + comment) + template sequential                                                   | 9 minggu  |
| v0.5  | Report + scale ke ~50 container (100 akun) stabil                                                          | 11 minggu |
| v1.0  | GA — 2 platform stabil, 100+ akun                                                                          | 13 minggu |

## 9. Risiko

- **ToS platform** — IG & Threads automation rentan banned. Mitigasi: residential proxy, jitter, low volume, dokumentasikan legal review.
- **Akun farm (100+)** — kualitas rendah = banned massal. Mitigasi: health score + auto-quarantine.
- **Proxy cost** — residential $15/GB × ratusan akun = mahal. Mitigasi: budget cap harian; pooling agresif; reuse koneksi.
- **Threads API limit** — Meta batasi write API. Mitigasi: Playwright browser automation (lebih risky).
- **IG rate limit** — 30/jam per akun ketat. Mitigasi: queue per akun + jitter acak.
- **Container blast radius** — 1 pod crash = N akun idle (N = platform/container, ≤2 MVP). Mitigasi: K8s readiness probe + orphan sweeper; ukuran container dibatasi `MAX_ACCOUNTS_PER_CONTAINER`.
- **Playwright detection** — IG/Threads update anti-bot. Mitigasi: stealth plugin + viewport random + canvas noise.
- **Ketergantungan 3rd-party analytics** — provider down / ubah API / rate limit → data akun resmi basi. Mitigasi: snapshot terakhir + badge `stale`, provider-agnostic adapter, `AnalyticsIngestRun` retry + alert.
- **Kualitas data provider** — metrik bisa berbeda dari native platform. Mitigasi: simpan `provider` + `fetchedAt` tiap snapshot (provenance) dan tampilkan sumber di UI.

## 10. User Stories per Persona

**Strategist (read-mostly):**

- US-S1: Sebagai strategist, saya ingin lihat KPI mingguan per **akun resmi** sehingga saya bisa laporkan ke klien.
- US-S2: Sebagai strategist, saya ingin pantau mention spike sehingga saya bisa reaktif.
- US-S3: Sebagai strategist, saya ingin export CSV metrik sehingga saya bisa olah di spreadsheet.
- US-S4: Sebagai strategist, saya ingin melihat analytics tiap platform resmi di halaman terpisah sehingga metrik yang berbeda tidak tercampur.

**Operator (action-heavy):**

- US-O1: Sebagai operator, saya ingin queue 50 comment ke 10 target sekaligus sehingga hemat klik.
- US-O2: Sebagai operator, saya ingin lihat container health real-time sehingga saya bisa kill yang stuck.
- US-O3: Sebagai operator, saya ingin quarantine akun langsung dari UI sehingga tidak banned massal.
- US-O4: Sebagai operator, saya ingin menyelesaikan 2FA/checkpoint langsung di dashboard (live screen noVNC + input kode) sehingga akun baru aktif tanpa menyentuh server.

**Analyst (query-heavy):**

- US-A1: Sebagai analyst, saya ingin filter post berdasarkan hashtag sehingga saya bisa analisis niche.
- US-A2: Sebagai analyst, saya ingin report builder custom sehingga saya bisa jawab pertanyaan klien.
- US-A3: Sebagai analyst, saya ingin share link report read-only sehingga tim bisa lihat tanpa login penuh.

## 11. Acceptance Criteria per Release

| Versi | Done bila                                                                                                                                                           |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| v0.1  | 1 akun IG terscrape end-to-end; Post + Comment masuk DB; tampil di dashboard                                                                                        |
| v0.2  | Tambah akun via dashboard → login interaktif s/d `authenticated` (termasuk alur `needs_input` 2FA); hapus akun → pod+PVC bersih; orphan sweeper recover 1 akun mati |
| v0.3  | Analytics akun resmi (IG + Threads) ter-ingest dari provider: snapshot tersimpan, tren tampil, alert mention spike fires; tambah Official Account via UI            |
| v0.4  | 100 like/comment tereksekusi via Playwright dengan success rate ≥ 85%; comment terverifikasi muncul di feed (bukan klaim buta)                                      |
| v0.5  | Report CSV/JSON export valid; alert worker down < 90 dtk                                                                                                            |
| v1.0  | 100+ akun stabil 30 hari; add/remove akun via UI tanpa redeploy; health score akurat; nol ban                                                                       |
