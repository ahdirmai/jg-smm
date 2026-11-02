# Deployment Guide — Elastic Worker Pool

> Cara men-deploy MVP-1-SMM dengan **elastic pool**: hanya N worker yang hidup
> sekaligus, sisanya tidur dengan biaya RAM 0, dan dibangunkan otomatis saat
> ada kerja.
>
> Dokumen ini untuk operator yang menaruh stack ini di server. Untuk analisis
> batas skala dan strateginya, lihat
> [`SCALE_MINSPEC_STRATEGY.md`](SCALE_MINSPEC_STRATEGY.md).

---

## 1. Apa yang dilakukan elastic pool

Masalahnya: satu worker container = satu Chromium headful + Xvfb + x11vnc +
noVNC. Terukur **~85 MiB saat idle, sampai ~460 MiB setelah sesi**, limit 4 GiB
untuk spike. Kalau 50 worker hidup bersamaan, itu ~50 GiB RAM — mesin 8 GiB
tidak akan sanggup, padahal **sebagian besar fleet idle hampir sepanjang waktu**
(hot path ≈ 1 action/menit/container).

Solusinya: jangan hidupkan semuanya.

| Keadaan | RAM | Waktu sampai bisa dipakai |
| --- | --- | --- |
| RUNNING | ~85–460 MiB | 0 (sudah jalan) |
| **PAUSED** | **0 B** (terukur) | **~3–5 detik** |
| STOPPED | 0 B | ~2–5 menit (login ulang manual) |

Pool menjaga jumlah worker RUNNING ≤ `ELASTIC_POOL_MAX_ACTIVE`, dan menidurkan
worker yang sudah sepi ≥ `ELASTIC_POOL_IDLE_SECONDS`.

```
        kerja datang                     kerja selesai + sepi N detik
             │                                      │
             ▼                                      ▼
   ┌──────────────────┐                  ┌──────────────────┐
   │  RUNNING         │                  │  PAUSED          │
   │  ~85–460 MiB     │ ───────────────► │  0 B RAM         │
   │                  │                  │  volume tetap    │
   └──────────────────┘                  └──────────────────┘
             ▲                                      │
             └──────────────────────────────────────┘
                 resume ~3–5 detik (waker)
```

**Tidak pernah** memakai `STOPPED`. Itu menghapus container **dan volume
session**-nya, yang berarti login ulang manual lewat noVNC. PAUSED menyimpan
volume, jadi resume cukup `docker start`.

---

## 2. Cara kerja (untuk yang akan men-debug nanti)

Pool **tidak** memanggil driver. Ia menulis `desired_state`, tuas yang sama yang
dipakai dashboard dan waker. Reconciler yang melakukan start/stop sebenarnya.

```
ElasticPool.Reconcile()            tiap tick (RECONCILE_INTERVAL_SECONDS)
   ├─ List workers                 semua worker, diurutkan
   ├─ Decide()                     policy murni, tanpa efek samping
   │     ├─ owns?                  hanya AUTO + desired in {RUNNING,PAUSED}
   │     ├─ idle?                  queue_depth==0 && now-last_seen >= idleAfter
   │     ├─ urutkan busy-first     terbaru menang; tie-break by id (stabil)
   │     └─ ≤ maxActive            sisanya "over-cap" → PAUSED
   └─ Update(desired_state)        hanya baris yang berubah

Reconciler                            tick terpisah
   └─ diff() → CreateWorker | StopWorker | DeleteWorker
```

Pemisahan ini disengaja: pool adalah loop kebijakan atas baris DB, reconciler
satu-satunya yang bicara ke driver. Jadi driver yang sedang down tidak membuat
pool "setengah jalan".

**Aturan keputusan**, berurutan:

1. **Bukan miliknya → jangan sentuh.** Hanya `source=AUTO`. Container `MANUAL`
   milik operator; `STOPPED` sedang dibongkar.
2. **Sepi → pause.** Kosong queue **dan** tidak ada aktivitas ≥ `idleAfter`.
   Queue kosong saja **tidak** cukup — clock dihitung dari aktivitas terakhir,
   supaya worker tidak dibunuh tepat setelah menyelesaikan satu job.
3. **Antre, terbaru menang.** Worker yang punya kerja diurutkan paling baru
   dulu, sampai `maxActive`. Sisanya PAUSED meski queue-nya tidak kosong —
   job-nya tetap aman di Redis (List durable) dan dilayani saat slot bebas.

`last_seen` = `last_action_at`, atau `last_heartbeat`, atau `created_at`.
Jadi worker baru mendapat jendela idle penuh, bukan langsung dimatikan.

---

## 3. Konfigurasi

