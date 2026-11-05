# 06 — Panduan Infrastruktur & Deployment `boxd` (Golden Worker + Control Plane)

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
** selaras dengan:** Catatan Infra `boxd` (`boxd-recap-smm-migration.md`) & `boxd-cli`

> [!CAUTION]
> **Aturan Kerahasiaan Kredensial (Wajib Dipatuhi Agent & Operator):**
> - **DILARANG KERAS** mencetak isi `~/.config/boxd/env`, `~/.config/cloudflare/*`, `.env`, atau `.boxenv` ke terminal/chat/log.
> - Gunakan `set -a; . ~/.config/boxd/env; set +a` di dalam subshell, dan kirim file konfigurasi rahasia hanya melalui `boxd env push <vm> --file <path> --dest <dest> --mode 600`.
> - Jangan menyentuh mesin produksi eksisting (`ahdirmai` dan `speech`).

---

## 1. Topologi Mesin di `boxd` (Region `fra`)

Seluruh mesin diberi label `app=smm` agar mudah diinventarisasi dan diaudit biayanya secara terpisah dari proyek lain:

| Nama Mesin | Ukuran | Role & Label | Pengaturan Tidur (`boxd`) | Bot Protection | Isi Layanan |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`smm-hub`** | `2vCPU / 8G` | `app=smm,role=hub` | **Always-On** (`auto-suspend=0`, `auto-hibernate=0`) | `off` | Docker (`postgres:5432`, `redis:6379`, `minio:9000` bind `127.0.0.1`), `pm2` (`smm-api:8080`, `smm-web:3000`), `nginx:8000` |
| **`smm-worker-golden`** | `2vCPU / 8G` | `app=smm,role=golden` | Di-`stop` setelah snapshot diambil | `off` | Template sumber: `Xvfb :99`, `x11vnc`, `websockify:6080`, Playwright Chromium, `@smm/worker:8090`, `nginx:8000` |
| **`smm-worker-01` .. `10`** | `2vCPU / 8G` (x10) | `app=smm,role=worker` | **`idle.ignore-outbound=on`**, `auto-suspend=300` (5 mnt), `auto-hibernate=3600` (1 jam) | `off` | Dibuat dari snapshot `smm-golden-v1` (`<200 ms`), bangun otomatis saat menerima HTTPS Push |

---

## 2. Setup Mesin Control Plane (`smm-hub`)

### 2.1 Pembuatan & Penguncian Konfigurasi VM
```bash
set -a; . ~/.config/boxd/env; set +a

boxd machine new smm-hub \
  --region fra \
  --vcpu 2 --memory 8G \
  --auto-suspend-timeout=0 \
  --auto-hibernate-timeout=0 \
  --label app=smm --label role=hub \
  --json

boxd machine config set smm-hub bot-protection off
boxd machine proxy set-port --vm smm-hub --port 8000
```

### 2.2 Arsitektur Layanan di Dalam `smm-hub` (Mengikuti Pattern Produksi §4)
1. **Docker Data Services (Hanya Bind `127.0.0.1` di dalam `smm-hub`):**
   - `timescale/timescaledb:2.17.2-pg16` -> `127.0.0.1:5432:5432`
   - `redis:7-alpine --appendonly yes` -> `127.0.0.1:6379:6379`
   - `pgsty/minio` -> `127.0.0.1:9000:9000`
   *(Tidak ada database atau Redis yang terbuka ke publik!)*
2. **`pm2` + `.boxenv` + `ecosystem.hub.config.cjs`:**
   - Upload file rahasia via `boxd env push smm-hub --file .local-runtime/boxenv/hub.boxenv --dest /home/boxd/smm/.boxenv --mode 600`.
   - `smm-api` mendengarkan di `127.0.0.1:8080`.
   - `smm-web` (Next.js standalone) dijalankan dengan `HOSTNAME=0.0.0.0 PORT=3000`.
   - Aktifkan autostart tahan-reboot:
     ```bash
     sudo env PATH=$PATH:/usr/local/bin pm2 startup systemd -u boxd --hp /home/boxd && pm2 save
     ```
   - Ingat aturan §4.3: setiap kali memperbarui `.boxenv`, restart dengan:
     ```bash
     set -a; . /home/boxd/smm/.boxenv; set +a && pm2 restart /home/boxd/smm/ecosystem.hub.config.cjs --update-env
     ```
3. **`nginx` Dispatch di `0.0.0.0:8000`:**
   - `/api/*` dan `/internal/*` -> `proxy_pass http://127.0.0.1:8080` (`client_max_body_size 5m;` untuk menampung callback session + screenshot).
   - `/*` -> `proxy_pass http://127.0.0.1:3000` (Next.js Web).
4. **Custom Domain Produksi Terkunci (`smm.ahdirmai.my.id` — Ikuti Urutan Wajib §1 & §7.4 Sebelum Spawn Worker):**
   1. Pasang DNS di Cloudflare **terlebih dahulu** dengan mode **DNS-only (grey cloud)**:
      - `A smm` -> `152.236.3.23`
   2. Baru jalankan saat setup `smm-hub` (sebelum men-spawn `smm-worker-01..10`, karena worker akan memakai `API_URL=https://smm.ahdirmai.my.id`):
      ```bash
      boxd machine domain add smm.ahdirmai.my.id --vm smm-hub -y
      ```

