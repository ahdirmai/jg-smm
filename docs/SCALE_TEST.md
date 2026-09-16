# Scale Test — ~50 Containers (P5-01)

Target: the fleet is **stable at ~50 containers / 100 accounts**, the packing
invariants hold, and the pod quota is not exceeded.

This document has two halves:

1. **What is proven in code** — the unit test that runs on every commit and
   proves the packing invariants at ~50-container scale.
2. **What an operator runs by hand** — the soak procedure against a real cluster.
   The local dev machine (Mac M2, 16 GB) **cannot** run 50 browser containers,
   so the live soak is an operator procedure on the real fleet, not a `make`
   target.

---

## 1. What is proven in code

`apps/api/internal/service/packer_test.go` → `TestPackScaleFiftyContainers`.

The test is table-driven over two shapes that both land on ~50 containers,
covering the quota both as documented and as shipped once every platform is
live:

| Shape | Platforms | `MAX_ACCOUNTS_PER_CONTAINER` | Accounts | Containers |
| --- | --- | --- | --- | --- |
| `mvp_2_platforms_quota_2` | 2 (MVP: instagram, threads) | 2 | 100 | 50 |
| `all_7_platforms_quota_7` | 7 (all supported) | 7 | 350 | 50 |

The `mvp_2_platforms_quota_2` row is the literal P5-01 scope
("100 akun ter-pack ~50 container", PRD §F3.9). The `all_7_platforms_quota_7`
row is the same fleet shape once all seven platforms ship — a container tops
out at one account per platform, so raising the cap to 7 does **not** collapse
50 containers into fewer.

For each shape the test asserts, across the whole fleet:

- **Packs without error** — every one of the 100/350 accounts is assigned.
- **Fleet size is exactly 50** — no container is over- or under-created.
- **The per-container cap holds** — no container hosts more than
  `MAX_ACCOUNTS_PER_CONTAINER` accounts (the quota invariant).
- **One account per platform per container** — no container holds two accounts
  of the same platform (`UNIQUE (worker_id, platform)` in
  `000003_accounts.up.sql`).
- **No account is left unpacked** — every account row has a `worker_id`.
- **AUTO containers are reaped once empty** — after releasing every account,
  zero containers remain; a MANUAL container would have survived (asserted by
  `TestReleaseKeepsManualContainer`).

Run it with:

```bash
cd apps/api && go test ./internal/service/ -run "TestPackScaleFiftyContainers" -v
```

These are the invariants the live soak below is checking for drift, not
re-deriving.

---

## 2. Why 50 containers is not runnable locally

Per `INFRA_ANALYST.md` §3.2, one worker container is sized at
**request 750 mCPU / 1 GiB**, **limit 2000 mCPU / 4 GiB** (Chromium memory
spike headroom; memory overcommit is disallowed because an OOMKill kills the
accounts on that container). Fifty of them is therefore
**~37 vCPU and ~50 GiB of memory reservation**.

A Mac M2 with 16 GB runs the stack with **`--scale worker=3`** at most
(`compose.yaml`). That is enough to develop and to verify packing behaviour,
and the unit test covers the invariant at scale — but the live 50-container
soak belongs on the real fleet.

---

## 3. Operator procedure — live soak (~50 containers)

Run this against the deployed fleet, not the dev laptop. It assumes the stack
is already up (`make up`, `make up-obs` for the Prometheus/Grafana/Loki tier)
and that migrations are applied (`make migrate-status`).

### 3.1 Before you start — capacity checks

Confirm the fleet has room for 50 containers and the quota is not already
saturated:

```bash
# Current containers and their account load (should be well under quota).
docker compose exec api sh -c 'echo "select id, name, source, desired_state from worker order by created_at;" | psql "$DATABASE_URL"'
```

Expected: `source=AUTO` containers exist only where auto-create was used; the
fleet is empty by default (containers are created by the operator, or
auto-created on demand when `PROVISION_AUTO_CREATE=true`).

Record a baseline from the dashboards (Grafana, `make up-obs`):

