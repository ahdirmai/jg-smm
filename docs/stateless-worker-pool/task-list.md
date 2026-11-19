# Master Task List — Implementasi & Migrasi 10 Stateless Worker Utama (`boxd`)

> **Peran Dokumen Ini:** Single Source of Truth (SSOT) eksekusi untuk Coding Agent.  
> **Cara Pakai oleh Agent:** Kerjakan berurutan mengikuti **2-Stage Gate** di bawah. Setiap task memiliki **Target File**, **Spesifikasi Presisi**, dan **Gate Verifikasi**. Jangan lanjut ke task/fase berikutnya sebelum Gate Verifikasi lulus (`exit code 0`), dan perbarui kotak `[ ]` menjadi `[x]` setiap satu task tuntas.
>
> ### Keputusan Eksekusi Terkunci (Locked Decisions)
> 1. **Eksekusi 2 Tahap dengan Approval Gate:**
>    - **Tahap A — Aplikasi & CI Lokal (`Fase 0 Lokal: T0.1, T0.3, T0.4` + `Fase 1` s/d `Fase 4`):** Selesaikan seluruh perubahan DB/SQL, Go API, TypeScript Worker, OpenAPI, dan Next.js Dashboard hingga `make ci` lulus 100% di lokal -> **WAJIB BERHENTI & minta konfirmasi user** sebelum menyentuh provisioning cloud `boxd`.
>    - **Tahap B — Cloud `boxd` & Cutover (`T0.2` + `Fase 5` s/d `Fase 6`):** Dijalankan setelah Tahap A disetujui.
> 2. **Domain Produksi Langsung:** Control Plane `smm-hub` langsung menggunakan custom domain **`https://smm.ahdirmai.my.id`** (Cloudflare DNS-only / grey cloud `A smm -> 152.236.3.23` dipasang tepat saat setup `smm-hub` di **`T5.2`**, **sebelum** ke-10 worker di-spawn pada `T5.5` dengan `API_URL=https://smm.ahdirmai.my.id`).

---

## 0. Hard Guardrails & Invariants (WAJIB DIPATUHI DI SETIAP TASK)

1. **Zero Secret Leak:**
   - **DILARANG KERAS** membaca untuk ditampilkan (`cat`/`echo`/`print`) isi `~/.config/boxd/env`, `~/.config/cloudflare/*`, `.env`, `.boxenv`, `CREDENTIAL_KEY`, `INTERNAL_API_SECRET`, `password_enc`, `storageState`, atau `pool_key` ke stdout, log, pesan error, commit, maupun chat.
   - Load auth `boxd` hanya di dalam subshell perintah: `(set -a; . ~/.config/boxd/env; set +a; boxd ... --json)`.
   - Upload rahasia ke VM hanya via `boxd env push <vm> --file <local> --dest <remote> --mode 600 --json`.
2. **Isolasi Mesin Produksi Eksisting di `boxd`:**
   - **JANGAN PERNAH** memodifikasi, me-restart, me-resize, atau menghapus mesin `ahdirmai` dan `speech`. Seluruh mesin baru wajib menggunakan prefix `smm-` dan label `--label app=smm`.
3. **Kepatuhan Layer Arsitektur Go (`apps/api/internal/arch/arch_test.go`):**
   - `domain` tidak boleh mengimpor package internal apa pun.
   - `port` hanya boleh mengimpor `domain`.
   - `service` hanya boleh mengimpor `domain`, `port`, dan `obs` (**DILARANG** mengimpor `repository`, `adapter`, `http`, `scheduler`, atau `config`).
4. **Single Source of Truth Codegen (Jangan Edit Manual File Generated):**
   - SQL -> edit `apps/api/db/migrations/*` & `apps/api/db/queries/*`, lalu jalankan **`make sqlc`** (menghasilkan `apps/api/internal/repository/sqlcgen/*`).
   - OpenAPI -> edit `openapi/openapi.yaml`, lalu jalankan **`make generate && pnpm --filter @smm/shared build`** (menghasilkan `apps/api/internal/http/oapigen/api.gen.go` dan `packages/shared/src/generated/api.ts`).
5. **Aturan Anti-Jebakan `boxd` & Playwright `1.48.2`:**
   - Semua proses server publik di dalam VM (`nginx:8000`, Next.js `HOSTNAME=0.0.0.0`, Worker RPC `0.0.0.0:8090` / `127.0.0.1:8090` di belakang `nginx:8000`) wajib mengikuti arsitektur reverse-proxy `nginx:8000` (hindari `502` edge `boxd`).
   - Setiap VM server (`smm-hub` & `smm-worker-01..10`) wajib `bot-protection off`.
   - Ke-10 worker VM wajib `idle.ignore-outbound on`, `auto-suspend.timeout 300`, `auto-hibernate.timeout 3600`, dan **tanpa** loop heartbeat/polling keluar yang menahan mesin tetap bangun saat idle.
   - Proxy per-context di Playwright Chromium (`1.48.2`) yang memakai `username:password` **wajib** berskema `http://` atau `https://` (`socks5://` ber-auth ditolak Chromium).
   - Setiap `BrowserContext` wajib menyetel `timezoneId` sesuai kota akun (`Asia/Jakarta` untuk WIB; `Asia/Makassar` untuk `Makassar`, `Denpasar`, `Balikpapan`) agar tidak bocor timezone Frankfurt (`fra`).
6. **Validasi Link Dokumentasi CI:**
   - Setiap perubahan file `.md` di `docs/` tidak boleh menambah broken link pada `python3 scripts/check_docs_links.py`.

---

## Fase 0 — Pre-Flight Audit, Session Rescue, & Backup Lokal
*Referensi: [`02-DATABASE-AND-DATA-MIGRATION.md`](./02-DATABASE-AND-DATA-MIGRATION.md) §2 & [`05-EXECUTION-ROADMAP-TESTING-ROLLBACK.md`](./05-EXECUTION-ROADMAP-TESTING-ROLLBACK.md) §1*

- [ ] **T0.1 — Pasang Git Secret Hooks, `.gitignore` Guard, & Verifikasi Baseline Repo**
  - **Aksi:** Pastikan `.gitignore` memblokir `.local-runtime/`, `.boxenv`, `.boxenv.*`, dan `*.boxenv`; jalankan `make hooks` untuk memastikan `.githooks` (`gitleaks`) aktif, lalu jalankan test arsitektur awal.
  - **Verifikasi:** `cd apps/api && go test ./internal/arch/...` -> `PASS`.

- [ ] **T0.2 — Verifikasi Konektivitas `boxd` CLI (Tanpa Print Token)**
  - **Aksi:** Jalankan `(set -a; . ~/.config/boxd/env; set +a; boxd auth && boxd machine list --json | jq 'map({name, status, size})')`.
  - **Kriteria Lulus:** Autentikasi aktif pada org yang benar; terlihat mesin eksisting (`ahdirmai`, `speech`, `belajar`); 0 karakter token tampil di output.

