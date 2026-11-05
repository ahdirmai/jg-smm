# 05 — Roadmap Eksekusi End-to-End, Testing Matrix, Cutover, & Rollback

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)

---

## 1. Urutan Kerja Terintegrasi (2 Tahap Ber-Gate: Fase 1–4 -> Fase 5–6)

Eksekusi dibagi menjadi **dua tahap tegas dengan Approval Gate** agar perubahan kode diuji tuntas di lokal sebelum menyentuh infrastruktur `boxd`:
- **Tahap A (Lokal / Repo Only):** Pre-flight lokal (`T0.1, T0.3, T0.4`) + **Fase 1 s/d Fase 4** hingga `make ci` hijau 100% -> **STOP untuk review & persetujuan user**.
- **Tahap B (Cloud `boxd` & Cutover `smm.ahdirmai.my.id`):** Verifikasi CLI `boxd` (`T0.2`) + **Fase 5 s/d Fase 6**.

---

### Fase 0 — Pre-Flight Audit, Backup DB Lokal, & Verifikasi `boxd` CLI
**Estimasi:** ~30 menit | **Risiko:** Nol (Read-Only & Backup)

1. **Verifikasi Akses `boxd` (Tanpa Mencetak Token):**
   ```sh
   (set -a; . ~/.config/boxd/env; set +a; boxd auth && boxd machine list --json)
   ```
   Pastikan terlihat 3 mesin eksisting (`ahdirmai`, `speech`, `belajar`) — mesin `ahdirmai` dan `speech` **tidak disentuh sama sekali**.
2. **Rescue Session Container Lokal (Jika Ada):**
   Pastikan seluruh baris `account` dengan `auth_status = 'AUTHENTICATED'` di PostgreSQL lokal sudah memiliki entri di tabel `account_session`.
3. **Backup Penuh Database Lokal + Hitung SHA-256:**
   ```sh
   mkdir -p .local-runtime/backups
   docker exec smm-postgres-1 pg_dump -U smm -Fc --no-owner --no-acl smm \
     > .local-runtime/backups/smm-pre-pool.dump
   shasum -a 256 .local-runtime/backups/smm-pre-pool.dump
   ```

---

### Fase 1 — Migrasi Skema `000024` & Layer `sqlc` (`apps/api/db/`)
**Estimasi:** ~1.5 jam | **Referensi:** [`02-DATABASE-AND-DATA-MIGRATION.md`](./02-DATABASE-AND-DATA-MIGRATION.md)

1. Tambahkan `000024_stateless_worker_pool.up.sql` & `.down.sql` (`location`, `latitude`, `longitude`, `timezone`, `user_agent` pada `account`; `endpoint_url` pada `worker`; lepas `account_worker_platform_key`; perbarui `account_refresh_due_idx`).
2. Perbarui `account.sql`, `account_session.sql`, dan `worker.sql`, lalu jalankan `sqlc generate`.
3. Perbarui `domain.Account`, `domain.Worker`, `domain.TimezoneForCity`, dan repository mappers.
4. **Gate Lulus Fase 1:** `go test ./internal/repository/...` terhadap PostgreSQL disposable lulus 100% (termasuk uji migrasi `up -> down -> up`).

---

### Fase 2 — Refactor Backend Go API: HTTPS Push Dispatcher, Lease, & Security
**Estimasi:** ~3 jam | **Referensi:** [`03-BACKEND-API-AND-ORCHESTRATION.md`](./03-BACKEND-API-AND-ORCHESTRATION.md)

1. Buat port `WorkerRPCClient` & `WorkerSlotCoordinator` + `AccountExecutionLock`, serta adapter HTTP TLS (`internal/adapter/workerrpc`) dan Redis lock (`internal/adapter/pool_lock.go`).
2. Perluas `proxyresolver` dengan `ForAccount` (substitusi sticky `{account_id}` + blokir `socks5://` ber-password).
3. Implementasikan tiket HMAC berumur pendek untuk proteksi URL noVNC publik (`internal/service/novnc_ticket.go`).
4. Perbarui `ActionScheduler`, `AccountService`, `SessionRefresher`, dan `JobService`:
   - Kirim `AccountRuntimeSpec` (session + GPS + timezone + proxy) saat running via HTTPS Push.
   - **Hapus `stopOTPWorker`** pada mode stateless pool agar error OTP tidak mematikan VM worker.
   - Naikkan `MaxCallbackBytes` di `internal.go` menjadi `2 MiB` (mendukung `updated_session` + bukti screenshot `.jpg` dalam satu HTTPS callback).
   - Perbaiki `AccountsByRegion` agar membaca `account.Location` di seluruh halaman (`listAllAccounts`).
