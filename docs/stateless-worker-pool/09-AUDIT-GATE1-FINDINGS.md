# 09 — Hasil Audit Gerbang 1 (Tahap A, Lokal) & Rencana Remediasi

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)
**Rencana acuan:** [08-MIGRATION-AUDIT-PLAN.md](./08-MIGRATION-AUDIT-PLAN.md) §2–§3
**Tanggal:** 5 November 2026
**Cakupan:** Gerbang Audit 1 saja (Fase 1–4, lokal). Gerbang Audit 2 (`boxd`) belum dijalankan.

> **Verdict putaran 1: `NO-GO`** — 4 temuan `P0`, 1 `P1`, 2 `P2`.
> **Verdict putaran 2 (setelah remediasi): `GO` untuk Tahap A** — semua temuan ditutup
> dengan regression test; 2 butir tidak dapat dieksekusi di mesin ini (§7).

---

## 1. Metode & Lingkungan Audit

| Aspek | Detail |
| :--- | :--- |
| Clone DB | `.local-runtime/backups/smm-pre-pool.dump` (versi migrasi **23**) di-restore ke `smm_audit_test` |
| Isi clone | **486 akun** (485 Instagram, 1 Threads), **15 `account_session`**, **511 baris `worker`** |
| Migrasi | `smm_audit_test` → `000024 up`, lalu `up→down→up` di `smm_audit_schema_test` |
| Bootstrapper | `PoolBootstrapper.EnsurePoolWorkers(10)` + `BackfillAccounts` dijalankan terhadap clone |
| Secret | Hanya panjang/keberadaan yang diperiksa (`CREDENTIAL_KEY` = 45 karakter b64, tidak dicetak) |

Angka hasil eksekusi (bukti mentah, bukan asumsi):

```
backfilled=17
unlocated=0
workers_total=521 running_with_endpoint=10
account=486  account_session=15  authenticated=15 (semua punya session)
Index Scan using account_refresh_due_idx  (EXPLAIN dengan enable_seqscan=off)
```

---

## 2. Ringkasan Temuan

| ID | Severity | Hotspot | Ringkas |
| :-: | :--- | :-: | :--- |
| F1 | **P0** | `S1` | `WithPool` terpasang hanya saat `ACTION_INTERVAL_SECONDS > 0` → mode pool tidak aktif di default lokal, `stopOTPWorker` tetap jalan |
| F2 | **P0** | `S2` | 521 baris worker (511 legacy `PAUSED`) ikut jadi kandidat slot → afinitas akun tanpa proxy mendarat di worker mati |
| F3 | **P0** | `S3` | `manualContext` worker TS dipakai ulang lintas akun (cookie/GPS/proxy akun A pada live view akun B), dan `isBusy()` mengabaikannya |
| F4 | **P0** | `S1` | Lease + slot Redis tidak dilepas pada login `AUTHENTICATED`/`FAILED` non-OTP, dan `ClearAuth` tidak pernah dipanggil |
| F5 | **P1** | `S5` | Bukti screenshot ditulis ke bucket `MINIO_BUCKET` (mis. `smm-raw`) padahal `minio-init` membuat bucket `screenshots` |
| F6 | **P2** | `S6` | `ExportSession`/`ImportSession` menolak akun idle (`worker_id = NULL`) — di pool itu keadaan normal |
| F7 | **P2** | `S4` | Kontrak `auth_request` nginx tidak dinyatakan di repo; bila dipasang di seluruh `location /novnc/`, `vnc.html` ikut 403 |

**Lulus tanpa temuan:** `S7` (nilai `worker.location` di dump hanya `Bandung`/`Jakarta`/`Surabaya` — tidak ada kota WITA sehingga pemetaan WIB/WITA tidak bisa salah), A1 (486/15 utuh, backfill 0 tersisa, index terpakai), A2 (arch guard + codegen sync), A5 (URL bertiket, bind standalone).

---

## 3. Detail Temuan & Akar Masalah

### F1 — `P0` · `WithPool` bergantung pada scheduler