- [ ] **T0.3 — Audit & Rescue Session Lokal Container ke `account_session`**
  - **Aksi:** Jika container PostgreSQL lokal sedang berjalan, periksa apakah ada akun `AUTHENTICATED` yang belum memiliki baris di `account_session`:
    ```sql
    SELECT a.id, a.username, a.worker_id
    FROM account a
    LEFT JOIN account_session s ON s.account_id = a.id
    WHERE a.auth_status = 'AUTHENTICATED' AND s.account_id IS NULL;
    ```
    Bila ada hasil, jalankan mekanisme `auth-export` ke container terkait dan simpan ke `account_session` hingga query menghasilkan `0 rows`.

- [ ] **T0.4 — Backup Penuh PostgreSQL Lokal + Checksum SHA-256**
  - **Aksi:** Buat direktori `.local-runtime/backups` (pastikan `.local-runtime` sudah ada di `.gitignore`), dump seluruh isi DB lokal:
    ```bash
    mkdir -p .local-runtime/backups
    docker exec smm-postgres-1 pg_dump -U smm -Fc --no-owner --no-acl smm \
      > .local-runtime/backups/smm-pre-pool.dump
    shasum -a 256 .local-runtime/backups/smm-pre-pool.dump \
      > .local-runtime/backups/smm-pre-pool.dump.sha256
    ```
  - **Gate Fase 0:** File `.local-runtime/backups/smm-pre-pool.dump` berukuran > 0 byte dan checksum tercatat.

---

## Fase 1 — Migrasi Database (`000024`), Domain Model, & Layer `sqlc`
*Referensi: [`02-DATABASE-AND-DATA-MIGRATION.md`](./02-DATABASE-AND-DATA-MIGRATION.md) §2–§5*

- [x] **T1.1 — Buat File Migrasi `000024_stateless_worker_pool.{up,down}.sql`**
  - **File Baru:**
    - `apps/api/db/migrations/000024_stateless_worker_pool.up.sql`
    - `apps/api/db/migrations/000024_stateless_worker_pool.down.sql`
  - **Spesifikasi `up.sql`:**
    1. `ALTER TABLE account` tambah `location text`, `latitude double precision`, `longitude double precision`, `timezone text NOT NULL DEFAULT 'Asia/Jakarta'`, `user_agent text`.
    2. Tambah constraint `account_location_coords_pairing` (`location`, `latitude`, `longitude` seluruhnya `NULL` atau seluruhnya `NOT NULL`).
    3. Backfill `location`, `latitude`, `longitude`, dan `timezone` (`Asia/Makassar` untuk `'Makassar'`, `'Denpasar'`, `'Balikpapan'`; selainnya `'Asia/Jakarta'`) dari `worker` via `account.worker_id = worker.id`.
    4. Backfill `user_agent` default Chrome 131 macOS untuk baris `WHERE user_agent IS NULL`.
    5. `ALTER TABLE worker ADD COLUMN endpoint_url text;`
    6. `ALTER TABLE account DROP CONSTRAINT IF EXISTS account_worker_platform_key;`
    7. `UPDATE account SET worker_id = NULL WHERE auth_status NOT IN ('AUTHENTICATING', 'NEEDS_INPUT');`
    8. `CREATE INDEX account_location_idx ON account (location);`
    9. `DROP INDEX IF EXISTS account_refresh_due_idx;` lalu buat ulang tanpa syarat `AND worker_id IS NOT NULL` (tambahkan `AND worker_id IS NULL` agar sinkron penuh dengan query `ListAccountsDueForRefresh`).
  - **Spesifikasi `down.sql`:** Kebalikan bersih sesuai spesifikasi di [`02-DATABASE-AND-DATA-MIGRATION.md`](./02-DATABASE-AND-DATA-MIGRATION.md) §2.2.

- [x] **T1.2 — Perbarui Query SQL Akun (`apps/api/db/queries/account.sql`)**
  - **File Modifikasi:** `apps/api/db/queries/account.sql`
  - **Spesifikasi:**
    1. Sertakan `location, latitude, longitude, timezone, user_agent` pada `GetAccountByID`, `ListAccounts`, `CreateAccount`, `UpdateAccount`, `AssignAccount`, `UnassignAccount`, `ListAccountsByWorker`, dan `ListStaleLoginAttempts`.
    2. Pada `ListAccountsDueForRefresh`: sertakan ke-5 kolom baru di `SELECT`, dan ubah `AND worker_id IS NOT NULL` menjadi `AND worker_id IS NULL` (wajib sinkron dengan predikat `account_refresh_due_idx` pada `T1.1`!).
    3. Pada `ReclaimStaleLoginAttempt`: tambahkan `worker_id = NULL` pada klausa `SET`.
    4. Tambahkan query:
       - `AcquireAccountWorkerLease :one` (`UPDATE account SET worker_id = $2, last_checked_at = now() WHERE id = $1 AND (worker_id IS NULL OR worker_id = $2) AND NOT EXISTS (SELECT 1 FROM account other WHERE other.id <> $1 AND other.worker_id = $2) RETURNING ...` — menjamin 1 layar Xvfb worker tidak pernah dipinjam 2 akun berbeda secara bersamaan bahkan di level DB)
       - `ReleaseAccountWorkerLease :exec` (`UPDATE account SET worker_id = NULL WHERE id = $1 AND worker_id = $2`)
       - `ListUnlocatedAccounts :many` (`SELECT ... FROM account WHERE location IS NULL ORDER BY created_at ASC LIMIT $1`)

- [x] **T1.3 — Perbarui Query SQL Session & Worker (`account_session.sql` & `worker.sql`)**
  - **File Modifikasi:**
    - `apps/api/db/queries/account_session.sql`
    - `apps/api/db/queries/worker.sql`
  - **Spesifikasi:**
    1. Di `account_session.sql`: tambahkan `RefreshAccountSessionState :exec` (upsert `storage_state` + `last_used_at = now()`, gunakan `COALESCE(EXCLUDED.expires_at, account_session.expires_at)` agar tidak menghapus jadwal expiry yang sudah ada).
    2. Di `worker.sql`: sertakan `endpoint_url` pada `CreateWorker` dan `UpdateWorker`; tambahkan `UpsertPoolWorkerByName :one` (`INSERT INTO worker (...) VALUES (...) ON CONFLICT (name) DO UPDATE SET endpoint_url = EXCLUDED.endpoint_url, novnc_service = EXCLUDED.novnc_service, desired_state = 'RUNNING', source = 'MANUAL' RETURNING *`).

- [x] **T1.4 — Regenerasi Kode `sqlc`**
  - **Aksi:** Jalankan `make sqlc` dari root workspace.
  - **Kriteria Lulus:** `apps/api/internal/repository/sqlcgen/*` ter-update tanpa error kompilasi SQL.

