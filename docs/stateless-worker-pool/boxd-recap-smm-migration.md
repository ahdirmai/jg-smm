# boxd — Rekapan Lingkungan untuk Migrasi Arsitektur SMM

> **Terakhir diperbarui:** 2026-11-04 23:59 WITA · **Organisasi:** Ridha's default org (admin)
> **Pengelola:** via `boxd` CLI dari Mac (lihat §8 untuk kredensial)
> **Konteks:** dokumen ini untuk Agent yang mengerjakan migrasi arsitektur SMM ke boxd.
> **Aturan kunci:** dokumen ini TIDAK memuat nilai rahasia — hanya lokasi file. Jangan pernah print/paste kunci ke chat/log.

---

## 0. TL;DR

- **boxd** = microVM persisten (hardware-isolated, root access, Docker preinstalled, boot ±5–10 ms, **hibernate saat idle & wake otomatis ±85 ms**).
- **Credit:** $150; pemakaian s.d. dokumen ini dibuat = **$0.36 total** (3 mesin, rincian §3).
- **Limit org:** 100 mesin per region · region tersedia: **fra (Frankfurt) & nyc (New York)** · size: `1vCPU/4G`, `2/8G`, `4/16G`, `8/32G`.
- **Mesin aktif saat ini:** `ahdirmai` (situs produksi), `speech` (VoiceStudio), `belajar` (sandbox).
- **CLI & auth:** `~/.local/bin/boxd`; token di `~/.config/boxd/env` → pakai `set -a; . ~/.config/boxd/env; set +a`.
- **Docs/skills untuk agent:** `https://docs.boxd.sh/llms.txt` + skill `boxd-cli` & `boxd-setup-hermes` (terpasang di `~/.claude/skills`, `~/.agents/skills`, `~/.hermes/skills`).

---

## 1. Konsep boxd yang wajib dipahami

| Konsep | Inti |
|---|---|
| **State** | `running` → `standby` (auto-suspend, off default) → `hibernated` (auto-hibernate, **default 4 jam idle**) → `stopped` |
| **Wake** | Hibernated: ±85 ms pada koneksi baru lewat boxd (HTTPS/SSH/exec). **Bukan** lewat raw port, **bukan** dari cron internal |
| **Tagihan** | Per detik. running = vCPU *aktif* + RAM terpakai + disk ditulis · hibernated = **disk saja** |
| **Idle timer** | Menghitung lalu-lintas jaringan (2 arah). Kerja CPU tanpa network = "dianggap idle" (clock berhenti saat tidur; cron tak jalan) |
| **Bot protection** | Default ON — memblokir curl/webhook yang mau membangunkan mesin. Untuk mesin server: `boxd machine config set <vm> bot-protection off` |
| **Custom domain** | DNS dulu (A @ → `152.236.3.23`; CNAME * → `<mesin>.boxd.sh`; **grey/DNS-only**) → baru `boxd machine domain add <domain> --vm <vm> -y` → verifikasi ±1 menit → cert GTS terbit otomatis |
| **Fork/snapshot** | Fork = kopi hidup (disk+RAM+proses) <200 ms · snapshot = titik awal reusable; keduanya mewarisi setting mesin sumber |
| **Mesh antar-mesin** | Semua mesin satu org berbagi private network default; bisa dipersempit lewat `networks` |

---

## 2. Perintah CLI yang paling sering dipakai

```bash
# identitas & inventaris
boxd auth                          # siapa & org mana
boxd machine list --json           # semua mesin
boxd machine get <vm>              # detail (status, size, ip, timeout, dll)

# siklus hidup
boxd machine new <nama> --vcpu 2 --memory 8G
boxd machine fork <src> <nama-baru>
boxd snapshots save <vm> <nama-snapshot>
boxd machine new <baru> --from-snapshot <nama-snapshot>
boxd machine hibernate|wake|pause|resume <vm>
boxd machine remove <vm> -y        # -y wajib di non-TTY

# operasi
boxd machine exec <vm> -- 'cmd'    # stdin forward; --timeout N; --json
boxd machine cp <lokalfile> <vm>:<path>
ssh <vm>.boxd                      # alias auto-sync ke ~/.ssh/config

# web / domain
boxd machine proxy set-port --vm <vm> --port 3900
boxd machine proxy add <nama> --vm <vm> --port 3020     # nama.<vm>.boxd.sh
boxd machine domain add <domain> --vm <vm> -y
boxd machine domain list

# biaya
boxd manage usage                  # pemakaian per mesin
boxd manage billing activity       # saldo & mutasi
```

---

## 3. Inventaris mesin (status 2026-11-04)

