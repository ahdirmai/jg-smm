# 08 — Rencana Audit komprehensif Hasil Implementasi & Migrasi Stateless Worker Pool

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
**Acuan Spesifikasi:** [task-list.md](./task-list.md) & Dokumen [`01`](./01-RESEARCH-ARCHITECTURE.md) s/d [`06`](./06-BOXD-INFRA-DEPLOYMENT-RUNBOOK.md)  
**Tanggal Penyusunan:** 5 November 2026  
**Target Audit:** Hasil Implementasi **Tahap A (`Fase 1–4` di Repo)** & Hasil Cutover **Tahap B (`Fase 5–6` di `boxd` / `https://smm.ahdirmai.my.id`)**

---

## 1. Tujuan & Struktur Audit Dua Gerbang (*Two-Gate Audit*)

Selaras dengan strategi eksekusi bertahap di [task-list.md](./task-list.md), rencana audit ini dibagi menjadi **dua gerbang verifikasi independen** agar tidak ada bug orkestrasi, kebocoran sesi antar-akun, atau celah keamanan yang lolos ke produksi `boxd`:

```mermaid
flowchart LR
    TA["Hasil Implementasi Tahap A<br/>(Fase 1-4 di Repo Lokal)"] --> G1{"GERBANG AUDIT 1<br/>Audit Kode, Kontrak, Keamanan,<br/>& Simulasi DB Clone 486 Akun"}
    G1 -->|"Temuan P0/P1"| FIX1["Remediasi Kode Lokal<br/>& Re-Audit Gerbang 1"]
    FIX1 --> G1
    G1 -->|"LULUS (0 P0, 0 P1)"| TB["Eksekusi Tahap B<br/>(Fase 5-6 di boxd + smm.ahdirmai.my.id)"]
    TB --> G2{"GERBANG AUDIT 2<br/>Audit Infra boxd, Rekonsiliasi Data,<br/>noVNC Security, Auto-Sleep, & Kredit"}
    G2 -->|"Temuan P0/P1"| FIX2["Remediasi Infra/Config<br/>atau Rollback Checkpoint"]
    FIX2 --> G2
    G2 -->|"LULUS (0 P0, 0 P1)"| LIVE["Sign-Off Produksi<br/>https://smm.ahdirmai.my.id"]
```

### Aturan Keras Pelaksanaan Audit (*Audit Hard Guardrails*)
1. **Zero Secret Exposure:** Auditor (manusia maupun agent) **dilarang keras** mencetak isi `.env`, `.boxenv`, `~/.config/boxd/env`, `~/.config/cloudflare/*`, `CREDENTIAL_KEY`, `INTERNAL_API_SECRET`, `password_enc`, `storage_state`, atau `pool_key` ke terminal, laporan audit, log, maupun chat. Verifikasi keberadaan secret hanya menggunakan panjang byte (`len > 0` / `wc -c`) atau status dekripsi boolean (`ok/fail`).
2. **Isolasi Database Lokal:** Semua uji migrasi `000024` (`up -> down -> up`) dan simulasi backfill 486 akun pada Gerbang Audit 1 **wajib** dijalankan di atas database PostgreSQL sementara yang terpisah (`smm_audit_test` yang di-restore dari `.local-runtime/backups/smm-pre-pool.dump`), **bukan** langsung menimpa DB kerja `smm`.
3. **Isolasi Mesin Produksi `boxd`:** Selama Gerbang Audit 2, mesin `ahdirmai` dan `speech` hanya boleh muncul di pembacaan daftar inventaris (`boxd machine list --json`) untuk memastikan statusnya tidak berubah — dilarang menjalankan `exec`, `config`, `restart`, atau `stop` ke keduanya.

---

## 2. Daftar *Suspect Hotspots* Prioritas Tinggi (`S1`–`S7`) untuk Gerbang Audit 1

Berdasarkan pembacaan awal (*static pre-audit*) atas kode Tahap A yang baru selesai ditulis, auditor **wajib memeriksa 7 titik rawan berikut paling pertama** karena unit test dengan mock berpotensi melewatkannya di runtime nyata:

| ID | Lokasi Kode Aktual | Risiko yang Teridentifikasi (Wajib Dibuktikan & Diperbaiki Bila Terkonfirmasi) | Target Severity |
| :-: | :--- | :--- | :-: |
| **`S1`** | [`apps/api/cmd/server/main.go`](../../apps/api/cmd/server/main.go) (`L474–L515`) & [`apps/api/internal/service/job.go`](../../apps/api/internal/service/job.go) (`L359–L458`) | **Lifecycle Lease Auth & Gating `WithPool`:** (a) Pemanggilan `jobSvc.WithPool(...)` berada di dalam blok `if cfg.ActionIntervalSeconds > 0`, sehingga bila `ACTION_INTERVAL_SECONDS=0` (default lokal), `JobService` menganggap sistem bukan mode pool dan tetap memanggil `stopOTPWorker`! (b) Pada `RecordAuthOutcome`, pelepasan lease (`ReleaseLease`), pelepasan Redis slot (`ReleaseWorkerSlot`), dan pemanggilan `workerRPC.ClearAuth` baru menangani cabang `AuthFailed + OTP` — belum dijalankan saat login berhasil (`AuthAuthenticated`) maupun gagal non-OTP (`AuthFailed`), dan belum ada goroutine keepalive `120 dtk` saat `AuthNeedsInput`! | **`P0 Blocker`** |
| **`S2`** | [`apps/api/cmd/server/main.go`](../../apps/api/cmd/server/main.go) (`L216–L218`, `L609–L626`) | **Pencampuran 58 Worker Warisan `STOPPED` di Kandidat Pool & Loop Reconciler:** (a) `Workers` di `PoolDispatch` memanggil `workerRepo.List(ctx, Limit: 500)` tanpa memfilter `DesiredRunning` dan `EndpointURL != nil`. Jika terdapat 58 baris worker lama yang `STOPPED` di DB, hash afinitas `fnv32a(accountID) % len(candidates)` bagi akun non-proxy (`allowFallback=false`) akan mendarat di worker mati (`endpoint_url = NULL`) ±85% waktu! (b) `reconciler.Run` dan `sweeper.Run` belum di-skip ketika `cfg.IsStatelessPool() == true`. | **`P0 Blocker`** |
| **`S3`** | [`apps/worker/src/index.ts`](../../apps/worker/src/index.ts) (`L383`, `L439–L442`, `L460–L481`) | **Kebocoran `manualContext` Lintas Akun & Status `isBusy`:** Pada handler RPC `openBrowser`, `manualContext` lama diteruskan ke `openLiveView` tanpa ditutup lebih dulu saat `manualAccountId !== runtime.accountId` (`if (!ctx)` bernilai salah sehingga Akun B memakai context/cookie/proxy/GPS milik Akun A!). Selain itu `isBusy()` belum bernilai `true` ketika `manualContext` sedang aktif dipakai operator di layar Xvfb, dan `startLogin` belum menutup `manualContext` beda akun. | **`P0 Blocker`** |
| **`S4`** | [`apps/api/internal/service/novnc_ticket.go`](../../apps/api/internal/service/novnc_ticket.go) (`L93–L100`) vs desain `nginx-worker.conf` | **Sinkronisasi Lokasi Query Tiket noVNC vs `nginx auth_request`:** `BuildProtectedNoVNCURL` menyematkan `?ticket=` di dalam parameter `path=novnc%2Fwebsockify%3Fticket%3D...` (ter-encode), bukan pada query terluar `GET /novnc/vnc.html` maupun request aset statis `/novnc/app/*.js`. Jika nantinya di Fase 5 `nginx` memasang `auth_request` pada seluruh `location /novnc/`, maka pemuatan `vnc.html` dan file `.js` akan ditolak `403`! Harus dipastikan kontrak `nginx` menaruh `auth_request` presisi di `location = /novnc/websockify` (jalur stream framebuffer RFB sesungguhnya) atau menyediakan mekanisme tiket halaman yang kompatibel. | **`P1 Critical`** |
| **`S5`** | [`apps/api/cmd/server/main.go`](../../apps/api/cmd/server/main.go) (`L335–L348`, `L511`) & [`apps/api/internal/service/job.go`](../../apps/api/internal/service/job.go) (`L271–L282`) | **Kesesuaian Bucket MinIO Bukti Screenshot:** `JobService.storeEvidence` mengunggah ke `s.blobs` (`rawStorage` yang terikat ke `MINIO_BUCKET=smm-raw` dengan key `"screenshots/<nama>"`), sedangkan `minio-init` membuat bucket terpisah `screenshots`. Wajib diaudit dari mana endpoint/UI bukti screenshot membaca objek tersebut agar gambar bukti tidak `404`. | **`P1 Critical`** |
| **`S6`** | [`apps/api/internal/service/account.go`](../../apps/api/internal/service/account.go) (`L558–L625`) | **`ExportSession` / `ImportSession` di Mode Stateless:** `ExportSession` saat ini menolak `409 account has no container` bila `a.WorkerID == nil`. Pada mode `stateless`, seluruh akun idle memiliki `worker_id = NULL` karena session tersimpan di `account_session` PostgreSQL — perlu diaudit apakah route export/import sudah melayani baca/tulis langsung ke `BrowserSessionStore` ketika `s.pool != nil`. | **`P2 Major`** |
| **`S7`** | [`apps/api/db/migrations/000024_stateless_worker_pool.up.sql`](../../apps/api/db/migrations/000024_stateless_worker_pool.up.sql) (`L41–L44`) | **Format Nama Kota saat Backfill SQL:** Klausa `WHEN w.location IN ('Makassar', 'Denpasar', 'Balikpapan')` perlu dicek terhadap variasi nilai aktual `worker.location` di dump 486 akun (mis. `"Makassar"` vs `"Makassar, Indonesia"`) agar akun WITA tidak keliru mendapat `Asia/Jakarta`. | **`P2 Major`** |