- [x] **T1.5 — Perbarui Domain, Port Store, Repository Mappers, & Fake Stores**
  - **File Modifikasi:**
    - `apps/api/internal/domain/location.go` -> tambah `TimezoneForCity(name string) string` dan konstanta `DefaultDesktopUserAgent`.
    - `apps/api/internal/domain/worker.go` *(catatan: struct `Account` dan `Worker` keduanya berada di `domain/worker.go`, sedangkan `domain/account.go` berisi enum status)* -> tambah `Location *string`, `Latitude *float64`, `Longitude *float64`, `Timezone string`, `UserAgent *string` pada `Account`, serta `EndpointURL *string` pada `Worker`.
    - `apps/api/internal/port/account.go` -> tambah `AcquireLease`, `ReleaseLease`, `ListUnlocated` pada `AccountStore` dan `UpsertPoolWorker(ctx context.Context, w domain.Worker) (domain.Worker, error)` pada `WorkerStore`.
    - `apps/api/internal/port/session.go` -> tambah `RefreshState(ctx context.Context, accountID string, sealedState []byte, expiresAt *time.Time, workerID *string) error` pada `BrowserSessionStore`.
    - `apps/api/internal/repository/account.go`, `session.go`, `worker.go` -> petakan seluruh kolom baru antara `sqlcgen` dan `domain` (**PENTING:** di `repository/worker.go`, perbarui juga konstanta `workerCols` dan fungsi manual `scanWorkerRow` agar menyertakan `endpoint_url`, sehingga `Claim` dan `GetByContainerID` tetap selaras!).
    - Perbarui ke-6 file fake store di unit tests agar kompilasi paket `http`, `adapter`, dan `service` tetap hijau:
      - `apps/api/internal/http/container_test.go` (`containerWorkerStore`, `containerAccountStore`)
      - `apps/api/internal/http/internal_test.go` (`fakeWorkerStore`, `fakeAccountStore`)
      - `apps/api/internal/adapter/static_provisioner_test_helpers_test.go` (`memWorkerStore`)
      - `apps/api/internal/service/reconciler_test.go` (`fakeWorkerStore`)
      - `apps/api/internal/service/packer_test.go` (`fakeAccountStore`)
      - `apps/api/internal/service/account_session_test.go` (`fakeSessionStore`)

- [x] **T1.6 — Pengujian Integrasi Repository & Migrasi `000024`**
  - **File Modifikasi:** `apps/api/internal/repository/repo_test.go`, `apps/api/internal/repository/account_pending_test.go`
  - **Catatan DB Uji:** `repo_test.go` mewajibkan `SMM_TEST_DB=1` dan `DATABASE_URL` yang menunjuk ke database disposable yang namanya mengandung `"test"` (mis. buat DB sementara `smm_test_pool` di container Postgres lokal, jalankan migrasi `000001..000024` ke sana, jangan pernah mengarahkan `SMM_TEST_DB=1` ke DB utama `smm`).
  - **Skenario Uji:**
    1. Migrasi `000024` (`up -> down -> up`) berjalan bersih di atas `000001..000023`.
    2. Perbarui `TestAccountAssignProvesUniqueConstraint` & `TestPendingAccountIsDueForFirstLogin` ke invariant baru: dua akun Instagram berbeda dapat dibuat dengan `worker_id = NULL` (muncul di `ListDueForRefresh`), `AcquireLease` memberikan eksklusivitas 1 akun aktif per worker (`ErrConflict` bila worker sedang di-lease akun lain), dan setelah `ReleaseLease` worker yang sama dapat dipinjam akun berikutnya.
    3. `RefreshState` meng-upsert session tanpa menimpa `expires_at` non-null dengan `NULL`.
  - **Gate Fase 1:** `make sqlc-check && cd apps/api && go test ./internal/domain/... ./internal/repository/...` -> `PASS`.

---

## Fase 2 — Backend Go API: HTTPS Push RPC, Lock/Lease, Tiket noVNC, & Perbaikan Service
*Referensi: [`03-BACKEND-API-AND-ORCHESTRATION.md`](./03-BACKEND-API-AND-ORCHESTRATION.md) §2–§6*

- [x] **T2.1 — Konfigurasi Environment Pool (`internal/config/config.go`)**
  - **File Modifikasi:** `apps/api/internal/config/config.go`, `apps/api/internal/config/config_test.go`
  - **Spesifikasi:** Tambahkan pembacaan env:
    - `WORKER_POOL_MODE` (`"stateless"` | `"legacy"`, default `"stateless"`)
    - `WORKER_POOL_SIZE` (`int`, default `10`)
    - `WORKER_POOL_ENDPOINTS` (`string`, daftar CSV `name=rpcUrl|novncUrl`, opsional — bila kosong di lokal pakai `http://smm-worker-XX:8090`)
    - `NOVNC_TICKET_TTL_SECONDS` (`int`, default `300`)
    - `ACCOUNT_LOCK_TTL_SECONDS` (`int`, default `300`)
    - `WORKER_LEASE_TTL_SECONDS` (`int`, default `660`)

- [x] **T2.2 — Buat Kontrak Port `WorkerRPCClient` & `PoolCoordinator` (`internal/port/`)**
  - **File Baru:**
    - `apps/api/internal/port/worker_rpc.go` (`WorkerRPCClient`, `AccountRuntimeSpec`, `ExecuteActionRequest`, `GeoPoint`, `ProxySpec`, `LoginCreds`)
    - `apps/api/internal/port/pool.go` (`AccountExecutionLock`, `WorkerSlotCoordinator`, `SlotKind`: `SlotAction` | `SlotAuthLease`)
  - **Cek Layer:** Pastikan hanya mengimpor `context`, `encoding/json`, `time`, dan `internal/domain`.

- [x] **T2.3 — Implementasi Adapter Redis Lock & Affinity Slot Coordinator**
  - **File Baru:** `apps/api/internal/adapter/pool_lock.go`, `apps/api/internal/adapter/pool_lock_test.go`
  - **Spesifikasi:**
    1. `AcquireAccount(ctx, accountID, token, ttl)` memakai `SET smm:lock:account:<id> <token> NX EX <ttl>`. `ReleaseAccount` menghapus kunci secara aman (Lua compare-and-delete atau key delete).
    2. `AcquireWorkerSlot(ctx, accountID, candidates []domain.Worker, kind SlotKind, allowFallback bool, ttl)`:
       - Hitung indeks afinitas konsisten `fnv32a(accountID) % len(candidates)` sehingga akun yang sama selalu memprioritaskan worker yang sama.
       - Jika `candidates[idx]` bebas (`SET smm:slot:worker:<id> <kind>:<accountID> NX EX <ttl>`), kembalikan worker tersebut.
       - Jika sibuk dan `allowFallback == true`, putar ke kandidat berikutnya hingga dapat yang bebas; jika semua penuh kembalikan `domain.ErrUnavailable`.
       - Worker yang sedang memegang `SlotAuthLease` (login/CAPTCHA operator di noVNC) **tidak akan pernah** diambil oleh `SlotAction`!

