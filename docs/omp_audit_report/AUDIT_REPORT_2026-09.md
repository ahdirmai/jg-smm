# Audit Report — MVP-1-SMM · 2026-09-29

> **Revision audited** `main` @ `cdb62cc` (14 commit terakhir).
> **Auditor** senior SWE / FE / QA, read-only. Tidak ada file existing yang diubah.
> **Live stack** `smm-api-1`, `smm-web-1`, `smm-postgres-1`, `smm-redis-1`, `smm-minio-1`, 7× `smm-worker-*` (dibangun 2026-09-29 02:43 UTC).
> **Severity** mengikuti `docs/SEVERITY.md` (SEV1 outage/data-loss · SEV2 data plane at risk · SEV3 degraded ada workaround · SEV4 noise).
> **Rahasia** tidak pernah dicetak. Kunci/kode/token disebut berdasarkan nama & lokasi.

---

## 1. Verdict

**Tidak, sistem ini tidak melakukan apa yang kontraknya klaim, dan saya tidak akan ship apa adanya.**

Arsitekturnya solid dan sebagian besar konvensi `DEVELOPMENT_RULE` benar-benar dipegang — RBAC per-route sudah benar, kripto AES-GCM-nya benar, kontrak callback worker-nya (retry, 4xx-stop, never-throw, dua Redis client) diverifikasi **dengan mengeksekusi modulnya**, bukan dengan membacanya. Yang rusak adalah lapisan yang menghubungkan semuanya, dan tiga di antaranya_MODE_: (1) **bug ekstensi screenshot `.jpg` vs `.png`** yang membuat *seluruh* verdict action ditolak `400` begitu screenshot benar-benar berhasil diambil — dan karena tidak ada sweeper job, tiap job yang callback-nya hilang mengunci `RUNNING` selamanya tanpa jalan keluar; (2) **kunci `CREDENTIAL_KEY` dan `JWT_SECRET` yang di-commit sebagai default di `compose.yaml`** sejak commit `1c10eea`, sehingga kredensial akun dan token OWNER bisa dibuka oleh siapa pun yang bisa read repo; (3) **Redis tanpa autentikasi di `0.0.0.0:24637`, MinIO console dengan password default publik di `0.0.0.0:24901`, Postgres di `0.0.0.0:24543`** — antarmuka data terbuka ke jaringan.

Selain itu, **fitur andalan yang baru dikirim (`5a705e5`, keyword batch scrape) secara struktural tidak bisa menghasilkan komentar** — DB live: 3 batch Threads sukses → 56 item → 56 post → **0 comment**; 52 post Threads di DB punya **0 comment**. Reply-join machinery yang jadi highest-churn code di repo hanya jalan di jalur per-post.

Yang membuat ini bisa lolos: **green CI yang tidak hijau.** Go coverage 36,9% (target 70%), 36 test DB skip diam-diam di balik env var yang tidak pernah di-set, `internal/http` 0% dengan **7 dari 11 handler tanpa file test sama sekali**, frontend **tidak punya test runner**, dan `govulncheck` / e2e / `sqlc-check` — ketiganya diwajibkan dokumen atau Makefile — **tidak ada di `.github/workflows/`**. Test yang paling penting justru yang tidak bisa gagal: `TestRunPendingNowSweepsPastTheBatchCap` lulus melawan fake yang **tidak pernah menyentuh `ScheduledAt`**, sementara produksiopathology-nya justru column itu.

**Perbaikan minimum sebelum ship: F-01, F-02, F-03 (SEV1), lalu F-04…F-09 (SEV2).** Sisa bisa masuk backlog terurut.

---

## 2. Findings

### SEV1

#### F-01 — Screenshot worker `.jpg`, API hanya menerima `.png` → 100% verdict action ditolak
`apps/worker/src/core/screenshot.ts:31,37` · `apps/api/internal/http/internal.go:32,302-304` · `apps/worker/src/platforms/dom.ts:730,815` · `apps/worker/src/transport/callback.ts:53-56`

```ts
// screenshot.ts:31
const basename = `task-${id}-${workerId}.jpg`;      // format: 'jpeg' di :37
```
```go
// internal.go:32
const screenshotExt = ".png"                          // :302-304 menolak apa pun yang bukan .png
```

`dom.ts:730-736` menempelkan screenshot pada **jalur sukses**, `dom.ts:815-821` pada jalur gagal — jadi hampir setiap callback membawa `.jpg`. `internal.go:125-128` membalas `400`. `callback.ts:53-56` memperlakukan 4xx sebagai permanen dan berhenti tanpa retry.

**Failure scenario.** Operator menjalankan `make up`, membuat container dari dashboard, lalu menjalankan satu like. Chromium berhasil capture → `POST /internal/action-callback` dengan `screenshot: "task-…jpg"` → API `400 "screenshot_path must be a .png file name"` → worker log `callback rejected`, berhenti → `RecordAttempt` tidak pernah dipanggil → tidak ada baris `action_log`, `applyVerdict` tidak pernah jalan → baris `action_job` **lumed di `RUNNING` selamanya**. Like-nya **benar-benar terjadi** di Instagram, tapi platform mencatat nol. Operator melihat antrean yang tidak pernah bergerak, menekan retry, dan meng-like lagi. Every. Single. Action.

**Kenapa belum meledak sekarang.** `SCREENSHOT_DIR` tidak diset di container yang di-provision, dan `/data/screenscriptions` kosong — CDP capture **selalu gagal**, jadi `captureScreenshot` selalu return `undefined` (lihat F-02b). Bug ini tertidur di balik bug kedua. Begitu screenshot diperbaiki, F-01 meledak.

**Fix.** `screenshot.ts:31` → `.png`, `:37` → `format: 'png'`, dan tambahkan test kontrak yang meng-`assert` basename dari worker cocok dengan `screenshotExt` di API (test yang hilang ini yang membiarkan lolos — lihat §4).

---

#### F-02 — Tidak ada sweeper job; satu callback yang hilang mengunci job `RUNNING` selamanya
`docs/DEVELOPMENT_RULE.md:229` (wajib) · `apps/api/cmd/server/main.go:461` · `apps/api/db/queries/action.sql:24,44-59`

`DEVELOPMENT_RULE:229` mewajibkan: *"Sweeper hanya `RUNNING > 10 menit tanpa heartbeat → reset PENDING (attempt++)` max 3×."* Sweeper itu **tidak ada**. `main.go:461` hanya membuat `NewOrphanSweeper`, dan itu menyapu **container**, bukan job. Bukti struktural — `grep -rn "JobStatusRunning" --include=*.go internal/ cmd/ | grep -v _test.go` mengembalikan tepat dua hit: `domain/scrape.go:77` (konstanta) dan `service/scrape_ondemand.go:117` (on-demand scrape, domain berbeda).

