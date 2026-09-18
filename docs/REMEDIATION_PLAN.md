# Remediation plan — local container provisioning and noVNC live view

Companion to [AUDIT_REPORT.md](AUDIT_REPORT.md): that file states what is broken
and why, this one states what changes, in what order, and how each change is
proven. `R-0n` ids are the unit of work; each is one commit.

Severity vocabulary comes from [SEVERITY.md](SEVERITY.md). Architectural
constraints come from [ADR 0012](adr/0012-desired-state-worker-provisioning.md)
and [ADR 0001](adr/0001-container-per-device.md).

## 1. Principles

1. **The provisioning seam does not move.** `port.K8sClient` stays the only path
   to a container platform. The Docker driver is a third peer of the k8s and
   static drivers, wired in `provisionDriver` — never a branch inside services.
2. **No new dependency if stdlib will do.** The Docker Engine API is plain HTTP
   over a unix socket; `net/http` with a custom `DialContext` reaches it. The
   Docker Go SDK is not pulled in.
3. **No migration if an existing column will do.** `worker.novnc_service` already
   stores the browser-reachable live-view URL. The chosen port is persisted there
   at create time, so the card can render the button before the first heartbeat.
4. **Loopback only.** The live-view port binds `127.0.0.1`; the k8s tier keeps its
   ClusterIP-only service. `INFRA_ANALYST.md` §7 stays true.
5. **Local tier only.** `PROVISIONER_MODE=docker` exists to make a workstation
   work. It is an explicit opt-in, never a production default.

## 2. Workstreams

### R-01 — Docker provisioner adapter · fixes F-01, F-06 · BLOCKER

**New** `apps/api/internal/adapter/dockerprovisioner/docker.go`, implementing
`port.K8sClient` against the Docker Engine API:

| Method         | HTTP                                                                                                 |
| -------------- | ---------------------------------------------------------------------------------------------------- |
| `CreateWorker` | inspect → delete stale generation → `POST /containers/create?name=…` → `POST /containers/{id}/start` |
| `DeleteWorker` | `DELETE /containers/{name}?force=1&v=1`, then the volumes                                            |
| `Observe`      | `GET /containers/{name}/json` → running + `smm.generation` label; `404` = absent                     |
| `ListRunning`  | `GET /containers/json?all=1&filters={"label":["smm.worker=true"]}`                                   |

Labels: `smm.worker=true`, `smm.worker.id`, `smm.generation`. Host config:
network = the compose network, per-worker named volume at `/data/sessions` and
`/data/screenshots`, restart policy `unless-stopped`, `6080/tcp` published to
`127.0.0.1:<port>`.

Environment injected into the container — this is what finally makes F-03 work:

```
WORKER_ID=worker-<row uuid>   API_URL=http://api:8080
REDIS_URL=redis://redis:6379  PLATFORMS=instagram,threads
NOVNC_PORT=6080               NOVNC_URL=http://localhost:<port>
ACTION_DRY_RUN=true
```

**Acceptance criteria**

- Creating twice at the same generation yields exactly one container.
- A bumped generation replaces the container.
- `Observe` reports `(gen,true,nil)` running and `(0,false,nil)` missing.
- `ListRunning` returns only `smm.worker`-labelled containers.
- Tests run against an `httptest.Server` standing in for the daemon and assert
  the request bodies, not just the returned structs.

### R-02 — Config and compose wiring · fixes F-01, F-02, F-06 · BLOCKER

`apps/api/internal/config/config.go` gains:

| Var                       | Default                | Purpose                       |
| ------------------------- | ---------------------- | ----------------------------- |
| `DOCKER_SOCKET`           | `/var/run/docker.sock` | Daemon endpoint               |
| `DOCKER_NETWORK`          | `smm_default`          | Network workers join          |
| `DOCKER_PUBLIC_HOST`      | `localhost`            | Host the browser resolves     |
| `NOVNC_PORT_MIN` / `_MAX` | `24100` / `24299`      | Allocatable range             |
| `WORKER_IMAGE`            | `smm-worker`           | Image the driver launches     |
| `WORKER_PLATFORMS`        | `instagram,threads`    | Passed through as `PLATFORMS` |