`apps/api/cmd/server/main.go` memanggil `jobSvc.WithPool(...)` **di dalam** blok
`if cfg.ActionIntervalSeconds > 0 && redisClient != nil`. Di lokal default `ACTION_INTERVAL_SECONDS=0`,
jadi `JobService.poolMode` tetap kosong dan:

- `RecordAuthOutcome` menjalankan `stopOTPWorker` (mem-pause worker bersama), dan
- `storeEvidence`/`persistSession`/`onJobDone` tidak aktif.

**Akar:** konfigurasi pool dan konfigurasi scheduler digabung padahal keduanya independen.

### F2 — `P0` · Kandidat slot memuat worker legacy

`PoolDispatch.Workers` = `workerRepo.List(Limit: 500)` tanpa filter. Clone punya **521** baris
`worker`, 511 di antaranya `PAUSED` tanpa `endpoint_url`. Karena `AffinityIndex` memakai
`fnv32a(accountID) % len(candidates)`, akun tanpa proxy (`allowFallback=false`) punya peluang
sangat besar mendarat di baris mati → `Push` gagal `ErrUnavailable` terus-menerus.

**Akar:** filter "anggota pool" tidak pernah dinyatakan; `List` juga dibatasi 500 sehingga
membuang baris nyata.

### F3 — `P0` · `manualContext` bocor antar akun (worker TS)

`apps/worker/src/index.ts`:

```ts
openBrowser: async (runtime, targetUrl) => {
  manualContext = await openLiveView(runtime, targetUrl, async () => ({}), manualContext);
  manualAccountId = runtime.accountId;
},
```

`openLiveView` hanya membuat context **bila `existing` kosong**. Untuk akun B, context akun A
dipakai ulang: cookie, proxy, GPS, dan timezone akun A terlihat operator sebagai akun B. Selain
itu `isBusy()` = `busy || authAccountId !== undefined` — tidak menghitung live view aktif, sehingga
satu job bisa dibuka di context yang sama.

### F4 — `P0` · Lease tidak dilepas saat login selesai

`RecordAuthOutcome` hanya menangani `AuthFailed + otp_required:` (melepas lease). Untuk
`AuthAuthenticated`, `AuthFailed` non-OTP, dan `AuthNeedsInput` tidak ada:

- `accounts.ReleaseLease`, `Slots.ReleaseWorkerSlot`, `workerRPC.ClearAuth` (Authenticated/Failed),
- keepalive `PingKeepalive` (NeedsInput).

Akibatnya `account.worker_id` dan key Redis `smm:slot:worker:<id>` menggantung sampai TTL
(`WORKER_LEASE_TTL_SECONDS=660`), dan browser worker tetap memegang sesi akun yang sudah selesai.

### F5 — `P1` · Bukti screenshot masuk bucket yang salah

`JobService.storeEvidence` memakai `port.RawStorage` (= `MINIO_BUCKET`, default `smm-raw`) dengan
key `screenshots/<name>`, sementara `compose.yaml` `minio-init` membuat bucket terpisah
`screenshots`. Tidak ada pembaca yang memakai key itu, sehingga bukti tidak bisa diambil dari UI.

### F6 — `P2` · Export/Import session menolak akun idle

Keduanya mensyaratkan `control` (Redis) dan — pada export — `a.WorkerID != nil`. Di pool, akun idle
memang `worker_id = NULL` dan sesinya ada di `account_session`, jadi endpoint itu selalu `409`.

### F7 — `P2` · Kontrak `auth_request` noVNC belum tertulis

Tiket berada di dalam parameter `path=` (benar untuk WebSocket), tetapi tidak ada berkas
`nginx-worker.conf` di repo yang menyatakan bahwa `auth_request` hanya dipasang pada
`location = /novnc/websockify`. Fase 5 berisiko memasangnya di seluruh `/novnc/`.

---

## 4. Rencana Remediasi (urut eksekusi)

