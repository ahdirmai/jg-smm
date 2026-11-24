# 03 — Rencana Perubahan Backend Go API, HTTPS Push Dispatcher, & Lease Manager

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
**Target Direktori:** `apps/api/internal/` & `apps/api/cmd/server/`

---

## 1. Kepatuhan Arsitektur Layer (`arch_test.go`)

Seluruh penambahan kode Go tetap mematuhi [`apps/api/internal/arch/arch_test.go`](../../apps/api/internal/arch/arch_test.go):
- `domain` -> murni tipe bisnis & fungsi kalkulasi kota/timezone (`TimezoneForCity`).
- `port` -> interface abstrak (`WorkerRPCClient`, `AccountExecutionLock`, `WorkerSlotPool`).
- `service` -> logika orkestrasi tanpa mengimpor `http`, `redis`, `repository`, atau `config`.
- `adapter` & `repository` -> implementasi HTTP client TLS ke worker `boxd`, Redis lock, dan PostgreSQL `sqlc`.

---

## 2. Mengapa Beralih ke `WorkerRPCClient` (HTTPS Push) untuk `boxd`?

Pada `boxd` microVM, mekanisme Redis `BLPOP` (pull) menyebabkan dilema:
- Jika worker terus terhubung ke Redis + mengirim heartbeat tiap 30 detik, trafik keluar menahan idle timer sehingga ke-10 VM `1vCPU/4G` menyala 24 jam penuh tanpa tidur.
- Jika `idle.ignore-outbound=on` diaktifkan agar VM berhibernasi saat sepi, proses di dalam VM ikut membeku sehingga tidak bisa menarik job dari Redis.

Dengan **`WorkerRPCClient` (HTTPS Push)** dari `smm-hub` ke `https://rpc.smm-worker-XX.boxd.sh`:
1. Request HTTPS masuk **membangunkan microVM `boxd` secara otomatis** (`<1 ms` dari `standby`, `±85 ms` dari `hibernated`).
2. Kredensial/session cookie, koordinat GPS, timezone, dan proxy **dikirim langsung saat running** melalui koneksi HTTPS TLS 1.3 ber-header `X-Internal-Secret` — **0 byte kredensial mampir ke Redis AOF**, dan port Redis sama sekali tidak perlu dibuka keluar `smm-hub`!
3. Begitu eksekusi selesai dan tidak ada job baru selama 5 menit, microVM worker otomatis tidur kembali (`$0 CPU & RAM`).

---

## 3. Konfigurasi Baru (`internal/config/config.go`)

| Variabel Env | Contoh Nilai Produksi (`boxd`) | Fungsi |
| :--- | :--- | :--- |
| `WORKER_POOL_MODE` | `stateless` | Mengaktifkan pool 10 worker stateless + HTTPS Push (`legacy` untuk rollback ke mode lama). |
| `WORKER_POOL_ENDPOINTS` | `smm-worker-01=https://rpc.smm-worker-01.boxd.sh,...` | Daftar ke-10 worker utama beserta endpoint RPC & URL noVNC publiknya untuk di-seed otomatis saat API boot. |
| `INTERNAL_API_SECRET` | *(32+ byte random di `.boxenv`)* | **Wajib terisi** di `boxd`: mengautentikasi dua arah `smm-hub <-> smm-worker-XX` serta menandatangani tiket HMAC noVNC. |
| `NOVNC_TICKET_TTL_SECONDS` | `300` | Masa berlaku tiket akses layar Live View operator (default 5 menit). |
| `ACCOUNT_LOCK_TTL_SECONDS` | `300` | Batas waktu kunci eksklusif Redis per akun selama aksi berjalan. |
| `WORKER_LEASE_TTL_SECONDS` | `660` | Batas waktu pinjaman worker untuk sesi interaktif login/CAPTCHA (11 menit). |

---

## 4. Desain Domain & Port Baru

