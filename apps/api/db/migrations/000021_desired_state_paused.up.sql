-- 000021 — add PAUSED to the desired_state enum, and PAUSE/RESUME to the
-- provision_op audit enum.
--
-- WHY A THIRD STATE
-- Until now a container was either RUNNING or STOPPED, and the reconciler
-- treated STOPPED as "delete the container". That made it useless as a pause:
-- waking an account afterwards meant provisioning from scratch. A slate of ~50
-- auto-spawned containers kept ~4GB of RAM held by idle browsers, and the only
-- declarative way to give it back was to destroy them.
--
-- PAUSED is the missing intent: stop the container, KEEP it and its volumes.
-- The reconciler stops/starts the existing container instead of deleting, so a
-- resume is seconds rather than a re-provision, and the persisted session on
-- the volume survives.
--
-- PAUSE/RESUME in provision_op keep the audit trail honest: a paused container
-- was not deleted, and a reader of provision_log must be able to tell.
--
-- PostgreSQL has no "DROP VALUE" for enums, so the down migration is
-- deliberately unable to remove these — see the down file.
ALTER TYPE desired_state ADD VALUE IF NOT EXISTS 'PAUSED';
ALTER TYPE provision_op ADD VALUE IF NOT EXISTS 'PAUSE';
ALTER TYPE provision_op ADD VALUE IF NOT EXISTS 'RESUME';