Satu-satunya jalan keluar dari `RUNNING` adalah `CompleteActionJob` / `RescheduleActionJob`, dan keduanya hanya terjangkau dari `applyVerdict` ← `RecordAttempt` ← callback.

**Failure scenario.** Worker di-OOM-kill di detik ke-40 dari satu aksi. Job sudah di-`BLPOP` ( hilang dari Redis), `ClaimNextActionJob` sudah menandainya `RUNNING`, dan query itu hanya pernah memilih `status='PENDING'` — jadi tidak ada kode mana pun yang bisa melihatnya lagi. Baris itu `RUNNING` selamanya. Antrean menumpuk di belakangnya. Operator tidak punya tombol untuk itu: `Retry` FE hanya dirender untuk `FAILED` (`actions/page.tsx:358`).

**Fix.** `service/job_sweeper.go`: ticker 60s → `SELECT id FROM action_job WHERE status='RUNNING' AND started_at < now() - interval '10 minutes'` → `RescheduleActionJob(attempt+1)`, dan `CompleteActionJob(FAILED)` pada attempt ke-4. Wire di `main.go` di samping reconciler.

---

#### F-03 — `CREDENTIAL_KEY` dan `JWT_SECRET` di-commit sebagai default compose; semua kredensial & token OWNER dapat dibukaoffline
`compose.yaml:139,141` (masih di history sejak `1c10eea`) · `apps/api/internal/config/config.go:222,225,335-341` · `.gitleaks.toml` · `scripts/check-credentials.sh:20-24`

```yaml
# compose.yaml:139,141  (nilai disamarkan di sini)
JWT_SECRET:     ${JWT_SECRET:-<placeholder default yang di-commit, 43+ char>}
CREDENTIAL_KEY: ${CREDENTIAL_KEY:-<base64 32-byte AES-256 key yang di-commit>}
```

`config.go:335-341` hanya memvalidasi **non-empty** dan `len >= 16` — default yang di-commit itu lolos. `.env` working-tree memakai **nilai yang sama persis**.

**Failure scenario.** Siapa pun dengan akses read ke repo (termasuk CI log, fork, cache npm/go, atau fork fork) mengambil key itu, lalu:
1. `openssl`/Python 5 baris mendekripsi setiap `account.password_enc` dari dump Postgres → **cookie sesi Instagram/Threads milik setiap akun**. Full account takeover, tanpa perlu menyentuh sistem yang berjalan.
2. Men forging token HS256 dengan claim `role=OWNER` → **seluruh RBAC jadi dekoratif**. Semua gate yang saya audit di §5 tidak berarti apa-apa terhadap penyerang yang sudah punya JWT.

Yang membuatnya lolos: `.gitleaks.toml` meng-allowlist string ini sebagai "local-dev placeholder", dan `scripts/check-credentials.sh:20-24` hanya mem-scan `*.go`/`*.ts`/`*.tsx` di `apps`/`packages` — **tidak pernah membaca `compose.yaml`**. Job `credential-guard` di `ci.yml:174` mencetak "credential guard: clean" sementara key-nya ada di tree.

**Fix.** (a) Ganti default dengan `${CREDENTIAL_KEY:?required}` / `${JWT_SECRET:?required}` sehingga boot gagal keras. (b) **Rotasi kedua secret** dan re-encrypt seluruh `password_enc` (butuh F-11 key-version lebih dulu, atau skrip migrasi satu-off). (c) Tambahkan `compose.yaml` ke scope `check-credentials.sh` dan hapus allowlist tersebut dari `.gitleaks.toml`. (d) `config.go` menolak key yang persis sama dengan yang pernah di-commit.

---

### SEV2

#### F-04 — Redis tanpa password, MinIO & Postgres dengan password default, semuanya bind ke `0.0.0.0`
`compose.yaml:12,25,29,41,44-46,179`

```
POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-smm}          compose.yaml:12
MINIO_ROOT_PASSWORD: ${MINIO_ROOT_PASSWORD:-smmsmmsmm}  :41   ← di-allowlist gitleaks
command: ['redis-server', '--appendonly', 'yes']       :25   ← tidak ada requirepass
ports:  '${REDIS_PORT:-24637}:6379'                    :29   ← bind 0.0.0.0
        '${MINIO_CONSOLE_PORT:-24901}:9001'            :46   ← bind 0.0.0.0
        '${API_PORT:-24080}:8080'                      :179
        '${POSTGRES_PORT:-24543}:5432'                 :16
```

Redis juga **tidak punya `maxmemory` / `maxmemory-policy`**, padahal `DEVELOPMENT_RULE:249` mewajibkannya.

**Failure scenario.** Siapa pun yang bisa mencapai port host (LAN, VPN, atau proses lokal mana pun) menjalankan:
- `redis-cli -p 24637 LPUSH queue:action:<workerId> '<payload forging>'` → menyuntik job palsu ke antrean worker, atau `FLUSHDB` → menghapus seluruh antrean dan flag cooldown.
- Buka `http://<host>:24901` → login MinIO dengan `smm`/`smmsmmsmm` (publik di repo) → mengunduh bucket `raw-payload` (**seluruh payload Apify yang pernah dibayar**), `screenshots`, dan `sessions` (**cookie jar**).
- `psql postgres://smm:smm@<host>:24543/smm` → dump penuh.

**Fix.** Bind semua port ke `127.0.0.1:` di compose. `requirepass` untuk Redis. Ganti default password Postgres/MinIO dengan `:?required`. Tambahkan `maxmemory` + kebijakan yang **tidak** men-evict list (lihat catatan F-16b).

---

#### F-05 — `/internal/*` tanpa autentikasi + `RecordAttempt` tidak cek kepemilikan job → callback palsu menandai job mana pun `SUCCESS`
`apps/api/internal/http/internal.go:68` · `apps/api/internal/service/job.go:129-185,559-573` · `db/queries/action.sql:44-49`

```go
// job.go:149 — attemptId dari klien, di-parse, tidak diverifikasi
jobID, attempt, err := parseAttemptID(r.AttemptID)
...
// job.go:570 — tanpa cek status row, tanpa cek worker pemilik
s.actions.CompleteActionJob(ctx, jobID, domain.JobStatusSuccess, nil)
```
`db/queries/action.sql:44-49` adalah `UPDATE action_job SET ... WHERE id = $1 RETURNING *` — **tanpa** predikat `AND status = 'RUNNING'`.

**Failure scenario.** Satu request:
```bash
curl -X POST http://<host>:24080/internal/action-callback \
  -H 'content-type: application/json' \
  -d '{"attemptId":"<uuid-job-manapun>:1","status":"SUCCESS"}'
```
Job mana pun — `PENDING`, `RUNNING` milik akun lain, atau sudah terminal — **dibalik jadi `SUCCESS`** dengan `verified: true`, dan `applyHealth`utative kredit health score untuk pekerjaan yang tidak pernah terjadi. Tidak ada kredensial yang dibutuhkan; endpoint-nya di-bind `0.0.0.0` (F-04). Kepercayaan operator pada "queue hijau = aksinya jalan" hilang total — itu justru nilai jual produk ini.

