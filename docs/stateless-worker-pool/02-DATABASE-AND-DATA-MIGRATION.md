# 02 — Rencana Skema Database, sqlc, & Migrasi Data 486 Akun ke `boxd`

**Bagian dari:** [Dokumentasi Stateless Worker Pool](./README.md)  
**Target Direktori:** `apps/api/db/migrations/` & `apps/api/db/queries/`

---

## 1. Prinsip Migrasi Data (*Zero Data Loss*)

Database lokal saat ini memiliki **486 akun live** (485 Instagram, 1 Threads) dan **58 baris worker**. Transisi ke `smm-hub` di `boxd` wajib menjamin:
1. Seluruh ciphertext `account.password_enc` dan `account_session.storage_state` berpindah utuh dengan `CREDENTIAL_KEY` yang sama (transfer via file `.boxenv` mode `600`, tidak pernah di-print ke chat/log).
2. Akun yang sebelumnya sudah terikat ke worker ber-lokasi tetap mewarisi `location`, `latitude`, dan `longitude` yang sama persis.
3. Setiap akun mendapatkan kolom `timezone` yang sesuai dengan kotanya (`Asia/Jakarta` atau `Asia/Makassar`) untuk mencegah kebocoran timezone Frankfurt (`fra`).

---

## 2. Spesifikasi Migrasi `000024_stateless_worker_pool`

### 2.1 File Baru: `apps/api/db/migrations/000024_stateless_worker_pool.up.sql`

```sql
-- 000024 — Stateless Worker Pool (boxd-ready):
-- 1. Pindahkan lokasi GPS, timezone, dan user_agent ke tabel account.
-- 2. Tambahkan kolom endpoint_url pada worker untuk alamat HTTPS boxd worker.
-- 3. Lepas constraint UNIQUE (worker_id, platform) agar 10 worker dapat dibagi pakai.
-- 4. Perbarui partial index session refresh tanpa syarat worker_id IS NOT NULL.

-- A. Kolom identitas perangkat & lokasi pada account
ALTER TABLE account
    ADD COLUMN location   text,
    ADD COLUMN latitude   double precision,
    ADD COLUMN longitude  double precision,
    ADD COLUMN timezone   text NOT NULL DEFAULT 'Asia/Jakarta',
    ADD COLUMN user_agent text;

ALTER TABLE account
    ADD CONSTRAINT account_location_coords_pairing
    CHECK (
        (location IS NULL AND latitude IS NULL AND longitude IS NULL)
        OR (location IS NOT NULL AND latitude IS NOT NULL AND longitude IS NOT NULL)
    );

-- Backfill lokasi dari worker eksisting agar histori GPS akun tidak berubah
UPDATE account a
SET
    location  = w.location,
    latitude  = w.latitude,
    longitude = w.longitude,
    timezone  = CASE
        WHEN w.location IN ('Makassar', 'Denpasar', 'Balikpapan') THEN 'Asia/Makassar'
        ELSE 'Asia/Jakarta'
    END
FROM worker w
WHERE a.worker_id = w.id
  AND w.location IS NOT NULL
  AND w.latitude IS NOT NULL
  AND w.longitude IS NOT NULL;

-- Backfill user_agent default agar akun lama memiliki fingerprint konsisten
UPDATE account
SET user_agent = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36'
WHERE user_agent IS NULL;

-- B. Kolom endpoint HTTPS untuk worker di boxd (mis. https://smm-worker-01.boxd.sh)
ALTER TABLE worker
    ADD COLUMN endpoint_url text;

-- C. Lepas constraint JANTUNG lama (1 container = maks 1 akun per platform)
ALTER TABLE account
    DROP CONSTRAINT IF EXISTS account_worker_platform_key;

-- Reset worker_id pada akun idle (worker_id kini bermakna Active Interactive Lease)
UPDATE account
SET worker_id = NULL
WHERE auth_status NOT IN ('AUTHENTICATING', 'NEEDS_INPUT');

CREATE INDEX account_location_idx ON account (location);

-- D. Perbarui partial index session refresh (sinkron dengan ListAccountsDueForRefresh: worker_id IS NULL)
DROP INDEX IF EXISTS account_refresh_due_idx;

CREATE INDEX account_refresh_due_idx
    ON account (cookie_expiry_at)
    WHERE (
        (auth_status = 'PENDING' AND cookie_expiry_at IS NULL)
        OR (
          cookie_expiry_at IS NOT NULL
          AND auth_status IN ('AUTHENTICATED', 'FAILED')
        )
      )
      AND status = 'ACTIVE'
      AND worker_id IS NULL;
```

### 2.2 File Rollback: `apps/api/db/migrations/000024_stateless_worker_pool.down.sql`

```sql
DROP INDEX IF EXISTS account_refresh_due_idx;

CREATE INDEX account_refresh_due_idx
    ON account (cookie_expiry_at)
    WHERE (
        (auth_status = 'PENDING' AND cookie_expiry_at IS NULL)
        OR (
          cookie_expiry_at IS NOT NULL
          AND auth_status IN ('AUTHENTICATED', 'FAILED')
        )
      )
      AND status = 'ACTIVE'
      AND worker_id IS NOT NULL;

DROP INDEX IF EXISTS account_location_idx;

UPDATE account SET worker_id = NULL;

ALTER TABLE account
    ADD CONSTRAINT account_worker_platform_key UNIQUE (worker_id, platform);

ALTER TABLE worker
    DROP COLUMN IF EXISTS endpoint_url;

ALTER TABLE account
    DROP CONSTRAINT IF EXISTS account_location_coords_pairing,
    DROP COLUMN IF EXISTS user_agent,
    DROP COLUMN IF EXISTS timezone,
    DROP COLUMN IF EXISTS longitude,
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS location;
```

