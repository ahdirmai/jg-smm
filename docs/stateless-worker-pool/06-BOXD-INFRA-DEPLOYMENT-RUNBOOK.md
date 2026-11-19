# 06 — Deployment `boxd` (Control Plane + Golden Worker + 11 Worker)

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)
**Status terakhir:** 2026-11-19 — sudah dijalankan di `fra` dan diverifikasi.

> [!CAUTION]
> **Kredensial tidak pernah dicetak/di-commit.** Jangan `cat` `.env`,
> `.boxenv.base`, `~/.config/boxd/env`, atau dump `account_session`. Kirim file
> rahasia hanya lewat `boxd machine cp --mode 600` / `boxd env push`; semua
> perintah di bawah memakai variabel, bukan nilai literal.
>
> **Jangan menyentuh mesin lain** (`ahdirmai`, `speech`, `pos`, `dml-lms`,
> `belajar`). Semua mesin SMM berlabel `app=smm`.

---

## 0. Kondisi yang sudah berjalan

| Hal | Nilai |
| --- | --- |
| Control plane | `smm-hub` (`2 vCPU / 8 GiB`, `fra`, always-on, bot protection off) |
| URL sementara | `https://smm-hub.boxd.sh` (proxy default port `8000`) |
| Domain | `smm.ahdirmai.my.id` **pending** di boxd (DNS/verifikasi belum selesai) |
| Login worker | `smm-login-01` (always-on; semua login/CAPTCHA/live view dipin di sini) |
| Action worker | `smm-worker-01` … `smm-worker-10` (idle sleep 5 menit, hibernate 1 jam) |
| Golden | `smm-worker-golden` (stopped), snapshot `smm-golden-v1` |
| Data | Postgres, Redis, MinIO di dalam Docker hub; port bind `127.0.0.1` |
| Akun | 15 akun + 14 `account_session` + 1 `app_user` diimpor dari lokal |
| Worker pool | `WORKER_POOL_MODE=stateless`, endpoint `https://rpc.<vm>.boxd.sh` |

Kalau domain custom sudah aktif, ganti `smm-hub.boxd.sh` di `.env` **sebelum
build web** (`NEXT_PUBLIC_*` di-inline saat build) lalu `docker compose ...
up -d --build web api`.

---

## 1. Topologi

| Mesin | Ukuran | Role | Tidur | Isi |
| --- | --- | --- | --- | --- |
| `smm-hub` | `2x8G` | `app=smm,role=hub` | selalu hidup | `compose.hub.yaml`: postgres, redis, minio, migrate, api, web, nginx:8000 |
| `smm-worker-golden` | `2x8G` | `golden` | stopped setelah snapshot | Xvfb/x11vnc/websockify, Playwright, worker RPC, nginx |
| `smm-login-01` | `2x8G` | `login` | selalu hidup | worker yang sama; menahan login/CAPTCHA yang diparkir |
| `smm-worker-01..10` | `2x8G` | `worker` | suspend 300s / hibernate 3600s | worker yang sama, satu akun per job |

Semua worker memakai nginx port `8000` sebagai satu-satunya gerbang:

- default `<vm>.boxd.sh` → `/novnc/...`
- named `rpc.<vm>.boxd.sh` → `/internal/...` dan `/healthz`

Keduanya menuju port `8000`; worker memverifikasi `X-Internal-Secret`, bukan
boxd.

---

## 2. Control plane (`smm-hub`)

### 2.1 Buat / pastikan mesin

```bash
set -a; . ~/.config/boxd/env; set +a

boxd machine new smm-hub --region fra --vcpu 2 --memory 8G \
  --auto-suspend-timeout=0 --auto-hibernate-timeout=0 \
  --label app=smm --label role=hub --json
boxd machine config set smm-hub bot-protection off
boxd machine config set smm-hub auto-suspend.timeout 0
boxd machine config set smm-hub auto-hibernate.timeout 0
boxd machine proxy set-port --vm smm-hub --port 8000
```

Kalau `smm-hub` sudah ada, cukup pastikan `bot-protection off` dan proxy
default port `8000`.

### 2.2 Kirim source

Jangan clone penuh dengan `node_modules`; kirim tracked files saja:

