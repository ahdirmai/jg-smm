-- 000007 down — reverse worker geolocation.
ALTER TABLE worker DROP CONSTRAINT IF EXISTS worker_location_coords_pairing;
ALTER TABLE worker DROP COLUMN IF EXISTS longitude;
ALTER TABLE worker DROP COLUMN IF EXISTS latitude;
ALTER TABLE worker DROP COLUMN IF EXISTS location;