---

## 3. Resep Pembuatan Golden Worker & 10 Worker Utama

### 3.1 Bangun 1 `smm-worker-golden`
```bash
set -a; . ~/.config/boxd/env; set +a

boxd machine new smm-worker-golden \
  --region fra \
  --vcpu 2 --memory 8G \
  --auto-suspend-timeout=0 \
  --auto-hibernate-timeout=0 \
  --label app=smm --label role=golden \
  --json

boxd machine config set smm-worker-golden bot-protection off
```
*(Catatan: selama proses instalasi paket yang panjang, `auto-suspend` & `auto-hibernate` diset `0` agar tidak tertidur di tengah instalasi Chromium!)*

Komponen yang dipasang satu kali di `smm-worker-golden`:
1. Paket sistem display virtual ringan: `xvfb`, `x11vnc`, `novnc`, `websockify`, `fonts-noto-color-emoji`, `fonts-liberation`.
2. Systemd unit untuk layar virtual (otomatis jalan saat boot/wake):
   - `xvfb.service`: `Xvfb :99 -screen 0 1280x800x24 -ac +extension RANDR`
   - `x11vnc.service`: `x11vnc -display :99 -localhost -forever -shared -nopw -rfbport 5900`
   - `novnc.service`: `websockify --web=/usr/share/novnc 127.0.0.1:6080 127.0.0.1:5900`
3. Build `@smm/worker` + `npx playwright install --with-deps chromium`, dikelola `pm2` (`DISPLAY=:99`, `TZ=Asia/Jakarta`, listen RPC `127.0.0.1:8090`).
4. **`nginx` di `0.0.0.0:8000` (Gerbang Tunggal Worker VM):**
   - `location /internal/` -> `proxy_pass http://127.0.0.1:8090` (worker memverifikasi header `X-Internal-Secret`).
   - `location = /_auth_ticket` -> `internal; proxy_pass http://127.0.0.1:8090/internal/verify-ticket;`
   - `location /novnc/` -> dilindungi `auth_request /_auth_ticket;` lalu meneruskan HTTP & WebSocket Upgrade (`Upgrade` / `Connection`) ke `http://127.0.0.1:6080/`.

### 3.2 Konfigurasi Sleep hemat-biaya & Ambil Snapshot `smm-golden-v1`
Setelah `smm-worker-golden` teruji sehat (`curl -s http://localhost:8000/healthz`), pasang konfigurasi hemat biaya sebelum mengambil snapshot (karena mesin hasil `--from-snapshot` mewarisi setting sumbernya!):

```bash
boxd machine proxy set-port --vm smm-worker-golden --port 8000
boxd machine config set smm-worker-golden bot-protection off
boxd machine config set smm-worker-golden idle.ignore-outbound on
boxd machine config set smm-worker-golden auto-suspend.timeout 300
boxd machine config set smm-worker-golden auto-hibernate.timeout 3600

# Simpan Golden Snapshot
boxd snapshots save smm-worker-golden smm-golden-v1 --json

# Matikan mesin template agar tidak memakan biaya running
boxd machine stop smm-worker-golden --json
```

### 3.3 Meluncurkan 10 Worker Utama (`smm-worker-01` s/d `smm-worker-10`)
```bash
for i in $(seq -w 1 10); do
  VM="smm-worker-${i}"
  boxd machine new "$VM" \
    --from-snapshot smm-golden-v1 \
    --label app=smm \
    --label role=worker \
    --label slot="$i" \
    --json

  # Tambahkan named proxy rpc.<vm>.boxd.sh (bebas bot-protection oleh desain boxd)
  boxd machine proxy add rpc --vm "$VM" --port 8000 --no-wait --json

  # Push konfigurasi rahasia per worker (mode 600, tanpa pernah mencetak isi file)
  boxd env push "$VM" --file ".local-runtime/boxenv/${VM}.boxenv" --dest /home/boxd/smm/.boxenv --mode 600 --json
  boxd machine exec "$VM" -- 'set -a; . /home/boxd/smm/.boxenv; set +a && pm2 restart /home/boxd/smm/ecosystem.worker.config.cjs --update-env && pm2 save'
done
```

---

## 4. Estimasi Pemakaian Kredit ($150 Balance)

Berkat perpaduan **Push-on-Run** + **`auto-suspend` (5 menit)** + **`auto-hibernate` (1 jam)** di `boxd`:
- **`smm-hub` (`2vCPU/8G` Always-On):** Seperti terukur pada mesin produksi `ahdirmai` (`2vCPU/8G` yang memakan hanya `~$0.19` selama berhari-hari karena `boxd` hanya menagih **vCPU aktif + RAM yang benar-benar terpakai**), `smm-hub` yang sebagian besar menunggu I/O sangat irit.
- **`smm-worker-01..10` (`2vCPU/8G` x 10):** Hanya menagih CPU/RAM pada detik-detik saat job Playwright berjalan + 5 menit ekor idle sebelum masuk `standby` (`$0 CPU & $0 RAM`). Di malam hari atau di sela jadwal kampanye, ke-10 worker tidur penuh.
- Saldo kredit **$150** sangat leluasa untuk mengoperasikan seluruh klaster ini dalam jangka panjang; tetap jalankan audit mingguan menggunakan:
  ```bash
  boxd manage usage --since 7d
  ```