### 4.1 `internal/domain/location.go` & `account.go`
1. Tambahkan helper **`TimezoneForCity(cityName string) string`** pada `domain/location.go`:
   - Kota WITA (`Makassar`, `Denpasar`, `Balikpapan`) -> `"Asia/Makassar"`.
   - Kota WIB lainnya (`Jakarta`, `Bandung`, `Surabaya`, `Medan`, `Semarang`, `Yogyakarta`, `Malang`, `Palembang`, `Pekanbaru`) -> `"Asia/Jakarta"`.
2. Perluas `domain.Account` dengan `Location *string`, `Latitude *float64`, `Longitude *float64`, `Timezone string`, dan `UserAgent *string`.
3. Perluas `domain.Worker` dengan `EndpointURL *string`.

### 4.2 `internal/port/worker_rpc.go` (Port Eksekusi Push ke Worker)
```go
type AccountRuntimeSpec struct {
    AccountID    string          `json:"accountId"`
    Platform     string          `json:"platform"`
    StorageState json.RawMessage `json:"storageState,omitempty"`
    Credentials  *LoginCreds     `json:"credentials,omitempty"`
    Geolocation  *GeoPoint       `json:"geolocation,omitempty"`
    Timezone     string          `json:"timezone"`
    UserAgent    string          `json:"userAgent,omitempty"`
    Proxy        *ProxySpec      `json:"proxy,omitempty"`
}

type ExecuteActionRequest struct {
    JobID           string             `json:"jobId"`
    Action          string             `json:"action"`
    TargetURL       string             `json:"targetUrl"`
    Text            string             `json:"text,omitempty"`
    TemplateID      string             `json:"templateId,omitempty"`
    ExpectedMention string             `json:"expectedMention,omitempty"`
    Attempt         int                `json:"attempt"`
    Runtime         AccountRuntimeSpec `json:"runtime"`
}

type WorkerRPCClient interface {
    ExecuteAction(ctx context.Context, endpointURL string, req ExecuteActionRequest) error
    StartLogin(ctx context.Context, endpointURL string, req AccountRuntimeSpec) error
    SubmitAuthInput(ctx context.Context, endpointURL, accountID, platform, code string) error
    ClearAuth(ctx context.Context, endpointURL, accountID string) error
    OpenBrowser(ctx context.Context, endpointURL string, req AccountRuntimeSpec, targetURL string) error
    PingKeepalive(ctx context.Context, endpointURL string) error
}
```
> **Catatan Asinkron vs Sinkron:** `ExecuteAction` pada worker dapat langsung merespons `202 Accepted` setelah job diterima ke dalam slot tunggal worker (agar goroutine scheduler tidak memblokir koneksi panjang jika tidak perlu), **namun** koneksi tetap diakhiri melalui `POST /internal/action-callback` ke `smm-hub` lengkap dengan `updatedSession` dan upload screenshot ke S3/MinIO (atau base64 JPG `< 150 KiB` di callback sehingga MinIO pun tidak perlu diekspos ke publik!).

### 4.3 `internal/port/pool.go` (Koordinasi Slot & Kunci di Redis Lokal `smm-hub`)
1. **`AccountExecutionLock` (`smm:lock:account:{accountId}`):**  
   Menjamin **1 akun tidak pernah aktif di 2 worker bersamaan**.
2. **`WorkerSlotCoordinator` (`smm:slot:worker:{workerId}`):**  
   Melacak mana dari ke-10 worker (`smm-worker-01..10`) yang sedang bebas (`IDLE`), sedang menjalankan 1 job action (`BUSY_ACTION`), atau sedang dipinjam sesi interaktif login/CAPTCHA operator (`LEASED_AUTH:<accountId>`).
   - **Algoritma Pemilihan Worker (*Affinity-First*):** Hitung indeks preferensi `idx = fnv32(accountID) % len(workers)`. Jika `workers[idx]` bebas, pilih worker tersebut (memaksimalkan konsistensi IP keluar bahkan saat mode tanpa proxy!). Jika `workers[idx]` sedang sibuk dan akun memiliki proxy residensial sendiri, putar ke worker bebas berikutnya (`(idx + i) % len(workers)`). Jika seluruh 10 worker sedang sibuk, kembalikan `ErrNoIdleWorker` dan `ActionScheduler` menjadwalkan ulang job 5 detik kemudian.

