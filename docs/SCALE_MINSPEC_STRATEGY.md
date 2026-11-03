# Strategi Skala — Mesin Minimum (4 vCPU / 8 GiB)

> Pertanyaan: bagaimana mesin spek minimum (4 vCPU, 8 GiB RAM) bisa "handle
> ratusan container"?
>
> **Jawaban singkat: tidak bisa — dan itu fakta aritmatika, bukan preferensi.**
> Dokumen ini menjelaskan batas kerasnya, lalu strategi yang benar-benar bisa
> dijalankan di mesin itu.

Semua angka di bawah **diukur langsung** di mesin ini lewat `docker stats`,
bukan diasumsikan.

---

## 1. Batas keras: kenapa 200–500 mustahil di satu mesin

### 1.1 Realita mesin ini

| Fakta | Nilai terukur |
| --- | --- |
| CPU host / RAM host | 8 vCPU / 16 GiB |
| **Yang benar-benar tersedia untuk Docker** | **4 vCPU / 7.74 GiB** |
| Container lain yang sudah tinggal (proyek lain) | **12 container, 2.50 GiB** |
| Stack SMM inti (api, web, pg, redis, minio) | **0.52 GiB** |
| Sisa benar-benar bebas | **4.72 GiB** |

Kuncinya baris kedua: Docker Desktop sudah di-cap di 4 vCPU / 7.74 GiB. Itu
sama dengan "spek minimum" yang kamu sebut — jadi mesinmu **sudah** mesin
minimum itu, dan sudah dipakai 12 container proyek lain.

### 1.2 Aritmatika cluster yang diminta

Angka sizing LOCKED di [`INFRA_ANALYST.md`](INFRA_ANALYST.md) §2.2:
request `750m / 1Gi`, limit `2 / 4Gi`.

```
50 container  =  50 GiB RAM + 38 vCPU  ->  2 node (16vCPU/32GiB)
100 container = 100 GiB RAM + 75 vCPU  ->  5 node
200 container = 200 GiB RAM + 150 vCPU ->  9 node
500 container = 500 GiB RAM + 375 vCPU -> 23 node
```

Mesin 4 vCPU / 8 GiB menyediakan 7.74 GiB dan 4 vCPU. Untuk 200 container
kamu butuh **200 GiB** — kekurangan **~26x**. Ini bukan soal tuning; ini soal
fisika. **Satu mesin 4vCPU/8GiB tidak akan pernah menjadi host 200–500 worker.**

### 1.3 Yang benar-benar muat di mesin ini

| Budget/worker | Container muat (sisa 4.72 GiB) | Asumsi |
| --- | --- | --- |
| 150 MiB | **32** | idle, tanpa spike |
| 250 MiB | **19** | idle + margin |
| 400 MiB | **12** | termasuk spike sedang |
| 750 MiB | 6 | aman untuk action aktif |

Dan itu pun **sebelum** CPU jadi penghalang: 4 vCPU dengan worker idle
(~1–13% masing-masing, terukur) memang bisa menampung puluhan container idle,
tapi begitu banyak worker mengeksekusi action bersamaan (spike Chromium
~0.5–1 core), praktis **4–6 action konkuren** yang bisa jalan.

> **Kesimpulan:** mesin ini adalah **control plane + dev/test**, bukan worker
> host. Peran yang benar untuknya adalah *mengelola* ratusan container yang
> berjalan di tempat lain, bukan *menjalankan* ratusan container itu.

---

## 2. Strategi yang benar untuk mesin minimum ini

Tiga jalur, dari yang paling murah. Pilih sesuai uang & waktu.

### Jalur A — Mesin ini sebagai control plane + fleet jauh (RECOMMENDED)

Mesin minimum menjalankan API, dashboard, Postgres, Redis, MinIO — **satu-satunya
tempat data hidup**. Worker berjalan di tempat lain, bisa 1 server besar atau
cluster K8s.