- [x] **T2.4 — Implementasi Adapter HTTPS Push `workerrpc`**
  - **File Baru:** `apps/api/internal/adapter/workerrpc/client.go`, `apps/api/internal/adapter/workerrpc/client_test.go`
  - **Spesifikasi:**
    - Mengimplementasikan `port.WorkerRPCClient` via `net/http` (`DialContext` timeout `10s` untuk mengakomodasi cold-wake `±85 ms` `boxd`, total timeout `30s`).
    - Selalu mengirim header `X-Internal-Secret: <secret>` dan `Content-Type: application/json`.
    - Memetakan HTTP `409` dari worker menjadi `domain.ErrConflict` (worker busy), `401` menjadi `domain.ErrUnauthorized`, `5xx`/network menjadi retryable error.
    - **Dilarang me-log payload request.**

- [x] **T2.5 — Perluas `proxyresolver` untuk Per-Account Sticky Proxy**
  - **File Modifikasi:** `apps/api/internal/adapter/proxyresolver/resolver.go`, `apps/api/internal/adapter/proxyresolver/resolver_test.go`
  - **Spesifikasi:**
    1. Tambahkan metode `ForAccount(ctx context.Context, accountID string, proxyGroupID *string) (*port.ProxySpec, error)` (menggunakan `GetProxyGroupByID`).
    2. Substitusi otomatis placeholder `{account_id}` atau `{session}` di dalam `username` proxy menjadi 12 karakter alfanumerik dari `accountID` (tanpa `-`).
    3. Validasi keras: jika `scheme == "socks5"` **dan** memiliki `username != ""` atau `password != ""`, kembalikan error eksplisit (`"socks5 proxy with authentication is not supported by Chromium BrowserContext; use http:// or https://"`).

- [x] **T2.6 — Buat Generator & Validator Tiket HMAC noVNC**
  - **File Baru:** `apps/api/internal/service/novnc_ticket.go`, `apps/api/internal/service/novnc_ticket_test.go`
  - **Spesifikasi:**
    - `SignLiveViewTicket(secret, workerName string, expiresAt time.Time) (string, error)` -> menghasilkan `<expUnix>.<workerName>.<hex(HMAC_SHA256(secret, expUnix + ":" + workerName))>`.
    - `BuildProtectedNoVNCURL(baseNoVNCURL, ticket string) string` -> menghasilkan URL lengkap `vnc.html` dengan parameter `path=novnc/websockify?ticket=<ticket>`.

- [x] **T2.7 — Buat `PoolBootstrapper` Service (`internal/service/pool_bootstrapper.go`)**
  - **File Baru:** `apps/api/internal/service/pool_bootstrapper.go`, `apps/api/internal/service/pool_bootstrapper_test.go`
  - **Spesifikasi:**
    1. `BackfillAccounts(ctx)`: mencari semua baris `account` dengan `location IS NULL` (`ListUnlocated`), memilih kota dari `domain.Cities` secara deterministik (`fnv32a(account.ID) % len(domain.Cities)`), menghitung titik `RandomPoint` dengan seed deterministik dari UUID akun, mengisi `TimezoneForCity`, dan `DefaultDesktopUserAgent`.
    2. `EnsurePoolWorkers(ctx, endpoints []PoolWorkerDef)`: melakukan `UpsertPoolWorker` untuk ke-10 worker utama (`smm-worker-01..10`) dan menandai baris worker warisan `source = AUTO` yang lama sebagai `STOPPED` bila tidak terpakai.

- [x] **T2.8 — Refactor `ActionScheduler` untuk Mode Stateless Push**
  - **File Modifikasi:** `apps/api/internal/service/action_scheduler.go`, `apps/api/internal/service/action_scheduler_test.go`
  - **Spesifikasi pada `WORKER_POOL_MODE == "stateless"`:**
    1. Hapus penolakan `acc.WorkerID == nil` (`action_scheduler.go:251`).
    2. Tolak/tunda lebih awal bila `acc.AuthStatus != domain.AuthAuthenticated` atau session kosong di `account_session` (tanpa membakar kuota cooldown/rate-limit).
    3. Akuisisi `AccountExecutionLock.AcquireAccount(ctx, acc.ID, job.ID, lockTTL)` -> jika gagal (akun sedang jalan/login), `reschedule` 15 detik ke depan.
    4. Resolve proxy via `proxyResolver.ForAccount(ctx, acc.ID, acc.ProxyGroupID)`. Jika akun tidak memakai proxy pihak ketiga (`proxy == nil`), set `allowFallback = false` (kunci ke worker afinitas akun agar IP `Latitude.sh` tidak lompat-lompat!); jika memakai proxy residensial (`proxy != nil`), set `allowFallback = true` (bebas memakai worker kosong mana pun dari ke-10 worker!).
    5. Akuisisi `WorkerSlotCoordinator.AcquireWorkerSlot` -> jika 10 worker penuh, lepas `AccountExecutionLock` dan `reschedule` 5 detik ke depan.
    6. Unseal `account_session.storage_state` di RAM, panggil `sessions.Touch(ctx, acc.ID)`, lalu panggil `workerRPC.ExecuteAction` ke `worker.EndpointURL`! Jika gagal kirim, lepas slot + lock lalu `reschedule`.

- [x] **T2.9 — Refactor `JobService`: Session Write-Back, Inline Screenshot Upload, & Fix `stopOTPWorker`**
  - **File Modifikasi:** `apps/api/internal/service/job.go`, `apps/api/internal/service/job_otp_test.go`, `apps/api/internal/service/job_test.go`
  - **Spesifikasi:**
    1. Pada `AttemptRecord` & `RecordAttempt`:
       - Terima field opsional `UpdatedSession json.RawMessage` dan `ScreenshotBytes []byte`.
       - Bila `ScreenshotBytes` ada, unggah ke MinIO (`screenshots/<screenshotName>`) dari sisi Go API (sehingga worker `boxd` tidak butuh akses langsung ke MinIO!).
       - Bila `Status == AttemptSuccess` dan `len(UpdatedSession) > 0`, segel ulang dengan `sealer.Seal` lalu panggil `sessions.RefreshState`.
       - Bila status bukan `RUNNING` (sudah selesai/gagal/retry), selalu panggil `accountLock.ReleaseAccount` dan `workerSlots.ReleaseWorkerSlot`!
    2. Pada `RecordAuthOutcome` (`job.go:323-325`):
       - **FIX KRITIS:** Bungkus pemanggilan `s.stopOTPWorker` agar **HANYA** dieksekusi jika `s.poolMode == "legacy"`. Pada mode `"stateless"`, container/VM worker bersama **dilarang keras dimatikan**!
       - Saat `o.AuthStatus` adalah `AuthAuthenticated` atau `AuthFailed`: panggil `accounts.ReleaseLease(ctx, acc.ID, workerID)`, `workerSlots.ReleaseWorkerSlot`, dan `workerRPC.ClearAuth(ctx, endpointURL, acc.ID)`.
       - Saat `o.AuthStatus` adalah `AuthNeedsInput`: jalankan keepalive goroutine ber-context yang memanggil `workerRPC.PingKeepalive` tiap `120 detik` selama akun masih `NEEDS_INPUT` di worker tersebut (maks 10 menit) agar VM `boxd` tidak tertidur saat operator mengambil OTP/menyelesaikan CAPTCHA.