---

## 5. Adapter Baru & Perbaikan Penting (`internal/adapter/` & `internal/http/`)

### 5.1 `internal/adapter/workerrpc/client.go`
- HTTP client dengan timeout wake-tolerant (`DialTimeout = 10s` untuk mengakomodasi cold-wake `±85 ms` dari hibernasi `boxd`, `RequestTimeout = 30s` untuk dispatch), menyertakan header `X-Internal-Secret` pada setiap panggilan, dan tidak pernah me-log body request.

### 5.2 `internal/adapter/proxyresolver/resolver.go` (`ForAccount`)
- Membuka segel `proxy_group.pool_key` dari `account.ProxyGroupID`.
- Mengganti placeholder `{account_id}` pada username proxy dengan ID pendek akun (sticky residential IP per akun).
- Menolak lebih awal skema `socks5://` ber-password karena Chromium di Playwright `1.48.2` hanya menerima autentikasi proxy context pada `http://` atau `https://`.

### 5.3 Tiket Keamanan noVNC (`internal/service/novnc_ticket.go`)
- Membuat token `ticket = <expiresUnix>.<workerName>.<hex(HMAC_SHA256(secret, expiresUnix + ":" + workerName))>`.
- Endpoint baru `POST /api/accounts/:id/live-view` mengembalikan URL siap pakai:
  `https://smm-worker-03.boxd.sh/novnc/vnc.html?autoconnect=true&resize=scale&path=novnc/websockify?ticket=<ticket>`
- Dengan demikian operator di dashboard tetap menikmati one-click Live View di iframe, sementara pemindai internet publik yang mencoba membuka `smm-worker-03.boxd.sh/novnc` tanpa tiket ditolak `403`.

### 5.4 Perbaikan Wajib pada Service & Handler Eksisting
1. **[`internal.go`](../../apps/api/internal/http/internal.go) — Naikkan `MaxCallbackBytes`:**  
   Ubah batas ukuran body pada grup `/internal` dari `64 KiB` menjadi **`2 MiB`** (cukup untuk menampung `updatedSession` `storageState()` JSON besar + bukti screenshot `.jpg` terkompresi dalam 1 callback HTTPS tanpa harus mengekspos port MinIO keluar `smm-hub`!).
2. **[`job.go`](../../apps/api/internal/service/job.go) — Hapus `stopOTPWorker` pada Mode Pool:**  
   Pada `RecordAuthOutcome` (baris 323–325), jangan pernah memanggil `stopOTPWorker` saat `WORKER_POOL_MODE=stateless`. Sebagai gantinya:
   - Saat hasil login `AUTHENTICATED` atau `FAILED` (termasuk `otp_required:`): lepaskan slot `LEASED_AUTH` worker, set `account.worker_id = nil`, dan panggil `workerRPC.ClearAuth` agar browser worker kembali bersih.
   - Saat hasil login `NEEDS_INPUT` (CAPTCHA / 2FA): pertahankan lease worker dan jalankan goroutine ringan *keepalive* (`PingKeepalive` tiap 120 dtk selama status masih `NEEDS_INPUT`, maks 10 menit) agar VM `boxd` tidak masuk `standby` saat operator sedang mengambil kode OTP.
3. **[`account.go`](../../apps/api/internal/service/account.go) — `AccountsByRegion`:**  
   Ubah `AccountsByRegion` agar membaca seluruh halaman akun (`listAllAccounts`) dan mengelompokkan langsung dari `account.Location` (tanpa bergantung pada `worker.Location`).
4. **Matikan Reconciler Docker Lokal di `smm-hub`:**  
   Dengan `PROVISIONER_MODE=static` dan `WORKER_POOL_MODE=stateless` di `smm-hub`, container `api` **tidak lagi membutuhkan `user: root` maupun mount `/var/run/docker.sock`**! Keamanan Control Plane meningkat drastis.
