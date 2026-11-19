# Remediation Plan — MVP-1-SMM audit 2026-09-29

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal** Menghapus tiga cacat SEV1 (verdict action mati total, job mengunci selamanya, kunci kredensial ter-commit) dan enam SEV2, lalu menutup gap CI/test yang membiarkan semuanya lolos.

**Architecture** Tidak ada restrukturisasi. Setiap tugas memperbaiki satu kondisi di satu lapisan, menambah test yang **dapat gagal** untuk kondisi itu, lalu commit. Tidak ada abstraksi baru, tidak ada dependensi baru, tidak ada perubahan kontrak HTTP publik kecuali yang disebut eksplisit.

**Tech Stack** Go 1.26 (Echo v4, pgx v5, sqlc, golang-migrate), Node 22 (Playwright, `node --test`), Next.js 15, Redis 7, MinIO, docker compose.

**Spec** `AUDIT_REPORT_2026-09.md` — setiap tugas di bawah menunjuk nomor temuan (F-nn) di sana. **Baca audit report sebelum mulai; plan ini tidak mengulang buktinya.**

**Companion plan** Tidak ada. Remediasi `../AUDIT_REPORT.md` (2026-09-18) sudah selesai — lihat §6 audit report. Plan ini tidak menyentuhnya.

---

## Global Constraints

- **Satu tugas = satu commit.** Kalau sebuah tugas butuh dua commit, itu dua tugas.
- **Tidak ada file existing yang diedit tanpa test yang gagal lebih dulu.** Kalau sebuah perubahan tidak bisa langsung diverifikasi, tulis test yang observably merah, lalu hijau.
- **TDD, tidakbolak-balik.** Steps sudah disusun sebagai: tulis test merah → jalankan, lihat merah → implementasi minimum → jalankan, lihat hijau → commit.
- **Tidak ada nilai rahasia di repo.** Setelah Task 3, `compose.yaml` **tidak boleh** memuat nilai default untuk `CREDENTIAL_KEY`/`JWT_SECRET`/`POSTGRES_PASSWORD`/`MINIO_ROOT_PASSWORD`. Kalau `make up` gagal karena env belum di-set, itu benar, bukan regresi.
- **Tidak ada `UPDATE`/`DELETE` terhadap stack yang sedang berjalan** selama mengerjakan plan ini, kecuali Task 3 Step 6 (rotasi key) yang butuhoperator explicit. Jangan pernah menjalankan migrasi terhadap DB non-test tanpa_window yang benar.
- **Port bind loopback** setelah Task 4: setiap service yang punya `ports:` di `compose.yaml` harus prefixed `127.0.0.1:`.
- **Naming** per `DEVELOPMENT_RULE §2`: `snake_case.go`, `kebab-case.ts`, test `*_test.go` / `*.test.ts`.
- **Sekret tidak pernah masuk log.** Kalau sebuah task menambah logging, `screenshot`/`token`/`cookie`/`password` dilarang sebagai argumen `slog` (DEVELOPMENT_RULE §5, §7.6).
- **Tidak menambah dependensi.** Semua yang dibutuhkan sudah terpasang.
- **Tidak menyentuh `apps/api/internal/domain` dari luar** — package itu wajib nol-dependency (§1).

## Review Focus

Lima kelas kegagalan yang paling mungkin menggigit orang yang memakai produk ini. Tiap baris punya test-nya di tugas pemiliknya — kalau baris ini tidak punya test di task mana pun, itu bug di plan ini, bukan di kode.

1. **Callback hilang, verdict hilang, job nyangkut.** Worker mati di tengah satu aksi → tidak ada yang pernah menulis verdict → job `RUNNING` selamanya, dan operator menekan Retry → aksi terjadi dua kali di akun IG asli. → Task 2 + Task 11.
2. **Screenshot mulai bekerja dan semuanya langsung mati.** fixing F-02b membuat CDP capture sukses → setiap callback jadi `.jpg` → API tolak 400 → 100% action hilang._fix F-01 harus mendarat **sebelum** Task 2b. → Task 1.
3. **Kredensial akun terbaca offline.** Dump Postgres (backup, port terbuka, atau `git clone` + key yang ter-commit) cukup untuk mengambil alih semua akun. → Task 3.
4. **Antrean bilang "selesai" padahal tidak.** 300 job, halaman tampil 50, semua angka dihitung dari 50 itu, `failed > 0` juga terpotong → operator percaya queue mengering. → Task 12.
5. **Reply Threads hilang tanpa jejak.** `includeReplies` tidak pernah dikirim di jalur keyword → 56 post, 0 comment, angka 0 berdiri sebagai fakta di dashboard. → Task 9.

---

## File Structure

| File | Tanggung jawab (setelah plan ini) |
|---|---|
| `apps/worker/src/core/screenshot.ts` | Satu-satunya producer nama file screenshot. Extension di sini adalah kontrak dengan API. |
| `apps/api/internal/http/internal.go` | Validasi trust boundary `/internal/*`: body cap, screenshot ext, `worker_id` length, field truncation. |
| `apps/api/internal/service/job_sweeper.go` (baru) | Pemulihan job `RUNNING` yang menggantung. Ditempel di `main.go` di samping reconciler. |
| `apps/api/internal/service/action.go` | `RunPendingNow` pindah ke keyset pagination, tanpa offset. |
| `apps/api/db/queries/action.sql` | Query baru `RescheduleDueActionJobs` (keyset) + predikat status pada `CompleteActionJob`. |
| `apps/api/internal/service/safeurl.go` (pindah dari `action.go`) | SSRF guard tunggal: skema, host allowlist per platform, IP hasil resolusi. Dipakai enqueue **dan** scrape target. |
| `apps/api/internal/http/worker_auth.go` (baru) | Middleware token worker untuk grup `/internal`. |
| `apps/api/internal/adapter/crypto/aesgcm.go` | Tambah key-version byte + AAD, supaya rotasi (Task 3) punya jalur migrasi. |
| `apps/api/internal/http/errors.go` | Kontrak error tunggal. Handler **tidak** pernah lagi mengarang `HTTPError` dengan `err.Error()`. |
| `apps/web/app/(dashboard)/actions/page.tsx` | Halaman antrean: jujur soal loading/error/truncasi, retry untuk `PENDING` basi. |
| `apps/web/lib/api.ts` | Klien HTTP. `limit` eksplisit di mana pun server punya cap. |
| `compose.yaml` | Loopback bind, `requirepass`, env wajib (`:?required`), digest pin, resource limit. |
| `scripts/check-credentials.sh` | Scan seluruh tree termasuk YAML/env, bukan hanya `apps`/`packages`. |
| `.github/workflows/ci.yml` | Tambah `govulncheck`, `sqlc-check`, e2e, trivy image, FE unit. |

---

# Phase 0 — SEV1 (ship blocker)

## Task 1: Kontrak ekstensi screenshot worker↔API (F-01)

**Files:**
- Modify: `apps/worker/src/core/screenshot.ts:21,31,37`
- Test: `apps/worker/src/test/screenshot.test.ts` (baru)
- Test: `apps/api/internal/http/internal_test.go` (tambah satu kasus)

**Interfaces:**
- Consumes: `sanitiseScreenshot(p *string) (string, error)` di `apps/api/internal/http/internal.go:297`.
- Produces: `export const SCREENSHOT_EXT = '.png'` di `screenshot.ts`, dipakai worker test; `screenshotExt` di Go tetap `".png"`.

- [ ] **Step 1: Tulis test worker yang meng-assert extension, bukan hanya `truthy`.**

Buat `apps/worker/src/test/screenshot.test.ts`:
```ts
import { test, assert } from 'node:test';
import { SCREENSHOT_EXT } from '../core/screenshot.js';

test('screenshot basename uses the extension the API accepts', () => {
  assert.equal(SCREENSHOT_EXT, '.png', 'API sanitiseScreenshot only accepts .png (internal.go:32)');
  assert.match(`task-abc-worker-1${SCREENSHOT_EXT}`, /\.png$/);
});
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/worker && pnpm test 2>&1 | grep SCREENSHOT_EXT`
Harvest: `SCREENSHOT_EXT` tidak diekspor → test gagal compile. Itu bukti yang benar.

- [ ] **Step 3: Ubah `screenshot.ts` agar `.png` dan ekspor konstantanya**

`screenshot.ts:21` (komentar), `:31`, `:37`:
```ts
/**
 * Extension MUST match the API's `screenshotExt` (internal.go:32) — a .jpg name
 * is rejected with 400 and the verdict is lost. Contract test:
 * src/test/screenshot.test.ts.
 */
export const SCREENSHOT_EXT = '.png';

const basename = `task-${id}-${workerId}${SCREENSHOT_EXT}`;
// ... format: 'png' (drop quality, meaningless for PNG)
```

- [ ] **Step 4: Jalankan test worker, harus HIJAU**

`cd apps/worker && pnpm test` → semua hijau (61 test existing + 1 baru).

- [ ] **Step 5: Tambah kasus Go yang mengikat API ke `.png`**

Di `apps/api/internal/http/internal_test.go`, tambahkan `TestActionCallbackAcceptsWorkerScreenshotName` yang mengirim `screenshot_path: "task-1-w1.png"` dan **meng-assert `screenshot` terisi** — bukan hanya 202. Ikuti pola `newTestInternal` di `:234`, tapi kali ini isi `ActionStore` dengan fake yang mengembalikan error pada `UpsertActionLog` sehingga `RecordAttempt` berhenti **setelah** `sanitiseScreenshot` dan capture hasilnya sebagai `ActionLog.ScreenshotURL`; assert `== "task-1-w1.png"`. Ini menggantikan kelemahan `TestActionCallbackScreenshotPathBase` yang hijau karena alasan salah (lihat audit §4).

- [ ] **Step 6: Jalankan test Go, harus HIJAU**

`cd apps/api && go test ./internal/http/ -run 'TestActionCallbackAcceptsWorkerScreenshotName|TestActionCallbackScreenshotPathBase' -count=1 -v`
Harvest: `--- PASS` untuk keduanya.

- [ ] **Step 7: Ubah `TestActionCallbackScreenshotPathBase` supaya menguji traversal, bukan Accepted**

Test itu sekarang **meng-assert** `../../evidence/shot.png` diterima — persis kebalikan dari yang namanya. Ubah agar `newTestInternal` diberi action store yanguxe bekerja, sehingga 400/422 dari sanitiser benar-benar terukur. Kalau setelah diperbaiki test tidak bisa dibedakan, hapus test itu dan ganti dengan yang meng-assert `../../evil.png` **ditolak** 400.