| Env | Default | Arti |
| --- | --- | --- |
| `ELASTIC_POOL_MAX_ACTIVE` | `0` | Maks container AUTO yang RUNNING, **fleet-wide**. Juga dipakai reconciler sebagai **hard cap**: create/resume ditolak begitu container yang benar-benar jalan di platform mencapai angka ini. `0` = pool dan cap mati. |
| `ELASTIC_POOL_IDLE_SECONDS` | `300` | Detik sepi sebelum di-pause. Wajib ≥ 1 bila pool aktif. |
| `RECONCILE_INTERVAL_SECONDS` | `5` lokal / `30` prod | Tick pool **dan** reconciler. Pool tidak jalan kalau ini `0`. |

### Dua lapis, keduanya perlu

Pool hanya mengatur **baris** (`desired_state`), jadi apa pun yang membuat
container tanpa baris RUNNING akan melewatinya. Itu persis yang dulu terjadi:
reconciler membuat container untuk setiap baris PAUSED, jadi 454 akun yang
"salah" tetap hidup dan menghabiskan RAM host.

| Lapis | Mengatur | Titik gagal yang ditutup |
| --- | --- | --- |
| Elastic pool | `desired_state` per baris | flapping, worker idle tetap hidup |
| **Reconciler cap** | container yang jalan di platform | create/resume yang tidak lewat baris RUNNING |

Cap dihitung sekali per tick dari `ListRunning`, lalu tiap create/resume
mengambil satu slot dan tiap stop/delete mengembalikannya. Kalau hitungan gagal,
tidak ada yang diizinkan — lebih baik telat satu tick daripada OOM.

### Cara menghitung `ELASTIC_POOL_MAX_ACTIVE`

```
max_active = RAM_tersedia_untuk_worker / 400 MiB
```

400 MiB = worker dengan margin spike sedang (idle 85 MiB, limit 4 GiB).

| Mesin | RAM tersedia utk worker | `MAX_ACTIVE` |
| --- | --- | --- |
| 4 vCPU / 8 GiB (stack app sudah makan ~0.5 GiB) | ~4.7 GiB | **5–8** |
| 8 vCPU / 16 GiB | ~13 GiB | **16–24** |
| 16 vCPU / 32 GiB | ~29 GiB | **35–48** |

**Batasan CPU:** 4 vCPU dengan Chromium aktif (~0.5–1 core saat action)
praktis hanya sanggup **4–6 action konkuren**. Jadi di mesin 4 vCPU, `5`
adalah plafon yang benar meski RAM masih sisa.

### Cara memilih `ELASTIC_POOL_IDLE_SECONDS`

| Nilai | Kapan dipakai |
| --- | --- |
| 60–120 | Action hampir selalu ada; ingin RAM kembali cepat |
| **300 (default)** | Seimbang |
| 900–1800 | Action datang bergelombang (mis. batch tiap 15 menit) |

Terlalu pendek = container pause/wake berulang; tiap resume ~3–5 detik. Terlalu
panjang = RAM tidak kembali.

---

## 4. Deploy

### 4.1 Prasyarat

```sh
cp infra/docker/.env.example .env     # atau `make env`
```

Wajib di-set sebelum produksi:

| Env | Kenapa |
| --- | --- |
| `INTERNAL_API_SECRET` | Autentikasi grup `/internal` (`X-Internal-Secret`, constant-time). **Wajib begitu API bisa dijangkau jaringan** — tanpa ini siapa pun yang sampai ke port API bisa memalsukan callback worker dan menandai job apa pun `SUCCESS`. Mekanismenya sudah ada (`internal.go:66-77`); yang wajib adalah **mengisinya** di produksi (default kosong = cek dilewati). |
| `JWT_SECRET`, `CREDENTIAL_KEY` | Ganti dari default dev. |
| `SECURE_COOKIES=true` | Kalau di belakang HTTPS. |
| `PROVISIONER_MODE` | `docker` (1 mesin) atau `k8s` (cluster). |

### 4.2 Aktifkan pool

```sh
# .env
ELASTIC_POOL_MAX_ACTIVE=5
ELASTIC_POOL_IDLE_SECONDS=300
RECONCILE_INTERVAL_SECONDS=30
```

> **Penting:** pool hanya berjalan bila `RECONCILE_INTERVAL_SECONDS > 0`.
> Kalau reconciler off, pool juga off — tidak ada yang menjalankan start/stop.

### 4.3 Jalankan

```sh
make up            # api + web + pg + redis + minio (TANPA worker replica)
```

**Jangan `make up-fleet` / `--scale worker=N`.** Replica dari `--scale` tidak
punya baris `worker` di DB, jadi `/internal/heartbeat` balas 404 dan container
**exit(1) setelah 3 gagal** (~90 detik). Worker yang sah dibuat dari dashboard
(atau auto-create saat menambah akun), dan pool yang mengatur hidup/matinya.

