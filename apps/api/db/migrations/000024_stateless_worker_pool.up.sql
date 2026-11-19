-- 000024 — stateless worker pool (boxd-ready).
--
-- 1. The browser fingerprint (city, GPS, timezone, UA) moves from worker to
--    account. A boxd worker is a shared, disposable microVM that wakes for one
--    job and sleeps again, so "which machine" no longer implies "which city" —
--    the account has to carry its own identity or every context would report
--    the worker's Frankfurt datacentre position.
-- 2. worker.endpoint_url carries the HTTPS RPC base the API pushes to
--    (https://rpc.smm-worker-01.boxd.sh in the fleet, http://smm-worker-01:8090
--    locally). One column serves both environments.
-- 3. The old UNIQUE(worker_id, platform) is the "one container hosts at most one
--    account per platform" rule. A worker now runs one account at a time, so the
--    rule would cap a 486-account fleet at 14 Instagram accounts. worker_id
--    becomes an ACTIVE INTERACTIVE LEASE instead: nil = idle and schedulable,
--    set = a login/CAPTCHA/live-view session is parked on that worker.

-- A. Per-account device identity.
ALTER TABLE account
    ADD COLUMN location   text,
    ADD COLUMN latitude   double precision,
    ADD COLUMN longitude  double precision,
    ADD COLUMN timezone   text NOT NULL DEFAULT 'Asia/Jakarta',
    ADD COLUMN user_agent text;

-- Half a location is a row no context can be built from, so the three travel
-- together or not at all.
ALTER TABLE account
    ADD CONSTRAINT account_location_coords_pairing
    CHECK (
        (location IS NULL AND latitude IS NULL AND longitude IS NULL)
        OR (location IS NOT NULL AND latitude IS NOT NULL AND longitude IS NOT NULL)
    );

-- Inherit the position the account already reported from, so an existing
-- account's GPS history does not jump on cutover. WITA cities otherwise.
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

-- The fingerprint Playwright must present; new contexts reuse it verbatim so
-- the platform sees one device across login and action.
UPDATE account
SET user_agent = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36'
WHERE user_agent IS NULL;

-- B. Worker RPC endpoint.
ALTER TABLE worker
    ADD COLUMN endpoint_url text;

-- C. worker_id: from placement to lease.
ALTER TABLE account
    DROP CONSTRAINT IF EXISTS account_worker_platform_key;

-- Only a parked interactive session (a login in flight, or one waiting for a
-- human) holds a worker now. Everything else is idle and schedulable, which is
-- what the due query below reads.
UPDATE account
SET worker_id = NULL
WHERE auth_status NOT IN ('AUTHENTICATING', 'NEEDS_INPUT');

CREATE INDEX account_location_idx ON account (location);

-- D. The due index must mirror ListAccountsDueForRefresh exactly, including its
-- `worker_id IS NULL` gate: a leased account is mid-interaction and must not be
-- re-dispatched. Postgres only uses a partial index whose predicate the query
-- provably implies.
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