`main.go` `provisionDriver()` grows a `case "docker"` arm; the existing default
keeps returning the static driver so nothing else changes behaviour.

`compose.yaml`: the `api` service mounts `/var/run/docker.sock`, gains the env
above, and sets `PROVISIONER_MODE: ${PROVISIONER_MODE:-docker}` with
`RECONCILE_INTERVAL_SECONDS: ${RECONCILE_INTERVAL_SECONDS:-5}`.

**Acceptance criteria**

- `docker`, `static`, `k8s` accepted; anything else is still a boot error.
- With `PROVISIONER_MODE=docker` the API logs the docker driver at boot, not
  "static driver (no cluster)".
- `go build ./...` and `go vet ./...` clean.

### R-03 — Allocate the live-view port and persist the URL · fixes F-03, F-05, F-12 · BLOCKER

`service/container.go`: `Create(ctx, name, region, location, novncPort *int)`
plus `allocateNovncPort()`. An explicit request is validated against the range
and against every existing worker's stored URL; otherwise the first free port in
the range is taken. The result is written to `worker.novnc_service` as
`http://<DOCKER_PUBLIC_HOST>:<port>` at insert time.

`openapi/openapi.yaml`: `CreateContainerRequest` gains an optional `novncPort`;
`Container` already exposes `novncUrl`. `make generate` regenerates both the Go
and TS types.

**Acceptance criteria**

- The `201` body already carries `novncUrl`, so Live view appears immediately.
- A port outside the range is a `400` naming the range.
- A port held by another worker is a `409`.
- Two consecutive creates never share a port.

### R-04 — Dashboard: port field and an honest live-view control · fixes F-04, F-05 · HIGH

`apps/web/app/(dashboard)/workers/page.tsx` and the containers hook:

- An optional "noVNC port" input in the create form, hinting the valid range.
- The Live view button is **always** rendered; without a `novncUrl` it is
  disabled and says why via tooltip, instead of vanishing.
- The card shows the live-view port when known.

**Acceptance criteria**

- No `novncUrl` → disabled, explained button (not a missing one).
- With a `novncUrl` → `LiveBrowserModal` opens on the right URL.
- An out-of-range port surfaces the API's `400` message inline.

### R-05 — Fleet defaults · fixes F-08 · MEDIUM

The `worker` compose service moves behind `profiles: ['fleet']`. `make up` stops
passing `--scale worker=3`; a new `up-fleet` target preserves the old behaviour.

**Acceptance criteria**

- `make up` yields zero worker containers; `docker ps` stays empty until the
  operator creates one from the dashboard.
- `make up-fleet` still brings up `WORKERS` replicas.

### R-06 — Surface provisioning progress · fixes F-07 · MEDIUM

Expose `provision_log` per worker (`GET /api/containers/{containerId}/logs`) and
render it in the create-progress UI, so a `PENDING` card explains itself. The
`LoggingDriver` already writes the rows; this is read-side only.

### R-07 — Documentation reconciliation · fixes F-10, F-11 · MEDIUM

| File                       | Change                                                                           |
| -------------------------- | -------------------------------------------------------------------------------- |
| `DEVELOPMENT_RULE.md` §8   | `PROVISIONER_MODE={static\|docker\|k8s}`; docker = local only                    |
| `INFRA_ANALYST.md` §7, §15 | local tier publishes the live view on loopback; record the socket-mount tradeoff |
| `ERD.md`                   | `novncService` is a browser URL locally, a ClusterIP name in k8s                 |
| `MANUAL_TESTING.md`        | live view is created with the container; drop "skip if unset"                    |
| `SECURITY_REVIEW.md`       | loopback binding + the docker-socket escalation                                  |
| `PROGRESS.md`              | audit and remediation entries                                                    |
| `compose.yaml`             | drop the inert `8080` runtime env on `web`                                       |

### R-08 — Sweeper label awareness · fixes F-09 · MEDIUM

The docker driver's `ListRunning` reports label-derived ids, which makes the
sweeper meaningful for the first time. Test that an orphaned container is deleted
only after the grace window and that a known worker is never swept.

## 3. Order