`applyHealth` (`health_score_wire.go:34-40`) justru **melakukan** pola yang benar: men-derived akun dari baris job, bukan dari payload. Pola itu benar — tapi hanya di satu jalur.

**Fix.** (a) Header `X-Worker-Token` per worker, diterbitkan di `POST /internal/claim`, diverifikasi di middleware grup `/internal`. (b) Di `RecordAttempt`, muat job-nya lalu tolak kalau `job.WorkerID != r.WorkerID` atau `job.Status != RUNNING`. (c) `CompleteActionJob` diberi predikat `AND status = 'RUNNING'`. (d) `port` internal di-bind ke network Compose saja.

---

#### F-06 — `safeTargetURL` hanya mengecek skema; worker melakukan `page.goto` ke alamatnya
`apps/api/internal/service/action.go:465-479` · `apps/worker/src/platforms/dom.ts:93,114` · `DEVELOPMENT_RULE §7.4` ("SSRF guard tugas BE sebelum publish")

```go
// action.go:473-479 — tidak ada satupun inspeksi host
if u.Scheme != "http" && u.Scheme != "https" { ... }
if u.Host == "" { ... }
return u, nil
```
Probe dieksekusi terhadap fungsi ini: **ACCEPT** `127.0.0.1`, `[::1]`, `169.254.169.254`, `10.0.0.1`, `localhost`, `2130706433` (desimal), `0177.0.0.1` (oktal), `http://allowed.host@evil.host`, homoglyph, `127.0.0.1.nip.io`. **REJECT** hanya `file:`/`gopher:`/`javascript:`. `SECURITY_REVIEW.md:18` menyatakan "strict allowlist" — yang ada adalah **skema allowlist**.

**Failure scenario.** User dengan role `act` enqueue target `http://minio:9000/minio/sessions/` atau `http://169.254.169.254/latest/meta-data/iam/security-credentials/`. Worker membuka URL itu di Chromium (browser sungguhan, jadi JS + cookie ikut), melakukan `page.goto`, dan teks halaman masuk ke `renderedText` → `action_log` → dibaca balik lewat `GET /api/actions`. Worker juga berada di network Compose yang sama dengan **Postgres, Redis, MinIO, dan API**.

**Fix.** Allowlist host per platform di `safeTargetURL` **dan** validasi IP hasil resolusi (tolak loopback/link-local/private/CGNAT) **dan** revalidasi tiap hop redirect **dan** validasi ulang di worker sebelum `page.goto`. Terapkan juga ke `scrape_ondemand.go:85-96` yang saat ini **tidak** melewati `safeTargetURL` sama sekali.

---

#### F-07 — `RunPendingNow` mengurutkan `OFFSET` dengan column yang ditulis ulang
`apps/api/internal/service/action.go:373-403` · `apps/api/db/queries/action.sql:67-72` vs `:51-59`

```sql
-- action.sql:67-72  (yang dibaca)
SELECT * FROM action_job WHERE status = $1 ORDER BY scheduled_at LIMIT $2 OFFSET $3;
-- action.sql:51-59  (yang ditulis, SET scheduled_at = $2)
UPDATE action_job SET status='PENDING', scheduled_at = $2, finished_at = NULL WHERE id = $1;
```
Sweep meng-`OFFSET` dengan urutan `scheduled_at`, lalu **menulis ulang `scheduled_at` tepat ke column itu**. `RescheduleActionJob` juga tanpa prekondisi status.

Reproduksi langsung di Postgres (read-only, `BEGIN`…`ROLLBACK`): 5 baris sudah-due lalu 15 baris akan datang. `page1` (offset 0) me-reschedule id 1-5 ke `now()`. Karena `now() > scheduled_at` lama, 5 baris itu **melompat ke belakang** urutan: sesudah page1 urutannya bergeser, dan `OFFSET 50` pada page 2 **melewati 50 baris yang belum tersentuh**.

**Failure scenario.** Antrean 300 job `PENDING`. Operator menekan "Run pending now". 200 tersapu, 100 **tidak pernah tersentuh** dan tidak dilaporkan — API membalas `{"moved": 200}` seolah-olah queue habis. Tekan lagi: page 1 sekarang membaca 50 baris yang **sama** (yang barusan di-reschedule) → `n` menghitung ulang baris yang sama → angka bergerak tidak sesuai apa pun yang dikerjakan. Dua operator menekan bersamaan: tidak ada lock, tidak ada advisory lock, tidak ada idempotency key → keduanya menghitung baris yang sama.

Hari ini **dorman**: `action_job` live = 18 `SUCCESS` + 1 `FAILED`, 0 `PENDING`, jadi tidak ada yang bisa dipicu. Ini bom waktu yang dinyalakan operator.

**Fix.** Buang paginasi. `ListActionJobsByStatus` dengan `WHERE status = $1 AND scheduled_at <= $2 ORDER BY scheduled_at LIMIT $3` (batas, bukan offset) — keyset pagination, tidak bisa skip dan tidak bisa double-hitung. Bungkus dalam satu transaksi, dan kembalikan `n` dari jumlah baris yang benar-benar ter-update.

---

#### F-08 — `ACTION_DRY_RUN` default `true` di-inject ke setiap worker yang di-provision
`apps/api/internal/config/config.go:209` · `compose.yaml:132` · `apps/api/internal/adapter/dockerprovisioner/docker.go:167`

```go
// config.go:209
ActionDryRun: envBool("ACTION_DRY_RUN", true),
// docker.go:167 — API meneruskan nilainya ke setiap container yang dibuat
"ACTION_DRY_RUN=" + boolStr(c.dryRun),
```

**Failure scenario — sudah diverifikasi tidak aktif di stack ini.** Ketiga worker yang saya cek langsung lewat `docker exec … printenv ACTION_DRY_RUN` mengembalikan **`false`** (operator menyetelnya di `.env`). Jadi 18 job `SUCCESS` itu **nyata**.

Tapi untuk instalasi baru: `git clone` → `make up` → buat container dari dashboard. Reconciler meng-inject `ACTION_DRY_RUN=true`. Container health, claim, heartbeat, menarik job — dan melaporkan `status: 'success'` dengan `durationMs: 0` **tanpa pernah membuka browser**. Dashboard hijau, tidak ada yang terjadi di Instagram, dan tidak ada objek di UI yang mengatakannya. Ini kegagalan senyap yang sempurna.

**Fix.** Jangan sebar `ACTION_DRY_RUN` dari API sama sekali; biarkan hanya worker compose yang bisa menyetelnya, dan default `false` di sisi worker. Kalau dry-run memang dibutuhkan, jadikan eksplisit di UI dan tulis `dryRun: true` di baris job.