---

## 3. Rincian Eksekusi Gerbang Audit 1 — Tahap A (Kode, DB Clone, & Simulasi Lokal)

### A1. Audit Skema Database, Reversibilitas Migrasi `000024`, & Data Riil 486 Akun
1. **Persiapan Sandbox DB (`smm_audit_test`):**
   - Buat database disposable `smm_audit_test` di container Postgres lokal.
   - Restore `.local-runtime/backups/smm-pre-pool.dump` ke `smm_audit_test`.
2. **Audit Eksekusi Migrasi `000024` & Reversibilitas:**
   - Jalankan migrasi `000024 up` -> verifikasi struktur kolom `account` & `worker`, constraint `account_location_coords_pairing`, penghapusan `account_worker_platform_key`, dan definisi `account_refresh_due_idx`.
   - Uji siklus `000024 down` lalu `000024 up` kembali di DB kosong terpisah (`smm_audit_schema_test`) untuk membuktikan migrasi balik tidak rusak.
3. **Audit Integritas Data 486 Akun & Rencana Query (`EXPLAIN`):**
   - Jalankan `PoolBootstrapper.EnsurePoolWorkers` + `BackfillAccounts` terhadap `smm_audit_test`.
   - Pastikan:
     - Total `account` tetap **486** (`485` Instagram, `1` Threads), dan seluruh **15** baris `account_session` untuk akun `AUTHENTICATED` tetap dapat dibuka (`sealer.Open`) dengan `CREDENTIAL_KEY` eksisting.
     - `SELECT count(*) FROM account WHERE location IS NULL OR latitude IS NULL OR longitude IS NULL OR timezone IS NULL OR user_agent IS NULL;` menghasilkan **`0`**.
     - Seluruh akun berkota `Makassar`, `Denpasar`, atau `Balikpapan` memiliki `timezone = 'Asia/Makassar'`, dan kota WIB memiliki `timezone = 'Asia/Jakarta'`.
     - Jalankan `EXPLAIN (COSTS OFF)` untuk query `ListAccountsDueForRefresh` (dengan `enable_seqscan = off`) guna membuktikan PostgreSQL benar-benar memakai **`account_refresh_due_idx`**.
   - Hapus `smm_audit_test` setelah selesai.

### A2. Audit Kepatuhan Arsitektur, Sinkronisasi Codegen, & Kualitas Kode
1. **Layer Guard (`arch_test.go`):** Jalankan `go test ./internal/arch/...` dan periksa seluruh file baru (`port/worker_rpc.go`, `port/pool.go`, `service/pool_*.go`, `adapter/poolmgr/*`, `adapter/workerrpc/*`) untuk memastikan `service` bebas dari impor `adapter`/`repository`/`http`/`config`.
2. **Drift Check Codegen (Zero Manual Edit pada File Generated):**
   - Jalankan `make sqlc-check`.
   - Jalankan `make generate && pnpm --filter @smm/shared build` lalu periksa `git diff --exit-code` pada `apps/api/internal/http/oapigen/api.gen.go` dan `packages/shared/src/generated/api.ts`.