```bash
git archive --format=tar HEAD | gzip > /tmp/smm-src.tgz
boxd machine cp /tmp/smm-src.tgz smm-hub:/home/boxd/smm-src.tgz
boxd machine exec smm-hub -- 'cd /home/boxd && rm -rf smm.new && mkdir smm.new && tar -xzf smm-src.tgz -C smm.new'
```

Pindahkan `.env` lama ke samping, lalu isi `.env` baru (mode `600`) dari
template lokal. Nilai wajib:

```dotenv
# Public URLs — dipakai build web dan worker; ganti domain sebelum build.
NEXT_PUBLIC_API_URL=https://smm-hub.boxd.sh
NEXT_PUBLIC_SSE_URL=https://smm-hub.boxd.sh/api/stream
API_PORT=8080
SECURE_COOKIES=true

# Tidak ada container yang dibuat API di boxd.
PROVISIONER_MODE=static
PROVISION_AUTO_CREATE=false
ELASTIC_POOL_MAX_ACTIVE=0
RECONCILE_INTERVAL_SECONDS=0

# Stateless pool: 10 action worker + 1 login worker.
WORKER_POOL_MODE=stateless
WORKER_POOL_SIZE=10
LOGIN_WORKER_NAME=smm-login-01
WORKER_POOL_ENDPOINTS=smm-login-01=https://rpc.smm-login-01.boxd.sh|https://smm-login-01.boxd.sh/novnc,smm-worker-01=https://rpc.smm-worker-01.boxd.sh|https://smm-worker-01.boxd.sh/novnc,...,smm-worker-10=https://rpc.smm-worker-10.boxd.sh|https://smm-worker-10.boxd.sh/novnc

# Harus sama di hub dan semua worker; kosong = stateless menolak boot.
INTERNAL_API_SECRET=<openssl rand -hex 32>
# Harus sama dengan lokal agar account_session lama bisa didekripsi.
CREDENTIAL_KEY=<nilai .env lokal>
ACTION_DRY_RUN=false
WORKER_PLATFORMS=instagram,threads,youtube
MAX_CONCURRENT_LOGINS=3
```

`WORKER_POOL_ENDPOINTS` harus memuat **login** juga. Kode `ensureLoginWorker`
tidak lagi menimpanya dengan `http://smm-login-01:8090`; alamat itu tidak
resolve di luar network compose.

### 2.3 Jalankan stack

```bash
cd /home/boxd/smm
docker compose --env-file .env -f infra/boxd/compose.hub.yaml up -d --build
docker compose --env-file .env -f infra/boxd/compose.hub.yaml ps
curl -fsS https://smm-hub.boxd.sh/healthz
```

Yang harus terlihat: `postgres`, `redis`, `minio`, `api`, `web`, `nginx`
healthy/running; `migrate` exit 0. Nginx mem-proxy `/api/` dan `/internal/`
ke `api:8080`, sisanya ke `web:3000`; SSE tidak dibuffer.

### 2.4 Impor akun + session dari lokal

Session browser adalah **bytea terenkripsi** di `account_session`; menyalin
file JSON saja tidak cukup. `CREDENTIAL_KEY` di hub wajib sama dengan lokal.
Dari mesin lokal (jangan print isi):

```bash
# accounts
docker exec smm-postgres-1 pg_dump -U smm -d smm --data-only \
  --table=public.account --column-inserts --no-owner > /tmp/smm-account.sql

# sessions; captured_on dikosongkan karena worker id lokal tidak ada di hub
docker exec -i smm-postgres-1 psql -U smm -d smm -c "\copy (select account_id, storage_state, expires_at, null::uuid as captured_on, captured_at, last_used_at from account_session) to '/tmp/account_session.csv' with (format csv)"
docker cp smm-postgres-1:/tmp/account_session.csv /tmp/account_session.csv

# dashboard user
docker exec smm-postgres-1 pg_dump -U smm -d smm --data-only \
  --table=public.app_user --column-inserts --no-owner > /tmp/smm-app-user.sql

for f in smm-account.sql account_session.csv smm-app-user.sql; do
  gzip -c "/tmp/$f" > "/tmp/$f.gz"
  boxd machine cp "/tmp/$f.gz" "smm-hub:/home/boxd/$f.gz"
done
```

Di hub, setelah `migrate` selesai:

```bash
cd /home/boxd
gunzip -f -c smm-account.sql.gz > smm-account.sql
gunzip -f -c account_session.csv.gz > account_session.csv
gunzip -f -c smm-app-user.sql.gz > smm-app-user.sql

docker exec -i smm-postgres-1 psql -U smm -d smm -v ON_ERROR_STOP=1 \
  -c 'truncate account_session, account cascade'
docker exec -i smm-postgres-1 psql -U smm -d smm -v ON_ERROR_STOP=1 < smm-account.sql
docker exec -i smm-postgres-1 psql -U smm -d smm -v ON_ERROR_STOP=1 \
  -c "\copy account_session (account_id,storage_state,expires_at,captured_on,captured_at,last_used_at) from stdin with (format csv)" < account_session.csv
docker exec -i smm-postgres-1 psql -U smm -d smm -v ON_ERROR_STOP=1 < smm-app-user.sql
rm -f smm-account.sql account_session.csv smm-app-user.sql

docker exec smm-postgres-1 psql -U smm -d smm -Atc \
  'select (select count(*) from account), (select count(*) from account_session), (select count(*) from app_user)'
```

Expected saat ini: `15|14|1`. `truncate account cascade` juga mengosongkan
post/comment/action lama; itu disengaja untuk migrasi session, bukan untuk
produksi berjalan.

---

## 3. Golden worker

### 3.1 Build

```bash
boxd machine new smm-worker-golden --region fra --vcpu 2 --memory 8G \
  --auto-suspend-timeout=0 --auto-hibernate-timeout=0 \
  --label app=smm --label role=golden --json
boxd machine config set smm-worker-golden bot-protection off
boxd machine proxy set-port --vm smm-worker-golden --port 8000

boxd machine cp /tmp/smm-src.tgz smm-worker-golden:/home/boxd/smm-src.tgz
boxd machine exec smm-worker-golden -- \
  'cd /home/boxd && rm -rf smm && mkdir smm && tar -xzf smm-src.tgz -C smm'
```

Buat `.boxenv.base` (mode `600`) berisi nilai yang sama untuk semua worker:

```dotenv
API_URL=https://smm-hub.boxd.sh
INTERNAL_API_SECRET=<sama dengan hub>
WORKER_POOL_MODE=stateless
PLATFORMS=instagram,threads,youtube
ACTION_DRY_RUN=false
DISPLAY=:99
WORKER_RPC_HOST=127.0.0.1
WORKER_RPC_PORT=8090
TZ=Asia/Jakarta
LOG_LEVEL=info
SCREENSHOT_DIR=/home/boxd/smm/.runtime/screenshots
SESSION_DIR=/home/boxd/smm/.runtime/sessions
```

`WORKER_ID` dan `NOVNC_URL` **tidak** ditulis di base; keduanya dibuat dari
hostname oleh `render-worker-env.sh`.

```bash
boxd machine cp --mode 600 /path/ke/.boxenv.base smm-worker-golden:/home/boxd/smm/.boxenv.base
boxd machine exec smm-worker-golden -- '/home/boxd/smm/infra/boxd/install-worker.sh'
```

`install-worker.sh` memasang `xvfb`, `x11vnc`, `novnc`, `websockify`, nginx,
pnpm, Chromium Playwright, lalu meng-enable:

- `smm-xvfb.service` → `Xvfb :99`
- `smm-x11vnc.service` → `x11vnc :99`, port 5900 loopback
- `smm-novnc.service` → `websockify 127.0.0.1:6080`
- `smm-worker.service` → `render-worker-env.sh`, lalu Node RPC `:8090`
- nginx `0.0.0.0:8000`

Verifikasi:

```bash
boxd machine exec smm-worker-golden -- \
  'systemctl is-active smm-worker smm-xvfb smm-x11vnc smm-novnc nginx; \
   curl -fsS http://127.0.0.1:8090/healthz; echo; \
   curl -fsS http://127.0.0.1:8000/healthz; echo; \
   curl -fsS -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8000/novnc/vnc.html'
```

### 3.2 Snapshot

```bash
boxd machine config set smm-worker-golden idle.ignore-outbound on
boxd machine config set smm-worker-golden auto-suspend.timeout 300
boxd machine config set smm-worker-golden auto-hibernate.timeout 3600
boxd snapshots save smm-worker-golden smm-golden-v1 --json
boxd machine stop smm-worker-golden
```

### 3.3 11 worker dari snapshot

