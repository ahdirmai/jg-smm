# 04 — Rencana Implementasi TypeScript Worker (`boxd` RPC), OpenAPI, & Web

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
**Target Direktori:** `apps/worker/`, `openapi/`, `packages/shared/`, `apps/web/`

---

## 1. Refactor Stateless Worker untuk `boxd` (`apps/worker/src/`)

Di lingkungan `boxd`, setiap `smm-worker-01..10` adalah microVM Ubuntu 24.04 mandiri. Worker diubah dari *Redis Poller* menjadi **Authenticated HTTP RPC Executor** yang mendengarkan di `0.0.0.0:8090` (di belakang `nginx:8000` lokal VM), sehingga VM dapat tidur (`standby`/`hibernated`) saat tidak ada tugas dan bangun otomatis saat menerima HTTPS Push dari `smm-hub`.

### 1.1 Server RPC Internal: `src/transport/rpc-server.ts` (Baru)
Menggunakan `node:http` bawaan Node 24 (tanpa dependency berat tambahan), bind ke `0.0.0.0:8090`:

| Endpoint di Worker (`:8090`) | Autentikasi | Fungsi |
| :--- | :--- | :--- |
| `GET /healthz` | Publik lokal (`nginx`) | Healthcheck cepat + endpoint keepalive saat sesi CAPTCHA diparkir. |
| `GET /internal/verify-ticket` | Subrequest `nginx auth_request` | Memvalidasi query `?ticket=<exp>.<vm>.<hmac>` menggunakan `INTERNAL_API_SECRET` (`timingSafeEqual`) sebelum `nginx` mengizinkan koneksi WebSocket noVNC. |
| `POST /internal/execute-action` | `X-Internal-Secret` | Menerima 1 job action beserta `runtime` (`storageState`, `geolocation`, `timezone`, `proxy`, `userAgent`). Menolak `409 Busy` jika worker sedang memegang job/login lain; merespons `202 Accepted` lalu mengeksekusi Playwright dan mengirim hasil ke `/internal/action-callback`. |
| `POST /internal/start-login` | `X-Internal-Secret` | Membuka context login headful di layar Xvfb `:99` dengan `credentials` + `storageState` lama + `geolocation` + `timezone` + `proxy` akun. |
| `POST /internal/auth-input` | `X-Internal-Secret` | Mengisi kode 2FA/checkpoint ke context yang sedang diparkir. |
| `POST /internal/auth-clear` | `X-Internal-Secret` | Menutup paksa context login / `manualSession` yang diparkir dan membersihkan layar Xvfb. |
| `POST /internal/browser-open` | `X-Internal-Secret` | Membuka profil akun di layar Live View noVNC menggunakan `storageState` + `geolocation` + `timezone` + `proxy` akun. |

> [!IMPORTANT]
> **Keamanan Log Ketat:** Seluruh handler di `rpc-server.ts` hanya boleh mencatat `{ jobId, accountId, platform, action }` ke log — field `runtime.storageState`, `runtime.credentials`, dan `runtime.proxy` **dilarang keras** masuk ke log maupun pesan error.

### 1.2 Isolasi Penuh `BrowserContext` (Proxy + GPS + Timezone Indonesia): [`src/core/browser.ts`](../../apps/worker/src/core/browser.ts)
1. **Launch Browser Sekali (Warm & Clean):**  
   Chromium diluncurkan *headful* pada `DISPLAY=:99` tanpa global proxy dan dengan argumen anti-deteksi yang konsisten (`--disable-blink-features=AutomationControlled`, `--start-maximized`).
2. **Perbarui `newAccountContext`:**  
   ```ts
   export interface AccountContextInit {
     platform: Platform;
     storageState?: unknown;
     geolocation?: Geolocation | null;
     timezoneId?: string; // "Asia/Jakarta" | "Asia/Makassar"
     proxy?: { server: string; username?: string; password?: string } | null;
     userAgent?: string;
   }

   export async function newAccountContext(
     browser: Browser,
     init: AccountContextInit,
   ): Promise<BrowserContext> {
     const ctx = await browser.newContext({
       locale: 'id-ID',
       timezoneId: init.timezoneId || 'Asia/Jakarta',
       viewport: FIXED_VIEWPORT,
       userAgent: init.userAgent ?? agentFor(init.platform),
       ...(init.storageState ? { storageState: init.storageState as never } : {}),
       ...(init.proxy
         ? {
             proxy: {
               server: init.proxy.server,
               ...(init.proxy.username ? { username: init.proxy.username } : {}),
               ...(init.proxy.password ? { password: init.proxy.password } : {}),
             },
           }
         : {}),
     });
     await pinGeolocation(ctx, init.geolocation);
     return ctx;
   }
   ```
   Dengan `timezoneId` diset per context, meskipun microVM berjalan di datacenter `fra` (Frankfurt), setiap halaman Instagram yang dibuka melihat jam, `Date()`, `Intl.DateTimeFormat`, dan koordinat GPS yang 100% sinkron di kota akun tersebut di Indonesia!