### 4.4 Verifikasi

```sh
# 1. Stack sehat
curl -s localhost:24080/healthz        # {"status":"ok"}

# 2. Pool jalan (log api)
docker compose logs api | grep "elastic pool"
#   elastic pool started maxActive=5 idleAfter=5m0s interval=30s

# 3. Hitung yang RUNNING — harus ≤ MAX_ACTIVE (MANUAL tidak dihitung)
docker exec smm-postgres-1 psql -U smm -d smm -c \
  "select desired_state, source, count(*) from worker group by 1,2;"

# 4. RAM yang dihemat
docker stats --no-stream --all --format '{{.Name}}\t{{.MemUsage}}' | grep smm-worker
#   container Exited harus menunjukkan 0B / 0B
```

### 4.5 Rollback

Set `ELASTIC_POOL_MAX_ACTIVE=0` lalu restart api. Semua worker kembali
mengikuti `desired_state` masing-masing, tanpa perubahan lain:

```sh
docker compose up -d api
```

---

## 5. Menambah akun (alur normal)

1. Dashboard → **Accounts → New** → pilih platform → isi kredensial.
2. Packer menaruh akun di container ber-slot kosong, atau **auto-create** satu
   (kalau `PROVISION_AUTO_CREATE=true`).
3. Container AUTO baru **lahir PAUSED** (`packer.go`: *"a slate of them held
   ~4GB of RAM in idle browsers"*).
4. Saat login/action/live-view dipicu, **waker** menyalakannya dan menunggu
   READY (~3–5 detik).
5. Setelah sepi `IDLE_SECONDS`, pool menidurkannya lagi.

Tidak ada langkah manual. Operator cukup menambah akun.

---

## 6. Troubleshooting

| Gejala | Sebab | Tindakan |
| --- | --- | --- |
| Semua worker PAUSED, aksi tidak jalan | `maxActive` terlalu kecil / pool salah menilai sepi | Cek log `reason=`; naikkan `MAX_ACTIVE` |
| Worker pause/wake berulang | `IDLE_SECONDS` terlalu pendek | Naikkan ke 900 |
| `elastic pool disabled` di log | `MAX_ACTIVE=0`, atau driver `nil`, atau `RECONCILE_INTERVAL_SECONDS=0` | Set ketiganya |
| Worker tidak pernah dibuat | reconciler off | `RECONCILE_INTERVAL_SECONDS > 0` |
| Container `Exited (1)`, log `heartbeat 404` | replica dari `--scale`, bukan dari dashboard | Buat worker dari dashboard |
| Akun minta login ulang setelah pause | **bug** — pool menulis STOPPED (seharusnya tidak) | Laporkan; pool hanya boleh menulis PAUSED |
| Aksi terasa lambat ~5 detik | resume dari pause | Normal. Naikkan `IDLE_SECONDS` bila sering |

### Yang **tidak** dilakukan pool

- Tidak menyentuh container `MANUAL`.
- Tidak menulis `STOPPED` (tidak pernah menghapus volume session).
- Tidak memanggil driver langsung (selalu lewat reconciler).
- Tidak mem-pause worker yang queue-nya **tidak** kosong hanya karena sepi —
  queue non-kosong selalu dianggap kerja.
- Tidak jalan kalau driver `nil` (dev lokal tanpa provisioner).

---

## 7. Batas dan catatan jujur

| Batas | Angka |
| --- | --- |
| Worker yang bisa *dikelola* (baris DB) | 200 (`pageBounds` clamp; lihat strategi §3.4a) |
| Worker yang bisa *hidup* di mesin ini | ~5–8 |
| Waktu resume | ~3–5 detik (worst case ~35 s bila heartbeat pertama gagal) |
| Worker menunggu saat over-cap | tidak dibatasi, job tetap di Redis |

**Yang belum ada** (butuh dikerjakan sebelum produksi serius):

1. **Manifest K8s** — mode `k8s` belum bisa start (`infra/k8s/` kosong; pod tanpa
   `resources`/`env`, dan tidak ada ServiceAccount).
2. **Retensi tabel `heartbeat`** — sudah 183 ribu baris di dev, tanpa pembersih.
3. **Reconciler serial** — 1 tick = 1 `GET` per worker; di ratusan worker tick
   tidak akan selesai (lihat strategi §3.3).
4. **Clamp 200 baris** — pool dan reconciler hanya melihat 100 worker terlama
   tertutup (`pageBounds`, strategi §3.4a). Di atas ~100 worker, sebagian fleet
   tidak terkelola.

Detail lengkap dan prioritasnya ada di
[`SCALE_MINSPEC_STRATEGY.md`](SCALE_MINSPEC_STRATEGY.md) §4.
