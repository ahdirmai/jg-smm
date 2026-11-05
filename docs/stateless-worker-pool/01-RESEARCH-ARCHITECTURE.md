# 01 — Riset Arsitektur: 10 Worker Utama di `boxd` & Dynamic Runtime Injection

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
**Tanggal:** 5 November 2026

---

## 1. Mengapa Ide "Push Kredensial & Lokasi Saat Running ke 10 Worker" Sangat Ideal untuk `boxd`?

Ketika menggabungkan kondisi aplikasi saat ini (**486 akun live**: 485 Instagram, 1 Threads) dengan karakteristik platform **`boxd` microVM**, arsitektur lama (*1 container per akun + Redis `BLPOP`*) memiliki ketidakcocokan mendasar, sedangkan arsitektur **10 Stateless Worker VMs + HTTPS Push-on-Run** justru memanfaatkan kekuatan terbaik `boxd`:

| Aspek | Arsitektur Lama (Docker per Akun + Redis `BLPOP`) | Arsitektur Baru di `boxd` (10 MicroVM Pool + HTTPS Push) |
| :--- | :--- | :--- |
| **Jumlah Mesin / Container** | 485 container (mentok batas 200 port noVNC & makan puluhan GB RAM) | **1 VM Control Plane (`smm-hub`) + 10 VM Worker (`smm-worker-01..10`)** dari 1 Golden Snapshot |
| **Mekanisme Bangun & Tidur (`boxd`)** | Loop `BLPOP` & heartbeat 30 dtk menahan jaringan tetap aktif -> VM **tidak pernah tidur** (tagihan jalan 24 jam) atau jika tidur **tidak bisa `BLPOP`** | **HTTPS Push membangunkan VM otomatis** (`<1 ms` dari `standby`, `±85 ms` dari `hibernated`); selesai kerja otomatis tidur (`$0 CPU & RAM`) |
| **Koneksi Antar-Mesin** | Butuh ekspos port TCP mentah Redis lintas internet | **100% HTTPS TLS 1.3** lewat edge `*.boxd.sh` (`X-Internal-Secret`), **0 raw TCP port** terbuka |
| **Penyimpanan Kredensial & Session** | Tersebar di ratusan volume `/data/sessions` + Redis | **100% Terpusat di PostgreSQL `smm-hub`** (AES-256-GCM); worker VM *stateless* (aman di-destroy/re-fork kapan saja) |
| **Lokasi GPS & Timezone** | Kaku di worker; timezone mengikuti host (`fra` = UTC) | **Melekat di Akun** (`Jakarta` -> `Asia/Jakarta`, `Makassar` -> `Asia/Makassar`) dan di-inject ke `BrowserContext` saat jalan |

---

## 2. Diagram Arsitektur Terintegrasi di `boxd` (`fra`)

```mermaid
flowchart LR
    subgraph HUB["boxd VM: smm-hub (2vCPU/8G, Always-On)"]
        PG[("PostgreSQL Vault<br/>486+ Akun, Session, Lokasi, Proxy")]
        RD[("Redis Lokal<br/>Cooldown, RateLimit, Lock")]
        MN[("MinIO Lokal<br/>Screenshots & Raw Scrape")]
        API["Go API (:8080)<br/>Scheduler + Dispatcher"]
        WEB["Next.js (:3000)<br/>HOSTNAME=0.0.0.0"]
        NGX_H["nginx (:8000)<br/>Domain Dispatch"]

        NGX_H --> WEB & API
        API <--> PG & RD & MN
    end

    subgraph FLEET["10x boxd MicroVMs: smm-worker-01..10 (2vCPU/8G, Auto-Suspend/Hibernate)"]
        W1["smm-worker-01<br/>nginx:8000 -> RPC:8090 + noVNC:6080"]
        W2["smm-worker-02<br/>nginx:8000 -> RPC:8090 + noVNC:6080"]
        W10["smm-worker-10<br/>nginx:8000 -> RPC:8090 + noVNC:6080"]
    end

    OP["Browser Operator"] -->|"HTTPS Dashboard"| NGX_H
    OP -.->|"WSS noVNC + Signed Ticket<br/>(Saat Login/CAPTCHA)"| W1
    API ==>|"HTTPS POST /internal/execute-action<br/>(Auto-Wake ±1-85ms + Inject Session/Geo/Proxy)"| W1 & W2 & W10
```