5. Buat `PoolBootstrapper` untuk mem-backfill lokasi 486 akun dan men-seed ke-10 worker utama dari `WORKER_POOL_ENDPOINTS`.
6. **Gate Lulus Fase 2:** `go test ./internal/arch/...` dan `go vet ./... && go test ./...` lulus 100%.

---

### Fase 3 — Refactor Stateless Worker TS (`apps/worker/`)
**Estimasi:** ~2.5 jam | **Referensi:** [`04-WORKER-AND-FRONTEND-IMPLEMENTATION.md`](./04-WORKER-AND-FRONTEND-IMPLEMENTATION.md)

1. Buat HTTP RPC server di `src/transport/rpc-server.ts` (`0.0.0.0:8090`, dilindungi `X-Internal-Secret` + endpoint `/internal/verify-ticket` untuk `nginx auth_request` noVNC).
2. Perbarui `newAccountContext` di `src/core/browser.ts` untuk menyetel `storageState`, `geolocation`, `timezoneId` (`Asia/Jakarta` / `Asia/Makassar`), `proxy`, dan `userAgent` per context.
3. Tambahkan ekstraksi `ctx.storageState()` (*session write-back*) dan pengiriman `screenshotBase64` sebelum `ctx.close()` di `controller.ts`, tanpa menulis file ke `/data/sessions` dan tanpa sleep jitter panjang di worker.
4. Matikan loop heartbeat 30 detik pada mode `boxd` agar microVM bebas masuk `standby`/`hibernated` saat idle.
5. **Gate Lulus Fase 3:** `pnpm --filter @smm/worker typecheck && pnpm --filter @smm/worker lint && pnpm --filter @smm/worker test` lulus 100%.

---

### Fase 4 — Kontrak OpenAPI & Penyesuaian Dashboard Web (`apps/web/`)
**Estimasi:** ~2 jam | **Referensi:** [`04-WORKER-AND-FRONTEND-IMPLEMENTATION.md`](./04-WORKER-AND-FRONTEND-IMPLEMENTATION.md)

1. Perbarui `openapi/openapi.yaml` dan regenerasi kode `oapigen` + `@smm/shared`.
2. Perbarui UI Accounts (kolom kota akun, pilih kota saat tambah/import, Live View ber-tiket `wss://`) dan UI Workers (monitor 10 microVM utama).
3. Pastikan konfigurasi build standalone Next.js menggunakan `HOSTNAME=0.0.0.0`.
4. **Gate Lulus Fase 4 (AKHIR TAHAP A — APPROVAL GATE):** `make ci` seluruh monorepo lulus 100%, serta simulasi lokal 1 API + 1 Worker RPC berjalan end-to-end. **Agent wajib berhenti di titik ini untuk meminta persetujuan user sebelum lanjut ke Fase 5–6.**

---

### Fase 5 — Provisioning Infrastruktur `boxd` (`smm-hub` di `smm.ahdirmai.my.id` + Golden Snapshot -> 10 Worker)
**Estimasi:** ~1.5 jam | **Referensi:** [`06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md`](./06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md)

1. Buat VM `smm-hub` (`2vCPU/8G`, region `fra`, `auto-suspend=0`, `auto-hibernate=0`, `bot-protection=off`). Pasang DNS Cloudflare `A smm.ahdirmai.my.id -> 152.236.3.23` (**DNS-only / grey cloud**) lebih dulu, lalu ikat `boxd machine domain add smm.ahdirmai.my.id --vm smm-hub -y` hingga TLS `active`. Siapkan Docker (`postgres`, `redis`, `minio`), `pm2` (`smm-api`, `smm-web`), dan `nginx:8000`.
2. Buat 1 VM `smm-worker-golden` (`2vCPU/8G`, region `fra`), pasang `Xvfb`, `x11vnc`, `websockify`/noVNC, Playwright Chromium, `@smm/worker` di `pm2`, dan `nginx:8000` (dengan `auth_request` tiket noVNC).
3. Uji 1 siklus eksekusi & wake dari `standby` pada `smm-worker-golden`.
4. Simpan snapshot `smm-golden-v1`, lalu luncurkan **`smm-worker-01` s/d `smm-worker-10`** dari snapshot tersebut (`--label app=smm --label role=worker`) dengan `API_URL=https://smm.ahdirmai.my.id`, pastikan `bot-protection=off`, `idle.ignore-outbound=on`, `auto-suspend.timeout=300`, `auto-hibernate.timeout=3600`.

