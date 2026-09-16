-- 000007 — worker geolocation.
-- Scope: per-worker GPS coordinates so Playwright can spoof a fixed position
-- before any job runs. Region stays the ISO country code (always ID for the
-- MVP); location is the CITY the worker operates from, and latitude/longitude
-- are one randomized point inside that city's radius, chosen once at create
-- time and frozen afterwards — a worker that jumps between cities between
-- actions looks bot-like, a stable per-worker point does not.
--
-- Invariants:
--   * location is the city name from the reference list the API owns; it is
--     free text here only because the list is a code constant, not a table.
--   * latitude/longitude are present whenever location is, and absent
--     together. A half-set pair would make the worker's geolocation
--     indeterminate.
--   * The coordinates are unconstrained numerically: the reference list is
--     small and validated in the service, and a CHECK here would duplicate
--     that while silently drifting if a city were added to code only.

ALTER TABLE worker
    ADD COLUMN location  text,
    ADD COLUMN latitude  double precision,
    ADD COLUMN longitude double precision;

-- One city, one point. Set together or not at all.
ALTER TABLE worker
    ADD CONSTRAINT worker_location_coords_pairing
    CHECK (
        (location IS NULL AND latitude IS NULL AND longitude IS NULL)
        OR (location IS NOT NULL AND latitude IS NOT NULL AND longitude IS NOT NULL)
    );