---

#### F-09 — Keyword batch scrape secara struktural tidak bisa menghasilkan komentar
`apps/api/internal/adapter/apify/runner.go:99-111` vs `:260-267` · `apps/api/internal/service/scrape_keyword.go:236-243` · `docs/platforms/threads.md:66-73`

Hanya `threadsPostInput` (`:260-267`) yang mengirim `includeReplies: true`. Jalur keyword/batch melalui `RunSearch` → `searchInput`, yang untuk Threads mengirim `mode`/`keywords`/`max_posts`/`search_filter`/`start_date`/`end_date` — **tanpa `includeReplies`**, dan ke **actor yang berbeda** (`futurizerush/meta-threads-scraper`, bukan `themineworks/threads-scraper`).

**Bukti live (read-only):**
```
platform | batches | items_read | posts_count | comments_count
INSTAGRAM|    5 SUCCEEDED |  8 |  8 |  0
THREADS  |    3 SUCCEEDED | 56 | 56 |  0

post:  INSTAGRAM 11 posts / 6 comments
post:  THREADS   52 posts / 0 comments
```

**Failure scenario.** Operator menjalankan keyword batch "-fashion jakarta", 8 batch SUCCEEDED, dashboard menampilkan "56 posts, 0 comments". Ia menyimpulkan hashtag-nya tidak ada diskusi. Padahal **seluruh reply tidak pernah diminta dari actor** — dan 200 baris reply-join machinery di `scrape_ingestor.go` (peta run-scoped, fallback orphan, `is_reply`/`parent_post_id`) **hanya pernah dieksekusi di jalur per-post**. Kolom `comments_count` secara struktural selalu 0 untuk batch. Ini bukan bug parsing; ini fitur yang tidak ada, dengancounter yang menyamar jadi data.

**Fix.** Keputusan produk lebih dulu, lalu kode: (a) pindah ke `themineworks/threads-scraper` untuk search **dan** kirim `includeReplies: true`; atau (b)iąż akui batasnya — hapus `comments_count` dari UI batch dan tulis "keyword search tidak mengambil komentar" di `docs/platforms/threads.md §4.2`. Yang **tidak** boleh terjadi adalah membiarkan angka 0 berdiri sebagai fakta.

---

### SEV3

| # | Temuan | Bukti | Skenario gagal |
|---|---|---|---|
| **F-10** | 5 route **GET** di-gate `act` — regresi dari fix RBAC P5-04 | `http/scrape.go:40,41,42,45,47` | STRATEGIST/ANALYST membuka `/scrape/batches` untuk review → **403**. Tidak ada yang dimutasi; bug yang sama persis dengan yang P5-04 perbaiki di group `/api`, sekarang muncul lagi di subgroups. Hapus `RequirePermission(PermAct)` dari kelima GET; biarkan `act` di `:38,39,46`. |
| **F-11** | `err.Error()` mentah bocor ke klien lewat field `error.code` | 12 handler: `user.go:115`, `proxy_group.go:53,67,108`, `action.go:209`, `audit.go:92`, `template.go:160`, `analytics.go:71,90,108,119,138,157` | `errors.go:63-65` memasukkan `he.Message` (string) ke `code`, sementara `message` tetap `StatusText`. Reproduksi end-to-end: `{"error":{"code":"repository.proxyGroup.List: failed to connect: host=db user=app_user password=… dbname=smm","message":"Internal Server Error"}}`. Galat koneksi DB membocorkan DSN ke browser ANALYST. Ganti semua dengan `return err` (errorHandler sudah memetakan ke 500 generik) + log server-side. |
| **F-12** | Job yang sudah di-`BLPOP` hilang diam-diam saat SIGTERM; tidak ada drain | `worker/src/index.ts:296-297`, `transport/queue.ts:54` | Job di-pop di T, browser di tengah `fillAndSubmit` di T+40s, SIGTERM datang → `process.exit(0)`. Job hilang dari Redis, row sudah `RUNNING`, tidak ada yang bisa melihatnya (lihat F-02). Set flag `stopping`, tunggu job in-flight dengan grace terbatas, lalu `LPUSH` balik. |
| **F-13** | Antrean FE dipotong diam-diam ke 50 baris; semua angka dihitung dari jendela itu | `apps/web/lib/api.ts:348-352`, `http/action.go:114-119` | Backlog 300 `PENDING` → halaman muat 50 → header Says "50 jobs, all settled", chip PENDING = 50. believing the queue draining. Chip `failed > 0` — **satu-satunya cabang yang memberi tahu operator ada yang gagal** — juga dihitung dari jendela terpotong itu, jadi kegagalan di baris >50 tak terlihat. Kirim `?limit=` eksplisit dan tampilkan "capped at N". |
| **F-14** | Counter "0 in flight" ditampilkan saat loading **dan** saat error | `actions/page.tsx:221-226` | API mati. Operator buka `/actions`, lihat `0 in flight` + tabel kosong, simpulkan fleet idle, pulang. Padahal ada worker dengan 40 job `PENDING`. `inFlight` diturunkan dari `actions` yang `[]` baik saat belum resolve maupun saat reject. Gate di `loading`/`error`. |
| **F-15** | Akun `QUARANTINED` bisa di-dispatch ulang lewat jalur login operator | `service/account.go:297-336`, `db/queries/account.sql:126-137` | Quarantine tidak melepas `worker_id`, jadi guard `Login` (`WorkerID == nil`) tetap lolos. `POST /api/accounts/:id/login` mengirim `auth-login` yang sama dan menulis `auth_status='AUTHENTICATING'` di akun yang di-quarantine. FE menawarkan "Log in" tanpa syarat (`accounts/page.tsx:760-764`). Refresh sendiri **benar** — `ListAccountsDueForRefresh` memfilter `status='ACTIVE'` di WHERE-nya (terverifikasi di Postgres) — jadi ini solely jalur racing. Tambahkan guard status. |
| **F-16** | Tidak ada resource limit di mana pun; rootfs tidak read-only; worker jalan sebagai root | `compose.yaml` (semua service), `docker.go:172-184`, `apps/worker/Dockerfile` | `DEVELOPMENT_RULE §8.2` mewajibkan Worker 750m/1Gi → 2 CPU/4Gi. `HostConfig` hanya set `RestartPolicy`/`Binds`/`PortBindings`. Feed scroll-infinite Meta yang bocor (persis failure yang didokumentasikan di `dom.ts:156-157`) membuat Chromium tumbuh tanpa batas; tidak ada yang membatasi, jadi **satu worker OOM-mell bunuh host 16 GB** dan membawa seluruh stack. |
| **F-17** | Ingest, counter, dan join batch ditulis dalam 3 transaksi terpisah; tidak ada tx, tidak ada resume | `scrape_keyword.go:212-232` | Crash antara loop `CreateKeywordBatchPost` dan `UpdateKeywordBatch` → baris ter-link dengan counter nol. `processBatch` adalah goroutine fire-and-forget tanpa retry/resume; batch jadi succeeded-looking dengan angka salah selamanya. Bungkus dalam satu `WithTx`. |
| **F-18** | Cooldown gate ada di sisi yang salah, key-nya salah, dan hit-nya reschedule senyap | `adapter/cooldown.go:59-61`, `service/action_scheduler.go:244,251-253` | Key-nya `smm:cooldown:comment:<accountUUID>:<sha256>`, bukan `cooldown:comment:<id>:<sha256(url)>` seperti kontrak §7.4. Ia juga meng-gate `like` dan `reply_comment` — sebuah like menghabiskan slot komentar. Dan saat kena, job **kembali ke `PENDING`**, bukan `FAILED`: dashboard menampilkan baris antrean, loop ulang tiap tick, operator melihat **nol**. Tidak ada cooldown di worker (`grep cooldown` di `apps/worker/src` = 0 hit). |
| **F-19** | LLM client: **nol** test non-200; `io.LimitReader` truncate diam-diam | `adapter/llm/client.go:116,121,128`; `client_test.go` | `client_test.go` tidak punya satu pun `WriteHeader`/`StatusCode` — kelima test memakai server implicit-200. Cabut cabang `StatusCode>=400` dan suite tetap hijau. `io.LimitReader` **memotong** alih-alih error, jadi respons >1 MiB muncul sebagai `llm: decode:` yang membingungkan, bukan "response too large". Timeout-nya sendiri **benar** (`http.Client.Timeout` = deadline penuh, body ter-batasi 1 MiB) — bagian ini bersih. |
| **F-20** | Rate limiter login per-IP saja, dan fail-open tanpa IP | `service/auth.go:40-43,92` | `if ip == "" { return true }` — proxy yang salah konfigurasi mematikan cap sepenuhnya. Penyerang dengan 50 IP proxy mendapat 500 attempt/menit terhadap satu akun. Tambahkan counter per-username yang independen dari IP. |
| **F-21** | `worker_id` tanpa batas panjang, `renderedText` tanpa truncate | `http/internal.go:141,194`; `maxErrorRunes` | §7.5 mewajibkan `worker_id ≤ 64`, `renderedText` 2000 char, error 500. Implementasi: `renderedText` **mentah** (sampai 64 KiB per attempt, dikembalikan lewat `GET /api/actions`), error dipotong di 4096 bukan 500, `worker_id` unbounded. |
| **F-22** | File sesi ditulis 0644 dan blob asing dipercaya apa adanya | `worker/src/core/session.ts:32-40` | `writeFile(path, data, 'utf8')` tanpa `mode` → 0644; `mkdir` tanpa mode → 0755. Proses mana pun di container (stack Xvfb, websockify) bisa membaca `/data/sessions/session-<platform>.json` — **cookie platform yang hidup**. Gunakan `mode: 0o600` / `0o700` dan validasi bentuk blob sebelum dipakai. |