3. **Full Monorepo Gate & Race Detector:**
   - Jalankan `make ci` serta `cd apps/api && go test -race ./internal/...`.

### A3. Audit Orkestrasi Go API, Concurrency Lock/Lease, & Proxy Resolver
1. **Verifikasi Perbaikan Hotspot `S1`, `S2`, `S5`, `S6`:** Pastikan wiring `main.go` dan pelepasan lease di seluruh cabang terminal `RecordAuthOutcome` + `ReclaimStaleLoginAttempt` berjalan tuntas tanpa kebocoran slot Redis maupun baris `worker_id` menggantung.
2. **Audit Eksklusivitas & Afinitas (`poolmgr` + `PoolDispatch`):**
   - Uji 2 goroutine mengeksekusi job untuk `accountID` yang sama secara bersamaan -> tepat 1 menang `AcquireAccount`, 1 menerima `ErrConflict` dan di-reschedule tanpa membakar kuota rate-limit/cooldown.
   - Uji akun tanpa proxy (`ProxyGroupID == nil`) selalu diarahkan ke 1 worker afinitas yang sama (`allowFallback == false`), sedangkan akun ber-proxy residensial (`ProxyGroupID != nil`) otomatis berpindah ke worker bebas berikutnya saat worker afinitas sibuk (`allowFallback == true`).
   - Uji worker yang sedang memegang `SlotAuthLease` (login/CAPTCHA) **tidak pernah** dipilih oleh `SlotAction`.
3. **Audit Proxy Resolver (`ForAccount`):**
   - Substitusi token `{account_id}` dan `{session}` menghasilkan ID sticky konsisten per akun.
   - Skema `socks5://user:pass@host:port` ditolak secara eksplisit sebelum dikirim ke Playwright Chromium `1.48.2`.

### A4. Audit Worker TypeScript RPC, Isolasi Context Playwright, & Kebersihan MicroVM
1. **Audit Mode Idle (`boxd` Sleep-Readiness):**
   - Jalankan proses `@smm/worker` dengan `WORKER_POOL_MODE=stateless` tanpa `REDIS_URL` (atau dengan port Redis tertutup) -> buktikan worker **tetap boot sehat**, mendengarkan di `:8090`, tidak membuka koneksi Redis, dan tidak menyalakan timer periodik `heartbeat` / queue depth polling.
2. **Audit Isolasi Penuh `BrowserContext` & `manualContext` (`S3`):**
   - Verifikasi 2 request berurutan ke worker yang sama (Akun 1: `Jakarta` / `Asia/Jakarta` / Proxy A; Akun 2: `Makassar` / `Asia/Makassar` / Proxy B) baik pada `/internal/execute-action`, `/internal/start-login`, maupun `/internal/browser-open`:
     - Context lama benar-benar tertutup sebelum context akun berbeda dibuka.
     - Cookie, `localStorage`, `timezoneId`, `geolocation`, `userAgent`, dan `proxy` 100% terpisah.
   - Verifikasi `/tmp/smm-screenshots` dan `/data/sessions` berisi **0 file** setelah job selesai.

### A5. Audit Kontrak OpenAPI, Frontend Dashboard (`apps/web/`), & Tiket Live View
1. **Kontrak UI & API:**
   - Form Tambah Akun & Import CSV mendukung pemilihan kota (`Auto` maupun nama kota eksplisit) tanpa mewajibkan pemilihan container worker.
   - `GET /api/accounts/by-region` mengembalikan pengelompokan kota lengkap untuk seluruh 486 akun (tidak terpotong di 200 akun).
   - Halaman `/workers` menampilkan ringkasan 10 worker pool dan status lease secara akurat.
2. **Keutuhan URL Tiket noVNC (`S4`):**
   - Verifikasi `POST /api/accounts/{id}/live-view` menghasilkan URL ber-tiket HMAC, komponen `LiveView` di `apps/web/components/live-view.tsx` mempertahankan tanda tangan tiket secara utuh, dan skema `https://` otomatis terhubung ke WebSocket `wss://`.