| # | Temuan | Perbaikan | Gerbang bukti |
| :-: | :--- | :--- | :--- |
| R1 | F1 | Pindahkan `jobSvc.WithPool(...)` ke blok inisialisasi pool (di luar blok scheduler) | Test: `RecordAuthOutcome` dengan `poolMode=stateless` **tidak** memanggil `stopOTPWorker` meski scheduler mati |
| R2 | F2 | Tambah filter kandidat: `desired_state = RUNNING`, `endpoint_url IS NOT NULL`, `source = MANUAL`; buang batas 500 untuk kandidat pool | Test repo di clone: kandidat = tepat 10 setelah seed; sebelum fix ≥ 500 |
| R3 | F3 | Tutup `manualContext` bila `manualAccountId !== runtime.accountId`; sertakan live view dalam `isBusy()`; tutup manual context pada `start-login` akun lain | Test worker: dua `browser-open` akun berbeda → dua context, context pertama `close()` dipanggil |
| R4 | F4 | Lengkapi `RecordAuthOutcome` (Authenticated/Failed/NeedsInput) + keepalive; relepas lease/slot via `PoolDispatch` | Test: semua cabang melepas lease & slot tepat sekali; keepalive berhenti saat status berubah |
| R5 | F5 | Unggah bukti ke bucket `screenshots` (storage terpisah), atau selaraskan bucket; simpan key yang benar pada `action_log.screenshot_url` | Test: `Put` dipanggil dengan key bucket `screenshots`; HTTP test callback memverifikasi key tersimpan |
| R6 | F6 | `ExportSession`/`ImportSession` mode pool: baca/tulis langsung `BrowserSessionStore` (tanpa control channel) | Test: akun idle → export sukses mengembalikan JSON, import menyimpan state |
| R7 | F7 | Tambahkan `infra/boxd/nginx-worker.conf` dengan `auth_request` **hanya** pada `location = /novnc/websockify` | Ditinjau di Fase 5 (Gerbang 2); di sini cukup kontrak tertulis + komentar di `novnc_ticket.go` |

**Aturan:** setiap perbaikan menyertakan regression test yang gagal sebelum fix.

---

## 5. Kriteria Re-Audit

Putaran audit diulang sampai **seluruh** butir berikut PASS:

1. `S1–S7` + `A1–A7` PASS dengan bukti perintah.
2. `make ci` hijau, `go test -race ./internal/...` hijau.
3. Clone DB: 486 akun, 0 unlocated, kandidat pool = 10, tidak ada lease menggantung setelah suite.
4. Tidak ada payload rahasia di grep log (A6).

---

## 6. Status Akhir

| Temuan | Status | Bukti |
| :--- | :--- | :--- |
| F1 | **FIXED** | `jobSvc.WithPool` dipindah keluar blok scheduler; `TestPoolOTPFailureNeverPausesWorkerWithoutScheduler` |
| F2 | **FIXED** | `ListPoolWorkers` (RUNNING + MANUAL + `endpoint_url NOT NULL`, tanpa paging); di clone riil **521 → 10 kandidat** |
| F3 | **FIXED** | `closeManualContext()` + `isBusy()` mencakup live view; `live view closes the previous account context…` (7/7 test worker) |
| F4 | **FIXED** | `ReleaseSession` + `KeepLoginAlive`; 4 test (`PoolAuth*`) + E2E live: lease `NULL`, worker tetap `RUNNING`/`200` |
| F5 | **FIXED** | Bucket `MINIO_SCREENSHOT_BUCKET=screenshots` + key tanpa prefix; `TestRecordAttemptStoresEvidenceInScreenshotsStore` |
| F6 | **FIXED** | Jalur store langsung (tanpa control channel) + `RefreshState` (bukan `Save`) agar expiry tidak hilang; E2E live: import `204`, export mengembalikan JSON identik |
| F7 | **FIXED** | `infra/boxd/nginx-worker.conf` membatasi `auth_request` pada `location = /novnc/websockify`; komentar kontrak di `novnc_ticket.go` |

### Re-audit gate (putaran 2)