```mermaid
graph TD
    R01[R-01 docker driver] --> R02[R-02 config and compose]
    R02 --> R03[R-03 port allocation]
    R03 --> R04[R-04 dashboard]
    R02 --> R05[R-05 fleet defaults]
    R03 --> R06[R-06 provision log]
    R02 --> R08[R-08 sweeper labels]
    R04 --> R07[R-07 docs]
    R06 --> R07
    R08 --> R07
```

R-01 + R-02 are the minimum that makes Create produce a container. R-03 + R-04
are the minimum that makes the live view appear and work. R-05 to R-08 close the
remaining findings.

## 4. Verification

Per ticket: `cd apps/api && go vet ./... && go build ./... && go test ./...`,
`pnpm lint && pnpm typecheck && pnpm build && pnpm test`, and
`python3 scripts/check_docs_links.py`.

End-to-end, recorded in `MANUAL_TESTING.md`:

1. `docker compose down -v && make up` → zero worker containers.
2. Log in, create a container in Jakarta.
3. `docker ps` shows one `smm-worker-*`, bound to a `127.0.0.1:<port>` in range.
4. The card flips `PENDING` → `READY` within one heartbeat interval.
5. Live view opens, shows the Xvfb desktop, and a modal click reaches it.
6. `psql` confirms `worker.novnc_service` matches the URL the card links to.
7. Delete → container, volumes and row all gone.

## 5. Risks and rollback

| Risk                                       | Mitigation                                                                                                                                                 |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| API runs as root to reach the socket (D-1) | Local tier only; `PROVISIONER_MODE=docker` is opt-in and never a production default. Upgrade path: a socket proxy that whitelists only container endpoints |
| A stale image is launched                  | `WORKER_IMAGE` is explicit; a pull failure is recorded in `provision_log` and surfaced on the card                                                         |
| Port collision with a foreign container    | The driver probes forward from the requested port and reports a hard error only when the range is exhausted                                                |
| Regression in the k8s tier                 | That path is untouched; its tests stay green, and `static` remains the `default` arm                                                                       |

Every commit is additive to `main`; `git revert` of a single `R-0n` commit is a
complete rollback for that workstream.

## 6. Bugs found while verifying the stack live

Both were found by exercising the fixed chain against a real stack, not by
reading code. Both are fixed in `7290536`; recorded here so the *class* is not
repeated.

| #   | Bug                                                                                      | Class                     | Fix                                                                                                                              | Guard that would catch a regression                                                                                     |
| --- | ---------------------------------------------------------------------------------------- | ------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| B-1 | Worker callback posted `status:"success"`; the API enum is `SUCCESS`. Every action verdict was a `400` and the job sat at `running` forever. | Wire-contract mismatch     | Map at the transport seam (`transport/callback.ts`), so the controller and DOM layers never see the wire spelling.               | A contract test that posts a verdict through the real `/internal/action-callback` and reads the job back `success`.     |
| B-2 | A recreated container changes hostname, so a row bound under a compose-derived boot id (`worker-<hostname>`) was unclaimable — the worker fell back to its boot id and its queue went unheard. | Identity binding          | Claim re-points the row named by the driver-injected `WORKER_ID` before looking for a free row (`repository/worker.go`).         | Claim a row, remove the container, let the reconciler recreate it, assert the same row is re-claimed (not a second one). |

B-1 is the one worth a test: the casing sat unnoticed because the worker's
callback fails *soft* (it logs a warning and stops on 4xx), so the only symptom
was a job that never finished. Any future drift in that enum will look exactly
like this again — silent.

## 7. Second verification round — auth chain and allocator convergence

Found by exercising the stack a second time, after the auth-login flow was
wired end to end. Three of the four were silent at rest; only the delete
orphan produced a visible symptom (a container that would not die).

