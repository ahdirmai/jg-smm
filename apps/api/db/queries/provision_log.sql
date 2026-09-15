-- provision_log queries: append-only audit of every provisioner op.

-- name: AppendProvisionLog :exec
INSERT INTO provision_log (worker_id, op, generation, k8s_ref, status, error)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: ListProvisionLogsByWorker :many
SELECT * FROM provision_log
WHERE worker_id = $1
ORDER BY ts DESC
LIMIT $2;