### 1.3 Session Write-Back & Bukti Screenshot via HTTPS Callback: [`src/core/controller.ts`](../../apps/worker/src/core/controller.ts)
1. Setelah `dispatch(adapter, ctx, job)` selesai:
   - Jika `result.ok === true`, ambil cookie terbaru: `const updatedSession = await ctx.storageState().catch(() => undefined)`.
   - Baca file bukti screenshot `.jpg` yang diambil adapter menjadi `screenshotJpegB64`, kirim sebagai `screenshot_jpeg_b64` di dalam payload `/internal/action-callback` ke `smm-hub`, lalu hapus file `.jpg` sementara dari disk lokal VM.
   - **Keuntungan Besar di `boxd`:** `smm-hub` yang menyimpan `.jpg` tersebut ke MinIO lokalnya! Port MinIO (`9000`) di `smm-hub` **tidak perlu diekspos ke internet publik sama sekali**, dan disk worker VM selalu bersih 0 file sampah!
2. Pada blok `finally`:
   - `await ctx?.close().catch(() => undefined)` langsung menutup context dan memutus koneksi proxy akun tersebut.
   - Tidak perlu lagi `sleep(jitter)` 30–90 detik di dalam worker (yang hanya menahan VM tetap menyala di `boxd`), karena penjadwalan jeda antar-aksi sudah dikendalikan di `ActionScheduler` (`smm-hub`). Worker bisa langsung selesai dalam `<1 detik` seusai aksi agar idle timer `boxd` bisa segera menidurkan VM bila tidak ada job berikutnya!

### 1.4 Penghapusan State Disk Lokal (`session.ts` & `heartbeat.ts`)
- Tidak ada lagi penulisan `/data/sessions/session-<platform>.json`.
- Loop `heartbeat` periodik 30 detik **dimatikan** pada mode `boxd` (agar tidak menggagalkan `auto-suspend` / `auto-hibernate`); sebagai gantinya, status kesehatan & latensi worker diperbarui otomatis oleh `smm-hub` setiap kali dispatch RPC / callback berlangsung, atau saat operator membuka halaman `/workers` (*on-demand health check*).

---

## 2. Perubahan Kontrak OpenAPI (`openapi/openapi.yaml`)

1. **`Account` DTO:**
   - Tambahkan `location` (`string`, nullable), `latitude` (`number`, nullable), `longitude` (`number`, nullable), dan `timezone` (`string`).
2. **`CreateAccountRequest` & `ImportAccountItem`:**
   - Tambahkan `location` (`string`, opsional — nama kota dari daftar referensi `Cities`; bila kosong dipilih otomatis secara merata).
3. **Endpoint Baru Live View Ber-Tiket:**
   - `POST /api/accounts/{id}/live-view` -> meminjam worker (jika belum ada lease aktif), membuka profil/sesi di Xvfb worker, dan mengembalikan `{ "liveViewUrl": "https://smm-worker-XX.boxd.sh/novnc/vnc.html?...&ticket=..." }`.
4. **`ActionCallback` (Internal):**
   - Tambahkan `updated_session` (objek JSON `storageState`) dan `screenshot_jpeg_b64` (string opsional base64 bukti `.jpg`).

---

## 3. Penyesuaian Web Dashboard (`apps/web/`) untuk `boxd`

1. **Wajib Bind `0.0.0.0` (Aturan Produksi `boxd` §4.2):**
   - Pastikan script start produksi / `ecosystem.config.cjs` menjalankan Next.js standalone dengan `HOSTNAME=0.0.0.0 PORT=3000` (jika bind ke `127.0.0.1` atau hostname container, proxy `nginx` / edge `boxd` akan mengembalikan `502`).
2. **Komponen [`live-view.tsx`](../../apps/web/components/live-view.tsx) & [`account-detail-panel.tsx`](../../apps/web/components/account-detail-panel.tsx):**
   - Mendukung URL `https://smm-worker-XX.boxd.sh/novnc/...` lengkap dengan query `ticket` dari endpoint `/api/accounts/{id}/live-view` (menggunakan `wss://` otomatis karena halaman dashboard disajikan lewat HTTPS, sehingga browser tidak memblokir *Mixed Content* `ws://` di dalam halaman `https://`!).
3. **Form Tambah/Import Akun & Tabel Akun:**
   - Pilihan **Kota Lokasi** langsung pada form tambah akun & bulk import; badge lokasi ditampilkan langsung dari `account.location`.
4. **Halaman `/workers`:**
   - Menampilkan panel kesepuluh microVM `smm-worker-01..10`, status slot saat ini (`IDLE / STANDBY-READY`, `EXECUTING JOB`, atau `LOGIN LEASED`), serta tombol Live View ber-tiket untuk inspeksi admin.