- [x] **T2.10 — Refactor `AccountService`, `SessionRefresher`, & `InternalHandler` (`2 MiB` Limit)**
  - **File Modifikasi:**
    - `apps/api/internal/service/account.go` (+ `account_test.go`, `account_region_test.go`, `account_session_test.go`)
    - `apps/api/internal/service/session_refresh.go` (+ `session_refresh_test.go`)
    - `apps/api/internal/http/internal.go` (+ `internal_test.go`)
  - **Spesifikasi:**
    1. `AccountService.Create` & `Import`: terima `location` opsional (validasi ke `domain.FindCity`, atau pilih kota otomatis bila kosong), isi `Latitude`, `Longitude` (`city.RandomPoint`), `Timezone` (`domain.TimezoneForCity`), dan `UserAgent`. Pada mode `stateless`, lewati pemanggilan `Packer.Pack`.
    2. `AccountService.StartLogin`, `OpenLiveView` (metode baru penghasil URL tiket noVNC), dan `SessionRefresher`: pinjam 1 worker bebas via `WorkerSlotCoordinator` (`SlotAuthLease`), simpan di `AcquireLease`, lalu panggil `workerRPC.StartLogin` / `workerRPC.OpenBrowser` lengkap dengan `AccountRuntimeSpec` (credentials + existing `storageState` + GPS + timezone + proxy).
    3. `AccountService.AccountsByRegion`: ganti pembacaan `Limit: 200` + join `worker.Location` menjadi pembacaan seluruh akun (`listAllAccounts`) yang dikelompokkan langsung berdasarkan `account.Location`!
    4. `InternalHandler` (`internal.go:23`): naikkan `MaxCallbackBytes` dari `64 << 10` menjadi **`2 << 20` (`2 MiB`)**, dan hubungkan field `updated_session` serta `screenshot_jpeg_b64` (dekode base64 dengan batas maks `1 MiB` untuk JPEG).

- [x] **T2.11 — Wiring Dependency Injection di `cmd/server/main.go`**
  - **File Modifikasi:** `apps/api/cmd/server/main.go`
  - **Spesifikasi:** Rakit `workerrpc.NewClient`, `adapter.NewPoolLock`, `PoolBootstrapper` (dijalankan saat startup ketika `cfg.WorkerPoolMode == "stateless"`), tiket noVNC, dan teruskan ke `AccountService`, `ActionScheduler`, `SessionRefresher`, dan `JobService`. Pastikan saat `cfg.WorkerPoolMode == "stateless"`, `ElasticPool` pause loop tidak mematikan worker pool.
  - **Gate Fase 2:** `make fmt-check && cd apps/api && go test ./internal/arch/... && go vet ./... && go test ./...` -> `PASS 100%`.

---

## Fase 3 — Refactor Stateless TypeScript Worker (`apps/worker/`)
*Referensi: [`04-WORKER-AND-FRONTEND-IMPLEMENTATION.md`](./04-WORKER-AND-FRONTEND-IMPLEMENTATION.md) §1*

- [x] **T3.1 — Perbarui Tipe & Konfigurasi Worker (`types.ts` & `core/config.ts`)**
  - **File Modifikasi:** `apps/worker/src/types.ts`, `apps/worker/src/core/config.ts`
  - **Spesifikasi:**
    - Tambahkan tipe `AccountRuntimeSpec` (`accountId`, `platform`, `storageState?`, `credentials?`, `geolocation?`, `timezone`, `userAgent?`, `proxy?`).
    - Perluas `ActionResult` dengan `updatedSession?: unknown` dan `screenshotJpegB64?: string`.
    - Tambahkan konfigurasi `poolMode` (`WORKER_POOL_MODE`, default `'stateless'`), `rpcHost` (`WORKER_RPC_HOST`, default `'0.0.0.0'`), `rpcPort` (`WORKER_RPC_PORT`, default `8090`).

- [x] **T3.2 — Injeksi Per-Context Proxy, Timezone, GPS, & StorageState (`core/browser.ts`)**
  - **File Modifikasi:** `apps/worker/src/core/browser.ts`
  - **Spesifikasi:**
    - Perbarui `newAccountContext` agar menerima `AccountContextInit` (`platform`, `storageState`, `geolocation`, `timezoneId`, `proxy`, `userAgent`) sambil tetap mempertahankan overload/kompatibilitas bila dipanggil dari test lama.
    - Teruskan `timezoneId: init.timezoneId || 'Asia/Jakarta'`, `locale: 'id-ID'`, `userAgent`, `storageState`, dan `proxy` langsung ke `browser.newContext(...)`, diikuti `await pinGeolocation(ctx, init.geolocation)`.

- [x] **T3.3 — Session Write-Back, Base64 Evidence Cleanup, & Zero Jitter Sleep (`core/controller.ts` & `transport/callback.ts`)**
  - **File Modifikasi:** `apps/worker/src/core/controller.ts`, `apps/worker/src/transport/callback.ts`
  - **Spesifikasi:**
    1. Di `controller.ts`: setelah `dispatch(adapter, ctx, job)`, jika `result.ok === true`, ambil `updatedSession = await ctx.storageState().catch(() => undefined)`.
    2. Jika `result.screenshot` menghasilkan file `.jpg` di disk lokal worker, baca menjadi `screenshotJpegB64` (base64) lalu hapus (`rm(..., { force: true })`) file lokal tersebut agar disk microVM `boxd` selalu bersih 0 file!
    3. Pada mode `stateless`, set `jitterRangeMs` ke `[0, 0]` (jeda antar-aksi sudah diatur scheduler `smm-hub`, sehingga microVM worker bebas langsung tidur setelah `ctx.close()`).
    4. Di `callback.ts`: kirim `updated_session` dan `screenshot_jpeg_b64` ke `/internal/action-callback` tanpa pernah mencetak isinya ke log.

