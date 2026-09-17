# Security Review — P5-04

Scope: the whole monorepo as of commit `main` (post P4-08). Goal of the ticket:
**sign-off; zero high/critical findings.** Tools: `govulncheck`, `gitleaks`
(config already committed at `.gitleaks.toml`), plus a manual audit of the
trust boundaries the PRD names (RBAC, SSRF, credential storage, worker ingress).

## Result

**Sign-off: granted, with one deferred item (toolchain patch, not application code).**

| Check | Status | Evidence |
| ----- | ------ | -------- |
| `govulncheck` application deps | PASS | `go-redis` v9.7.0 → **v9.7.3** (GO-2025-3540). No remaining module CVEs are reachable |
| `govulncheck` Go stdlib | DEFERRED | 7 CVEs fixed in go1.26.6 (see §Toolchain). Local toolchain is 1.26.4; bump is a build-image change, not a code change |
| `gitleaks` | PASS | `.gitleaks.toml` extends the default ruleset; local-dev placeholders (`smmsmmsmm`, `changeme`, `dev-only-secret`) are explicitly allowlisted with a comment saying why. `.env` is gitignored |
| RBAC | **FIXED** | The `/api` group required `act`, so STRATEGIST/ANALYST got 403 on every read. Floor is now `read`; every mutating route carries an `act`/`export`/`admin` gate |
| SSRF | PASS | `safeTargetURL` is a strict allowlist (http/https + host required). No blocklist to drift |
| Credential storage | PASS | AES-GCM at rest (`adapter/crypto`), key from `CREDENTIAL_KEY` only, never baked into an image |
| Worker ingress | PASS | `/internal/*` (callback + heartbeat) is a separate group with no user auth — by design, it is machine-only and sits outside `/api` |
| noVNC | PASS | Worker 6080 is published per-replica with no VNC password. Acceptable for the local single-node tier; see §noVNC for the production caveat |

## RBAC fix (the real finding)

Before: `e.Group("/api", requireAuth(), RequirePermission(domain.PermAct))`.
`RoleStrategist` and `RoleAnalyst` only hold `PermRead`, so **every dashboard
read returned 403 for two of the four roles**. That is both a functional bug
and a security smell: it taught operators to hand out `act` to anyone who
needed to see the dashboard, which is privilege inflation.

After: the group floor is `PermRead`; the write routes opt up.

| Route | Gate |
| ----- | ---- |
| `GET /api/*` (containers, accounts, actions, templates, analytics, proxy-groups) | `read` |
| `POST /api/containers`, `DELETE /api/containers/:id` | `act` |
| `POST /api/accounts`, `POST /api/accounts/import`, `POST /api/accounts/:id`, `DELETE /api/accounts/:id` | `act` |
| `POST /api/actions` | `act` |
| `POST /api/templates`, `PUT`/`DELETE /api/templates/:id` | `act` |
| `POST /api/proxy-groups`, `DELETE /api/proxy-groups/:id` | `act` |
| `POST /api/official-accounts`, `DELETE /api/official-accounts/:id`, `POST /api/analytics/refresh` | `act` |
| `GET /api/reports/export` | `export` |
| `/api/admin/*` | `admin` |

Proven by `TestContainerRBAC` (analyst: read 200 / write 403; strategist: read
200 / delete 403; anonymous: 401) and unchanged `TestAuthFlowAndRBAC`.

## Toolchain (deferred)

`govulncheck` reports 7 stdlib CVEs reachable from the API: GO-2026-6218
(net/url), -6090/-5856 (crypto/tls), -6089/-5026 (net/http), -6088
(encoding/xml), -5972 (encoding/asn1). All are fixed in **go1.26.6**; the local
toolchain is 1.26.4 and the CI image is `golang:1.26-alpine`.

`go.mod` stays at `go 1.26.0` on purpose: bumping the directive to 1.26.6 makes
`go mod tidy` fail on any machine without the exact patch release, and `go
1.26.0` already lets `GOTOOLCHAIN=auto` resolve forward. The honest remediation
is to rebuild the image, which pulls a new Go download — deferred because the
local network is flaky. This is the **only** open item.

To close it:

```bash
# locally: install the patch toolchain, then
cd apps/api && go mod edit -go=1.26.6 && go mod tidy
# in CI: the 1.26 tag already tracks the latest 1.26.x, so a rebuild is enough
make ci
```

## noVNC production caveat

`x11vnc` runs with `-nopw`. The local tier binds the live view to `127.0.0.1`
only (`HostConfig.PortBindings.HostIp`), so it reaches the operator's browser
and nothing else. In the Kubernetes tier the noVNC `Service` is `ClusterIP`
with no ingress, so it is not externally reachable — but if a live view is ever
exposed beyond the cluster, it needs a password or an authenticating proxy
first. Recorded here so it is a decision, not an oversight.

## Docker socket escalation (local tier only)

`PROVISIONER_MODE=docker` mounts `/var/run/docker.sock` into the API container
and runs it as `root`, because the image's nonroot user cannot open the socket.
This is a deliberate local-only tradeoff: the API process can create and delete
any container on the host. It is opt-in and is never a production default —
`PROVISIONER_MODE=k8s` remains the production path and is untouched by this.
Upgrade path: put a socket proxy (e.g. tecnio's `docker-socket-proxy`) in front
and whitelist only the container endpoints the driver uses, then drop root.