---

### SEV4 (diringkas)

- **Tidak ada affordance retry** di 8 halaman FE (`actions/page.tsx:276`, `accounts/page.tsx:560`, `workers/page.tsx:269`, `templates/page.tsx:305`, `monitoring/page.tsx:63,147`, `settings/team-panel.tsx:113`, `actions/scrape-history.tsx:119`). Semua sudah punya banner error yang benar; tidak ada yang punya tombol. Di `accounts/page.tsx:294-298,305-307` dan `templates/page.tsx:155` error **ditelan `console.error` saja** — operator tidak pernah diberi tahu mutasi gagal.
- **`CANCELLED` jatuh ke stage 1** (`actions/page.tsx:57-68` `default` branch) → job yang dibatalkan terlihat identik dengan job yang baru diantrekan. `<div role="row" tabIndex={0}>` tanpa `role="cell"`/`aria-rowindex` → screen reader mengumumkan "table" tanpa header.
- **Selector hardcoded bocor keluar dari `SEL`**: `platforms/dom.ts:218` menduplikasi `sel/index.ts:142`.
- **Verifikasi ground truth polling `document.body.innerText`, bukan `[role=feed]`** (`dom.ts:739`) —kontrak §7.4 bilang `[role=feed] includes`. Lebih longgar dari yang dijanjikan.
- **`ACTION_DRY_RUN` aside, `LIFO` by default**: `publisher.go:29-35` `LPUSH` → worker `BLPOP` dapat yang **terbaru dulu**; `domain/action.go:11` menyebutnya "the same per-account FIFO shape". Batch 10 komentar berjalan J..A.
- **Context per-job, bukan per-account** (`worker/src/index.ts:100-110`): `job.accountId` tidak pernah dibaca; file sesi dinamai `session-<platform>.json` (`session.ts:64`). Dua akun platform sama di satu container → B hydrates storageState milik A.
- **Primitif shadcn diedit langsung** (`packages/ui/src/components/badge.tsx:6-10`, `button.tsx:1-2`) — melanggar §6.1; `shadcn add` berikutnya akan menimpa tinting itu tanpa jejak.
- **`api/health`, `x11vnc`**: noVNC bind `127.0.0.1` saja (docker.go:181-183) — **benar**. Alokasi port noVNC TOCTOU-racy (`novnc_allocator.go:62-77`); port dari row tidak di-clamp ke range (`docker.go:270-285`).
- **AES-GCM tanpa AAD dan tanpa key-version** (`crypto/aesgcm.go:88-106`) — ciphertext tidak terikat ke baris akunnya, dan rotasi key (F-03!) akan membuat semua `password_enc` tidak bisa didekripsi tanpa jalur migrasi.
- **`SMM_BACKUP_DIR` keluar dari ignore** (`infra/backup/lib.sh:15-16` vs `.gitignore:61-63`) — `docs/BACKUP.md:32` mendokumentasikan env ini; output di luar tree default **tidak** ter-ignore. Default-nya sendiri **tertutup** (`git check-ignore` memverifikasi tiap path bersarang).
- **Duplikat target Make** (`Makefile:36-37` dan `:41-42`) → `warning: overriding commands` di stderr setiap pemanggilan.
- **`timescale` drift**: `compose.yaml:8` = `2.17.2-pg16`, `DEVELOPMENT_RULE:248` = `2.14.2-pg16`; tidak ada `@sha256:` di seluruh compose.
- **i18n tidak ada sama sekali** — `next-intl` tidak ada di `apps/web/package.json`, semua string hardcoded English, dan satu string Bahasa Indonesia tersesat di `scrape/batches/[id]/page.tsx:210`.

---

## 3. Doc / code drift