- worker pod count and `k8s_pod_pending_total` (must be 0 — a pending pod means
  the ResourceQuota or node capacity is short, `INFRA_ANALYST.md` §11),
- worker memory: request vs actual (should sit under 1 GiB idle),
- Redis `used_memory` as a % of `maxmemory` (alert at >70%).

### 3.2 Load the fleet to ~50 containers

Create 100 accounts across the two MVP platforms (50 instagram, 50 threads),
then let the packer place them:

```bash
# 1. Seed the accounts (via the API as the bootstrap owner).
#    The dashboard "Accounts > New" page or the API both work; the packer
#    runs on account create.
# 2. Watch the fleet converge to ~50 containers at quota 2:
make ps
```

What you are checking, at each step:

| Check | Pass condition |
| --- | --- |
| Fleet size | 50 containers for 100 accounts at quota 2 |
| Cap | No container holds more than `MAX_ACCOUNTS_PER_CONTAINER` accounts |
| One platform per container | No container holds two accounts of the same platform |
| No orphan accounts | Every account row has a `worker_id` |
| Pod quota | `k8s_pod_pending_total` stays 0; ResourceQuota (500 pod) is not hit |
| Reap | Deleting all accounts off an AUTO container removes the container |

### 3.3 Soak (stability)

Hold the fleet at ~50 containers under a realistic action load and watch for
drift over the soak window (start with 30 minutes; the GA sign-off in
`GA_SIGNOFF.md` asks for longer):

```bash
# Drive the API with the k6 load test (P5-07), p95 target 1500 ms.
make load-test
```

Pass conditions:

- **Stable** — no worker restarts, no OOMKills
  (`docker compose ps`, `docker compose logs worker | grep -i oom` empty).
- **Resource fits** — worker memory stays under the 4 GiB limit; CPU request
  750 m is not saturated.
- **Quota held** — no pod pending, ResourceQuota unused headroom remains.

### 3.4 Chaos + DR (the P5-01 test plan)

- **Kill a node / container**: `docker compose restart worker` (or stop one
  container). Expect the reconciler to bring the desired state back; the
  accounts on that container go idle for the duration (blast radius = the
  accounts on that one container, bounded by the cap).
- **DR drill**: `make drill` — backs up Postgres + WAL, MinIO and worker
  sessions, wipes the app volumes, restores, and verifies. **Destructive:
  never run against production data.**

### 3.5 After the soak

- Confirm the fleet drains cleanly: remove the accounts, and every AUTO
  container should be reaped (MANUAL ones survive by design).
- File the result against this ticket: fleet size reached, p95 from the load
  test, any OOMKill/restart, and the Grafana snapshot link.

---

## 4. When this test fails

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Fleet size != 50 | A platform conflict looped past every container, or auto-create is off | Check `PROVISION_AUTO_CREATE` and the account platform mix |
| Container over the cap | `MAX_ACCOUNTS_PER_CONTAINER` raised without a matching platform set | The cap and the platform count must agree; a container holds at most one account per platform |
| Two same-platform accounts on one container | `UNIQUE (worker_id, platform)` constraint missing or bypassed | Check `000003_accounts.up.sql`; the store constraint is the authority |
| AUTO container survives a full drain | Reap path skipped, or the container is MANUAL | `Release`/`ReapEmpty` only delete empty AUTO containers; check `worker.source` |
| Pod pending during the live soak | Node memory or ResourceQuota exhausted | See `INFRA_ANALYST.md` §3.2 for node sizing; 50 pods ≈ 37 vCPU / ~50 GiB |

---

## 5. References

- Ticket: `tickets/p5_01.md` · PRD §F3.9 · `DEVELOPMENT_RULE.md` (bin-packing)
- Packing logic: `apps/api/internal/service/packer.go`
- Invariants at scale: `apps/api/internal/service/packer_test.go`
- Load test (P5-07): `infra/load/api.js`, `LOAD_TEST.md`
- Sizing and quota: `INFRA_ANALYST.md` §3.2, §11
- DR procedure: `runbooks/` and `make drill`