```bash
for i in $(seq -w 1 10); do
  VM="smm-worker-${i}"
  boxd machine new "$VM" --from-snapshot smm-golden-v1 \
    --label app=smm --label role=worker --label slot="$i" --json
  boxd machine config set "$VM" bot-protection off
  boxd machine proxy add rpc --vm "$VM" --port 8000 --no-wait --json
done

boxd machine new smm-login-01 --from-snapshot smm-golden-v1 \
  --label app=smm --label role=login --json
boxd machine config set smm-login-01 bot-protection off
boxd machine config set smm-login-01 auto-suspend.timeout 0
boxd machine config set smm-login-01 auto-hibernate.timeout 0
boxd machine config set smm-login-01 idle.ignore-outbound off
boxd machine proxy add rpc --vm smm-login-01 --port 8000 --no-wait --json
```

**Wajib setelah setiap fork:** snapshot membawa proses worker golden yang
sudah berjalan, jadi `.boxenv` masih berisi `WORKER_ID=smm-worker-golden`.
Restart sekali agar `render-worker-env.sh` menulis identitas hostname baru:

```bash
for VM in smm-login-01 smm-worker-01 smm-worker-02 smm-worker-03 \
          smm-worker-04 smm-worker-05 smm-worker-06 smm-worker-07 \
          smm-worker-08 smm-worker-09 smm-worker-10; do
  boxd machine exec "$VM" -- 'sudo systemctl restart smm-worker'
done
```

Cek satu per satu:

```bash
boxd machine exec smm-worker-03 -- 'grep ^WORKER_ID= /home/boxd/smm/.boxenv; curl -fsS http://127.0.0.1:8090/healthz'
```

### 3.4 Endpoint pool di API

`WORKER_POOL_ENDPOINTS` harus memakai nama yang sama dengan hostname worker:

```text
smm-worker-03=https://rpc.smm-worker-03.boxd.sh|https://smm-worker-03.boxd.sh/novnc
```

API menyimpan `rpc.*` sebagai endpoint push dan `*.boxd.sh/novnc` sebagai
live-view. `smm-login-01` harus ada di daftar itu; jangan hanya mengandalkan
`LOGIN_WORKER_NAME`.

---

## 4. Live view / noVNC (dua bug yang sudah diperbaiki)

1. **Path socket harus relatif.** noVNC membentuk URL `<wss>://host/` + `path`.
   API sekarang mengirim `websockify?ticket=…` untuk worker root dan
   `novnc/websockify?ticket=…` untuk worker yang dipasang di `/novnc`. Bentuk
   lama `/websockify` menjadi `//websockify` dan 404.
2. **`auth_request` tidak menginterpolasi variabel di URI-nya.** Nginx worker
   harus menyalin query ticket ke variabel yang dibaca subrequest:

```nginx
location = /novnc/websockify {
    set $smm_ticket $arg_ticket;
    auth_request /_auth_ticket;
    proxy_pass http://smm_novnc/websockify;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
}

location = /_auth_ticket {
    internal;
    proxy_pass http://smm_rpc/internal/verify-ticket?ticket=$smm_ticket;
    proxy_pass_request_body off;
    proxy_set_header Content-Length "";
}
```

Verifikasi (browser memakai HTTP/1.1; curl HTTP/2 akan terlihat 404 karena
tidak mengirim upgrade):

```bash
# health semua worker
for VM in smm-login-01 smm-worker-01 smm-worker-10; do
  curl -fsS "https://rpc.$VM.boxd.sh/healthz"
done

# WebSocket tanpa ticket harus 403; dengan ticket valid harus 101
# (ticket dibuat API lewat POST /api/accounts/<id>/live-view)
```

Bukti browser yang sudah dijalankan: noVNC membuka canvas dan
`Connected (encrypted) to …:99` pada worker login.

---

## 5. Operasional & biaya

- `smm-hub` dan `smm-login-01`: `auto-suspend=0`, `auto-hibernate=0`.
- `smm-worker-*`: `idle.ignore-outbound=on`, suspend 300s, hibernate 3600s.
- `smm-worker-golden` tetap `stopped`; snapshot `smm-golden-v1` adalah sumber
  rebuild.
- Cek biaya/inventory:
  ```bash
  boxd machine list --json
  boxd manage usage --since 7d
  ```
- Jangan `remove` snapshot atau hub tanpa backup `.env` dan volume
  `smm_pgdata`; session akun ada di Postgres, bukan di disk worker.
