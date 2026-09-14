-- Bootstrap migration for P0-02 (walking skeleton). Real schema starts in P0-04.
-- Keeps `migrate up` valid on an empty repo and proves the toolchain wires up.
CREATE TABLE IF NOT EXISTS schema_bootstrap (
    id         smallint PRIMARY KEY DEFAULT 1,
    applied_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT schema_bootstrap_singleton CHECK (id = 1)
);

INSERT INTO schema_bootstrap (id) VALUES (1) ON CONFLICT DO NOTHING;