---

### Fase 6 — Migrasi Data Live, E2E Verification, & Cutover `smm.ahdirmai.my.id`
**Estimasi:** ~1 jam

1. Upload `.local-runtime/backups/smm-pre-pool.dump` ke `smm-hub` via `boxd machine cp`, verifikasi SHA-256, restore ke PostgreSQL `smm-hub`, dan jalankan migrasi `000024`.
2. Pastikan `.boxenv` (mode `600`, berisi `CREDENTIAL_KEY` yang sama dengan lokal + `INTERNAL_API_SECRET` baru) aktif di `smm-hub` dan ke-10 worker via `boxd env push`.
3. Simpan checkpoint `boxd machine checkpoint save smm-hub post-migration-verified` sebagai titik aman.
4. Jalankan E2E Smoke Test penuh melalui `https://smm.ahdirmai.my.id` (lihat Bagian 2) dan matikan scheduler container `smm-api-1` lokal agar tidak terjadi eksekusi ganda.

---

## 2. Matriks Pengujian Wajib (Testing Matrix)

| Area Uji | Skenario | Hasil yang Diharapkan |
| :--- | :--- | :--- |
| **Layer Guard** | `go test ./internal/arch/...` | Lulus tanpa pelanggaran impor antar-layer. |
| **Wake from Standby/Hibernate** | `boxd machine hibernate smm-worker-01` lalu trigger 1 job dari `smm-hub` | `smm-worker-01` bangun otomatis (`±85 ms`), menjalankan aksi, callback sukses, lalu kembali tidur setelah idle timeout. |
| **Security noVNC Publik** | Buka `https://smm-worker-01.boxd.sh/novnc/vnc.html` tanpa `?ticket=` vs dengan tiket valid | Tanpa tiket -> `403 Forbidden`; dengan tiket dari dashboard -> tersambung mulus via `wss://`; tiket kadaluarsa (>5 mnt) -> `403`. |
| **Timezone & GPS di `fra`** | Jalankan job dengan akun kota `Jakarta` lalu akun kota `Makassar` di `smm-worker-01` (`fra`) | Halaman melihat `Asia/Jakarta` + koordinat Jakarta pada job 1, lalu `Asia/Makassar` + koordinat Makassar pada job 2. |
| **OTP Isolation** | Akun terkena `otp_required: perlu OTP` saat login | Akun menjadi `FAILED`, lease worker dilepas (`worker_id = NULL`), microVM `smm-worker-XX` **tidak dimatikan**. |
| **Session Write-Back (>64 KiB)** | Job berhasil dengan `storageState()` berukuran `150 KiB` | Callback HTTP `200`, `account_session` ter-update terenkripsi, file sementara di worker bersih. |
| **Auto-Sleep Cost Check** | Biarkan ke-10 worker tanpa job selama > 6 menit lalu cek `boxd machine list --label role=worker` | Ke-10 worker berpindah dari `running` ke `standby` (`0 CPU / 0 RAM billing`). |

---

## 3. Prosedur Rollback Cepat

1. **Stack Lokal Mac Tidak Dihapus Selama Masa Cutover:** Selama 7 hari pertama setelah migrasi ke `boxd`, stack & volume database lokal di Mac dibiarkan utuh (cukup di-stop container-nya) sebagai *fallback* instan 100%.
2. **In-Place Checkpoint di `smm-hub`:** Sebelum perubahan berisiko di `smm-hub`, ambil `boxd machine checkpoint save smm-hub <nama>`; bila terjadi kesalahan konfigurasi, pulihkan dalam hitungan detik dengan `boxd machine checkpoint restore smm-hub <nama> -y`.
3. **Rekonstruksi Fleet Worker `< 30 Detik`:** Karena ke-10 worker bersifat *stateless* (seluruh session ada di `smm-hub`), jika ada worker bermasalah cukup jalankan `boxd machine remove --label role=worker -y` lalu spawn ulang 10 unit baru dari snapshot `smm-golden-v1`!