- [x] **T3.4 — Refactor Login Stateless & Isolasi `manualSession` (`core/auth.ts`)**
  - **File Modifikasi:** `apps/worker/src/core/auth.ts`
  - **Spesifikasi:**
    - `runLogin` menerima `runtime: AccountRuntimeSpec` langsung di memori (tidak membaca/menulis `/data/sessions/session-<platform>.json` pada mode `stateless`).
    - Jika `runtime.storageState` tersedia, hidrasi ke `newAccountContext` terlebih dahulu: bila `probeSession` langsung `proven`, segera laporkan `verified` tanpa mengetik ulang form login!
    - Saat login baru berhasil (`proven`), panggil `await ctx.storageState()` -> kirim melalui `onSession` ke `/internal/session-export` (`requestId = 'login-success'`) -> tutup context.

- [x] **T3.5 — Buat HTTP RPC Server & Verifikator Tiket noVNC (`transport/rpc-server.ts` & `index.ts`)**
  - **File Baru:** `apps/worker/src/transport/rpc-server.ts`
  - **File Modifikasi:** `apps/worker/src/index.ts`
  - **Spesifikasi `rpc-server.ts` (`node:http`):**
    1. `GET /healthz` -> balas `200 {"status":"ok","busy":<bool>}` (tanpa butuh secret, untuk probe `nginx` lokal).
    2. `GET /internal/verify-ticket?ticket=...` -> verifikasi format `<expUnix>.<workerId>.<hmacHex>` menggunakan `crypto.timingSafeEqual` + `INTERNAL_API_SECRET` dan `Date.now()/1000 < expUnix`. Balas `204` jika sah, `403` jika tidak sah/kadaluarsa (dipakai oleh `nginx auth_request` untuk mengunci `/novnc/`!).
    3. Grup route bermesin (`X-Internal-Secret` wajib cocok secara `timingSafeEqual`, maks body `2 MiB`):
       - `POST /internal/execute-action`: tolak `409` jika sedang ada action aktif atau sesi login/manual aktif (`authContexts.size > 0` atau `manualSession` aktif); bila kosong, kunci flag `busy`, balas `202 {"accepted":true}`, jalankan `controller.run` di background -> `callback.post` -> lepas flag `busy`.
       - `POST /internal/start-login`: tutup `manualSession` lama jika beda `accountId`, jalankan alur `runLogin` dengan `runtime`, laporkan ke `authCallback`.
       - `POST /internal/auth-input`: teruskan kode 2FA ke `submitAuthInput`.
       - `POST /internal/auth-clear`: batalkan attempt, panggil `clearAuthContext(accountId)`, tutup `manualSession` jika milik `accountId` tersebut, balas `200`.
       - `POST /internal/browser-open`: tutup `manualSession` lama jika milik akun berbeda, buka `newAccountContext` dengan `runtime` akun baru, navigasi ke `targetUrl`, balas `200`.
    4. Di `index.ts`: ketika `config.poolMode === 'stateless'`, jalankan `rpcServer.listen(config.rpcPort, config.rpcHost)` dan **matikan** loop `heartbeat.start()`, `claimRow()`, serta `queue.next()` Redis agar worker 100% tenang saat tidak ada tugas (siap `auto-suspend` di `boxd`!).

- [x] **T3.6 — Unit & Context-Isolation Tests Worker**
  - **File Baru:** `apps/worker/src/test/rpc-server.test.ts`
  - **File Modifikasi:** `apps/worker/src/test/auth.test.ts`
  - **Skenario Uji:**
    1. Verifikasi tiket HMAC noVNC (`204` valid, `403` salah signature, `403` expired, `403` salah nama worker).
    2. Penolakan `401` tanpa `X-Internal-Secret` dan `409` saat worker sedang menjalankan job lain atau sedang memarkir sesi CAPTCHA.
    3. Dua panggilan `execute-action` berurutan (`Jakarta`/`Asia/Jakarta` lalu `Makassar`/`Asia/Makassar`) meneruskan parameter `storageState`, `geolocation`, `timezoneId`, dan `proxy` yang terpisah penuh ke `newAccountContext`, mengembalikan `updated_session`, dan tidak menyentuh disk `/data/sessions`.
  - **Gate Fase 3:** `pnpm --filter @smm/worker lint && pnpm --filter @smm/worker typecheck && pnpm --filter @smm/worker test` -> `PASS 100%`.

---

## Fase 4 — Kontrak OpenAPI, `@smm/shared`, & Next.js Dashboard (`apps/web/`)
*Referensi: [`04-WORKER-AND-FRONTEND-IMPLEMENTATION.md`](./04-WORKER-AND-FRONTEND-IMPLEMENTATION.md) §2–§3*

- [x] **T4.1 — Perbarui `openapi/openapi.yaml` & Jalankan Codegen**
  - **File Modifikasi:** `openapi/openapi.yaml`
  - **Spesifikasi:**
    1. Tambah `location` (`string`, nullable), `latitude` (`number`, nullable), `longitude` (`number`, nullable), dan `timezone` (`string`) pada schema `Account`.
    2. Tambah `location` (`string`, opsional) pada `CreateAccountRequest` dan item bulk import.
    3. Tambah endpoint `POST /api/accounts/{id}/live-view` -> response `200` `{ liveViewUrl: string, workerId: string, expiresAt: string }`.
    4. Tambah `updated_session` (`object`, opsional) dan `screenshot_jpeg_b64` (`string`, opsional) pada schema `ActionCallback`.
  - **Aksi Codegen:** Jalankan `make generate && pnpm --filter @smm/shared build`.

- [x] **T4.2 — Hubungkan Handler HTTP Go ke Tipe `oapigen` Baru**
  - **File Modifikasi:** `apps/api/internal/http/account.go`, `apps/api/internal/http/internal.go`, `apps/api/internal/http/router.go`
  - **Spesifikasi:** Petakan `Location`, `Latitude`, `Longitude`, `Timezone` di `toAccountResponse`, daftarkan handler `POST /api/accounts/:id/live-view`, dan uji lewat `apps/api/internal/http/*_test.go`.

- [x] **T4.3 — Perbarui Form Tambah & Import Akun di Dashboard**
  - **File Modifikasi:**
    - `apps/web/lib/api.ts`
    - `apps/web/components/add-account-dialog.tsx`
    - `apps/web/app/(dashboard)/accounts/new/page.tsx`
    - `apps/web/components/import-accounts-dialog.tsx`
  - **Spesifikasi:**
    - Sediakan pilihan **Lokasi Kota** (`Auto (Distribusi Merata)` + daftar kota dari `/api/containers/locations` atau konstanta kota Indonesia) saat membuat/meng-import akun.
    - Hilangkan keharusan memilih container worker saat membuat akun baru.

