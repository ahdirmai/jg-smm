# Runbook index

Every alert in MVP-1-SMM has a runbook here. Severity definitions, ack/resolve
SLAs and the escalation ladder are in [`../../docs/SEVERITY.md`](../../docs/SEVERITY.md).
Backup strategy and the DR drill mechanics are in
[`../../docs/BACKUP.md`](../../docs/BACKUP.md).

Each runbook follows the same shape: **ALERT / Severity / Symptoms / Impact /
Likely causes / Diagnosis / Remediation / Verification / Post-mortem prompt**.
Diagnosis commands are copy-pasteable and ordered — run them top to bottom, and
escalate when the first pass is inconclusive instead of improvising.

## Index

### Data plane (SEV1 if down)

| Runbook                                        | Sev  | Fires when                                       |
| ---------------------------------------------- | ---- | ------------------------------------------------ |
| [postgres-unhealthy.md](postgres-unhealthy.md) | SEV1 | healthcheck failing — no reads or writes at all  |
| [redis-unhealthy.md](redis-unhealthy.md)       | SEV1 | healthcheck failing — action dispatch stops      |
| [minio-unhealthy.md](minio-unhealthy.md)       | SEV2 | healthcheck failing — scrapes and evidence break |
| [backup-failure.md](backup-failure.md)         | SEV2 | `make backup` exited non-zero — RPO at risk      |

### App plane

| Runbook                                                | Sev  | Fires when                                     |
| ------------------------------------------------------ | ---- | ---------------------------------------------- |
| [api-unhealthy.md](api-unhealthy.md)                   | SEV1 | API healthcheck (`/healthz`) failing           |
| [api-readiness-degraded.md](api-readiness-degraded.md) | SEV2 | `/readyz` reports `degraded` — a check is down |
| [worker-crashloop.md](worker-crashloop.md)             | SEV2 | heartbeat budget exhausted / container looping |
| [worker-auth-failure.md](worker-auth-failure.md)       | SEV2 | login outcome `rejected` or `failed`           |
| [quarantine-wave.md](quarantine-wave.md)               | SEV1 | > 5 accounts auto-quarantined in 1h            |

### Fleet signals (Prometheus)

| Runbook                                                  | Sev  | Fires when                      |
| -------------------------------------------------------- | ---- | ------------------------------- |
| [action-success-rate-low.md](action-success-rate-low.md) | SEV2 | success rate < 85% over 10m     |
| [fleet-health-low.md](fleet-health-low.md)               | SEV2 | avg account health < 50 for 15m |
| [queue-depth-high.md](queue-depth-high.md)               | SEV2 | avg queue depth > 50 for 15m    |

### Cost (P5-08)

| Runbook                                              | Sev  | Fires when                              |
| ---------------------------------------------------- | ---- | --------------------------------------- |
| [apify-budget-exceeded.md](apify-budget-exceeded.md) | SEV2 | Apify spend > 90% of `APIFY_BUDGET_USD` |
| [proxy-budget-exceeded.md](proxy-budget-exceeded.md) | SEV2 | proxy egress > 90% of `PROXY_BUDGET_GB` |

### Control plane

| Runbook                                            | Sev  | Fires when                                       |
| -------------------------------------------------- | ---- | ------------------------------------------------ |
| [reconciler-drift.md](reconciler-drift.md)         | SEV2 | desired worker state not applied on the platform |
| [rate-limit-exhausted.md](rate-limit-exhausted.md) | SEV3 | platform rate budget spent — actions gated       |
| [cooldown-stalled.md](cooldown-stalled.md)         | SEV3 | cooldown gate blocking all action dispatch       |

### Business alerts (AlertEngine)

| Runbook                                          | Sev  | Fires when                                       |
| ------------------------------------------------ | ---- | ------------------------------------------------ |
| [alert-mention-spike.md](alert-mention-spike.md) | SEV2 | mentions > 3x the 7-day baseline (critical ≥ 6x) |
| [alert-views-drop.md](alert-views-drop.md)       | SEV3 | views < 50% of the prior 24h (critical < 25%)    |

### Maintenance

| Runbook                    | Sev  | When to use                                          |
| -------------------------- | ---- | ---------------------------------------------------- |
| [dr-drill.md](dr-drill.md) | SEV3 | quarterly DR drill, or after any restore-path change |

## How to read a runbook fast

1. **Symptoms** — is this actually my alert? (Alert noise is a real cost.)
2. **Diagnosis** — the commands. This is the 5-minute budget.
3. **Remediation** — ordered, and each step says what it changes.
4. **Verification** — the alert is closed when this passes, not when the
   symptom disappears.

## Common diagnosis commands

```bash
make ps                    # stack status: health + which services are up
make logs S=api            # tail one service (S=worker, S=postgres, ...)
make down && make up       # the "have you tried turning it off" rung
make backup                # before touching data, always
```

Queue depth per worker — flat means nothing is consuming, draining means work
is flowing. Referenced from several runbooks as "queue depth":

```bash
for k in $(docker compose exec -T redis redis-cli --scan --pattern 'queue:action:*'); do
  echo "$k = $(docker compose exec -T redis redis-cli llen "$k")"
done
```

MinIO bucket listing, used by the minio/backup/drill runbooks:

```bash
docker compose run --rm --no-deps \
  -e MINIO_ROOT_USER="$(docker compose exec -T minio printenv MINIO_ROOT_USER)" \
  -e MINIO_ROOT_PASSWORD="$(docker compose exec -T minio printenv MINIO_ROOT_PASSWORD)" \
  --entrypoint /bin/sh minio-init \
  -c 'mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; mc ls local'
```

Environment switches that change which background loops run (all default `0` =
off, see `compose.yaml`):

```bash
ALERT_INTERVAL_SECONDS=600     # AlertEngine evaluation loop
AGGREGATE_INTERVAL_SECONDS=1800
ACTION_INTERVAL_SECONDS=60     # action scheduler (cooldown + ratelimit gates)
RECONCILE_INTERVAL_SECONDS=30
PROVISIONER_MODE=static        # local: no cluster, docker compose --scale
ACTION_DRY_RUN=true            # worker stops before committing an action
```

## Post-mortem prompt

Every SEV1/SEV2 gets a post-mortem within 48 h. Answer these four, in this
order:

1. **What was the user-visible blast radius, and for how long?** (From the
   first failed healthcheck to the passing verification step.)
2. **When did we first have the signal, and when did we act on it?** If the
   gap is > 5 minutes the runbook needs a faster diagnosis step.
3. **What was the root cause, in one sentence?** Not the symptom chain — the
   cause. "Postgres was down" is a symptom.
4. **What code, runbook or alert change prevents this class of failure?** An
   incident with no follow-up item will recur.

## Adding a runbook

1. Copy an existing one in the same severity band; keep the section order.
2. Every command must have been run at least once — a runbook with an
   unverified command is worse than no runbook.
3. Link it from this index **and** from `../../docs/SEVERITY.md`'s mapping table.
4. If the alert can destroy data, its remediation links to `../../docs/BACKUP.md`.