```
┌──────────────────────────┐         ┌─────────────────────────────┐
│  MESIN MINIMUM 4vCPU/8GiB │         │  WORKER HOST (K8s / VPS)    │
│  api + web + pg + redis   │◄───────►│  N worker container          │
│  + minio  (0.52 GiB)      │  Redis  │  (k8s driver, PVC session)   │
│                           │ + HTTP  │                              │
│  = CONTROL PLANE          │         │  = DATA PLANE                │
└──────────────────────────┘         └─────────────────────────────┘
```

Cara kerja: worker **tidak** pernah sentuh DB (ADR 0011) — mereka hanya
subscribe Redis dan POST callback ke API. Jadi API/Redis boleh di mesin
minimum, worker di mana saja, **asalkan Redis dan API bisa dijangkau**.

Yang perlu diubah agar ini jalan dari mesin minimum:

1. **Redis tidak boleh di `127.0.0.1`.** Set `REDIS_ADDR` ke host yang bisa
   dijangkau worker, dengan password + TLS (Redis sekarang single replica tanpa
   auth — lihat [`INFRA_ANALYST.md`](INFRA_ANALYST.md) §6.2).
2. **API harus punya alamat publik** untuk callback worker
   (`POST /internal/action-callback`), plus **shared secret di grup `/internal`**
   (temuan H-1 di [`IG_WORKFLOW_AUDIT`](IG_WORKFLOW_AUDIT_2026-09-30.md): grup
   `/internal` sekarang **tanpa auth apa pun** — ini prasyarat wajib begitu API
   terekspos jaringan).
3. `PROVISIONER_MODE=k8s` (bukan `docker`) — API tidak meluncurkan container
   lokal, tapi bicara ke K8s API. **Tapi baca §4: mode k8s belum lengkap.**
4. `RECONCILE_INTERVAL_SECONDS` tetap 60 (bukan 5) — reconciler satu tick
   menyapu seluruh fleet secara berurutan (`reconciler.go:119-136`).

**Biaya:** mesin ini (sudah ada) + VPS worker. 50 container ≈ 2 node
16vCPU/32GiB (mis. Hetzner CPX/CCX ≈ $120–160/bln, lihat
[`INFRA_ANALYST.md`](INFRA_ANALYST.md) §3).

### Jalur B — Mesin minimum sebagai dev + fleet lokal kecil (SEKARANG)

Untuk *testing dan pengembangan*, bukan produksi. Ini yang sudah jalan:

- `make up` (api/web/pg/redis/minio) + worker **dibuat dari dashboard**
  (bukan `--scale`, lihat §3).
- Anggaran realistis: **8–12 worker** dengan sisa RAM saat ini. Sudah ada 8
  worker SMM jalan (total 1.09 GiB, rata-rata 136 MiB).
- Tetapkan `MAX_ACCOUNTS_PER_CONTAINER=2` dan `ACTION_BATCH_PARALLELISM=2`
  (lokal) supaya spike Chromium tidak datang bersamaan.
- Buat **worker image dibuat sekali**, jangan rebuild per worker.

### Jalur C — Naikkan mesin (kalau tetap mau lokal)

Kalau tujuannya benar-benar banyak worker di satu mesin: **worker adalah beban
memory, bukan CPU**. Naikkan RAM Docker Desktop, bukan CPU:

| RAM Docker | Worker @750 MiB | Catatan |
| --- | --- | --- |
| 8 GiB | ~6 | mesin sekarang |
| 32 GiB | ~35 | Mac Studio / server kecil |
| 64 GiB | ~75 | batas praktis satu daemon |

Di atas ~100 container satu daemon jadi SPOF, port noVNC mentok di 200 slot
(`NOVNC_PORT_MIN/MAX` 24100–24299), dan `nofile`/`nf_conntrack` mulai jadi
masalah. **Ratusan = multi-host, titik.**

---

## 3. Bug nyata yang menghalangi (ditemukan live saat sesi ini)

### 3.1 `make up-fleet` / `--scale worker=N` SELALU gagal