3. **Kesiapan Bind `boxd` pada Next.js:**
   - Verifikasi konfigurasi start standalone Next.js menghormati `HOSTNAME=0.0.0.0` (bukan `127.0.0.1` atau nama host container).

### A6. Audit Keamanan & Zero Credential Leak
1. **Audit Log & Error Sanitisation:**
   - Grep seluruh pemanggilan `logger.*` / `slog.*` / `fmt.Errorf` pada jalur `workerrpc`, `pool_dispatch`, `internal.go`, `rpc-server.ts`, `callback.ts`, dan `auth.ts` untuk memastikan body payload (`storageState`, `updated_session`, `credentials`, `proxy.password`, `X-Internal-Secret`) tidak pernah ikut tercatat, bahkan ketika terjadi gagal parse JSON atau HTTP error `4xx/5xx`.
2. **Audit Autentikasi & Batas Ukuran Endpoint Internal:**
   - Uji request ke `/internal/*` di Go API dan Worker RPC tanpa `X-Internal-Secret` atau dengan secret salah -> wajib `401` dengan perbandingan *constant-time*.
   - Uji payload `1.5 MiB` (`updated_session` + base64 JPEG) -> `200 OK`; uji payload `> 2 MiB` -> ditolak bersih (`400`/`413`) tanpa crash memori.
3. **Secret Scan Repository:** Jalankan `.githooks/pre-commit` / `gitleaks` pada seluruh commit baru.

### A7. Audit Simulasi Runtime E2E Lokal (Pre-`boxd` Dress Rehearsal)
1. Nyalakan `smm-api` lokal dalam mode `WORKER_POOL_MODE=stateless` + 2 proses `@smm/worker` (`smm-worker-01` di `:8091`, `smm-worker-02` di `:8092`).
2. Jalankan 4 skenario terintegrasi:
   - **Skenario 1:** Dispatch action `dry-run=true` & `dry-run=false` (terhadap halaman stub lokal) -> pastikan push HTTPS `202 Accepted` -> callback `/internal/action-callback` -> `updated_session` terenkripsi masuk ke `account_session` -> bukti `.jpg` tersimpan di MinIO dan dapat diakses dari API/UI -> Redis lock & worker slot bersih kembali.
   - **Skenario 2:** Buka Live View akun A di `smm-worker-01`, verifikasi `/internal/verify-ticket` menjawab `204` untuk tiket valid dan `403` untuk tiket worker lain (`smm-worker-02`) atau tiket expired.
   - **Skenario 3:** Simulasikan callback `FAILED` dengan `otp_required: perlu OTP` -> pastikan lease akun lepas (`worker_id = NULL`), slot Redis bebas, dan worker **tetap hidup** melayani job berikutnya.
   - **Skenario 4:** Matikan proses worker lokal selesai uji.

---

## 4. Rincian Eksekusi Gerbang Audit 2 — Tahap B (Pasca-Cutover `boxd` & `https://smm.ahdirmai.my.id`)

Gerbang Audit 2 dijalankan tepat setelah **Fase 5 & Fase 6** pada [task-list.md](./task-list.md) dieksekusi di infrastruktur `boxd`:

### B1. Audit Inventaris, Isolasi, & Konfigurasi 11 Mesin `boxd` (`fra`)
1. **Keselamatan Mesin Eksisting:** Jalankan `(set -a; . ~/.config/boxd/env; set +a; boxd machine list --json | jq 'map({name, status, size, labels})')` -> pastikan mesin `ahdirmai` dan `speech` tetap pada status & konfigurasi semula tanpa perubahan apa pun.
2. **Audit `smm-hub`:**
   - `status == "running"`, `auto_suspend == "off"`, `auto_hibernate == "off"`, `bot-protection == "off"`, proxy utama mengarah ke `:8000`, dan domain `smm.ahdirmai.my.id` berstatus `active`.
   - Di dalam `smm-hub`, jalankan `ss -tulpn`: pastikan PostgreSQL (`:5432`), Redis (`:6379`), dan MinIO (`:9000`/`:9001`) **hanya** mendengarkan pada `127.0.0.1` (atau bridge internal Docker yang tidak terpublikasi ke `0.0.0.0`), sementara `nginx` mendengarkan di `0.0.0.0:8000`.
   - Pastikan file `/home/boxd/smm/.boxenv` memiliki permission `600` (`stat -c '%a' /home/boxd/smm/.boxenv` == `600`) dan `/tmp/smm.dump` sudah tidak ada.