| Klaim dokumen | Realita | Status |
|---|---|---|
| `SECURITY_REVIEW.md:18` — "`safeTargetURL` is a strict allowlist… No blocklist to drift" | Hanya cek skema + host non-kosong. Semua vektor alamat internal **lolos** (F-06). | **Drift, sign-off tidak berlaku** |
| `SECURITY_REVIEW.md:19` — "key from `CREDENTIAL_KEY` only, never baked into an image" | Secara harfiah benar (ada di compose, bukan image) — tapi key-nya **di-commit** dan `.env` lokal memakainya. Row tabel tidak menangkap "public key". | **Drift** |
| `SECURITY_REVIEW.md:22` — "noVNC PASS, acceptable for the local tier" | Masih benar. `docker.go:181-183` bind `127.0.0.1`; k8s Service ClusterIP tanpa Ingress (`k8s/client.go:266-283`). | **✓ masih berlaku** |
| `SECURITY_REVIEW.md:45` — `TestContainerRBAC` "proven" | Betul untuk container/proxy-group/admin. Tapi **tidak ada test yang pernah memasang** handler Actions atau Scrapes di router (`grep "Actions:\|Scrapes:"` di `http/*_test.go` = **nol**). 4 dari ~40 route punya pasangan allow+deny. | **Drift** |
| `DEVELOPMENT_RULE.md:229` — sweeper `RUNNING > 10 menit` wajib ada | Tidak ada di kode (§F-02). Satu-satunya sweeper adalah `OrphanSweeper` untuk container. | **Drift** |
| `DEVELOPMENT_RULE.md:117` — "Idempotency-Key **wajib** untuk POST mutasi" | Tidak diimplementasikan di mana pun. `RunPendingNow` (`http/action.go:64`) tidak punya; tidak ada middleware yang membacanya. | **Drift** |
| `DEVELOPMENT_RULE.md:118` — coverage target 70% BE | Terukur **36,9%**. `repository` 0,4%, `adapter/apify` 5,6%, `adapter/k8s` 8,2%, `http` 35,1% dengan **setiap handler func 0%**. | **Drift** |
| `DEVELOPMENT_RULE.md:291` — FE coverage 60% + axe-core di CI | `apps/web` **tidak punya test runner** (tanpa `vitest`, tanpa script `test`). Tidak ada axe-core. | **Drift** |
| `DEVELOPMENT_RULE.md:5` — "`govulncheck ./...` di CI per PR" | `grep -c govulncheck .github/workflows/*.yml` = **0**. Sign-off keamanan menjatuhkan bukti di atas eksekusi manual sekali jalan. | **Drift** |
| `DEVELOPMENT_RULE.md:248` — `timescale/timescaledb:2.14.2-pg16` (pin digest) | `compose.yaml:8` = `2.17.2-pg16`, tanpa digest. Tidak ada `@sha256:` di compose. | **Drift** |
| `DEVELOPMENT_RULE.md:249` — Redis `maxmemory 512mb` + `allkeys-lru` | `compose.yaml:25` = `['redis-server','--appendonly','yes']`. Tidak ada memory cap → queue tumbuh tanpa batas. | **Drift** |
| `DEVELOPMENT_RULE.md:274` — `RECONCILE_INTERVAL_SECONDS` (lokal=5) | Default kode `0`; compose menyetel `:-5` (`compose.yaml:125`). Klaim separuh benar, default tidak. | **Drift** |
| `DEVELOPMENT_RULE.md:5` (SSOT) — "`openapi/openapi.yaml` = SSOT kontrak HTTP" | **24 operasi yang di-router tidak ada di spec**; 5 ada di spec tapi tidak di-router; 11 method hilang. Tipe yang di-generate ke FE **berbohong** soal surface HTTP. | **Drift, parah** |
| `ERD.md` | Mencakup **8 dari 26** tabel live. | **Drift** |
| `docs/AUDIT_REPORT.md` (2026-09-18) | F-01…F-09, F-11, F-12 **FIXED**; F-10 **sebagian** (nilai `PROVISIONER_MODE` sudah sesuai, default `RECONCILE_INTERVAL_SECONDS` masih salah). Detail di §6. | — |
| `docs/AUDIT_REPORT.md:218` (`INFRA_ANALYST.md:204`) — "job hilang → perlu re-enqueue" | Benar sebagai STATEMENT, tapi tidak ada mekanisme: tidak ada sweeper (F-02) dan tidak ada tombol FE untuk job `PENDING` (F-13). | **Drift** |

---

## 4. Test yang tidak bisa gagal

Ini bagian yang paling penting: **suite hijau, sistem rusak.** Setiap "test" di bawah lulus hari ini dan akan tetap lulus setelah mutasi yang saya sebutkan.