Diverifikasi live: `smm-worker-1/2/3` **exit(1)** dalam ~1 menit.

```
heartbeat status 404  (x3)
heartbeat budget exhausted, exiting
```

Sebab: replica compose **tidak punya baris `worker` di DB**. `/internal/heartbeat`
menolak worker tak dikenal → worker self-exit setelah 3 gagal (by design,
[`DEVELOPMENT_RULE.md`](DEVELOPMENT_RULE.md): fleet default kosong, container
dibuat dari dashboard). Lihat `internal.go:203` + `reconciler.go`.

**Jadi: jangan pakai `--scale`.** Buat worker dari dashboard, atau seed baris
`worker` dulu. `Makefile:37` juga duplikat target `up-fleet` (warning
`ignoring old commands`).

### 3.2 Mode K8s belum bisa start sama sekali

`infra/k8s/` **kosong** (hanya `.gitkeep`), padahal driver mengasumsikan:

- ServiceAccount `smm-provisioner` **sudah ada** (`client.go:225`) — tidak ada
  manifest-nya → pod gagal create.
- Pod **tanpa `resources`** (`client.go:230-247`) → di bawah ResourceQuota
  `requests.memory`, setiap create **ditolak**.
- Pod **tanpa `env` sama sekali** → worker boot tanpa `REDIS_ADDR`/API URL.

Artinya `PROVISIONER_MODE=k8s` dijalankan hari ini = pod Pending/gagal.
Manifest minimum yang wajib ada: Namespace, SA+Role+RoleBinding, ResourceQuota,
LimitRange, PriorityClass, NetworkPolicy, StorageClass.

### 3.3 Reconciler: plafon keras 500 + sweep berurutan

`reconciler.go:120` → `List(port.WorkerFilter{Limit: 500})`. Dua konsekuensi:

- Worker ke-501 **tidak akan pernah direconcile**.
- Tiap tick memanggil `driver.Observe` **berurutan** per worker (1 K8s GET
  masing-masing). 500 worker = 500 GET serial per tick → tick bisa lebih lama
  dari intervalnya sendiri.

(Sebelum 500 pun sudah rusak — lihat §3.5.)

---

### 3.4 Bug yang sudah aktif SEKARANG (bukan nanti di 500)

Diverifikasi langsung terhadap DB & kode yang jalan. Ini penting karena
mesin minimum pun sudah menyentuh batasnya.

### a. Data loss mulai worker ke-101 — BUG AKTIF

`repository/convert.go:92-94`:

```go
if limit <= 0 || limit > 200 {
    limit = 100        // ← diam-diam jadi 100
}
```

`reconciler.go:120` minta `Limit: 500` → **dapat 100**. Dan `ListWorkers`
mengurutkan `created_at DESC` (`db/queries/worker.sql:11-14`), jadi yang
didapat adalah **100 worker terbaru**; worker terlama tidak pernah
di-observe, tidak pernah di-refresh, tidak pernah di-stop.

**Live: DB sekarang punya 58 baris `worker`** — tinggal ~43 lagi dari ambang
bug ini. Sweeper punya masalah sama (worker #101+ dianggap orphan →
delete-eligible, `orphan_sweeper.go:72-101`).

### b. Reconciler tidak akan pernah konvergen di 500

Satu tick = 1 List + **500 `GET /pods/<id>` berurutan** (`reconciler.go:119-136`,
`client.go:140-149`). client-go default membatasi **5 QPS / 10 burst**, jadi
satu sweep butuh **≥100 detik** sementara `time.Ticker` (default 30 dtk)
terus memicu. Ticks tidak pernah selesai, drift tumbuh tak terbatas.

### c. Provisioning 500 pod butuh ≥50 menit

`CreatesPerMinute: 10` di-hardcode (`cmd/server/main.go:571`, `client.go:75`).
Sementara `ContainerService.Create` balas `PENDING` dalam ~1 ms → operator
melihat ratusan kartu PENDING tanpa backing dan membuat lebih banyak lagi.