3. **Audit `smm-worker-golden` & `smm-worker-01..10`:**
   - `smm-worker-golden` dalam keadaan `stopped` (tidak memakan kredit runtime) dan snapshot `smm-golden-v1` terdaftar di `boxd snapshots list --json`.
   - Seluruh 10 worker (`smm-worker-01` s/d `smm-worker-10`) memiliki `bot-protection == "off"`, `idle.ignore-outbound == "on"`, `auto-suspend == "300s"`, `auto-hibernate == "3600s"`, named proxy `rpc.smm-worker-XX.boxd.sh -> :8000`, dan `.boxenv` bermode `600` dengan `API_URL=https://smm.ahdirmai.my.id`.

### B2. Audit Rekonsiliasi Data Cutover (`Mac Source` vs `smm-hub`)
Bandingkan agregat SQL antara dump sumber di Mac dan PostgreSQL di `smm-hub` (tanpa menampilkan kolom rahasia):
- `SELECT platform, auth_status, count(*) FROM account GROUP BY 1, 2 ORDER BY 1, 2;` -> wajib cocok 100% (total **486** akun).
- `SELECT count(*) FROM account_session;` -> wajib cocok 100% (**15**+ sesi tersimpan), dan uji dekripsi internal (`RuntimeSpec`) pada 1 sampel akun `AUTHENTICATED` berhasil tanpa error `cipher: message authentication failed`.
- `SELECT count(*) FROM account WHERE location IS NULL OR timezone IS NULL OR user_agent IS NULL;` -> wajib **`0`**.
- `SELECT count(*) FROM worker WHERE desired_state = 'RUNNING' AND endpoint_url IS NOT NULL;` -> wajib tepat **`10`** (`smm-worker-01..10`).

### B3. Audit Keamanan Perimeter Publik (`smm.ahdirmai.my.id` & `*.boxd.sh`)
1. **Validasi DNS & TLS:**
   - `dig +short smm.ahdirmai.my.id` mengarah langsung ke IP ingress `boxd` (`152.236.3.23`, Cloudflare DNS-only / grey cloud, bukan IP proxy Cloudflare) dan `curl -fsSI https://smm.ahdirmai.my.id/healthz` mengembalikan `200` dengan sertifikat TLS valid.
2. **Uji Penetrasi Permukaan Worker Publik:**
   - `curl -i -X POST https://rpc.smm-worker-01.boxd.sh/internal/execute-action -d '{}'` (tanpa secret) -> wajib **`401 Unauthorized`**.
   - Percobaan koneksi WebSocket tanpa tiket ke `wss://smm-worker-01.boxd.sh/novnc/websockify` -> wajib ditolak **`403 Forbidden`** oleh `nginx auth_request`.
   - Percobaan koneksi dengan tiket milik `smm-worker-02` yang dikirim ke `smm-worker-01` -> wajib ditolak **`403 Forbidden`**.
   - Pembukaan Live View resmi dari dashboard `https://smm.ahdirmai.my.id` -> layar Xvfb tampil mulus melalui `wss://` (`101 Switching Protocols`).

### B4. Audit Siklus *Instant Wake* & *Auto-Sleep* Hemat Kredit di `boxd`
1. **Cold-Wake dari Hibernasi:**
   - Jalankan `boxd machine hibernate smm-worker-01 -y` dan pastikan statusnya `hibernated`.
   - Picu request ke `smm-worker-01` dari `smm-hub` -> catat latensi total wake + respons HTTP (`target < 2 detik` termasuk TLS handshake & wake microVM `±85 ms`), pastikan tidak kena `502` atau challenge `bot-protection`.
2. **Auto-Sleep ke `standby`:**
   - Biarkan klaster tanpa job aktif selama **6 menit** (> `300 detik`).
   - Periksa `boxd machine list --label role=worker --json` -> pastikan kesepuluh `smm-worker-01..10` otomatis berpindah dari `running` ke **`standby`** (membuktikan tidak ada proses nakal/polling di dalam worker yang menahan VM tetap menyala!).