| Cek | Hasil |
| :--- | :--- |
| `go vet ./...` + `go test ./...` | PASS |
| `go test -race ./internal/service/ ./internal/repository/` | PASS (race pada test probe ditemukan & diperbaiki — probe kini memakai mutex) |
| Worker `tsc --noEmit` + `eslint src` + 97 test | PASS |
| Clone 486 akun: migrasi `up`, backfill 17 → 0 unlocated, index `account_refresh_due_idx` terpakai | PASS |
| Kandidat pool di clone (521 baris worker) | **10**, semuanya RUNNING + endpoint |
| E2E: push action → `202` → callback → `SUCCESS`, `account.worker_id = NULL`, Redis `smm:lock:*` / `smm:slot:*` kosong | PASS |
| E2E: OTP failure → lease lepas, worker tetap hidup (`200`) | PASS |
| E2E: session import/export akun idle | PASS |

---

## 7. Yang Tidak Dapat Diverifikasi di Mesin Ini (bukan PASS, bukan FAIL)

1. **Action Playwright nyata + capture JPEG.** Chromium Playwright build v1140 gagal diunduh
   (`playwright.azureedge.net` timeout / `ENOTFOUND`). Yang terbukti hanyalah jalur transport
   (push/callback/session write-back) dengan `ACTION_DRY_RUN=true`.
2. **`/internal/browser-open` & live view ujung-ke-ujung.** Keduanya mencapai worker dan ditolak
   hanya oleh binary Chromium yang tidak ada — bukan oleh bug orkestrasi. Tiket sudah diverifikasi
   terpisah (`204` valid, `403` worker-lain/expired).

Keduanya wajib dieksekusi ulang di Gerbang Audit 2 (worker `boxd` punya Chromium bawaan snapshot).

---

## 8. Observasi Non-Blocking (tidak menggugurkan gate)

- `WORKER_POOL_ENDPOINTS` tanpa bagian `|novncUrl` membuat live view `409`; pesan error kini
  menyebut variabel yang harus diperbaiki, dan `.env.example` menjelaskannya.
- **`make sqlc-check` memberi false positive selama migrasi belum di-commit**: target itu
  membandingkan hasil generate dengan `HEAD`, jadi regenerasi yang benar dari query yang belum
  di-commit selalu dilaporkan "stale". Dibuktikan in-sync dengan membandingkan direktori
  `sqlcgen` sebelum/sesudah `make sqlc` (identik). Sebaiknya target membandingkan ke working tree.
- `make generate` gagal di mesin ini karena `proxy.golang.org` tidak terjangkau; codegen Go
  diverifikasi dengan binary `oapi-codegen` yang dibangun dari module cache — output identik
  (tidak ada drift).
- `List()` worker masih memakai filter di Go setelah `LIMIT` SQL. Tidak lagi dipakai untuk kandidat
  pool (lihat F2), tetapi filter `status/desired_state` di endpoint dashboard tetap bisa terpotong
  pada fleet > 500 baris. Diusulkan sebagai tiket terpisah, bukan blocker Tahap A.

---

## 9. Real Testing (stack lokal nyata, 5 Nov 2026)

Dijalankan terhadap DB kerja `smm` (bukan clone) setelah migrasi `000024` + `000025`, API mode
`stateless`, dan **10 container worker sungguhan** (`smm-worker-01..10`, image `smm-worker`).

| Uji | Hasil | Bukti |
| :--- | :--- | :--- |
| Migrasi DB kerja 23 → 25 | PASS | `schema_migrations = 25` |
| Bersihkan 511 worker legacy | PASS | `worker=0` → lalu bootstrapper seed `worker=10` |
| Riwayat tidak ikut terhapus | PASS | `heartbeat=69911`, `provision_log=1646` tetap utuh (FK diubah ke SET NULL di `000025`) |
| 486 akun & 15 sesi utuh | PASS | `accounts=486`, `sessions=15`, `unlocated=0` |
| `/workers` menampilkan 10 slot | PASS | `containers=10`, status `IDLE`, reconciler container dimatikan |
| Push action nyata ke worker | PASS | job `SUCCESS`, `action_log.verified=t`, dieksekusi `smm-worker-10` (`dry-run: skipping action`) |
| Lease dibersihkan setelah job | PASS | `accounts_leased=0`, `smm:lock:*=0`, `smm:slot:*=0` |
| Live view + tiket dengan Chromium nyata | PASS | `200`, tiket **204** di worker pemilik, **403** di worker lain |
| Isolasi context antar akun | PASS | `healthz busy=true` selama live view; `execute-action` → **409** |
| OTP isolation | PASS | `auth_status=FAILED`, `worker_id=NULL`, worker tetap `{"status":"ok","busy":false}` |