### d. Heartbeat: 183 ribu baris, tanpa retensi, pool 10 koneksi

Live: **`heartbeat` = 183.199 baris**, dan **tidak ada** `DELETE FROM
heartbeat` / retention policy di `db/queries/` maupun migrations (retention
hanya ada untuk tabel analytics, `000004_scrape_analytics.up.sql:205`).
Di 500 worker = **1.44 juta baris/hari**, semua lewat `pgxpool` **MaxConns=10**
(`adapter/postgres.go:26`).

### e. Dashboard truncate di 200 (N+1)

`container.go:192` (`Limit: 200`) lalu `accounts.ListByWorker` **sekali per
worker** (`:196`) → 201 query. Dipanggil tiap mount + tiap frame
`account-updated` (`use-containers.ts:82,131`).

### f. Throughput action mentok ~1 detik⁻¹, terlepas dari jumlah worker

`action_scheduler.go:214-232`: `TickBudget = ActionBatchParallelism × 10 = 20`
job per tick, interval 20 dtk → **1 action/detik untuk seluruh fleet**. Punya
500 pod tidak menambah throughput sama sekali; 500 pod duduk idle.

---

### 3.5 Latensi resume: worker mati → bisa dipakai

Terukur di mesin ini (docker driver, container `Exited`). Penting dibedakan
karena "mati" punya tiga arti dengan biaya sangat berbeda.

| Kondisi | RAM dipakai | Waktu sampai dipakai |
| --- | --- | --- |
| **RUNNING** | ~85–460 MiB | 0 (sudah jalan) |
| **PAUSED** (container Exited, volume tetap) | **0 B** (terukur) | **~2–3 detik** |
| **STOPPED/DELETE** (container + volume dihapus) | 0 B | ~2–5 menit (login ulang) |

### Timeline PAUSED → siap (terukur)

```
docker start              0.39 s   ← diukur langsung (date +%s.%N)
entrypoint (Xvfb/x11vnc)  ~1–2 s   ← "worker entrypoint ready"
node boot + advertise     0.37 s   ← dari log: 03:22:24.305 → 03:22:24.670
─────────────────────────────────
total ke "siap kerja"     ~2–3 s
```

Dari log nyata `smm-worker-3`:

```
03:22:24.305  worker starting
03:22:24.647  action loop started (sequential, batch)   ← +342 ms
03:22:24.670  control subscriber ready                  ← +365 ms
```

Jadi worker benar-benar **siap BLPOP job dalam ~2–3 detik**.

### Tapi API baru "melihat" READY setelah heartbeat

`waker.awaitReady` (`waker.go:38`) menunggu `status=READY` **dan**
`last_heartbeat` lebih muda dari 30 detik. Heartbeat pertama dikirim **segera**
saat start (`heartbeat.ts:80` — "Send an immediate beat"), jadi bukan menunggu
30 detik:

| Skenario | Waktu total |
| --- | --- |
| Normal (beat pertama sukses) | **~3–5 detik** |
| Beat pertama gagal, tunggu siklus berikut | ~35 detik |
| Batas keras `WakeTimeout` | 45 detik (`waker.go:31`) |

**Angka praktis: ~3–5 detik** dari PAUSED ke bisa dipakai. Itu murah.

### Konsekuensi untuk auto-pause

Karena resume cuma ~3–5 detik dan PAUSED = **0 B RAM**, auto-pause adalah
pertukaran yang sangat bagus di mesin minimum:

| | Nilai |
| --- | --- |
| Biaya pause | 0 (RAM langsung 0 B) |
| Biaya resume | ~3–5 detik, sekali |
| Ambang idle wajar | 10–15 menit |

Dengan idle 15 menit: sebuah worker yang dipakai 1× per jam memakai RAM
15 menit/jam (~25%) bukan 100%, dan menambah ~4 detik pada satu aksi. Trade-off
yang jelas menguntungkan di mesin 8 GiB.