---

## 3. Alur Eksekusi Runtime (*Push-on-Run*)

```mermaid
sequenceDiagram
    autonumber
    participant DB as PostgreSQL (smm-hub)
    participant API as Go API Scheduler (smm-hub)
    participant EDGE as boxd Edge Proxy (TLS)
    participant W as smm-worker-XX (Standby/Hibernated)
    participant IG as Instagram / Threads

    API->>DB: Klaim ActionJob yang due & cek Gate Cooldown/RateLimit/AccountLock
    API->>DB: Baca & Decrypt storage_state + proxy + {location, lat, lng, timezone, userAgent}
    API->>EDGE: POST https://rpc.smm-worker-03.boxd.sh/internal/execute-action (X-Internal-Secret)
    Note over EDGE,W: Jika VM Standby (<1ms) atau Hibernated (±85ms),<br/>boxd membangunkan VM otomatis sebelum meneruskan paket!
    EDGE->>W: Teruskan ke nginx:8000 -> worker RPC :8090 (Payload di RAM)
    W-->>API: HTTP 202 Accepted (Slot worker terkunci)
    W->>W: browser.newContext({ storageState, geolocation, timezoneId, proxy, userAgent })
    W->>IG: Navigasi & Jalankan Aksi (Like / Comment / Reply) + Ambil Screenshot (.jpg)
    IG-->>W: Sukses + Set-Cookie terbaru
    W->>W: updatedSession = await ctx.storageState()
    W->>W: await ctx.close() (Bersihkan seluruh jejak memori, cookie, & file sementara)
    W->>API: POST /internal/action-callback { status: "SUCCESS", screenshot_jpeg_b64, updated_session }
    API->>DB: Encrypt & Upsert account_session + Simpan bukti ke MinIO + Tulis action_log + Release Lock/Slot
    Note over W: Tidak ada request masuk selama 5 menit -> VM kembali Standby ($0 CPU/RAM)
```

---

## 4. Analisis Realita Jaringan `boxd` & Strategi Anti-Deteksi Meta

Berdasarkan pengukuran faktual dari tim infra pada lingkungan `boxd` (`boxd-recap-smm-migration.md` §6):
1. Setiap microVM `smm-worker-01..10` memang mendapatkan **IP keluar (egress) publik yang berbeda-beda** (mis. `67.213.121.191`, `109.94.96.51`).
2. **NAMUN** seluruh IP tersebut berada di bawah satu ASN hosting datacenter yang sama: **`AS396356 — Latitude.sh`**, dan reputasi subnet dipakai bersama antar-tenant `boxd`.

Oleh karena itu, desain arsitektur kita menyediakan **2 lapisan perlindungan simultan**:

### 4.1 Lapisan Egress IP: Per-Account Sticky Residential Proxy + Sticky Worker Affinity
- **Jalur Utama (486 Akun Aktif):** Setiap akun di database memiliki `proxy_group_id` menuju provider proxy residensial/mobile Indonesia menggunakan skema **`http://` atau `https://`** dengan sticky session ID per akun (`http://user-country-id-session-{account_id}:pass@gate.provider:port`). Saat `browser.newContext({ proxy })` dipanggil, seluruh trafik ke Instagram keluar dari IP residensial Indonesia — IP `Latitude.sh` milik `boxd` sama sekali tidak terlihat oleh Meta!
- **Jalur Fallback (Tanpa Proxy Pihak Ketiga / Direct `boxd` IP):** Jika untuk sebagian akun uji coba Anda belum memasang proxy pihak ketiga, dispatcher di Go API menggunakan **Consistent Hash Affinity** (`hash(accountId) % 10`) sebagai pilihan pertama worker. Artinya, Akun X akan selalu diprioritaskan jalan di `smm-worker-04` (IP `Latitude.sh` yang sama, tidak lompat-lompat antar ke-10 IP), dan hanya *failover* ke worker lain bila diperlukan. *Catatan sadar-risiko:* tanpa proxy residensial, hindari menjalankan ratusan akun sekaligus di atas 10 IP datacenter `AS396356` untuk mencegah *account clustering*.