- [ ] **Step 8: Commit**

```bash
git add apps/worker/src/core/screenshot.ts apps/worker/src/test/screenshot.test.ts \
        apps/api/internal/http/internal_test.go
git commit -m "fix(actions): screenshot extension must match the API contract

screenshot.ts produced .jpg while internal.go:screenshotExt is .png, so every
action callback carrying evidence was rejected 400 and the verdict was never
recorded. Pin both ends with a test that asserts the extension, not truthiness."
```

---

## Task 2: Sweeper job RUNNING yang menggantung (F-02)

**Files:**
- Create: `apps/api/internal/service/job_sweeper.go`
- Create: `apps/api/internal/service/job_sweeper_test.go`
- Modify: `apps/api/db/queries/action.sql` (tambah 1 query)
- Modify: `apps/api/internal/repository/sqlcgen/` (regenerate), `repository/action.go`, `port/action.go`
- Modify: `apps/api/cmd/server/main.go` (wire, dekat `sweeper :=` di `:461`)

**Interfaces:**
- Produces:
  - `port.ActionStore.RescheduleStuckActionJobs(ctx, stuckBefore time.Time, limit int32) (int64, error)` — mengembalikan jumlah baris yang ter-update.
  - `service.NewJobSweeper(jobs port.ActionStore, clock port.Clock, cfg JobSweeperConfig, logger *slog.Logger) *JobSweeper`
  - `type JobSweeperConfig struct { StuckAfter time.Duration; MaxAttempts int; Interval time.Duration; Clock port.Clock; Logger *slog.Logger }`
  - `func (s *JobSweeper) Sweep(ctx context.Context) (int64, error)`
  - `func (s *JobSweeper) Run(ctx context.Context)`

- [ ] **Step 1: Tulis query keyset + test merah**

Tambah di `apps/api/db/queries/action.sql`:
```sql
-- name: RescheduleStuckActionJobs :many
-- F-02: a RUNNING job whose callback never arrived is invisible to every
-- other query (ClaimNextActionJob only ever selects PENDING). Reset it.
UPDATE action_job
SET status = CASE WHEN attempts >= $2 THEN 'FAILED' ELSE 'PENDING' END,
    scheduled_at = now(),
    attempts     = attempts + 1,
    started_at   = NULL
WHERE status = 'RUNNING'
  AND started_at < $1
  AND worker_id IS NOT NULL
  AND NOT EXISTS (
        SELECT 1 FROM worker w
        WHERE w.id = action_job.worker_id
          AND w.last_heartbeat > $1 - interval '90 seconds'
  )
RETURNING id, status, attempts;
```
`$1` = `stuckBefore` (now − 10 menit). Predikat heartbeat sengaja ada supaya job yang **sedang berjalan** pada worker yang hidup tidak di-sweep.

Lalu `apps/api/internal/service/job_sweeper_test.go` dengan `newFakeActionStore()` yang sudah ada di package test:
```go
func TestJobSweeperResetsARunningJobWithNoCallback(t *testing.T) {
	stuck := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	store := newFakeActionStore()
	store.seedJob(domain.ActionJob{ID: "j1", Status: domain.JobStatusRunning, Attempts: 1, StartedAt: &stuck})
	clock := newStepClock()
	clock.set(stuck.Add(11 * time.Minute))
	s := NewJobSweeper(store, clock, JobSweeperConfig{StuckAfter: 10 * time.Minute, MaxAttempts: 3, Clock: clock})

	n, err := s.Sweep(context.Background())
	if err != nil { t.Fatalf("sweep: %v", err) }
	if n != 1 { t.Fatalf("rescheduled %d, want 1", n) }
	if got := store.job("j1").Status; got != domain.JobStatusPending {
		t.Fatalf("status = %s, want PENDING", got)
	}
	if got := store.job("j1").Attempts; got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}
```
Tambah dua kasus lagi di file yang sama: (a) job `RUNNING` dengan `started_at` 2 menit lalu **tidak** tersentuh; (b) job `RUNNING` dengan `attempts == MaxAttempts` berakhir `FAILED`, bukan `PENDING`.

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/service/ -run TestJobSweeper -count=1`
Harvest: `undefined: NewJobSweeper`.

- [ ] **Step 3: Implementasikan**

`job_sweeper.go` —kzvig Ridik Service, bukan god object:
```go
type JobSweeper struct { jobs port.ActionStore; cfg JobSweeperConfig }
```
`Sweep` memanggil `RescheduleStuckActionJobs(ctx, s.cfg.Clock.Now().Add(-s.cfg.StuckAfter), int32(s.cfg.MaxAttempts))`, log baris yang di-return, dan mengembalikan jumlahnya. `Run` adalah `ticker` standar dengan `select { case <-ctx.Done(): ... }` — pola yang sama dengan `orphan_sweeper.go:109-125`; **jSalin bentuknya, jangan tulis ulang**.

Regenerate sqlc (`make -C apps/api sqlc-gen` atau `sqlc generate` sesuai `sqlc.yaml`), tambahkan `RescheduleStuckActionJobs` ke `port.ActionStore` dan implementasinya di `repository/action.go` dengan `fmt.Errorf("repository.action.RescheduleStuckActionJobs: %w", err)`.

- [ ] **Step 4: Jalankan, harus HIJAU**

`cd apps/api && go test ./internal/service/ -run TestJobSweeper -count=1 -v` → 3 PASS.

- [ ] **Step 5: Wire di `main.go`**

Tepat setelah blok sweeper yang sudah ada (`main.go:461`), tambahkan goroutine yang sama dengan guard `if cfg.ReconcileIntervalSeconds > 0`. Log satu baris saat start: `"job sweeper started"`, `"stuckAfter"`, `"interval"`.

- [ ] **Step 6: Commit**

```bash
git add apps/api/db/queries/action.sql apps/api/internal/repository/sqlcgen \
        apps/api/internal/repository/action.go apps/api/internal/port/action.go \
        apps/api/internal/service/job_sweeper.go apps/api/internal/service/job_sweeper_test.go \
        apps/api/cmd/server/main.go
git commit -m "fix(actions): recover RUNNING jobs whose callback never arrived

DEVELOPMENT_RULE:229 requires a sweeper that resets RUNNING > 10min to PENDING
(attempt++) max 3x. It did not exist, so one lost callback wedged a job - and
everything queued behind it - permanently."
```

---

## Task 3: Rotasi dan_fail-loud kunci kredensial (F-03)

> **Task ini mengubah data produksi. Baca Step 6 tiga kali.** Ada window ketika credential tidak bisa didekripsi; lakukan di luar jam sibuk, dan **backup `account.password_enc` sebelum mulai**.

**Files:**
- Modify: `compose.yaml:139,141`
- Modify: `apps/api/internal/config/config.go` (validasi)
- Modify: `apps/api/internal/adapter/crypto/aesgcm.go` (key-version + AAD)
- Test: `apps/api/internal/adapter/crypto/aesgcm_test.go`
- Create: `apps/api/cmd/rekey/main.go` (alat migrasi satu-off)
- Modify: `scripts/check-credentials.sh:20-24`
- Modify: `.gitleaks.toml` (hapus allowlist)

**Interfaces:**
- Produces:
  - `crypto.Sealer` gaining `KeyVersion() byte`.
  - Ciphertext format baru: `byte(version) || nonce(12) || sealed` — reader harus menerima v0 (tanpa prefix) **dan** v1.
  - `cmd/rekey` dengan flag `-old-key`, `-new-key`, `-dsn`, meng-decrypt dengan key lama lalu re-encrypt dengan key baru, satu batch per transaksi, dan **idempoten** (baris yang sudah v1 dilewati).

- [ ] **Step 1: Test merah untuk key-version + AAD**

Tambah ke `aesgcm_test.go`:
```go
func TestSealIsBoundToTheAccountID(t *testing.T) {
	s := newTestSealer(t)
	aad := []byte("account:11111111-1111-1111-1111-111111111111")
	ct, err := s.Seal([]byte("secret"), aad)
	if err != nil { t.Fatalf("seal: %v", err) }
	if _, err := s.Open(ct, []byte("account:22222222-2222-2222-2222-222222222222")); err == nil {
		t.Fatal("ciphertext opened under a different account id — AAD is not enforced")
	}
}