- [x] **T4.4 — Perbarui Tabel Akun, Live View Ber-Tiket, & Monitor 10 Worker Utama**
  - **File Modifikasi:**
    - `apps/web/app/(dashboard)/accounts/page.tsx`
    - `apps/web/components/account-detail-panel.tsx`
    - `apps/web/components/live-view.tsx`
    - `apps/web/app/(dashboard)/workers/page.tsx`
  - **Spesifikasi:**
    1. Tampilkan kota akun (`account.location`) langsung pada tabel Accounts & `AccountDetailPanel`.
    2. Saat operator membuka Live View / saat status login `AUTHENTICATING` atau `NEEDS_INPUT`, panggil `api.openAccountLiveView(account.id)` untuk mendapatkan `liveViewUrl` lengkap dengan `?ticket=...` (mendukung `https://` -> `wss://` secara native di `live-view.tsx` tanpa memotong query string `ticket`!).
    3. Pada halaman `/workers`, tampilkan ke-10 worker pool secara bersih lengkap dengan status slot dan indikator mode Stateless Pool.

- [x] **T4.5 — Parity `compose.yaml` Lokal & Validasi Full CI Monorepo**
  - **File Modifikasi:** `compose.yaml`, `infra/docker/.env.example`, `apps/web/package.json` (pastikan start standalone menghormati `HOSTNAME=0.0.0.0`).
  - **Gate Fase 4 (AKHIR TAHAP A — WAJIB STOP UNTUK REVIEW USER):** Jalankan **`make ci`** (`pnpm lint`, `pnpm typecheck`, `make fmt-check`, `pnpm build`, `pnpm test`, `go vet ./...`, `go build ./...`, `go test ./...`, dan validasi link docs) -> **WAJIB LULUS 100%**. Setelah hijau, **berhenti dan minta persetujuan eksplisit user** sebelum mengeksekusi **Fase 5–6 (`boxd`)**.

---

## Fase 5 — Template Infra, Domain `smm.ahdirmai.my.id`, & Provisioning Fleet di `boxd` (`fra`)
*Referensi: [`06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md`](./06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md) §1–§3*

- [ ] **T5.1 — Siapkan Artefak Konfigurasi `infra/boxd/` di Repo (Tanpa Rahasia)**
  - **File Baru:**
    - `infra/boxd/nginx-hub.conf` (`listen 0.0.0.0:8000`, route `/api/` & `/internal/` ke `127.0.0.1:8080` dengan `client_max_body_size 5m`, `/*` ke `127.0.0.1:3000`)
    - `infra/boxd/nginx-worker.conf` (`listen 0.0.0.0:8000`, `/healthz` & `/internal/` ke `127.0.0.1:8090`, `/novnc/` dilindungi `auth_request /_auth_ticket` + WebSocket `Upgrade` headers ke `127.0.0.1:6080`)
    - `infra/boxd/ecosystem.hub.config.cjs` (`smm-api` + `smm-web` dengan `HOSTNAME: '0.0.0.0'`, `PORT: '3000'`)
    - `infra/boxd/ecosystem.worker.config.cjs` (`smm-worker` dengan `DISPLAY: ':99'`, `TZ: 'Asia/Jakarta'`, `WORKER_POOL_MODE: 'stateless'`)
    - `infra/boxd/systemd/{xvfb,x11vnc,novnc}.service`
    - `infra/boxd/compose.hub-data.yaml` (`postgres`, `redis`, `minio` + `minio-init`, seluruh port di-bind ketat ke `127.0.0.1`!)

- [ ] **T5.2 — Provision `smm-hub` (`2vCPU/8G`, `fra`, Always-On) & Pasang Domain `smm.ahdirmai.my.id`**
  - **Aksi:**
    1. Buat VM `smm-hub` (`--region fra --vcpu 2 --memory 8G --auto-suspend-timeout=0 --auto-hibernate-timeout=0 --label app=smm --label role=hub --json`).
    2. Set `bot-protection off` dan `proxy set-port --vm smm-hub --port 8000`.
    3. **Pasang Custom Domain Langsung (`smm.ahdirmai.my.id` — Urutan Wajib §7.4):**
       - Pastikan record DNS `A` `smm.ahdirmai.my.id` -> `152.236.3.23` sudah terpasang di Cloudflare dalam mode **DNS-only (grey cloud)** terlebih dahulu.
       - Jalankan `boxd machine domain add smm.ahdirmai.my.id --vm smm-hub -y` dan tunggu TLS berstatus `active` *(wajib aktif sebelum `T5.5` agar `API_URL=https://smm.ahdirmai.my.id` dari ke-10 worker langsung tersambung valid)*.
    4. Upload kode build (`smm-api` Linux binary, `apps/web/.next/standalone` + `static` + `public`, migrasi SQL, dan `infra/boxd/*`) via `boxd machine cp`.
    5. Nyalakan data stack (`docker compose -f compose.hub-data.yaml up -d`).
    6. Siapkan `.local-runtime/boxenv/hub.boxenv` secara lokal (mode `600`, menyalin `CREDENTIAL_KEY`, `APIFY_TOKEN`, `LLM_*` dari `.env` lokal + men-generate `INTERNAL_API_SECRET` & `JWT_SECRET` kuat dengan `openssl rand -base64 32` tanpa mencetak nilainya, serta `CORS_ORIGINS=https://smm.ahdirmai.my.id`), lalu kirim via `boxd env push smm-hub --file .local-runtime/boxenv/hub.boxenv --dest /home/boxd/smm/.boxenv --mode 600 --json`.
    7. Pasang `nginx-hub.conf`, reload `nginx`, nyalakan `pm2` (`set -a; . /home/boxd/smm/.boxenv; set +a && pm2 start ecosystem.hub.config.cjs && pm2 save`), dan aktifkan `pm2 startup systemd`.
  - **Verifikasi:** `curl -fsS https://smm.ahdirmai.my.id/healthz` (dan `https://smm-hub.boxd.sh/healthz`) mengembalikan `200 OK` dengan sertifikat TLS valid.

- [ ] **T5.3 — Bangun & Uji `smm-worker-golden` (`2vCPU/8G`, `fra`)**
  - **Aksi:**
    1. Buat VM `smm-worker-golden` (`--region fra --vcpu 2 --memory 8G --auto-suspend-timeout=0 --auto-hibernate-timeout=0 --label app=smm --label role=golden --json`) dan set `bot-protection off`.
    2. Pasang paket `xvfb x11vnc novnc websockify fonts-noto-color-emoji fonts-liberation`, aktifkan `xvfb.service`, `x11vnc.service`, dan `novnc.service`.
    3. Upload dist `@smm/worker` + `@smm/shared`, jalankan `pnpm install --prod` dan `npx playwright install --with-deps chromium` (gunakan background execution agar tidak memblokir TTY sesuai aturan `boxd-cli`).
    4. Pasang `nginx-worker.conf` (`:8000`), daftarkan `pm2` + `pm2 startup systemd`.
  - **Verifikasi di Dalam Golden VM:**
    - `curl -fsS http://127.0.0.1:8000/healthz` -> `200`.
    - `curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8000/novnc/vnc.html` -> **`403`** (terkunci rapat tanpa tiket!).