**Bug yang tertangkap oleh real testing (tidak terlihat di unit test):** `000025` pertama kali
memakai `ON DELETE SET NULL` pada kolom `NOT NULL`, sehingga `DELETE` worker selalu gagal — kolom
kini di-*relax* lebih dulu (`heartbeat.worker_id`, `provision_log.worker_id`).


---

## 10. Temuan dari Real Testing noVNC & Session (5 Nov 2026)

Semua di bawah ditemukan **hanya** karena menjalankan stack sungguhan; unit test tidak melihatnya.

| ID | Severity | Akar masalah | Perbaikan | Bukti |
| :-: | :--- | :--- | :--- | :--- |
| F8 | **P1** | `NEEDS_INPUT` memblokir login ulang meski `worker_id = NULL` (tidak ada sesi hidup) → 26 akun terdampar di error "submit the code instead" | Guard hanya berlaku bila lease benar-benar dipegang | `TestPoolLoginAllowsRetryWhenParkedButUnleased` |
| F9 | **P1** | `Login` bercabang pada `s.pool != nil`, bukan `poolMode` → guard pool terlewati | Bercabang pada mode + dispatcher | test yang sama |
| F10 | **P1** | Path noVNC salah: builder memakai `novnc/websockify` sementara worker menjalankan `websockify --web=/usr/share/novnc` (klien di `/`, socket di `/websockify`) → 404 | Path root-relatif; kontrak ditulis di komentar | `vnc.html` **200**, WebSocket **101** |
| F11 | **P1** | Port noVNC tidak di-publish ke host (`6080/tcp`) sehingga iframe menunjuk port mati | Publish `127.0.0.1:243NN->6080` per worker + `novnc_service` di `.env` | `curl localhost:24307/vnc.html` = 200 |
| F12 | **P1** | `.env` berisi `WORKER_POOL_ENDPOINTS` tanpa `\|novncUrl`, dan bootstrapper menimpa nilai manual setiap boot | Sumber tunggal di `.env`, bootstrapper melakukan upsert (by design) | 10 baris worker punya `novnc_service` |
| F13 | **P0** | **`CREDENTIAL_KEY` di shell berbeda dari `.env`** → API tidak bisa membuka 15 session asli (`cipher: message authentication failed`) | Container dijalankan tanpa override shell; key dibuktikan benar dengan probe dekripsi | `session/export` **200** mengembalikan cookie asli (`csrftoken`) |
| F14 | **P0** | **`INTERNAL_API_SECRET` shell ≠ `.env`** → worker menolak setiap push (`401`) | API & worker dijalankan dengan secret yang sama | `live-view` **200**, tiket **204/403** |
| F15 | **P2** | Handler live view menelan error 500 tanpa log | `c.Logger().Errorf` sebelum translate | log menampilkan penyebab tepat |
| F16 | **P2** | Worker `PENDING` selamanya + reconciler container mencoba memprovision baris pool | Status lahir `IDLE`; reconciler dimatikan di mode pool | `/workers` = 10 `IDLE` |

**Pelajaran operasional (penting):** variabel shell **mengalahkan** `.env` pada `docker compose`.
Selalu jalankan `env -u CREDENTIAL_KEY -u INTERNAL_API_SECRET docker compose up -d` bila shell
memuat nilai berbeda, atau pindahkan nilai itu ke shell agar satu sumber.

### Verifikasi akhir noVNC
- `POST /api/accounts/{id}/live-view` → **200** dengan URL `http://localhost:243NN/vnc.html?...&path=%2Fwebsockify%3Fticket%3D...`
- `GET vnc.html` → **200**; `verify-ticket` di worker pemilik → **204**, di worker lain → **403**
- WebSocket `/websockify?ticket=...` → **101 Switching Protocols**

---

## 11. Audit Menyeluruh Proyek (5 Nov 2026)