| #   | Bug                                                                                      | Class                     | Fix                                                                                                                              | Guard that would catch a regression                                                                                     |
| --- | ---------------------------------------------------------------------------------------- | ------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| B-3 | `DELETE /api/containers` returned 204 but the container kept running and polling forever. The row was deleted *before* the reconciler could observe `STOPPED`, so nothing ever called `DeleteWorker` — the only consumer of desired state was gone. | Desired-state teardown     | `ContainerService.Delete` now calls `driver.DeleteWorker` while the row still exists, then removes the row (`service/container.go`). Verified live: delete → 204, container + both volumes gone within seconds. | Create a worker, wait READY, delete it, assert `docker ps -a` shows nothing and the API row 404s.                        |
| B-4 | The orphan sweeper 400'd on every tick: `docker engine 400 Bad Request: invalid filter`. The daemon's label filter is `{"label":["smm.worker=true"]}` (a list of `k=v` predicates), not a map — and the JSON was passed raw into the query string. | Wire-contract mismatch     | Build the predicate-list form and URL-encode it (`dockerprovisioner/docker.go`). Verified live: sweeper tick is clean.             | A test that asserts the list request path round-trips through a real `url.Parse` and carries the encoded filter.        |
| B-5 | `Packer.createWorker` never set `novnc_service`, so an AUTO-spawned card had a dead Live view button, and the manual and AUTO paths picked ports from two separate computations of "free", so they could collide. | Convergent allocation      | One `NovncAllocator` is built in `main.go` and handed to both the container service and the packer (`service/novnc_allocator.go`). | `TestNovncAllocatorSharesPorts`: allocate on path A, seed the row, assert path B gets the next port and path A's is a 409. |
| B-6 | `Pause` never released the container slot — a paused account stayed assigned, so the worker kept presenting it. | Lifecycle                 | `Pause` calls `packer.Release` after the status write; the failure is logged, not swallowed (`service/account.go`).               | Pause an assigned account, assert `workerId` is null and an empty AUTO container is reaped.                             |
| B-7 | Logout cleared the refresh cookie on `Path=/` but it was set on `Path=/api/auth`, so the browser kept it and the session stayed live until expiry. | Cookie scoping             | `clearCookies` clears each cookie on the path it was set with (`http/auth.go`). Verified live via `Set-Cookie` headers.            | Assert the logout response carries a `smm_rt` expiry with `Path=/api/auth`, not `/`.                                    |

The auth chain itself was the bigger finding: `P1-12` was recorded as DONE, but
the worker's login outcome never reached the API at all — there was no
`/internal/account-callback` POST on the worker side, no endpoint to publish
`auth-login`, and no dashboard affordance to start a login or enter a code. The
pieces (parked contexts, `auth-input`, the callback handler) each existed; the
wiring between them did not. That is now wired: two new endpoints
(`POST /api/accounts/{id}/login` and `.../input`), a `createAuthCallback` on the
worker that posts the outcome with the same soft-fail contract as the action
callback, and dashboard actions for both. The lesson is the one from B-1: a
chain verified one link at a time is not verified.

## 8. Third verification round — claim safety and the credential endpoint

| #   | Bug                                                                                      | Class                     | Fix                                                                                                                              | Guard that would catch a regression                                                                                     |
| --- | ---------------------------------------------------------------------------------------- | ------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| B-8 | The login endpoint had no attempt cap. It is the only public credential endpoint, so an unbounded rate made it a brute-force oracle. | Missing rate limit        | Ten attempts per source IP per minute, in-process (`service/auth.go`). An absent client IP fails open — a proxy that forwarded nothing must not lock the deployment out. | `TestLoginRateLimit`: fill the window with wrong passwords, assert the 11th is refused even with the right one, and a second IP is unaffected. |
| B-9 | The by-id claim path rebound its row unconditionally, so a stale `WORKER_ID` stole a row a live container was still serving — two workers on one queue. | Identity binding          | Refuse with `ErrConflict` when the named row is owned by a different container (`repository/worker.go`).                          | `TestWorkerClaimRefusesStolenRow`: claim a row, then claim it again under a second container id naming the row's uuid; assert refusal and that the owner keeps the row. |
| B-10 | The by-id claim query 22P02'd on a compose-derived `worker-<hostname>`, because that is not a uuid. Every scaled worker that fell through to it errored instead of taking a free row. | Wire-contract mismatch    | Gate the by-id lookup behind `isUUID`, so non-uuid container ids fall straight through to the free-row path.                       | `TestWorkerClaimProvesAssignment`, which uses hostname-shaped ids and now passes against a live database.               |

B-8 and B-9 are the two worth a second look: both are silent at rest and both
cost an account. B-8 because the endpoint answers every attempt with a clean
401, and B-9 because the second worker wins the heartbeat — the first keeps
running with a queue nothing writes to.