| Test | Mutasi yang harus membuatnya merah | Verdict |
|---|---|---|
| `worker/src/test/adapters.test.ts:320,345,391,480` — `assert.ok(result.screenshot)` | Ubah `screenshot.ts:31` ke ekstensi apa pun. Test hanya memeriksa `truthy`, tidak pernah mencocokkan dengan `screenshotExt` API. **Inilah yang membiarkan F-01 ship.** | **TIDAK BISA GAGAL** |
| `service/action_retry_test.go:113` `TestRunPendingNowSweepsPastTheBatchCap` | Ubah query ke `WHERE scheduled_at <= now()`. Fake-nya (`job_retry_test.go:93-96`) **tidak pernah menyentuh `f.pending[i].ScheduledAt`** — urutan fake-nya statis sementara urutan produksi berubah tepat di bawah `OFFSET` (F-07). | **TIDAK BISA GAGAL** untuk multipage nyata |
| `maxRunPendingRows` bound | Set konstanta ke `1`. | **TIDAK BISA GAGAL** — identifier cuma muncul di `action.go:25` dan `:383`; nol test |
| `service/scrape_keyword_test.go:332` `…ItemsReadCountsParsedItems` | Assert `ItemsRead == 3` (nilai yang benar, pre-window). Gagal dengan `got 2` — jadi test ** aktif**, tapi **mem-pinning angka yang salah**. Baris out-of-window tidak dihitung di mana pun. | **MENYESATKAN** |
| `http/internal_test.go:333` `TestActionCallbackScreenshotPathBase` | Hapus `path.Base` dari `sanitiseScreenshot`. Test justru **meng-assert** bahwa `../../evidence/shot.png` **diterima** (202), dan hijau **alasan salah**: `newTestInternal` (`internal_test.go:234`) mengirim `nil` untuk action store, jadi 202 datang dari jalur `ErrUnavailable` (`job.go:145-147`) — nilai tersanitasi dibuang sebelum bisa di-assert. Komentarnya "proving sanitisation passed" **tidak benar**. | **TIDAK BISA GAGAL** |
| `http/internal_test.go:381` `TestBodyLimit` | Hapus `http.MaxBytesReader` di `middleware.go:55`. Hijau tetap, karena cek `ContentLength > max` di `:50` menyala lebih dulu dan `httptest.NewRequest` di atas `*bytes.Buffer` **selalu** menyetel ContentLength eksak. | **TIDAK BISA GAGAL** untuk guard streaming. Butuh: body chunked `Content-Length: -1` yang melebihi batas |
| `http/internal_test.go:349` `TestActionCallbackRejectsInvalidStatus` | Hapus `parseAttemptStatus` di `internal.go:110`. Guard `!r.Status.Valid()` (`job.go:130-132`) juga memberi 400 — dua jalur 400 duplikat, test tidak bisa membedakannya. | **TIDAK BISA GAGAL** |
| `http/internal_test.go:399` `TestEnumParity` | n/a | **TAUTOLOGI** — membandingkan dua cast konstanta compile-time |
| Seluruh handler non-200 LLM | Hapus `StatusCode>=400` (`client.go:121`) atau cabang `out.Error` (`:128`). `client_test.go` nol `WriteHeader`/`StatusCode`; kelima test pakai server implicit-200. | **TIDAK ADA — 0 test** |
| Cap 1 MiB LLM | Hapus `io.LimitReader`. | **TIDAK ADA** — dan `LimitReader` **truncate diam-diam**, bukan error |
| RBAC untuk `/actions/:jobId/retry`, `/actions/run-pending`, `/scrape/*` | Hapus `RequirePermission(PermAct)` dari `action.go:40,43` atau `scrape.go:39-42`. | **TIDAK ADA** — nol test pernah memasang route itu di router |
| Callback forged lintas-worker | Buat `actionCallback` mengabaikan kepemilikan job. | **TIDAK ADA** — `grep other\|another\|stolen\|forged\|mismatch` di `internal_test.go` = nol |
| Gate drift sqlc | Edit nama kolom manual di `db/queries/*.sql`. | **TIDAK BISA GAGAL DI CI** — `sqlc-check` (`Makefile:117`) tidak ada di `ci.yml` maupun target `ci:`, meski komentarnya sendiri bilang "(CI)" |
| `internal/arch/arch_test.go:20` `TestLayerDependencies` | `import _ ".../internal/repository"` di `service/action.go` → memang menggigit (sudah diverifikasi). **Tapi** hanya 4 dari 11 package internal yang diawasi, file `_test.go` dikecualikan, direktori layer yang hilang dilewati diam-diam, `modulePrefix` di-`hand-copy` dari `go.mod:1` (rename modul → memeriksa **nol file** sambil tetap melapor PASS), dan **tidak ada self-test fixture** yang membuktikan aturannya menyala. | **SEBAGIAN** |

**Yang benar-benar menggigit** (saya sudah buktikan mutasinya): `TestThreadsPostInput` (`runner_test.go:116` — flip `includeReplies` ke `false` → merah), `TestSearchInputPerActor` (`:40`), `TestLayerDependencies`, `TestRunPendingNowSweepsPastTheBatchCap` (kasus satu page saja), `TestCompleteTruncatedStillFails`. Test-test ini bagus. Masalahnya bukan mereka salah — masalahnya **area yang paling berbahaya justru yang tidak punya test sama sekali.**

**Angka lain:** `go build` 0 · `go vet` 0 · `go test ./... -count=1` 0 (541 top-level, 887 dengan subtest, **36 SKIPPED** di balik `SMM_TEST_DB=1` yang tidak pernah di-set CI) · worker 61/0 · **FE: tidak ada runner** · e2e: 10 spec ada, **tidak dieksekusi**.

---

## 5. Peta route → gate (verifikasi menyeluruh)

Semua route `POST`/`PUT`/`PATCH`/`DELETE` di bawah `/api` **memang** membawa gate `act`/`export`/`admin` yang benar — termasuk route yang baru ditambahkan. `SECURITY_REVIEW`'s fix RBAC **tahan** di level group. Dua pengecualian, keduanya di `scrape.go`.

| Method | Path | Group | Gate | Handler |
|---|---|---|---|---|
| GET | `/healthz`, `/readyz`, `/metrics`, `/` | root | none (disengaja) | `health.go:25,26`, `metrics.go:26`, `router.go:84` |
| POST | `/api/auth/login`, `/refresh`, `/logout` | root | public | `auth.go:38,39,40` |
| GET | `/api/auth/me` | root | requireAuth | `auth.go:41` |
| GET | `/api/admin/ping` | `/api/admin` | **admin** | `router.go:93` |
| POST/DELETE | `/api/containers`, `/api/containers/:id` | `/api` | **act** | `container.go:31,34` |
| POST | `/api/containers/:containerId/browser` | `/api` | **act** | `container.go:35` |
| GET | `/api/containers`, `/api/containers/:containerId`, `/api/locations` | `/api` | read | `container.go:32,33,37` |
| POST/DELETE | `/api/accounts`, `/import`, `/:id`, `/:id/login`, `/:id/input` | `/api` | **act** | `account.go:32,36,37,38,41,42` |
| POST | `/api/accounts/:accountId/session/export`, `/import` | `/api` | **admin** | `account.go:46,47` |
| GET | `/api/accounts`, `/by-region` | `/api` | read | `account.go:33,35` |
| POST | `/api/actions` | `/api` | **act** ✓ | `action.go:33` |
| POST | `/api/actions/:jobId/retry` | `/api` | **act** ✓ | `action.go:40` |
| POST | `/api/actions/run-pending` | `/api` | **act** ✓ | `action.go:43` |
| GET | `/api/actions`, `/batches`, `/batches/:batchId` | `/api` | read | `action.go:34,36,37` |
| POST | `/api/official-accounts`, `/api/analytics/refresh` | `/api` | **act** | `analytics.go:33,38` |
| GET | `/api/official-accounts`, `/analytics/overview`, `/analytics/:platform` | `/api` | read | `analytics.go:34,39,40` |
| GET | `/api/audit`, `/api/stream` | `/api` | read | `audit.go:29`, `stream.go:33` |
| POST/DELETE | `/api/proxy-groups`, `/api/proxy-groups/:id` | `/api` | **act** | `proxy_group.go:30,32` |
| POST | `/api/scrape/target`, `/api/scrape/keywords`, `/api/comments/generate` | `/api` | **act** | `scrape.go:38,39,46` |
| GET | `/api/scrape/batches`, `/:id`, `/:id/posts`, `/posts/:postId/comments`, `/recent` | `/api` | **`act` ← F-10** | `scrape.go:40,41,42,45,47` |
| GET | `/api/reports/actions`, `/targets`, `/analytics` | `/api` | read | `report.go:27,28,29` |
| GET | `/api/reports/export` | `/api` | **export** ✓ | `report.go:32` |
| POST/PUT/DELETE | `/api/templates`, `/api/templates/:id` | `/api` | **act** | `template.go:30,32,33` |
| GET | `/api/templates`, `/api/users` | `/api` | read | `template.go:31`, `user.go:29` |
| POST/PUT/DELETE | `/api/users`, `/api/users/:userId` | `/api` | **admin** | `user.go:30,31,32` |
| POST | `/internal/{action-callback,account-callback,heartbeat,claim,session-export}` | `/internal` | **none ← F-05** | `internal.go:68,69,70,75,80` |
| GET | `/internal/worker/:workerId/geolocation` | `/internal` | none | `internal.go:76` |