---

## 3. Struktur Baris 10 Worker Utama di Tabel `worker` (`boxd` Fleet)

Pada lingkungan `boxd`, tabel `worker` berisi tepat **10 baris permanen** yang merepresentasikan ke-10 microVM worker di region `fra`:

| `name` | `endpoint_url` | `novnc_service` | `source` | `desired_state` | `region` |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `smm-worker-01` | `https://rpc.smm-worker-01.boxd.sh` | `https://smm-worker-01.boxd.sh/novnc` | `MANUAL` | `RUNNING` | `ID` |
| `smm-worker-02` | `https://rpc.smm-worker-02.boxd.sh` | `https://smm-worker-02.boxd.sh/novnc` | `MANUAL` | `RUNNING` | `ID` |
| `...` | `...` | `...` | `...` | `...` | `...` |
| `smm-worker-10` | `https://rpc.smm-worker-10.boxd.sh` | `https://smm-worker-10.boxd.sh/novnc` | `MANUAL` | `RUNNING` | `ID` |

> [!TIP]
> **Kompatibilitas Lokal (Docker) vs Cloud (`boxd`):**  
> Karena `endpoint_url` hanyalah URL HTTP/HTTPS standar, di laptop lokal nilainya bisa diset ke `http://smm-worker-01:8090` (jaringan internal Docker), sementara di `boxd` nilainya `https://rpc.smm-worker-01.boxd.sh`. Kode Go API dan TypeScript Worker **100% identik** di kedua lingkungan!

---

## 4. Rencana Perubahan Query `sqlc` (`apps/api/db/queries/`)

1. **[`account.sql`](../../apps/api/db/queries/account.sql):**
   - Tambahkan `location, latitude, longitude, timezone, user_agent` ke seluruh query baca/tulis `account`.
   - Pada `ListAccountsDueForRefresh`: hapus `AND worker_id IS NOT NULL`, ganti menjadi `AND worker_id IS NULL` (tidak menabrak akun yang sedang dipinjam sesi interaktif lain).
   - Tambahkan `AcquireAccountWorkerLease` (mengisi `worker_id` saat sesi login/CAPTCHA/Live View dimulai) dan `ReleaseAccountWorkerLease` (mengembalikan `worker_id = NULL` saat selesai).
   - Pada `ReclaimStaleLoginAttempt`: tambahkan `worker_id = NULL` agar lease yang kedaluwarsa otomatis bersih.
2. **[`account_session.sql`](../../apps/api/db/queries/account_session.sql):**
   - Tambahkan `RefreshAccountSessionState` untuk menulis balik (`upsert`) `storage_state` terbaru seusai eksekusi action berhasil sambil mempertahankan `expires_at` apabila tidak berubah.
3. **[`worker.sql`](../../apps/api/db/queries/worker.sql):**
   - Sertakan `endpoint_url` pada `CreateWorker` dan `UpdateWorker` (plus query `UpsertPoolWorkerByName` untuk bootstrapper idempotent ke-10 worker utama).

---

## 5. Prosedur Transfer Database & Object Storage dari Mac ke `boxd` (`smm-hub`)

Mengikuti aturan transfer data teruji dari `boxd-recap-smm-migration.md` §5 & §8:

1. **Rescue Session Lokal di Mac (Sebelum Dump):**  
   Pastikan seluruh baris `account` dengan `auth_status = 'AUTHENTICATED'` sudah memiliki entri di `account_session`.
2. **Buat Dump PostgreSQL Terkompresi + Hitung SHA-256 di Mac:**
   ```sh
   mkdir -p .local-runtime/backups
   docker exec smm-postgres-1 pg_dump -U smm -Fc --no-owner --no-acl smm \
     > .local-runtime/backups/smm-pre-pool.dump
   shasum -a 256 .local-runtime/backups/smm-pre-pool.dump \
     > .local-runtime/backups/smm-pre-pool.dump.sha256
   ```
3. **Streaming Upload ke `smm-hub` via `boxd machine cp` (Bukan lewat SSH tar pipe):**
   ```sh
   set -a; . ~/.config/boxd/env; set +a
   boxd machine cp --mode 600 .local-runtime/backups/smm-pre-pool.dump smm-hub:/tmp/smm.dump
   ```
4. **Verifikasi SHA-256 & Restore di Dalam `smm-hub`:**
   ```sh
   boxd machine exec smm-hub -- 'sha256sum /tmp/smm.dump'
   boxd machine exec smm-hub -- 'docker exec -i smm-postgres pg_restore -U smm -d smm --clean --if-exists < /tmp/smm.dump && shred -u /tmp/smm.dump'
   ```
5. **Jalankan Migrasi `000024` & Backfill Lokasi:**  
   Begitu `smm-api` dinyalakan di `smm-hub`, migrasi `000024` dijalankan dan `PoolBootstrapper` memastikan ke-486 akun memiliki kota + GPS + timezone Indonesia yang valid serta membersihkan 58 baris worker Docker lama menjadi 10 baris `smm-worker-01..10`.