**Yang harus dihindari:** pause saat `queue_depth > 0`. Resume 4 detik itu
memakan waktu, dan job yang menunggu akan terasa lambat. Guard `queue_depth == 0`
dari heartbeat (`job.go:315`) wajib ada.

**Jangan pakai STOPPED untuk auto-pause.** `StopWorker` menyimpan volume
(aman, resume cepat); `DeleteWorker` menghapus PVC → session IG hilang →
harus login ulang manual via noVNC. Itu bukan "pause", itu "bunuh".

---

## 4. Kalau kamu tetap mau 200–500: yang wajib dikerjakan

Urut prioritas. Ini prasyarat, bukan opsional.

| # | Item | Kenapa | Bukti |
| --- | --- | --- | --- |
| 1 | Auth `/internal` (shared secret + HMAC) | Tanpa ini, siapa pun di jaringan bisa forge callback → tandai job orang lain `SUCCESS`/membunuh akun | `IG_WORKFLOW_AUDIT` H-1, terbukti live |
| 2 | Manifest K8s lengkap | Mode k8s tidak bisa start tanpa SA/quota/limit | `infra/k8s/` kosong |
| 3 | `resources` di pod spec (750m/1Gi, 2/4Gi) | Tanpa request, quota menolak; tanpa limit, Chromium OOM membunuh tetangga | `client.go:230-247` |
| 4 | `env` di pod spec (Redis/API/MinIO) | Worker sekarang boot tanpa config | `client.go:230-247` |
| 5 | Naikkan/lepas plafon reconciler + paralelkan `Observe` | 500 = plafon; sweep serial tidak selesai dalam 1 tick | `reconciler.go:119-136` |
| 6 | Redis HA + sharding queue per region | Redis 1 replica = SPOF; eviction `queue:action:*` mungkin | [`INFRA_ANALYST.md`](INFRA_ANALYST.md) §6.2 |
| 7 | Reverse-proxy noVNC (hostname), buang 200-port | 1 port/worker tidak skalabel; noVNC tidak boleh publik | §7 INFRA |
| 8 | PriorityClass worker < API | 500 pod memory-bound harus preempt sesama, bukan BE | — |
| 9 | Cluster-autoscaler | 500 pod = 18–25 node; provisioning manual tak mungkin | INFRA §R9 |
| 10 | Node math dengan **request CPU = 0.75** | CPU jadi pengikat di 21 pod/node, bukan 28 (memory) | §1.2 |

### Node math yang benar (koreksi terhadap dokumen lama)

Dokumen lama menghitung 28 pod/node (memory-bound saja). Dengan
`request.cpu=750m`, **CPU mengikat lebih dulu**:

```
slot/node = min(28 GiB / 1 GiB,  16 vCPU / 0.75) = min(28, 21) = 21 pod/node
```

Jadi 500 pod butuh **24+1 = 25 node**, bukan 20. Kalau mau tetap 20 node,
turunkan `request.cpu` ke ~600m.

---

## 5. Rekomendasi akhir

| Tujuan | Jalur | Aksi konkret |
| --- | --- | --- |
| **Testing sekarang** | B | `make up`, buat 8–12 worker dari dashboard, jangan `--scale` |
| **Hemat, serius** | A | Mesin ini = control plane; worker di VPS/K8s; perbaiki auth `/internal` dulu |
| **Tetap lokal, lebih besar** | C | Naikkan RAM Docker ke 32–64 GiB → ~35–75 worker |
| **200–500 sungguhan** | A + §4 | Cluster K8s multi-node; kerjakan 10 item §4 |

**Yang jangan dilakukan:** mencoba memaksa 200–500 container ke mesin 4vCPU/8GiB.
Secara aritmatika butuh ~200 GiB RAM untuk 200 container; sisa RAM bebas mesin
ini 4.7 GiB. Selisihnya tidak bisa ditutup dengan tuning, swap, atau
`LimitRange` — hanya dengan mesin tambahan.