### 4.2 Penyelarasan Geolocation + Timezone + Locale (Kritis untuk Server Region `fra`)
Karena microVM `boxd` berjalan di **Frankfurt (`fra`)**, jam default OS/browser bila tidak diatur adalah zona Eropa/UTC. Jika GPS melaporkan `Surabaya` sementara JavaScript `Intl.DateTimeFormat().resolvedOptions().timeZone` melaporkan `UTC` atau `Europe/Berlin`, sistem anti-bot Meta langsung menandainya sebagai *spoofed browser*.
- **Solusi di Plan Ini:** Setiap kota di `domain.Cities` dipetakan ke zona waktu IANA yang presisi:
  - `Jakarta`, `Bandung`, `Surabaya`, `Medan`, `Semarang`, `Yogyakarta`, `Malang`, `Palembang`, `Pekanbaru` -> **`Asia/Jakarta`**
  - `Makassar`, `Denpasar`, `Balikpapan` -> **`Asia/Makassar`**
- Worker menyetel `timezoneId` tersebut langsung pada `browser.newContext({ timezoneId, geolocation, locale })`.

### 4.3 Pengamanan Layar Live View (noVNC) di Domain Publik `boxd`
Di lokal Mac, port `24100–24109` hanya bisa diakses dari `127.0.0.1`. Di `boxd`, alamat `https://smm-worker-01.boxd.sh` dapat dijangkau dari internet publik!
- **Solusi Keamanan Wajib:** Port `x11vnc`/`websockify` **tidak pernah dibuka mentah (`--raw`)**. Trafik noVNC wajib melewati `nginx:8000` di dalam worker VM dengan verifikasi **Short-Lived HMAC Ticket** (`?ticket=<exp>.<workerId>.<hmac_sha256>`) menggunakan `INTERNAL_API_SECRET` dan TTL 5 menit. Tanpa tiket valid dari Go API, akses ke `/novnc` langsung ditolak `403 Forbidden`.

---

## 5. Penanganan Jebakan Idle Timer `boxd` Saat Job & Manual CAPTCHA

Dari catatan infra (`boxd-recap` §1, §6, §7.6):
- Dengan `idle.ignore-outbound = on`, lalu-lintas keluar dari worker ke Instagram **tidak** me-reset timer tidur `boxd` — hanya paket **masuk** ke worker yang me-reset timer.
- Bagaimana mencegah worker tertidur di tengah job atau saat operator sedang menyelesaikan CAPTCHA 5 menit?
  1. **Untuk Eksekusi Action (~20–45 detik):** Set `auto-suspend.timeout = 300` (5 menit) dan `auto-hibernate.timeout = 3600` (1 jam). Koneksi HTTP POST masuk dari `smm-hub` sudah me-reset timer penuh 5 menit — lebih dari cukup untuk menuntaskan 1 job Playwright.
  2. **Untuk Sesi Login / Manual CAPTCHA (`NEEDS_INPUT` hingga 10 menit):** Selama operator membuka Live View di dashboard, aliran frame mouse/ping WebSocket masuk (`wss://smm-worker-XX.boxd.sh/novnc/websockify`) terus me-reset timer secara alami; ditambah Go API mengirim *keepalive ping* ringan setiap `120 detik` ke worker yang sedang memegang *Active Login Lease* hingga login selesai!