| Mesin | Size | Status | Isi | Biaya s.d. kini |
|---|---|---|---|---|
| `ahdirmai` | 2×8G | running | **Produksi**: pm2 ×5 (ahdirmai web:8002, gw:8080, aigw:8090; foundme web:3020, api:4010) + nginx:8000 (dispatch by host) + Docker ×4 (postgres:5432, redis:6379, minio:9000/9001, mysql:3308) | $0.1948 |
| `speech` | 4×16G | running | **VoiceStudio** (Docker `omnivoice`, port 3900, model cache 3,1 GB) | $0.1640 |
| `belajar` | 1×4G | hibernated | Sandbox kosong (nginx) — bebas dipakai/dihapus | $0.0016 |

- Domain yang mengarah ke `ahdirmai`: `ahdirmai.my.id`, `www.ahdirmai.my.id`, `foundme.ai.id` (A record → `152.236.3.23`, DNS-only).
- Domain `speech`: `speech.ahdirmai.my.id`.
- **IP publik kedua mesin sama** (`152.236.3.23` — shared edge boxd; routing berdasar hostname/SNI). **Egress keluar berbeda per mesin** — lihat §6.

---

## 4. Pattern yang sudah terbukti di produksi (WAJIB diikuti)

1. **Satu mesin, banyak app → nginx dispatch di :8000** (port default proxy boxd):
   `listen 8000 default_server` → app A; `server_name domainB` → app B. Custom domain boxd **selalu** masuk ke port default proxy.
2. **Bind ke `0.0.0.0`, BUKAN `127.0.0.1`.** Next.js standalone: set `HOSTNAME=0.0.0.0`. Kalau bind loopback → **502 dari edge boxd**.
3. **pm2 + ecosystem file:** sumber env via `.boxenv` (`set -a; . ./.boxenv; set +a`) **sebelum** start. `pm2 restart <nama>` TIDAK membaca ulang file — pakai `pm2 restart ecosystem.config.cjs --update-env`.
4. **Autostart pm2** (bertahan reboot/restore):
   `sudo env PATH=$PATH:/usr/local/bin pm2 startup systemd -u boxd --hp /home/boxd && pm2 save`
5. **Install global pakai `--prefix /usr/local`**: `sudo npm i -g --prefix /usr/local <pkg>` (plain `sudo npm i -g` masuk ke prefix root nvm & tak ada di PATH).
6. **Yang sudah preinstall di setiap mesin:** Docker (29.x), Node 24 (+ corepack → `pnpm`), Python 3 + uv, git, gh, ffmpeg, jq, ripgrep, Chrome headless, nginx 1.24, **Claude Code & Codex**, bun (`/usr/local/bin/bun`). User: `boxd` (sudo tanpa password). Ubuntu 24.04.

---

## 5. Transfer data (pelajaran mahal)

- **Mac ↔ boxd:** `boxd machine cp` — streaming, andal, sampai >1 GiB OK.
- **Antar-mesin (bedakan provider / relay goyah):** jangan tar-stream lewat SSH relay (putus ±2 MB). Pola yang terbukti: **receiver HTTP mini di mesin tujuan** (python http.server + token one-time + PUT → simpan file + balas sha256 di body) → sumber `curl -T` langsung ke `https://<domain-boxd>/u/<token>`. Terukur **2 MB/s, 34 MB dalam 16 detik, sha256 terverifikasi**.
  - Script pola: `recv.py` (di `/tmp/boxd-stage/` Mac) — bisa dipakai ulang & dimodifikasi.
- **boxd → boxd tidak ada jalur langsung** — lewat perantara (Mac) atau pola receiver di atas via internet.

---

## 6. Catatan khusus skenario IG / 10 mesin Playwright (fakta terverifikasi)

> Keputusan operator: lanjut dengan **10 mesin + Playwright + akun berbeda**. Bagian ini mencatat fakta lingkungannya supaya keputusan & desain bisa diambil secara sadar-risiko.

**Network (diukur langsung):**
- Egress IP tiap mesin **berbeda** (contoh terukur: `67.213.121.191` dan `109.94.96.51`).
- **TAPI semua dari ASN `AS396356 — Latitude.sh` (datacenter/hosting).** Meta menandai ASN hosting berbasis range, sebelum analisis perilaku.
- IP boxd dipakai-bersama antar tenant (risiko "kontaminasi reputasi").
- boxd **tidak menyediakan** IP residential/mobile — kebutuhan IP trust harus dari provider pihak ketiga.