Cakupan: seluruh repo (apps/api, apps/worker, apps/web, packages, infra, DB), runtime nyata, dan supply chain.

### 11.1 Temuan yang diperbaiki

| ID | Severity | Area | Masalah | Perbaikan |
| :-: | :--- | :--- | :--- | :--- |
| A1 | **P0** | Live view | Lease live view **tidak pernah dilepas**. Slot Redis habis TTL, tetapi `account.worker_id` adalah baris DB → akun terkunci permanen ("already has a live session") | Endpoint `DELETE /api/accounts/{id}/live-view` + `AccountService.CloseLiveView`; UI memanggilnya saat panel ditutup / watch dimatikan |
| A2 | **P1** | Web | URL bertiket di-cache **tanpa kedaluwarsa**; tiket 5 menit → iframe blank tanpa pesan | Cache menyimpan `expiresAt`, margin 60 detik |
| A3 | **P1** | Web | Tiket di URL ikut sebagai `Referer` ke setiap request aset noVNC | `referrerPolicy="no-referrer"` pada iframe |
| A4 | **P1** | Build | `next dev` dan `next build` berbagi `.next` → `TypeError: e[o] is not a function` (manifest produksi dibaca runtime dev) | `distDir` dapat dikonfigurasi; dev memakai `.next-dev`; eslint/gitignore disesuaikan |
| A5 | **P2** | Worker build | Test ikut ter-compile ke `dist/` (296 KB) dan ikut image | `tsconfig.test.json` → `dist-test`; `dist` bersih |
| A6 | **P2** | Compose | Worker container default `WORKER_POOL_MODE=legacy` meski API `stateless` → dua sistem terputus | Dokumentasi kontrak di compose; RPC port diekspos |
| A7 | **P3** | Lint | `.next-dev` tidak di-ignore ESLint | Ditambahkan |

### 11.2 Temuan yang BELUM diperbaiki (butuh keputusan)

| ID | Severity | Temuan | Dampak | Rekomendasi |
| :-: | :--- | :--- | :--- | :--- |
| A8 | **P0 (supply chain)** | `next@15.1.6` memiliki **2 advisory CRITICAL**: RCE React Flight (GHSA-9qr9-h5gf-34mp) dan Authorization Bypass middleware (GHSA-f82v-jwr5-mffw), plus 4 HIGH | Dashboard terekspos bila dipublikasikan. Middleware tidak dipakai (bypass tidak aktif), tetapi RCE tetap berlaku | Naikkan ke `next >= 15.1.9` (atau 15.5.x). Perlu rebuild + regresi UI |
| A9 | P1 | `playwright@1.48.2` HIGH: tidak memverifikasi SSL saat mengunduh browser (GHSA-7mvr-c777-76hp) | Hanya saat `playwright install`; image saat ini memakai browser bawaan | Naikkan ke >= 1.55.1 **hanya bila** perilaku page/selector tetap kompatibel |
| A10 | P2 | `pnpm audit --prod` total 50 kerentanan (3 low, 21 moderate, 21 high, 5 critical) | Sebagian transitif | Tiket terpisah; prioritaskan `next` |

### 11.3 Yang diperiksa dan LULUS

- Race detector: `go test -race` pada `service`, `http`, `repository` — bersih.
- Secret: tidak ada `.env`/`.key`/`.dump` ter-track; tidak ada literal secret di kode; payload (`storageState`, credentials, proxy) tidak pernah masuk log Go maupun TS; tiket tidak di-log.
- Data: 486 akun, 0 pelanggaran pairing lokasi, 0 mismatch timezone, **15/15 sesi terdekripsi** lewat API.
- noVNC end-to-end: open **200**, `vnc.html` **200**, tiket **204/403**, WebSocket **101**, close **204**, reopen **200**, `leased=0`.
- Konfigurasi: seluruh env pool terdokumentasi di `.env.example` dan dibaca `config.go`.

---

## 12. Remediasi Seluruh Temuan Audit (§11) — 5 Nov 2026

### 12.1 Status akhir