**Yang benar dan saya buktikan:** tidak ada sub-group yang mendaftarkan ulang path di luar gate-nya (`grep "g.Group("` = nol di luar `router.go`); `/internal/*` dan `/api/*` tidak berbagi prefix, jadi gate `/api` tidak bisa dilewati trik path; `/api/admin` adalah **saudara** `/api`, bukan anaknya; `GET /api/reports/export` memang `export`.

---

## 6. Status audit lama (`docs/AUDIT_REPORT.md`, 2026-09-18)

| # | Status | Bukti |
|---|---|---|
| F-01 static provisioner degrade | **FIXED** | `internal/adapter/dockerprovisioner/` ada; `main.go:519-533` mengembalikan `dockerprovisioner.New` untuk `case docker` |
| F-02 reconciler tidak pernah jalan | **FIXED** | `compose.yaml:125` set `RECONCILE_INTERVAL_SECONDS :-5` |
| F-03 noVNC URL tidak pernah ada | **FIXED** | `compose.yaml:222-233` mendokumentasikan `NOVNC_URL`/`NOVNC_BASE_URL` sebagai parameter create-time; port benar-benar dialokasikan |
| F-04 sembunyikan tombol = failure mode buruk | **FIXED di workers page** (`workers/page.tsx:403-413`: tombol `disabled` + `title` yang menjelaskan). **TIDAK** di accounts page — `accounts/page.tsx:339-351` masih diam-diam melakukan nothing, persis di alur OTP/2FA (§F-13/SEV4) |
| F-05 tidak ada alokasi port | **FIXED** | `novnc_allocator.go` + `NOVNC_PORT_MIN/MAX` + validasi form. Sisa: TOCTOU |
| F-06 nonroot tidak bisa buka docker.sock | **FIXED blunt** | `compose.yaml:183` `user: root` + socket di-mount `:185`. Bypass, bukan solusi — dandefault `PROVISIONER_MODE` sekarang `docker` (F-04) |
| F-07 provision_log tidak pernah ditampilkan | **FIXED** | `container.go:33,99-136` + `workers/page.tsx:180,496-511`. Catatan: method FE `api.ts:335` menunjuk ke path **detail container**, bukan resource log tersendiri |
| F-08 `make up` scale worker=3 | **FIXED** | `Makefile:31` sekarang hanya api/web/db; scaling pindah ke `up-fleet` opt-in; service di balik `profiles: ['fleet']` (`compose.yaml:198`) |
| F-09 orphan sweeper inert | **FIXED** | di-wire `main.go:458-465` |
| F-10 dokumentasi drift | **SEBAGIAN** | `PROVISIONER_MODE` sudah `{static\|docker\|k8s}`; `RECONCILE_INTERVAL_SECONDS` masih kontradiktif |
| F-11 `NEXT_PUBLIC_API_URL` | **FIXED** | hanya build-arg yang tersisa (`compose.yaml:247`) |
| F-12 `.env.example` tidak bisa ekspresikan live view | **FIXED** | `infra/docker/.env.example:46-63` |

---

## 7. Yang tidak saya periksa

Jujur, supaya Anda tahu batas audit ini:

- **e2e Playwright tidak dieksekusi** (butuh container web + fixture). Relevan: image `smm-web-1` dibangun 02:43, commit `1f4d4e9` (redesign FE) 03:00 — spec kemungkinan besar tidak cocok dengan yang 24081 menyajikan. Saya belum bisa memastikan apakah build web sudah memuat `1f4d4e9`; ini yang paling perlu Anda cek sendiri lebih dulu.
- **Test integrasi DB tidak dijalankan** (`SMM_TEST_DB=1` disengaja) — akan menulis ke stack yang menyerupai produksi.
- **`govulncheck` tidak dijalankan** — hanya klaim versi `go.mod` yang diverifikasi, bukan reachability CVE.
- **Tidak ada pass DAST/browser.** `dangerouslySetInnerHTML` tidak ada (grep), tapi sink transitif di komponen pihak ketiga belum diverifikasi.
- **`infra/k8s/` kosong di checkout ini** (`git ls-files` hanya `.gitkeep`) — tidak ada manifest untuk diaudit. Yang diaudit adalah kode `adapter/k8s/client.go`; `securityContext`-nya partial (tidak ada `readOnlyRootFilesystem`, `seccompProfile`, `automountServiceAccountToken: false`), tidak ada init container migrate, tidak ada resource limit, `imagePullPolicy` absen.
- **Pemrosesan kredensial proxy** (`service/proxy_group.go`) tidak dibaca baris-per-baris — kemungkinan suffer F-11 key-version yang sama.
- **Grant level DB / `SECURITY DEFINER` di migration** tidak direview.
- **Rate limit di luar login** tidak ditelusuri penuh: `POST /api/scrape/target` (Apify berbayar) dan `POST /api/comments/generate` (LLM berbayar) tampak tidak punya cap per-user.
- **Pass-through proxy untuk media URL** (`scrape_ingestor.go:786-793` → `post.media_urls` → `<img src>`) — bukan SSRF server-side (tidak ada fetch), tapi URL actor pihak ketiga masuk ke DOM tanpa validasi. Ditemukan, diberi rating SEV3.
- **Tidak ada `UPDATE`/`DELETE`, tidak ada provisioning, tidak ada enqueue, tidak ada Apify run** yang dieksekusi. Semua query adalah `SELECT`/`EXPLAIN`/`\d`; semua reproduksi transaksi di-`ROLLBACK`.

---

## 8. Ringkasan angka

| Metrik | Target | Aktual |
|---|---|---|
| Go coverage | 70% | **36,9%** |
| FE coverage | 60% | **0%** (tidak ada runner) |
| Route `/api` dengan pasangan test allow+deny | semua | **4 dari ~40** |
| Handler HTTP dengan file test | semua | **7 dari 11** |
| Test DB yang dijalankan | semua | **36 di-skip diam-diam** |
| Gate wajib di CI: `govulncheck` / e2e / `sqlc-check` | 3 | **0** |
| `action_job` live | — | 18 SUCCESS, 1 FAILED, 0 PENDING |
| `keyword_batch` live | — | 8 SUCCEEDED, 111 items, 111 posts, **0 comments** |
| `apify_run.run_id` terisi | 100% | **26/26** (fix `7f34425` bekerja) |
| `action_log` dengan screenshot | — | **0 / 21** (F-01 + F-02b) |
| Worker `ACTION_DRY_RUN` | `false` | `false` ✓ (tapi defaultnya `true`, F-08) |
| `account` ter-quarantine | — | 0 |