func TestCiphertextCarriesItsKeyVersion(t *testing.T) {
	s := newTestSealer(t)
	ct, _ := s.Seal([]byte("secret"), []byte("account:x"))
	if ct[0] != 1 { t.Fatalf("leading byte = %d, want 1", ct[0]) }
	// A v0 blob (no prefix) must still open — that is what the current DB holds.
	v0 := legacySeal(t, []byte("secret"))
	got, err := s.Open(v0, []byte("account:x"))
	if err != nil { t.Fatalf("open legacy v0: %v", err) }
	if string(got) != "secret" { t.Fatalf("got %q", got) }
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/adapter/crypto/ -run 'TestSealIsBoundToTheAccountID|TestCiphertextCarriesItsKeyVersion' -count=1`
Harvest: `Seal` tidak menerima AAD → compile gagal.

- [ ] **Step 3: Implementasikan format v1**

`aesgcm.go`: `Seal(plaintext, aad []byte)` dan `Open(ciphertext, aad []byte) []byte`. `Seal` menulis `append([]byte{KeyVersion}, nonce...)` lalu `aead.Seal(nil, nonce, plaintext, aad)`. `Open` membaca byte pertama: `0` → legacy (coba tanpa AAD, lalu fallback dengan AAD untuk ciphertext yang sejak F-11 sudah punya); `1` → AAD wajib. `KeyVersion` = `1` untuk sekarang.

Call site kripto ada di `service/account.go` (password session) — teruskan `"account:"+accountID` sebagai AAD di sana.

- [ ] **Step 4: Jalankan crypto + account tests, harus HIJAU**

`cd apps/api && go test ./internal/adapter/crypto/ ./internal/service/ -run 'Crypto|Seal|Account' -count=1`

- [ ] **Step 5: Tulis `cmd/rekey` dan tes idempotensinya**

`cmd/rekey/main.go`: `-old-key`, `-new-key` (base64), `-dsn`. Loop `SELECT id, password_enc FROM account WHERE password_enc IS NOT NULL FOR UPDATE SKIP LOCKED` per batch 200, decrypt dengan key lama + AAD `"account:"+id`, re-encrypt dengan key baru, `UPDATE`, commit. Hitung `migrated` dan `skippedAlreadyV1`; keluar non-zero kalau ada decrypt gagal — **jangan** diam-diam skip, itu-repeat F-01.

- [ ] **Step 6: Rotasi (manual, di luar jam sibuk)**

```bash
# 1. BACKUP DULU
docker exec smm-postgres-1 pg_dump -U smm -d smm -t account > /tmp/account-$(date +%s).dump

# 2. Buat key baru (JANGAN pernah dicetak ke log/terminal bersama repo)
NEW_KEY=$(openssl rand -base64 32)

# 3. Migrasi ciphertext dulu (API masih jalan dengan key LAMA)
cd apps/api && go run ./cmd/rekey -old-key "$OLD_KEY" -new-key "$NEW_KEY" -dsn "$DATABASE_URL"

# 4. Baru putar JWT
NEW_JWT=$(openssl rand -hex 32)
```

Setelah `migrated` == jumlah akun dan `skippedAlreadyV1` == 0, update `.env` lokal dengan `CREDENTIAL_KEY=$NEW_KEY` dan `JWT_SECRET=$NEW_JWT`, lalu **restart semua container** (`make down && make up`). Verifikasi: login dashboard masih bisa, satu `POST /api/accounts/:id/session/import` round-trip berhasil.

> Kalau `rekey` melaporkan decrypt gagal pada satu baris: **berhenti**, jangan lanjutkan rotasi. Itu artinya ada ciphertext yang tidak berasal dari key yang diangkut, dan itu temuan baru — laporkan, jangan dipaksa.

- [ ] **Step 7: Fail-loud di compose + config**

`compose.yaml:139,141` → `JWT_SECRET: ${JWT_SECRET:?JWT_SECRET must be set}` dan `CREDENTIAL_KEY: ${CREDENTIAL_KEY:?CREDENTIAL_KEY must be set}`. `config.go` menambah: tolak `CREDENTIAL_KEY` yang base64-decode-nya bukan 32 byte, dan tolak nilai yang sama dengan yang pernah ter-commit.

- [ ] **Step 8: Tutup celah guard**

`scripts/check-credentials.sh:20-24`: hapus `--include` sehingga scan seluruh tree, dan tambahkan `compose.yaml` serta `.env*` secara eksplisit. `.gitleaks.toml`: hapus allowlist untuk string placeholder yang selama ini menutupi F-03.

- [ ] **Step 9: Buktikan guard masih hijau, lalu commit**

```bash
./scripts/check-credentials.sh          # harus: credential guard: clean
cd apps/api && go test ./... -count=1   # harus 0 fail
git add -A && git commit -m "fix(security): rotate credential key, fail loud when secrets are absent

CREDENTIAL_KEY and JWT_SECRET shipped as compose defaults since 1c10eea and were
allowlisted as local placeholders, so every account credential and an OWNER
token were derivable from a repo clone. Adds key-version + AAD so the rotation
has a migration path."
```

> `git filter-repo` untuk membersihkan history **tidak** termasuk plan ini — itu keputusan operator, bukan perubahan kode. Catat di changelog bahwa secret lama harus dianggap kompromis selamanya.

---

# Phase 1 — SEV2

## Task 4: Tutup permukaan jaringan data plane (F-04)

**Files:**
- Modify: `compose.yaml:12,16,25,29,41,45-46,96,179`
- Create: `infra/docker/redis.conf`
- Test: `scripts/check-compose-exposure.sh` (baru, dipanggil dari `ci.yml`)

**Interfaces:**
- Produces: `scripts/check-compose-exposure.sh` — gagal kalau ada `ports:` entry yang tidak diawali `127.0.0.1:`, atau `POSTGRES_PASSWORD`/`MINIO_ROOT_PASSWORD`/`JWT_SECRET`/`CREDENTIAL_KEY`/`REDIS_PASSWORD` yang punya `:-` default. Exit non-zero = CI merah. Ini adalah gate yang membuat Task 4 tidak bisaundur diam-diam.

- [ ] **Step 1: Tulis gate, jalankan, harus GAGAL**

`scripts/check-compose-exposure.sh`:
```bash
#!/bin/sh
# F-04: the data plane must never bind beyond loopback. Fails if any published
# port lacks a 127.0.0.1: prefix, or any secret has a committed default.
set -eu
cd "$(dirname "$0")/.."
fail=0
grep -nE "^\s+- '?[0-9]" compose.yaml | grep -v "127.0.0.1:" && {
  echo "exposure: a published port is not bound to 127.0.0.1" >&2; fail=1; }
for k in POSTGRES_PASSWORD MINIO_ROOT_PASSWORD JWT_SECRET CREDENTIAL_KEY REDIS_PASSWORD; do
  grep -nE "\${$k:-" compose.yaml && {
    echo "exposure: $k still has a committed default" >&2; fail=1; }
done
exit $fail
```
`sh scripts/check-compose-exposure.sh` → harus keluar 1 dan mencetak daftar port.

- [ ] **Step 2: Loopback-bind semua port**

`compose.yaml`: tambahkan prefiks `127.0.0.1:` pada `POSTGRES_PORT` (`:16`), `REDIS_PORT` (`:29`), `MINIO_PORT`+`MINIO_CONSOLE_PORT` (`:45-46`), `API_PORT` (`:179`), `WEB_PORT` (`:265`), dan port observability/mailpit. **Kecuali** noVNC, yang memang sudah `127.0.0.1` via `docker.go:181-183`.

- [ ] **Step 3: Redis auth + memory bound**

`infra/docker/redis.conf`:
```
requirepass ${REDIS_PASSWORD}
maxmemory 512mb
maxmemory-policy noeviction
appendonly yes
appendfsync everysec
save 3600 1
```
`compose.yaml:25` → `command: ['redis-server', '/usr/local/etc/redis/redis.conf']` + mount file + `REDIS_PASSWORD: ${REDIS_PASSWORD:?required}`. API/worker: `REDIS_URL: redis://:${REDIS_PASSWORD}@redis:6379`.

> **`maxmemory-policy noeviction`, bukan `allkeys-lru`.** `DEVELOPMENT_RULE §8.1` menyebut `allkeys-lru`, tapi antrean action adalah **Redis List yang durable** — `allkeys-lru` akan meng-evict job yang sudah dibayar dan tidak pernah dieksekusi. Dokumen itu yang salah; kodenya yang harus benar. Perbarui `DEVELOPMENT_RULE.md:249` di Task 20.

- [ ] **Step 4: Password default untuk Postgres/MinIO jadi wajib**

`compose.yaml:12,41` → `${POSTGRES_PASSWORD:?required}` / `${MINIO_ROOT_PASSWORD:?required}`, dan ikutkan di `DATABASE_URL`/`S3_*` (`:96,99-103`) serta perintah `minio-init` (`:63`).

- [ ] **Step 5: Jalankan gate, harus HIJAU, lalu `make up` dan cek**

```bash
sh scripts/check-compose-exposure.sh   # exit 0, tanpa output
make down && make up                    # boot harus gagal jelas kalau .env belum di-set
curl -s localhost:24080/healthz
```

- [ ] **Step 6: Commit**

```bash
git add compose.yaml infra/docker/redis.conf scripts/check-compose-exposure.sh
git commit -m "fix(infra): bind the data plane to loopback and authenticate Redis

Redis had no requirepass, MinIO console shipped the gitleaks-allowlisted
default password, and all three were published on 0.0.0.0. Adds
check-compose-exposure.sh so this cannot regress silently."
```

---

## Task 5: Autentikasi `/internal/*` dan kepemilikan job (F-05)

**Files:**
- Create: `apps/api/internal/http/worker_auth.go`
- Create: `apps/api/internal/http/worker_auth_test.go`
- Modify: `apps/api/internal/http/internal.go:67`
- Modify: `apps/api/internal/service/job.go:129-185`
- Modify: `db/queries/action.sql` (`CompleteActionJob`, tambah predikat status)
- Modify: `apps/worker/src/index.ts` (kirim header)
- Test: `apps/worker/src/test/claim.test.ts` (tambah kasus)

**Interfaces:**
- Consumes: `POST /internal/claim` sudah ada (`internal.go:75`).
- Produces:
  - `func workerTokenMiddleware(tokens port.WorkerTokenStore) echo.MiddlewareFunc` — menolak request tanpa `X-Worker-Token`, dengan 401.
  - `port.WorkerTokenStore` dengan `Verify(ctx, token string) (workerID uuid.UUID, err error)`.
  - `Token` dikirim balik di `ClaimResponse` sebagai `workerToken`.
  - `CompleteActionJob` sekarang `WHERE id = $1 AND status = 'RUNNING'`; 0 baris ter-update = `domain.ErrConflict`.

- [ ] **Step 1: Test merah — forged callback**

`worker_auth_test.go`:
```go
func TestActionCallbackWithoutAWorkerTokenIsRejected(t *testing.T) {
	e, _ := newTestInternal(t)
	req := httptest.NewRequest(http.MethodPost, "/internal/action-callback",
		strings.NewReader(`{"attemptId":"j:1","status":"SUCCESS"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — an unauthenticated caller reached RecordAttempt", rec.Code)
	}
}
```
Dan di `service/job_test.go` (paket `service`, pakai `newFakeActionStore`):
```go
func TestRecordAttemptRejectsAWorkerThatDoesNotOwnTheJob(t *testing.T) {
	store := newFakeActionStore()
	store.seedJob(domain.ActionJob{ID: "j1", Status: domain.JobStatusRunning, WorkerID: "worker-A"})
	svc := newTestJobService(store)
	err := svc.RecordAttempt(context.Background(), AttemptRecord{
		AttemptID: "j1:1", Status: domain.AttemptSuccess, WorkerID: "worker-B",
	})
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if store.job("j1").Status != domain.JobStatusRunning {
		t.Fatalf("job status = %s, want it untouched at RUNNING", store.job("j1").Status)
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/http/ ./internal/service/ -run 'WorkerToken|DoesNotOwn' -count=1`
Harvest: 401 test gagal dengan 202; ownership test gagal karena `RecordAttempt` mengembalikan nil.

- [ ] **Step 3: Implementasikan token**

- `port`: `type WorkerTokenStore interface { Issue(ctx, workerID uuid.UUID, ttl time.Duration) (string, error); Verify(ctx, token string) (uuid.UUID, error) }`
- `repository`: implementasi Redis, key `worker:token:<sha256(token)>` → workerID, TTL 24 jam, **diset saat container dibuat** (bukan saat claim, supaya token tetap hidup meski worker restart). Token dikirim ke worker via env `WORKER_TOKEN` oleh `dockerprovisioner/docker.go:167` — satu baris tambahan di slice `env` yang sudah ada.
- `http/worker_auth.go`: middleware Echo yang baca header, `Verify`, simpan workerID di context dengan key konstanta.
- `internal.go:67`: `e.Group("/internal", limitBody(MaxCallbackBytes), workerTokenMiddleware(...))`.
- `RecordAttempt`: setelah `parseAttemptID`, muat job; `if job.WorkerID != r.WorkerID → ErrForbidden`; `if job.Status != RUNNING → ErrConflict` (kecuali replay dari attempt sebelumnya, yang tetap diizinkan agar idempotensi `UpsertActionLog` tetap berlaku).
- `action.sql` `CompleteActionJob`: tambah `AND status = 'RUNNING'`.
- `worker/src/index.ts`: `X-Worker-Token` di setiap header callback (`callback.ts:45-50`, `auth-callback.ts`, `session-callback.ts`).

> `/internal/claim` **harus tetap terbuka** — worker belum punya token saat boot. 대신, claim memvalidasi `containerId` terhadap baris yang belum di-claim (sudah dijaga uuid guard di `repository/worker.go` step 2). Tambahkan rate limit pada `/internal/claim` supaya tidak bisa dipakai untuk menebak worker row satu per satu.

- [ ] **Step 4: Jalankan, harus HIJAU**

`cd apps/api && go test ./internal/http/ ./internal/service/ -run 'WorkerToken|DoesNotOwn|RecordAttempt' -count=1 -v` → semua PASS.
`cd apps/worker && pnpm test` → 61+ hijau.

- [ ] **Step 5: Verifikasi end-to-end di stack**

```bash
make down && make up
# tanpa token → harus 401
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:24080/internal/action-callback \
  -H 'content-type: application/json' -d '{"attemptId":"x:1","status":"SUCCESS"}'   # 401
# dengan token dari container → 202
docker exec smm-worker-jk2-68d06b1a-46c printenv WORKER_TOKEN
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(actions): authenticate /internal callbacks and bind a verdict to its worker

RecordAttempt trusted the caller's attemptId and workerId and CompleteActionJob
had no status predicate, so an unauthenticated POST could mark any job on any
account SUCCESS. Adds a per-worker token at claim time, an ownership check in
RecordAttempt, and AND status='RUNNING' on CompleteActionJob."
```

---

## Task 6: SSRF guard tunggal untuk URL yang masuk dari request (F-06)

**Files:**
- Create: `apps/api/internal/service/safeurl.go` (pindah dari `action.go:460-480`)
- Modify: `apps/api/internal/service/action.go` (hapus definisi lama, panggil baru)
- Modify: `apps/api/internal/service/scrape_ondemand.go:85-96`
- Test: `apps/api/internal/service/safeurl_test.go` (baru)

**Interfaces:**
- Produces:
  ```go
  // safeurl.go
  type SafeURLChecker struct {
      AllowedHosts map[domain.Platform][]string // exact host suffixes
      Resolver     func(ctx context.Context, host string) ([]net.IP, error) // injectable for tests
  }
  func (c *SafeURLChecker) Validate(ctx context.Context, platform domain.Platform, raw string) (*url.URL, error)
  ```
  Rule persis: scheme `http`/`https`; host wajib; **setiap** IP hasil resolusi ditolak kalau `IsLoopback() || IsLinkLocalUnicast() || IsLinkLocalMulticast() || IsPrivate() || IsUnspecified()` atau berada di `100.64.0.0/10` (CGNAT). Alias numerik (desimal `2130706433`, oktal `0177.0.0.1`, hex) dinormalisasi `net.ParseIP` lebih dulu — `net.ParseIP` menolak bentuk oktal, jadi hex/octal harus ditolak eksplisit lewat regex `^[0-9.]+$` + parse, bukan diterima.

- [ ] **Step 1: Test merah dengan vektor nyata**

`safeurl_test.go` — tabel, `t.Run` per kasus (DEVELOPMENT_RULE §5):
```go
func TestSafeURLRejectsInternalTargets(t *testing.T) {
	c := newTestChecker(t) // Resolver stub: 127.0.0.1 -> 127.0.0.1, instagram.com -> 93.184.216.34
	for _, tc := range []struct{ name, raw string; wantErr bool }{
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"loopback ip", "http://127.0.0.1:9000/minio/sessions/", true},
		{"loopback name", "http://localhost:9000/", true},
		{"ipv6 loopback", "http://[::1]:8080/", true},
		{"rfc1918", "http://10.0.0.1/", true},
		{"cgnat", "http://100.64.0.1/", true},
		{"decimal ip", "http://2130706433/", true},
		{"octal ip", "http://0177.0.0.1/", true},
		{"userinfo", "http://instagram.com@127.0.0.1/", true},
		{"rebinding name", "http://internal.ssm.nip.io/", true},
		{"file scheme", "file:///etc/passwd", true},
		{"javascript scheme", "javascript:alert(1)", true},
		{"legit instagram", "https://www.instagram.com/p/abc/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Validate(context.Background(), domain.PlatformInstagram, tc.raw)
			if tc.wantErr && err == nil { t.Fatal("accepted a target that must be rejected") }
			if !tc.wantErr && err != nil { t.Fatalf("rejected a legitimate target: %v", err) }
		})
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/service/ -run TestSafeURLRejectsInternalTargets -count=1`
Harvest: `undefined: newTestChecker`.

- [ ] **Step 3: Implementasikan dan pindahkan call site**

Pindahkan `safeTargetURL` ke `safeurl.go`, ubah jadi metode. Ganti call site di `action.go` (yang memanggilnya saat `Enqueue`) dan **tambahkan yang baru di `scrape_ondemand.go:85-96`** — jalur itu hari ini tidak melewati guard sama sekali, dan itu temuan terpisah yang harus ikut tertutup di commit yang sama.

- [ ] **Step 4: Tambahkan revalidasi redirect di client Apify**

`adapter/apify/runner.go:71` — ganti `&http.Client{Timeout: timeout}` dengan `CheckRedirect` yang memanggil validator yang sama untuk tiap hop. Kalau satu hop ditolak, kembalikan error (bukan diam).

- [ ] **Step 5: Validasi ulang di worker**

`worker/src/platforms/dom.ts` sebelum `page.goto(job.targetUrl)`: tolak kalau hostname-nya bukan `www.instagram.com`/`instagram.com`/`www.threads.net`/`threads.net`. Worker adalah lapisan terakhir; BE tidak boleh jadi satu-satunya dinding (`DEVELOPMENT_RULE §7.4` menyebut worker "bukan validator URL", tapi itu asumsi bahwa BE benar — defense in depth di siniMurah).

- [ ] **Step 6: Jalankan, harus HIJAU**

`cd apps/api && go test ./internal/service/ -count=1` → semua hijau, 13 subtest baru HIJAU.
`cd apps/worker && pnpm test` → hijau.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "fix(actions): reject internal-network targets before they reach the browser

safeTargetURL only checked the scheme, so cloud metadata, loopback, RFC1918 and
numeric IP forms all passed and the worker page.goto'd them. Validates resolved
IPs, per-platform host allowlist, and re-checks every redirect hop; also routes
the on-demand scrape target through the same guard, which it previously skipped."
```

---

## Task 7: `RunPendingNow` tanpa offset (F-07)

**Files:**
- Modify: `apps/api/db/queries/action.sql` (ganti `ListActionJobsByStatus` dengan varian keyset)
- Modify: `apps/api/internal/repository/action.go:135-152`
- Modify: `apps/api/internal/service/action.go:373-403`
- Test: `apps/api/internal/service/action_retry_test.go` (tambah kasus)
- Test: `apps/api/internal/repository/action_test.go` (kalau `SMM_TEST_DB` tersedia)

**Interfaces:**
- Produces: `port.ActionStore.RescheduleDueActionJobs(ctx, at time.Time, limit int32) (int64, error)` — **satu** statement, mengembalikan jumlah baris ter-update. Menghilangkan kebutuhan `ListActionJobsByStatus` + loop sama sekali.
- `func (s *ActionService) RunPendingNow(ctx context.Context) (int, error)` — tanda tangan tidak berubah; sekarang mengembalikan jumlah baris yang benar-benar ter-update.

- [ ] **Step 1: Test merah yang benar-benar memodelkan produksi**

Teks di `action_retry_test.go` yang sekarang thrilled adalah fake-nya **tidak pernah menyentuh `ScheduledAt`**. Perbaiki dulu fake-nya supaya jujur, baru test-nya akan menggigit:
```go
// di newFakeActionStore: RescheduleActionJob HARUS mengubah urutan
func (f *fakeActionStore) RescheduleActionJob(ctx context.Context, id string, at time.Time) (domain.ActionJob, error) {
	for i := range f.pending {
		if f.pending[i].ID == id {
			f.pending[i].ScheduledAt = at          // ← ini yang hilang, dan itu sebabnya test hijau
			sort.SliceStable(f.pending, func(a, b int) bool {
				return f.pending[a].ScheduledAt.Before(f.pending[b].ScheduledAt)
			})
			return f.pending[i], nil
		}
	}
	return domain.ActionJob{}, domain.ErrNotFound
}
```
Lalu test yang kembali:
```go
func TestRunPendingNowSweepsEveryPageWithoutSkippingOrDoubleCounting(t *testing.T) {
	store := newFakeActionStore()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	clock := newStepClock(); clock.set(now)
	total := MaxActionBatch*3 + 17           // 167 rows: 4 pages, last one short
	for i := 0; i < total; i++ {
		store.seedPending(domain.ActionJob{
			ID: fmt.Sprintf("j%03d", i),
			ScheduledAt: now.Add(-time.Duration(total-i) * time.Minute), // semua sudah due, tie mungkin
		})
	}
	svc := newTestActionService(store, clock)
	n, err := svc.RunPendingNow(context.Background())
	if err != nil { t.Fatalf("run: %v", err) }
	if n != total { t.Fatalf("moved %d, want %d — a page was skipped or double-counted", n, total) }
	for i, j := range store.pending {          // tidak ada yang boleh ter-skip
		if !j.ScheduledAt.Equal(now) {
			t.Fatalf("row %d (%s) still scheduled at %s — the OFFSET window skipped it",
				i, j.ID, j.ScheduledAt)
		}
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/service/ -run TestRunPendingNowSweepsEveryPage -count=1`
Harvest: dengan fake yang jujur, implementasi offset-based **skip baris** → `moved != 167`.

- [ ] **Step 3: Ganti dengan satu statement keyset**

`action.sql`:
```sql
-- name: RescheduleDueActionJobs :execrows
-- F-07: the previous sweep paged with OFFSET over ORDER BY scheduled_at while
-- rewriting scheduled_at — the window moved underneath it. One statement, no
-- offset, no ordering to invalidate.
UPDATE action_job
SET scheduled_at = $1, finished_at = NULL
WHERE status = 'PENDING'
  AND scheduled_at <= $1
  AND id IN (
      SELECT id FROM action_job
      WHERE status = 'PENDING' AND scheduled_at <= $1
      ORDER BY scheduled_at, id
      LIMIT $2
  );
```
`id` sebagai tiebreaker`:\}$ menjaga$kunci unik, jadi dua baris dengan `scheduled_at` identik tidak pernah tertukar.

`action.go:373-403` jadi:
```go
func (s *ActionService) RunPendingNow(ctx context.Context) (int, error) {
	now := s.clock.Now()
	total := 0
	for {
		n, err := s.actions.RescheduleDueActionJobs(ctx, now, MaxActionBatch)
		if err != nil { return total, fmt.Errorf("run pending now: %w", err) }
		total += int(n)
		if n < MaxActionBatch { return total, nil }
	}
}
```
Loop ini **terminasi dengan bukti**: setiap iterasi me-`UPDATE` ≥1 baris (kalau 0, `n < 50` dan langsung keluar), dan tidak ada lagi query yang bisa membuat baris yang sama masuk dua kali karena `id IN (...)`.READ dipakai ulang setelah di-update (`scheduled_at` sudah `now`, tidak lagi `<= now` pada tick berikutnya karena `now` di-capture sekali).

- [ ] **Step 4: Jalankan, harus HIJAU**

`cd apps/api && go test ./internal/service/ -run TestRunPendingNow -count=1 -v` → semua PASS, termasuk `…SweepsPastTheBatchCap` yang lama.

- [ ] **Step 5: Verifikasi batas `maxRunPendingRows` masih ada**

Tambah guard: kalau `total >= maxRunPendingRows`, putuskan loop dan **log** `"run-pending: bound reached, queue not fully drained"` plus sisa yang tidak tersentuh. Angka yang dikembalikan ke UI harus jujur soal ini — `http/action.go:70` sudah mengembalikan `{"moved": n}`; tambahkan `"bounded": true` saat kena batas supaya FE bisa bilang "terbatas, masih ada sisa".

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(actions): stop paging RunPendingNow over the column it rewrites

ORDER BY scheduled_at + OFFSET while UPDATE SET scheduled_at=$now moved the
paging window underneath itself, so a 300-row queue reported 'moved: 200' and
silently left 100 unswept. Replaced with a single keyset UPDATE, and the test
fake now mutates ScheduledAt so the suite can actually see the defect."
```

---

## Task 8: `ACTION_DRY_RUN` tidak lagi di-inject ke worker (F-08)

**Files:**
- Modify: `apps/api/internal/adapter/dockerprovisioner/docker.go:167`
- Modify: `apps/api/internal/config/config.go:49-51,209`
- Modify: `apps/worker/src/index.ts` (default eksplisit `false`)
- Test: `apps/worker/src/test/unit.test.ts`

**Interfaces:**
- Produces: env `ACTION_DRY_RUN` **tidak lagi** dibuat API. Worker membacanya sendiri dengan default `false`; hanya compose `worker` service (profile `fleet`) yang boleh menyetelnya.

- [ ] **Step 1: Test merah di worker**

`apps/worker/src/test/unit.test.ts` — tambahkan:
```ts
test('dry-run defaults OFF when ACTION_DRY_RUN is unset', () => {
  delete process.env.ACTION_DRY_RUN;
  const c = loadConfig({ WORKER_ID: 'w1' });
  assert.equal(c.dryRun, false, 'a provisioned worker must not silently no-op every action');
});
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/worker && pnpm test 2>&1 | grep dry-run` → merah.

- [ ] **Step 3: Implementasikan**

- `docker.go:167`: **hapus** baris `"ACTION_DRY_RUN=" + boolStr(c.dryRun)` dari slice env. Hapus field `dryRun` dari `dockerprovisioner.Config` kalau tidak ada pemakai lain (cek `grep -rn "DryRun" apps/api`).
- `config.go`: pertahankan `ActionDryRun` hanya kalau ada konsumen; kalau tidak, hapus beserta baris `:209`.
- `worker/src/core/config.ts`: `dryRun: process.env.ACTION_DRY_RUN === 'true'` (default false).
- `compose.yaml:132`: beri komentar bahwa ini hanya untuk `fleet` profile.

- [ ] **Step 4: Jalankan, harus HIJAU**

`cd apps/worker && pnpm test` → hijau. `cd apps/api && go build ./... && go test ./internal/adapter/dockerprovisioner/ -count=1` → hijau.

- [ ] **Step 5: Buktikan di stack, lalu commit**

```bash
make down && make up && sleep 5
docker exec <container-worker-baru> printenv ACTION_DRY_RUN   # harus: kosong (tidak ada)
```
```bash
git add -A
git commit -m "fix(worker): stop provisioning ACTION_DRY_RUN=true into every worker

config.go defaulted dry-run to true and the reconciler injected it, so a fresh
install reported 'success' with durationMs=0 without ever opening a browser.
The env is now worker-local and defaults off."
```

---

## Task 9: Putuskan dan implementasikan komentar pada keyword batch (F-09)

> **Ini keputusan produk, bukan hanya kode.** Bukti live: 3 batch Threads sukses → 56 item → 56 post → **0 comment**; 52 post Threads di DB → **0 comment**. Ambil keputusan ini sebelum mulai. Rekomendasi saya: **Opsi A** — keyword batch memang alat monitoring, danxylenon tidak harus jadi alat komentar. Tapi kalau begitu, implementasikan B, supaya angka 0 tidak lagi berdiri sebagai fakta.

**Opsi A (disarankan): pindah ke actor yang bisa mengambil reply.**
- Modify: `apps/api/internal/adapter/apify/runner.go:99-111` —/searchInput untuk Threads memakai `APIFY_POST_ACTOR_THREADS` (satu actor, satu payload builder) dan menambah `"includeReplies": true`.
- Modify: `apps/api/internal/config/config.go:241-243` — hapus `ApifySearchActorThreads`, atau jadikan fallback ke post-actor yang sama.
- Test: `runner_test.go` — `TestSearchInputThreadsIncludesReplies` yang assert `payload["includeReplies"] == true`. **Test ini yang tidak ada sekarang, dan itulah sebabnya bug-nya lolos.**

**Opsi B: akui batasnya di UI + dokumen.**
- Hapus `commentsCount` dari kartu batch di `apps/web/app/(dashboard)/scrape/batches/`.
- Tulis di `docs/platforms/threads.md §4.2`: "mode search tidak meminta reply; `comments_count` selalu 0 untuk batch. Reply hanya lewat §4.1."
- Ubah `../BATCH_SCRAPE.md` supaya tidak menjanjikan komentar.

- [ ] **Step 1: Putuskan A atau B, tulis keputusannya di `docs/adr/`**

Buat `docs/adr/` dengan Status/Context/Decision/Consequence. Graham Potter dalam PR.

- [ ] **Step 2: (Opsi A) Test merah**

`apps/api/internal/adapter/apify/runner_test.go`:
```go
func TestSearchInputThreadsRequestsReplies(t *testing.T) {
	got := r.searchInput(port.ApifySearchInput{Platform: domain.PlatformThreads,
		Keywords: []string{"fashion"}}, 50)
	if got["includeReplies"] != true {
		t.Fatalf("includeReplies = %v, want true — without it a Threads keyword batch ingests 0 comments", got["includeReplies"])
	}
}
```

- [ ] **Step 3: (Opsi A) Implementasikan, jalankan**

`cd apps/api && go test ./internal/adapter/apify/ -run TestSearchInputThreads -count=1 -v` → merah dulu, hijau setelah.

- [ ] **Step 4: (Opsi A) Verifikasi dengan run sungguhan**

```bash
# lewat dashboard, bukan curl: paid actor run
# lalu: batch Threads berikutnya harus punya comments_count > 0
docker exec smm-postgres-1 psql -U smm -d smm -A -F' | ' \
  -c "SELECT platform, sum(comments_count) FROM keyword_batch GROUP BY 1;"
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat(scrape): keyword batches now request Threads replies, or say they cannot

3 succeeded Threads batches stored 56 posts and 0 comments because
searchInput never sent includeReplies and used a different actor than the
per-post path. Either send the flag or stop showing a 0 that looks like data."
```

---

# Phase 2 — SEV3

## Task 10: RBAC scrape GET + hentikan kebocoran `err.Error()` (F-10, F-11)

**Files:**
- Modify: `apps/api/internal/http/scrape.go:40,41,42,45,47`
- Modify: `apps/api/internal/http/{user,proxy_group,action,audit,template,analytics}.go` (semua `NewHTTPError(500, err.Error())`)
- Test: `apps/api/internal/http/rbac_matrix_test.go` (baru)

**Interfaces:**
- Produces: `rbac_matrix_test.go` yang menelusuri `NewRouter(Dependencies{...}).Routes()` dan **gagal** kalau ada `/api*` route `POST|PUT|PATCH|DELETE` yang tidak membawa `RequirePermission`, **atau** route `GET` yang carries `PermAct`. Ini gate yang membuat F-10 tidak bisa kembali tanpa dilihat.

- [ ] **Step 1: Test merah — matriks route**

`apps/api/internal/http/rbac_matrix_test.go`:
```go
// F-10: five GET scrape routes carried PermAct, locking STRATEGIST/ANALYST out
// of reads. This walks the real router so the next one cannot ship silently.
func TestEveryMutatingAPIRouteIsGatedAndNoReadRouteDemandsAct(t *testing.T) {
	e := NewRouter(Dependencies{Auth: newTestAuthHandler(t)}) // semua handler ter-stub
	for _, r := range e.Routes() {
		if !strings.HasPrefix(r.Path, "/api") { continue }
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !hasPermissionMiddleware(r) {
				t.Errorf("%s %s is mutating but carries no RequirePermission", r.Method, r.Path)
			}
		case http.MethodGet:
			if hasPermission(r, domain.PermAct) {
				t.Errorf("GET %s requires 'act' — reads must stay readable by STRATEGIST/ANALYST", r.Path)
			}
		}
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/http/ -run TestEveryMutatingAPIRoute -count=1`
Harvest: 5 kegagalan, satu per route `GET /api/scrape/*`.

- [ ] **Step 3: Perbaiki gate**

`scrape.go` — hapus `RequirePermission(domain.PermAct)` dari `:40,41,42,45,47`. `act` tetap di `:38,39,46`.

- [ ] **Step 4: Jalankan, harus HIJAU**

`go test ./internal/http/ -run TestEveryMutatingAPIRoute -count=1 -v` → PASS.

- [ ] **Step 5: Test merah — kebocoran error**

Tambah di `errors_test.go`:
```go
func TestFiveHundredNeverLeaksTheWrappedError(t *testing.T) {
	e, _ := newTestProxyGroupHandler(t) // stub yang mengembalikan errors.New("dial: password=hunter2")
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-groups", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError { t.Fatalf("status = %d", rec.Code) }
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("500 body leaked the wrapped error: %s", rec.Body.String())
	}
}
```

- [ ] **Step 6: Ganti semua `NewHTTPError(500, err.Error())` dengan `return err`**

12 call site. `errors.go` sudah memetakan unknown error ke `500 "internal"` dengan `message` = `StatusText` — yang bocor cuma `code`, karena `errors.go:63-65` memasukkan `he.Message` ke `code`. Setelah semua handler berhenti mengarang string, `code` kembali ke `http.StatusText`/sentinel.

Tambahkan **satu** pengecualian yang boleh: `echo.NewHTTPError(400, "…")` dengan string **literal** (bukan `err.Error()`) — pattern itu aman dan dipakai untuk pesan validasi.

- [ ] **Step 7: Jalankan, harus HIJAU**

`go test ./internal/http/ -count=1` → semua hijau termasuk yang baru.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "fix(api): reads stay readable, and 500 bodies stop carrying err.Error()

Five GET scrape routes required 'act', the same lockout class P5-04 fixed at the
group level. Separately, 12 handlers built HTTPError with err.Error(), which
errorHandler emits verbatim as error.code — a pgx failure returned the DSN,
password included. Add a route-table test so neither can regress."
```

---

## Task 11: Drain SIGTERM di worker + perms file sesi (F-12, F-22)

**Files:**
- Modify: `apps/worker/src/index.ts:296-297`
- Modify: `apps/worker/src/transport/queue.ts`
- Modify: `apps/worker/src/core/session.ts:32-40`
- Test: `apps/worker/src/test/queue.test.ts` (baru), `session.test.ts`

**Interfaces:**
- Produces: `export async function createQueue(opts) { …, stop(): Promise<void> }` — `stop()` berhenti menerima job baru dan menunggu job in-flight selesai (bounded oleh `shutdownGraceMs`, default 30 s). Exposed supaya `index.ts` shutdown handler bisa `await queue.stop()` sebelum `process.exit`.
- `writeFile(path, data, { encoding: 'utf8', mode: 0o600 })` + `mkdir(dir, { recursive: true, mode: 0o700 })`.

- [ ] **Step 1: Test merah — job in-flight di-restart**

`apps/worker/src/test/queue.test.ts`:
```ts
test('stop() waits for the in-flight job instead of dropping it', async () => {
  const jobs = makeFakeQueue([{ id: 'j1' }, { id: 'j2' }]);
  const started: string[] = [];
  const q = createQueue({ source: jobs, run: async (j) => {
    started.push(j.id);
    await delay(30);
  }});
  const inflight = q.take();          // j1 mulai
  await q.stop();                     // harus MENUNGGU, bukan abort
  await inflight;
  assert.deepEqual(started, ['j1']);  // j2 belum diklaim
});
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/worker && pnpm test 2>&1 | grep 'in-flight'` → merah (API `stop` belum ada).

- [ ] **Step 3: Implementasikan drain**

`queue.ts`: tambah state `stopping`, `inFlight: Promise<void> | null`; `stop()` set `stopping`, await `inFlight` dengan `Promise.race([inFlight, delay(30_000)])`, lalu return. `index.ts:296-297` jadi:
```ts
process.on('SIGTERM', () => {
  logger.info('shutdown: draining');
  void (async () => {
    await queue.stop();
    await browserHandle?.close().catch(() => undefined);
    process.exit(0);
  })();
});
```

- [ ] **Step 4: Perbaiki perms sesi**

`session.ts`: `mkdir(dir, { recursive: true, mode: 0o700 })`; `writeFile(path, JSON.stringify(state), { encoding: 'utf8', mode: 0o600 })`. Plus validasi bentuk: sebelum dipakai, `JSON.parse` dan pastikan itu object dengan `cookies` array; kalau tidak, perlakukan sebagai tidak ada (bukan crash loop).

- [ ] **Step 5: Jalankan, harus HIJAU**

`cd apps/worker && pnpm test` → semua hijau.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(worker): drain in-flight actions on SIGTERM and lock down session files

A BLPOPped job was dropped on shutdown with no requeue, leaving the row RUNNING
forever. Session files were written 0644 into a 0755 directory, readable by any
process in the container."
```

---

## Task 12: Halaman antrean jujur soal loading, error, dan truncasi (F-13, F-14)

**Files:**
- Modify: `apps/web/app/(dashboard)/actions/page.tsx:221-226,276,358,477-489`
- Modify: `apps/web/lib/api.ts:348-352`
- Modify: `apps/web/lib/hooks/use-actions.ts:52-63`
- Test: `apps/web/e2e/specs/actions.spec.ts` (tambah kasus)

**Interfaces:**
- `listActions(status?: JobStatus, limit?: number, signal?: AbortSignal)` — parameter `limit` baru, **opsional**, default tidak berubah supaya tidak ada caller yang patah. Halaman ini memanggilnya dengan `limit: 200`.
- `ActionJobList` di `packages/shared/src/generated/api.ts` — **tidak** diubah di sini. Kalau `total` tidak tersedia dari server, UI menulis "menampilkan 200 terbaru" alih-alihreload8230claiming "all settled". Mengubah kontrak OpenAPI adalah Task 20, jangan dicampur.

- [ ] **Step 1: E2E merah — counter saat loading**

`apps/web/e2e/specs/actions.spec.ts`:
```ts
test('the in-flight counter does not claim zero while the queue is loading', async ({ authedPage, api }) => {
  await api.route('/api/actions', (r) => r.delay(1500).fulfill({ status: 200, body: JSON.stringify({ actions: [] }) }));
  await authedPage.goto('/actions');
  await expect(authedPage.getByText(/in flight/)).not.toContainText('0 in flight');
});
```

- [ ] **Step 2: Jalankan e2e, harus GAGAL**

`cd apps/web && pnpm exec playwright test e2e/specs/actions.spec.ts` → merah pada assertion `0 in flight`.

- [ ] **Step 3: Perbaiki counter**

`actions/page.tsx:221-226`: `{loading ? '—' : `${inFlight} in flight`}`, dan titik pulse hanya saat `!loading && !error`. Import `error` dari `useActions()` kalau belum.

- [ ] **Step 4: E2E merah — truncasi**

```ts
test('a capped queue says so instead of claiming the whole queue settled', async ({ authedPage, api }) => {
  await api.seedActions(250, { status: 'SUCCESS' });
  await authedPage.goto('/actions');
  await expect(authedPage.getByText(/menampilkan 200 terbaru|showing 200 newest/)).toBeVisible();
  await expect(authedPage.getByText(/all settled/i)).toHaveCount(0);
});
```

- [ ] **Step 5: Implementasikan limit eksplisit + label jujur**

`api.ts:348-352` terima `limit`; `use-actions.ts` pass `200`; `actions/page.tsx` terima `total?: number` dari hook, dan:
- kalau `actions.length === limit` → ganti "N jobs, all settled" dengan "menampilkan N terbaru" + tautan/petunjuk.
- `failed > 0` harus dihitung dari data yang sama **dan** diberi label bahwa hanya mencakup jendela ini.

- [ ] **Step 6: E2E merah → hijau untuk retry PENDING basi**

Kontrol Retry (`:358`) dan dialog (`:438`) sekarang hanya untuk `FAILED`. smearedUbah jadi: Retry tampil bila `status === 'FAILED'` **atau** (`status === 'PENDING'` && `scheduledAt` sudah lewat > 1 interval backoff). Test: seed satu job `PENDING` dengan `scheduledAt` 2 jam lalu, buka halaman, klik Retry → job berubah jadi `PENDING` dengan `scheduledAt = now`.

- [ ] **Step 7: Tambahkan tombol retry di banner error**

`:276` — banner error dapat `{onClick={refresh}}` "Coba lagi". `refresh` sudah ada di `use-actions.ts:52` dan sekarang benar-benar didestruksikan (F-13 audit: "destructured nowhere").

- [ ] **Step 8: Debounce coalescing SSE**

`use-actions.ts:56-63` — `onEvent` tidak lagi `void refresh()` langsung; pakai `debounce(refresh, 150)` yang di-`clear` di `finally`. 20 verdict dalam 1 detik = 1 refetch, bukan 20.

- [ ] **Step 9: Jalankan e2e, harus HIJAU**

`cd apps/web && pnpm exec playwright test e2e/specs/actions.spec.ts` → semua hijau.

- [ ] **Step 10: Commit**

```bash
git add -A
git commit -m "fix(web): stop the queue page from lying about loading, errors and size

'in flight' rendered a confident 0 during both loading and failure; the server
caps at 50 rows with nothing in the response saying so, so every count, the
failed tally and the 'all settled' verdict were computed over a window
presented as the whole queue; and a job parked PENDING with no receiver had no
control at all, because Retry was gated on FAILED only."
```

---

## Task 13: Akun QUARANTINED tidak bisa di-dispatch ulang (F-15)

**Files:**
- Modify: `apps/api/internal/service/account.go:297-336` (`Login`)
- Modify: `db/queries/account.sql:126-137` (`BeginSessionRefresh`)
- Test: `apps/api/internal/service/account_session_test.go`

**Interfaces:**
- Consumes: `Account.IsPackable()` — sudah ada di domain; pakai itu, jangan tulis aturan status baru.
- `Login` mengembalikan `domain.ErrConflict` dengan pesan yang menyebut statusnya, supaya FE bisa menampilkan alasan.

- [ ] **Step 1: Test merah**

```go
func TestLoginRefusesAQuarantinedAccount(t *testing.T) {
	store := newFakeAccountStore()
	store.seed(domain.Account{ID: "a1", Status: domain.AccountQuarantined,
		WorkerID: "w1", AuthStatus: domain.AuthFailed})
	svc := newTestAccountService(store)
	_, err := svc.Login(context.Background(), "a1")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict — a quarantined account was re-dispatched", err)
	}
	if store.byID("a1").AuthStatus != domain.AuthFailed {
		t.Fatal("auth_status was rewritten; the quarantine no longer holds")
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL**

`cd apps/api && go test ./internal/service/ -run TestLoginRefusesAQuarantined -count=1` → merah.

- [ ] **Step 3: Implementasikan**

`Login`: setelah cek `WorkerID == nil`, tambahkan
```go
if !a.IsPackable() {
	return domain.Account{}, fmt.Errorf("%w: account is %s, not dispatchable", domain.ErrConflict, a.Status)
}
```
`BeginSessionRefresh`: `WHERE id = $1 AND status = 'ACTIVE'`.

- [ ] **Step 4: Jalankan, harus HIJAU**

`go test ./internal/service/ -run 'Login|Refresh|Quarantin' -count=1 -v` → hijau.

- [ ] **Step 5: FE — tombol Log in yang benar**

`accounts/page.tsx:760-764`: sembunyikan item "Log in" kalau `a.status !== 'ACTIVE'`, dan **perbaiki** ternary Resume/Pause di `:744-757` yang hanya menguji `a.status === 'PAUSED'` sehingga akun QUARANTINED mendapat tombol "Pause" (yang tidak mengeluarkan apa pun). Keduanya harus menguji statusnya sendiri.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "fix(accounts): refuse to dispatch a quarantined account via the login path

Quarantine leaves worker_id set, so Login's only guard passed and the operator's
'Log in' republished the same auth-login the scheduler avoids, rewriting
auth_status on an account the health policy took out of the pool. The per-row
menu also offered 'Pause' for a QUARANTINED row, which does nothing."
```

---

## Task 14: Resource limit di compose dan di driver docker (F-16)

**Files:**
- Modify: `compose.yaml` (tambah `deploy.resources` per service)
- Modify: `apps/api/internal/adapter/dockerprovisioner/docker.go:172-184`
- Modify: `apps/worker/Dockerfile` (tambah `USER`)
- Modify: `compose.yaml:183` (API tetap root — lokal saja, catat alasannya)

**Interfaces:**
- `dockerprovisioner.Config` baru: `WorkerMemory int64`, `WorkerNanoCPUs int64`, default 4 GiB / 2 CPU sesuai `DEVELOPMENT_RULE §8.2`. Di-wire dari env `WORKER_MEMORY_BYTES` / `WORKER_NANO_CPUS` di `config.go`.

- [ ] **Step 1: Test merah di driver**

`apps/api/internal/adapter/dockerprovisioner/docker_test.go` — test yang mengambil `HostConfig` hasil build dan meng-assert `Memory` dan `NanoCpus` non-nol:
```go
func TestCreateWorkerSetsResourceLimits(t *testing.T) {
	cfg := testConfig() // Memory: 4<<30, NanoCpus: 2e9
	hc := cfg.hostConfigFor(testWorker())
	if hc["Memory"] == nil { t.Fatal("no Memory limit — a runaway Chromium can OOM the host") }
	if hc["NanoCpus"] == nil { t.Fatal("no CPU limit") }
}
```

- [ ] **Step 2: Jalankan, harus GAGAL** → **Step 3: implementasikan**

`docker.go:172-184`: tambahkan `"Memory": c.WorkerMemory, "NanoCpus": c.WorkerNanoCPUs` ke `HostConfig`. `compose.yaml`: tambahkan blok `deploy.resources` untuk keenam service sesuai tabel `DEVELOPMENT_RULE §8.2` (BE 256m/256Mi→500m/512Mi, FE 100m/128Mi→200m/256Mi, Worker 750m/1Gi→2CPU/4Gi, PG 1/2Gi→2/4Gi, Redis 250m/256Mi→500m/512Mi).

`apps/worker/Dockerfile`: tambah `USER 10001` **setelah** memastikan `/data/sessions` dan `/data/screenshots` writable oleh uid itu (sudah named volume, jadi owner-nya dari image — set `RUN chown -R 10001:10001 /data`). Xvfb/x11vnc harus tetap bisa jalan sebagai non-root (X11 socket, tinggal `HOME` dan `-nolisten tcp`).

- [ ] **Step 4: Jalankan, harus HIJAU** → **Step 5: `make down && make up`, cek semua container healthy** → **Step 6: commit**

```bash
git commit -m "fix(infra): bound every container's memory and CPU

DEVELOPMENT_RULE 8.2 mandates per-service limits; none were set, in compose or
in the docker driver's HostConfig, so one runaway Chromium takes the whole host.
Also run the worker as uid 10001 instead of root."
```

---

## Task 15: Satu transaksi untuk ingest, counter, dan join batch (F-17)

**Files:**
- Modify: `apps/api/internal/service/scrape_keyword.go:212-232`
- Modify: `apps/api/internal/port/keyword_batch.go` (tambah varian tx-aware)
- Test: `apps/api/internal/service/scrape_keyword_test.go`

**Interfaces:**
- `port.ScrapeStore` / `port.KeywordBatchStore` mendapat
  `WithTx(ctx context.Context, fn func(ctx context.Context) error) error` — helper transaksi yang sudah ada di repo (`DEVELOPMENT_RULE §5` menyebutnya); **cek dulu** apakah sudah ada, kalau sudah ada jangan tulis ulang.
- Setelah task ini: `ingestPost`/`ingestComment`/snapshot, loop `CreateKeywordBatchPost`, dan `UpdateKeywordBatch` jalan di **satu** `fn`.

- [ ] **Step 1: Test merah — kegagalan di tengah join**

```go
func TestBatchCountsAndJoinLandInOneTransaction(t *testing.T) {
	// store fake yang mengembalikan error pada CreateKeywordBatchPost ke-3
	_, err := runBatch(t, twoItems(), failingJoinAt(3))
	if err == nil { t.Fatal("expected the batch to fail") }
	// Tidak boleh ada post yang ter-link ke batch dengan counter 0:
	links, _ := fake.linksFor(batchID)
	if len(links) != 0 {
		t.Fatalf("%d links survived a failed batch — counters and rows are not atomic", len(links))
	}
}
```

- [ ] **Step 2: Jalankan, harus GAGAL** → **Step 3: bungkus dalam `WithTx`** → **Step 4: jalankan, harus HIJAU** → **Step 5: commit**

---

## Task 16: Cooldown sesuai kontrak, dan hit-nya terlihat operator (F-18)

**Files:**
- Modify: `apps/api/internal/adapter/cooldown.go:59-61`
- Modify: `apps/api/internal/service/action_scheduler.go:244,251-253`
- Modify: `apps/worker/src/core/controller.ts` (tambah gate worker-side)
- Test: `apps/api/internal/adapter/cooldown_test.go`, `apps/worker/src/test/controller.test.ts`

**Interfaces:**
- Key jadi `smm:cooldown:comment:<sha256(targetURL)>` (tanpa account UUID — kontrak §7.4, dan cooldown itu sifatnya per-target).
- `service.ActionService.Fail(ctx, jobID, reason string)` — dipakai scheduler untuk cooldown hit, supaya job benar-benar `FAILED` dengan alasan terbaca, bukan `PENDING` lagi.
- Controller mengembalikan `{ status: 'failed', error: 'target is cooling down' }`.

- [ ] **Step 1: Test merah di worker** — job `comment` kedua ke URL sama dalam 60 dtk harus `failed` dengan pesan cooldown, bukan dikirim.
- [ ] **Step 2: Jalankan → GAGAL** → **Step 3: implementasikan gate di `controller.ts` sebelum `dispatch()`** → **Step 4: test merah di API** — scheduler tidak boleh me-reschedule; harus `Fail`.
- [ ] **Step 5: implementasikan** → **Step 6: test hijau** → **Step 7: commit**

---

## Task 17: Error LLM dan tes yang benar-benar menutupnya (F-19)

**Files:**
- Modify: `apps/api/internal/adapter/llm/client.go:116,121,128`
- Test: `apps/api/internal/adapter/llm/client_test.go`

**Interfaces:**
- `ErrResponseTooLarge = errors.New("llm: response exceeds 1 MiB")` — diekspor supaya caller bisa bedakan.
- `Client.Complete` mengembalikan error yang **klasifiable**; `classify.go` menambahkan kelas non-retryable untuk response-too-large dan untuk 401/403 dari provider.

- [ ] **Step 1: Test merah**

```go
func TestCompleteRejectsANon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, strings.Repeat("x", 50_000))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv.URL).Complete(ctx, "hi")
	if err == nil { t.Fatal("429 accepted as a success") }
	if !strings.Contains(err.Error(), "429") { t.Fatalf("err = %v, want the status in the message", err) }
}

func TestCompleteRejectsAnOversizedBodyInsteadOfTruncating(t *testing.T) {
	// server menulis 2 MiB JSON valid
	_, err := newTestClient(t, bigURL).Complete(ctx, "hi")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v — io.LimitReader truncated silently, so an oversized body looks like a decode error", err)
	}
}
```

- [ ] **Step 2: GAGAL** → **Step 3: ganti `io.LimitReader` dengan `io.LimitReader(r, max+1)` lalu cek `len > max` → `ErrResponseTooLarge`; tambahkan cabang 401/403 eksplisit** → **Step 4: HIJAU** → **Step 5: commit**

---

## Task 18: Rate limit login per-username + batas field callback (F-20, F-21)

**Files:**
- Modify: `apps/api/internal/service/auth.go:40-43,92`
- Test: `apps/api/internal/service/auth_rate_limit_test.go`
- Modify: `apps/api/internal/http/internal.go:141,194`, `maxErrorRunes`
- Test: `apps/api/internal/http/internal_test.go`

- [ ] **Step 1: Test merah** — 5 attempt dari 5 IP berbeda ke username yang sama harus diblokir pada attempt ke-5; dan `X-Forwarded-For` kosong **tidak** lagi mematikan cap.
- [ ] **Step 2: Jalankan → GAGAL** → **Step 3: tambah counter per-username (key `smm:rl:login:user:<hash>`, TTL sama) selain per-IP; hapus jalur fail-open `if ip == "" { return true }` dan ganti dengan bucket bersama `smm:rl:login:unknown`** → **Step 4: HIJAU**
- [ ] **Step 5: Test merah untuk batas field** — `worker_id` 200 char → 400; `renderedText` 5000 char → terpotong 2000; `error` 800 char → terpotong **500** (bukan 4096).
- [ ] **Step 6: implementasikan** di `internal.go`: helper `bounded(s string, max int) *string`, dan `if len(req.WorkerId) > 64 { 400 }` di ketiga callback. Ubah `maxErrorRunes` dari 4096 ke 500. → **Step 7: HIJAU** → **Step 8: commit**

---

# Phase 3 — Tutup celah test & CI (ini yang membuat semua di atas lolos)

## Task 19: Test yang benar-benar bisa gagal

**Files:**
- Create: `apps/api/internal/http/testhelpers_test.go` — `newTestInternal` yang **menerima action store yang berfungsi** (perbaiki sumber kelemahan test di audit §4)
- Create: `apps/api/internal/arch/arch_self_test.go`
- Modify: `apps/api/internal/repository/*_test.go` (hapus `t.Skip` senyap)
- Create: `apps/api/internal/arch/testdata/`
- Test: `apps/web/vitest.config.ts` + test runner pertama (lihat §19.4)

**Interfaces:**
- `arch_test.go` diekstrak ke `arch.Check(t, modulePrefix string) []Violation` di file non-test, lalu `arch_test.go` memanggilnya, lalu `arch_self_test.go` **membuktikan** ia menyala dengan fixture di `testdata/` yang melanggar aturan. Tanpa ini, gate §5.1 bisa mati diam-diam — persis yang terjadi pada `modulePrefix` yang di-hand-copy.

- [ ] **Step 1: Hentikan skip senyap di test repository**

36 test di balik `SMM_TEST_DB=1`. Ganti `t.Skip` dengan `t.Skip` **hanya** kalau env belum di-set **dan** tulis pesan yang menghitung: `t.Log("SKIPPED (needs SMM_TEST_DB=1) — this package contributes ~0% coverage")`. Lalu tambahkan ke `ci.yml` satu job yang benar-benar menjalankan `SMM_TEST_DB=1 go test ./internal/repository/...` dengan service Postgres. Kalau job itu tidak bisa dijalankan, **hapus** ekspektasi coverage repository dari dokumen — jangan biarkan 0,4% terlihat seperti "ok".

- [ ] **Step 2: Perbaiki `newTestInternal`**

`internal_test.go:234` mengirim `nil` untuk action store, jadi setiap test callback hijau lewat jalur `ErrUnavailable`. Ubah helper-nya menerima store, dan default-nya store yang **sukses** sehingga `screenshot` yang disanitasi benar-benar bisa di-assert.

- [ ] **Step 3: Self-test untuk arch gate**

Buat `arch/testdata/violating/service/…` yang meng-import `repository`. `arch_self_test.go` menjalankan `arch.Check` terhadap fixture itu dan **gagal kalau tidak ada violation yang dilaporkan**. Kalau `arch.Check` melaporkan 0 untuk fixture yang jelas melanggar, gate-nya mati — dan sekarang ada yang mengetahuinya.

- [ ] **Step 4: Runner test frontend**

`apps/web` tidak punya runner sama sekali. Tambahkan `vitest` + script `test`, lalu **satu** test yang bernilai: `lib/api.test.ts` meng-assert `listActions(…, 200)` benar-benar menambahkan `?limit=200` (menjaga Task 12), dan satu test untuk `safeNext()` yang menolak `//evil.com` dan `\\evil.com`. Ini setengah bukti bahwa FE punya coverage; angka 60% adalah pekerjaan terpisah dan **tidak boleh diklaim** sampai ada runner yang mengukurnya.

- [ ] **Step 5: `pnpm test` harus gagal kalau satu workspace tidak punya script test**

Tambahkan script `test` di **semua** workspace yang punya kode (`packages/ui`, `packages/shared`), sehingga turbo tidak diam-diam melewati yang tidak punya.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "test: make the existing gates capable of failing

36 repository tests skipped silently behind an env var CI never set, the arch
test had no self-test, newTestInternal passed a nil store so every callback test
was green for the wrong reason, and the frontend had no runner at all."
```

---

## Task 20: Gate CI yang diwajibkan dokumen (F-17, §3)

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile` (hapus duplikat `up-fleet` di `:41-42`)

- [ ] **Step 1: Tambahkan `govulncheck`**

Baris di job `go`, setelah `go vet`:
```yaml
- name: Vulnerability scan
  run: |
    go install golang.org/x/vuln/cmd/govulncheck@latest
    /go/bin/govulncheck ./...
```
`DEVELOPMENT_RULE §5` mewajibkannya "di CI per PR"; `../SECURITY_REVIEW.md` memakainya sebagai bukti sign-off sementara job ini tidak pernah ada.

- [ ] **Step 2: Tambahkan `sqlc-check`**

`sqlc-check` sudah ada di `Makefile:117` dan komentarnya bilang "(CI)" — hanya belum dipanggil. Tambahkan step `run: make -C apps/api sqlc-check`. Drift skema adalah SEV1 (query yangujuk kolom yang tidak ada).

- [ ] **Step 3: Tambahkan trivy image scan**

Tambah setelah build: `aquasecurity/trivy-action` dengan `scan-type: image`, `image-ref` untuk masing-masing Dockerfile. `scan-type: fs` yang sekarang hanya melihat source tree dan lockfile — tidak pernah melihat satu pun CVE di layer apt Playwright/Xvfb, base distroless, atau `node_modules` Next standalone.

- [ ] **Step 4: Tambahkan e2e**

`DEVELOPMENT_RULE §10` menyatakan `e2e (PR only)`. Tambahkan job yang menjalankan `pnpm exec playwright test` di `apps/web` terhadap compose yang sudah dinyalakan. **Prasyarat**: pastikan image `smm-web-1` dibangun **setelah** commit FE terakhir — saya menemukan image (02:43) lebih tua dari commit `1f4d4e9` (03:00), jadi spec sekarang mungkin tidak cocok dengan yang disajikan. Job e2e harus depend pada build, bukan pada container yang sudah ada.

- [ ] **Step 5: Tambahkan `check-compose-exposure.sh` ke job `credential-guard`**

Satu baris, dan sekarang F-04 punya gate.

- [ ] **Step 6: Hapus duplikat Makefile** dan commit

---

# Phase 4 — Rekonsiliasi dokumentasi

## Task 21: Perbaiki semua drift di §3 audit report

**Files:** `../DEVELOPMENT_RULE.md`, `../SECURITY_REVIEW.md`, `../ERD.md`, `../BATCH_SCRAPE.md`, `docs/platforms/*.md`, `../INFRA_ANALYST.md`, `openapi/openapi.yaml`, `../../CHANGELOG.md`

- [ ] **Step 1: `openapi/openapi.yaml` — 24 operasi hilang, 5 hantu, 11 method hilang**

Jalankan perbandingan router↔spec, lalu tambahkan operasi yang hilang dan hapus yang hantu. Spec adalah SSOT (DRY §0), jadi selama 24 route tidak ada di sana, tipe yang di-generate ke FE **berbohong** — dan setiap orang yang baca FE akan menyimpulkan endpoint itu tidak ada.

- [ ] **Step 2: `../DEVELOPMENT_RULE.md`**

Perbaiki: `:229` (sweeper — setelah Task 2 sudah ada, tambahkan catatan bahwa ia *tidak pernah* ada selama ini); `:117` (Idempotency-Key — sudah_selected_after Task 7; kalau tidak diimplementasikan, ubah jadi "dianjurkan" **atau** implementasikan; jangan biarkan klaim palsu); `:249` (`allkeys-lru` → `noeviction`, Task 4); `:248` (tag timescale); `:274` (`RECONCILE_INTERVAL_SECONDS` default 0 di kode, 5 di compose — tulis keduanya); `:291` (FE coverage: tulis angka yang **benar** hari ini, bukan 60%); `:5` (OpenAPI SSOT — catat bahwa sync-nya baru dijalankan di CI setelah Task 20).

- [ ] **Step 3: `../SECURITY_REVIEW.md`**

 Tambahkan revisi baru yang **menggantikan** sign-off P5-04, dengan tabel yang sama tapi, dengan menunjukkan: SSRF "strict allowlist" → allowlist skema + IP; credential "PASS" → key pernah ter-commit; RBAC → tetap FIXED tapi tambahkan matriks route sebagai bukti. Tulis tanggal dan commit yang diaudit. Jangan hapus revisi lama — tulis yang baru di atasnya, dengan label "superseded".

- [ ] **Step 4: `../ERD.md`**

Lengkapi 18 tabel yang belum ada. Kalau tidak ada energi untuk itu sekarang, tulis **acknowledged gap** yang eksplisit di bagian atas — ERD yang mengklaim lengkap padahal mencakup 8 dari 26 lebih berbahaya daripada ERD yang jujur soal belum selesai.

- [ ] **Step 5: `docs/platforms/*.md` + `../BATCH_SCRAPE.md`**

Selaraskan dengan keputusan Task 9. Kalau Opsi A, perbarui `threads.md §4.2` dengan payload `includeReplies` yang baru. Kalau Opsi B, tulis batasan dengan jujur.

- [ ] **Step 6: `../../CHANGELOG.md`**

Satu entri untuk setiap task di plan ini, Conventional Commits, plus satu entri eksplisit: "SECURITY: CREDENTIAL_KEY dan JWT_SECRET versi lama dianggap kompromis; rotasi dilakukan pada <tanggal>".

- [ ] **Step 7: `scripts/check_docs_links.py` dan commit**

---

## Pelaksanaan

Urutan yang saya sarankan, dan alasannya:

1. **Task 1 → Task 2 → Task 3** (SEV1). Task 1 harus pertama: memperbaiki Task 11/12b membuat screenshot bekerja, dan **itu akan memicu F-01** kalau Task 1 belum masuk.
2. **Task 4 → Task 5 → Task 6** (permukaan serangan). Urutan ini penting: Task 5 diawali Task 4, karena sementara Redis terbuka, token worker bisa dicuri dari antrean.
3. **Task 7, Task 8, Task 9** (SEV2 correctness) — independen satu sama lain, bisa paralel.
4. **Phase 2** bisa paralel per file; tidak ada dependensi data.
5. **Task 19–20** (test & CI) sebenarnya **seharusnya mendahului** Phase 2 — setiap task Phase 2 menambahkan test-nya sendiri, dan Task 19 membuat gate itu bisa dipercaya. Kalau waktunya mepet, memangkannya juga, geser Task 19 ke depan..
6. **Task 21** terakhir, setelah kode berhenti bergerak.

Setiap task selesai bila test yang ditulisnya hijau **dan** suite penuh (`go test ./... -count=1`, `pnpm test`, e2e) tetap hijau, dan commit-nya land. Kalau satu task butuh dua commit, pecah jadi dua task — bukan menggabung.