**Arsitektur fleet yang efisien di boxd:**
- Bikin **1 "golden worker"**: semua dependency (Playwright, Chromium, skrip, profile dirs) terpasang & teruji → `snapshots save golden` → mesin worker dibuat dengan `--from-snapshot` / `fork` (**<200 ms**, proses & state ikut) — jangan setup 10× manual.
- **1 akun per mesin** memaksimalkan pemisahan IP (sesuai keputusan); jangan campur banyak akun di 1 mesin.
- Sizing worker browser: mulai `2vCPU/8G` (Chromium + Node cukup); naikkan ke `4/16G` bila banyak tab/paralel.
- **Hibernation:** job yang dipicu lewat `boxd machine exec` / SSH **membangunkan** mesin otomatis; worker yang selesai kerja akan hibernate sendiri (murah). Untuk job panjang tanpa traffic, set `auto-hibernate.timeout 0` agar tidak tidur di tengah kerja.
- Mesin sekali-pakai: `--delete-after 2h` sebagai jaring aman + `boxd machine remove` di akhir job.
- Region: **fra** (paling dekat dari 2 pilihan).
- Biaya: contoh riil sekarang — seluruh org baru memakai **$0.36**; 10 worker aktif beberapa jam/hari juga masih kecil dibandingkan biaya IP/proxy pihak ketiga (yang menjadi komponen mahal di skenario ini).

**Realita deteksi (ringkas — untuk kesadaran desain, bukan panduan):**
- Lapisan deteksi platform: (1) reputasi IP/ASN datacenter, (2) fingerprint browser (Playwright mudah dikenali fingerprint vanilla: webdriver flags, TLS/HTTP2, canvas/WebGL), (3) perilaku (irama aksi seragam/superhuman), (4) **clustering akun** — akun di-link via IP/ASN/fingerprint/perilaku; 1 kena → merambat ke yang ter-link.
- Jalur resmi (untuk bagian yang bisa): akun **Creator** (bukan harus Business) + API resmi → publish/insights/DM tanpa proxy, jalan mulus dari boxd. Cocok untuk memindahkan beban "konten & DM" keluar dari fleet browser.

---

## 7. Jebakan yang sudah pernah memakan korban

1. Nama mesin reserved: `demo`, `hello` ditolak — siapkan penamaan yang aman.
2. Bot protection default ON → curl/API tak bisa membangunkan mesin; set `off` untuk mesin server.
3. Non-TTY: semua argumen harus eksplisit + `-y` untuk aksi destruktif.
4. Custom domain: DNS **harus** sudah ada sebelum `domain add`; record **grey (DNS-only)** — proxied bisa mengganggu verifikasi.
5. Hibernasi = clock berhenti: cron internal TIDAK fire saat tidur (jadwal platform/`exec` yang membangunkan).
6. Kerja CPU panjang tanpa network lama-lama dianggap idle → hibernasi di tengah job (set timeout 0 untuk job panjang).
7. `pm2 restart <nama>` tidak membaca ulang ecosystem file → env baru tidak terpasang (pakai path file + `--update-env`).
8. Next standalone bind `127.0.0.1` = 502 dari edge (lihat §4.2).
9. Fork/snapshot mewarisi setting sumber (termasuk auto-hibernate) — periksa tiap kali.

---

## 8. Kredensial & dokumen (LOKASI saja — isi tidak boleh ditampilkan)

| Item | Lokasi |
|---|---|
| Token boxd CLI | `~/.config/boxd/env` (`BOXD_TOKEN=...`) |
| Cloudflare API / tunnel token | `~/.config/cloudflare/api.token`, `~/.config/cloudflare/tunnel.token` |
| VoiceStudio admin key | `~/.config/voicestudio/env` |
| Skill boxd untuk agent | `~/.hermes/skills/boxd-cli` (beserta panduan multi-domain nginx deploy di folder `references/`), `boxd-setup-hermes` |
| Dokumen platform | `https://docs.boxd.sh/llms.txt` · `https://boxd.sh/pricing` |
| Catatan migrasi situs (referensi pola) | `~/.cache/boxd-migrate/` (rollback DNS, script staging) |

**Aturan kredensial:** tidak pernah di-print/di-paste ke chat/log; transfer hanya lewat file (mode 600) atau `--env-file`; `boxd machine cp` streaming (bukan argv).

---

## 9. Rekomendasi langkah awal untuk Agent

1. Verifikasi akses: `boxd auth` + `boxd machine list` (harus 3 mesin: ahdirmai, speech, belajar).
2. Buat **golden worker** (Playwright + Chromium + skrip worker + profile isolation) di mesin size `2/8G`, test 1 akun end-to-end.
3. `boxd snapshots save golden` → fork untuk worker ke-2..10 (nama: `smm-worker-01..10`), masing-masing 1 akun.
4. Orkestrasi dari Mac (atau mesin orkestrator terpisah) via `boxd machine exec`; catat mesin mana ↔ akun mana (file mapping lokal, bukan di chat).
5. Pantau biaya: `boxd manage usage` (mingguan) — alarm alami kalau ada worker yang lupa dimatikan.
6. Untuk komponen bernilai tinggi (konten/DM), pertimbangkan paralel jalur **Creator + API** agar tidak semua beban bergantung pada fleet browser.
7. Simpan semua script/setup di repo/snapshot — jangan mengandalkan state satu mesin.

---

*Dokumen ini dibuat otomatis dari lingkungan boxd aktual (terverifikasi). Semua angka berasal dari pengukuran langsung pada 2026-11-04.*