- [ ] **T5.4 — Kunci Setting Auto-Sleep, Simpan Snapshot `smm-golden-v1`, & Stop Template**
  - **Aksi:**
    ```bash
    (
      set -a; . ~/.config/boxd/env; set +a
      boxd machine proxy set-port --vm smm-worker-golden --port 8000
      boxd machine config set smm-worker-golden bot-protection off
      boxd machine config set smm-worker-golden idle.ignore-outbound on
      boxd machine config set smm-worker-golden auto-suspend.timeout 300
      boxd machine config set smm-worker-golden auto-hibernate.timeout 3600
      boxd snapshots save smm-worker-golden smm-golden-v1 --json
      boxd machine stop smm-worker-golden --json
    )
    ```

- [ ] **T5.5 — Spawn `smm-worker-01` s/d `smm-worker-10` dari Snapshot `smm-golden-v1`**
  - **Aksi:** Loop `01..10`:
    1. `boxd machine new smm-worker-$i --from-snapshot smm-golden-v1 --label app=smm --label role=worker --label slot=$i --json`
    2. `boxd machine proxy add rpc --vm smm-worker-$i --port 8000 --no-wait --json`
    3. Push `.boxenv` masing-masing worker (`WORKER_ID=smm-worker-$i`, `API_URL=https://smm.ahdirmai.my.id`, `INTERNAL_API_SECRET` yang sama dengan `smm-hub`, mode `600`) via `boxd env push`.
    4. Jalankan `set -a; . /home/boxd/smm/.boxenv; set +a && pm2 restart /home/boxd/smm/ecosystem.worker.config.cjs --update-env && pm2 save`.

- [ ] **T5.6 — Audit Konfigurasi Ke-11 Mesin `boxd`**
  - **Gate Fase 5:** Periksa output `boxd machine get <vm> --json` untuk `smm-hub` dan `smm-worker-01..10`:
    - `smm-hub`: `status == "running"`, `auto_suspend == "off"`, `auto_hibernate == "off"`.
    - `smm-worker-01..10`: `idle-ignore-outbound` aktif, `auto_suspend == "300s"`, `auto_hibernate == "3600s"`, `bot-protection == "off"`, dan `https://rpc.smm-worker-XX.boxd.sh/healthz` menjawab `200`.

---

## Fase 6 — Migrasi Data 486 Akun, E2E Verification, & Cutover
*Referensi: [`02-DATABASE-AND-DATA-MIGRATION.md`](./02-DATABASE-AND-DATA-MIGRATION.md) §5 & [`05-EXECUTION-ROADMAP-TESTING-ROLLBACK.md`](./05-EXECUTION-ROADMAP-TESTING-ROLLBACK.md) §2*

- [ ] **T6.1 — Transfer & Restore Database Lokal ke `smm-hub` + Jalankan Migrasi `000024`**
  - **Aksi:**
    1. Upload `.local-runtime/backups/smm-pre-pool.dump` ke `smm-hub:/tmp/smm.dump` dengan `boxd machine cp --mode 600`.
    2. Bandingkan `sha256sum /tmp/smm.dump` di `smm-hub` dengan `.local-runtime/backups/smm-pre-pool.dump.sha256` -> **wajib identik 100%**.
    3. Hentikan sementara `smm-api`, jalankan `pg_restore --clean --if-exists` ke container Postgres `smm-hub`, hapus aman `/tmp/smm.dump` (`shred -u /tmp/smm.dump`).
    4. Jalankan migrasi `000024` hingga versi terbaru, lalu nyalakan kembali `smm-api` (`PoolBootstrapper` akan meng-upsert 10 baris `smm-worker-01..10`, mem-backfill lokasi kota & timezone ke-486 akun, dan membersihkan baris worker Docker lama).
  - **Verifikasi SQL di `smm-hub`:**
    - `SELECT count(*) FROM account;` == `486` (atau jumlah aktual saat dump).
    - `SELECT count(*) FROM account WHERE location IS NULL OR timezone IS NULL;` == **`0`**.
    - `SELECT count(*) FROM worker WHERE desired_state = 'RUNNING';` == **`10`** (`smm-worker-01..10`).

- [ ] **T6.2 — Simpan Checkpoint Pengaman di `smm-hub`**
  - **Aksi:** `(set -a; . ~/.config/boxd/env; set +a; boxd machine checkpoint save smm-hub post-migration-verified --json)`

- [ ] **T6.3 — Eksekusi Matriks Verifikasi E2E Live di `boxd`**
  - **Wajib Lulus Seluruh 5 Uji Berikut:**
    1. **Auto-Wake dari Hibernasi:** Jalankan `boxd machine hibernate smm-worker-01 -y`, lalu picu 1 job / live-view ke `smm-worker-01` -> pastikan bangun otomatis (`±85 ms`) dan berhasil melayani request.
    2. **Keamanan noVNC Publik:** Request langsung `curl -i https://smm-worker-01.boxd.sh/novnc/vnc.html` -> `403 Forbidden`; buka Live View dari Dashboard SMM -> tersambung `200` / `101 Switching Protocols` (`wss://`).
    3. **Region Grouping 486 Akun:** Panggil `GET /api/accounts/by-region` -> seluruh 486 akun terkelompok rapi per kota Indonesia tanpa terpotong di 200 baris.
    4. **Eksekusi Action & Session Write-Back:** Jalankan 1 action riil pada akun `AUTHENTICATED` -> job mencapai `SUCCESS`, `action_log` memiliki bukti screenshot di MinIO `smm-hub`, dan `account_session.last_used_at` ter-update.
    5. **Verifikasi Auto-Sleep Hemat Kredit:** Tunggu 6 menit tanpa job aktif -> pastikan `boxd machine list --label role=worker --json` menunjukkan worker berpindah ke `standby` (`0 CPU / 0 RAM billing`).

- [ ] **T6.4 — Verifikasi End-to-End Produksi pada `https://smm.ahdirmai.my.id`**
  - **Aksi:**
    1. Pastikan login operator ke dashboard `https://smm.ahdirmai.my.id`, pembuatan job action, serta pembukaan iframe Live View (`wss://smm-worker-XX.boxd.sh/novnc/...`) berjalan mulus tanpa error CORS atau Mixed Content.
    2. Hentikan container `smm-api-1` lokal di Mac agar tidak ada scheduler ganda yang berjalan bersamaan terhadap akun yang sama (simpan stack & backup DB lokal selama 7 hari sebagai cadangan rollback).

- [ ] **T6.5 — Dokumentasi Penutup & Audit Biaya Awal**
  - **Aksi:**
    1. Catat ringkasan hasil migrasi di [docs/PROGRESS.md](../PROGRESS.md) (tanpa menyertakan kredensial apa pun).
    2. Cek `boxd manage usage --json` untuk mencatat baseline pemakaian kredit awal.
    3. Jalankan `python3 scripts/check_docs_links.py` dan `git status` untuk memastikan tidak ada file `.boxenv` atau `.dump` yang ikut ter-track oleh Git.