### B5. Audit Anti-Deteksi Browser di Region Frankfurt (`fra`)
Melalui 1 eksekusi diagnostik non-destruktif pada worker di `boxd` (`fra`), verifikasi nilai yang terlihat oleh halaman di dalam `BrowserContext`:
- Akun berkota `Jakarta`: `Intl.DateTimeFormat().resolvedOptions().timeZone === "Asia/Jakarta"`, `getTimezoneOffset() === -420`, `navigator.language === "id-ID"`, koordinat berada dalam radius 25 km pusat Jakarta, `navigator.webdriver === false`.
- Akun berkota `Makassar`: `Intl.DateTimeFormat().resolvedOptions().timeZone === "Asia/Makassar"`, `getTimezoneOffset() === -480`, koordinat berada dalam radius 18 km pusat Makassar.
- Jika akun terikat `proxy_group`: IP keluar yang terbaca berasal dari ISP residensial Indonesia (bukan `AS396356 Latitude.sh`) dan konsisten untuk `account_id` yang sama.

### B6. Audit Ketahanan Reboot, Single-Scheduler Guard, & Baseline Kredit
1. **Ketahanan Reboot:** Jalankan `boxd machine restart smm-worker-01` dan verifikasi layanan `xvfb`, `x11vnc`, `novnc`, `nginx`, dan `pm2` (`smm-worker`) otomatis pulih `online` tanpa intervensi manual.
2. **Pencegahan Double-Scheduler:** Pastikan container `smm-api-1` di Mac lokal sudah dihentikan sebelum scheduler produksi di `smm-hub` diaktifkan pada akun riil, sementara backup lokal disimpan selama 7 hari masa observasi.
3. **Audit Biaya Awal:** Jalankan `boxd manage usage --json` pada T+1 jam dan T+24 jam setelah cutover untuk memverifikasi *burn rate* aktual sesuai proyeksi hemat saldo `$150`.

---

## 5. Klasifikasi Temuan & Kriteria Kelulusan (*Sign-Off Criteria*)

Setiap temuan selama pelaksanaan audit wajib diklasifikasikan ke dalam 4 tingkat keparahan berikut:

| Tingkat | Definisi | Dampak terhadap *Go / No-Go* |
| :-: | :--- | :--- |
| **`P0 — Blocker`** | Kebocoran rahasia/kredensial, percampuran sesi antar-akun, rusaknya data DB, worker bersama mati saat OTP, noVNC terbuka ke publik tanpa autentikasi, atau fitur inti (dispatch/login/callback) gagal jalan. | **NO-GO MUTLAK.** Wajib diperbaiki & diuji ulang sebelum melangkah ke tahap berikutnya. |
| **`P1 — Critical`** | Worker gagal `auto-suspend` (membakar kredit 24/7), mismatch timezone/GPS di `fra`, afinitas worker meleset ke baris `STOPPED`, atau bukti screenshot gagal diakses. | **NO-GO.** Wajib diselesaikan sebelum *production cutover*. |
| **`P2 — Major`** | Degradasi UX non-destruktif, edge-case fitur sekunder (mis. tombol export manual pada akun idle), atau ketidaksinkronan metrik minor di dashboard. | Boleh lanjut dengan tiket perbaikan terjadwal (*tracked follow-up*) atas persetujuan user. |
| **`P3 — Minor`** | Kosmetik UI, perapian komentar/dokumentasi, atau optimasi mikro. | Tidak menahan *sign-off*. |

### Struktur Laporan Hasil Audit Saat Plan Ini Dieksekusi
Ketika Anda menugaskan agent untuk menjalankan rencana audit ini, agent wajib menghasilkan laporan ringkas terstruktur berisi:
1. **Ringkasan Eksekutif & Verdict (`GO` / `NO-GO`)** untuk Gerbang Audit terkait.
2. **Tabel Status Pemeriksaan (`PASS` / `FAIL` / `SKIPPED`)** untuk seluruh butir `S1–S7` dan `A1–A7` (serta `B1–B6` untuk Tahap B) beserta bukti perintah/test yang dijalankan.
3. **Daftar Temuan Detail** (`ID`, `Severity`, `File:Line`, `Akar Masalah`, `Rekomendasi Fix Presisi`).
4. **Rencana Aksi Remediasi Berurutan** (jika terdapat temuan `P0`/`P1`).
