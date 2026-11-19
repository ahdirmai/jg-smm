-- 000024 rollback: restore the container-placement model.
--
-- worker_id is cleared before the UNIQUE(worker_id, platform) is re-added:
-- several accounts of one platform now share a lease, and the constraint would
-- otherwise fail on rows the up-migration rewrote.

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