| ID | Temuan | Status | Bukti |
| :-: | :--- | :--- | :--- |
| A1 | Lease live view tidak pernah dilepas | **FIXED** | `DELETE /live-view` + UI; siklus nyata open 200 → close 204 → reopen 200 → `leased=0`; `TestCloseLiveView*` |
| A2 | Cache URL bertiket tanpa kedaluwarsa | **FIXED** | cache menyimpan `expiresAt`, margin 60 s |
| A3 | Tiket bocor via `Referer` | **FIXED** | `referrerPolicy="no-referrer"` pada iframe |
| A4 | `next dev` vs `next build` berbagi `.next` | **FIXED** | `distDir` konfigurabel; dev → `.next-dev`; eslint/gitignore disesuaikan |
| A5 | Test ikut ke `dist/` | **FIXED** | `tsconfig.test.json` → `dist-test`; `dist` bersih |
| A6 | Worker container default `legacy` saat API `stateless` | **FIXED (dokumentasi)** | kontrak tertulis di `compose.yaml`; volume inersia dijelaskan |
| A7 | `.next-dev`/`next-env.d.ts` mengotori lint | **FIXED** | ditambahkan ke `eslint.config.mjs` ignore |
| A8 | `next` 2 CRITICAL + 4 HIGH | **FIXED** | `15.1.6` → **`15.5.29`**; audit prod turun dari 50 → 1 moderate |
| A9 | `playwright` HIGH (verifikasi SSL unduhan) | **FIXED (kode)** | `1.48.2` → **`1.55.1`**; 99/99 test worker lolos |
| A10 | 50 advisory total | **FIXED** | `pnpm audit --prod`: **1 moderate, 0 high, 0 critical** |

**Tambahan (dari laporan operator):** panel akun menampilkan "the worker has not published its noVNC port"
karena URL bertiket hanya diminta saat toggle diklik. Kini tiket dimintakan **otomatis** saat panel
dibuka dalam mode watch (login berjalan / operator membuka live view).

### 12.2 Image worker (SELESAI)

Rebuild sempat gagal karena unduhan Chromium timeout; setelah jaringan stabil:

- `docker compose --profile fleet build worker` → **sukses**
- Image memuat **playwright 1.55.1** dan **Chromium build 1193** (`/ms-playwright/chromium-1193`)
- 10 worker dijalankan ulang dari image baru

Verifikasi runtime pada image baru:

| Uji | Hasil |
| :--- | :--- |
| `browser-open` (launch Chromium nyata) | **200**, log worker `live view opened` |
| `auth-clear` | worker kembali `busy:false` |
| Live view end-to-end | open **200** → `vnc.html` **200** → close **204** → `busy:false` |
| noVNC dari host | `:24303/vnc.html` **200**, `:24307/vnc.html` **200** |

### 12.3 Catatan kompatibilitas upgrade

`next 15.1.6 → 15.5.29` diverifikasi dengan: `tsc --noEmit`, `next build` (sukses), `make ci` hijau,
dan dev server menyajikan `/`, `/login`, `/accounts`, `/workers` **200** dengan **0** TypeError.
`playwright 1.48.2 → 1.55.1` diverifikasi dengan seluruh suite worker.

### 12.4 Dua bug tambahan yang ditemukan saat verifikasi remediasi

| ID | Masalah | Perbaikan | Bukti |
| :-: | :--- | :--- | :--- |
| A11 | Panel akun menampilkan "worker has not published its noVNC port" karena tiket hanya dimintakan saat toggle diklik | Tiket dimintakan otomatis saat panel dibuka dalam mode watch | `useEffect` pada `detailAccount.id`/`isWatching` |
| A12 | `close` melepas lease DB tetapi **context browser di worker tetap terbuka** (`busy:true`), sehingga open berikutnya menggantung/timeout | Branch `else if` yang salah tempat membuat `ClearAuth` terlewat; dijadikan guard mandiri + log | Siklus nyata: open **200** → close **204** → `busy:false`; 2 siklus beruntun sukses; `leased=0` |

### 12.5 Status audit dependensi (akhir)

```
pnpm audit --prod  ->  1 moderate (postcss transitif), 0 high, 0 critical
```
Turun dari **50 temuan (5 critical)** sebelum remediasi.


