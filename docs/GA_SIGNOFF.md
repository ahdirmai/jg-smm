# GA Sign-off (P5-10)

> Ticket: `tickets/p5_10.md` — "Uji 30 hari + acceptance v1.0".
> Exit criteria: `DEVELOPMENT_PHASE.md` → P5.

**Status: NOT GRANTED.** This ticket is a 30-day production soak; it cannot be
closed by a code change. What this document does is fix the acceptance bar, record
what is already satisfied in code, and state exactly what evidence must be
collected over the 30-day window before general availability.

---

## 1. Acceptance criteria (the bar)

From `DEVELOPMENT_PHASE.md` → P5 Exit Criteria, plus the ticket's own AC:

| #   | Criterion                        | Target                             | Evidence                                                         |
| --- | -------------------------------- | ---------------------------------- | ---------------------------------------------------------------- |
| A1  | 100+ accounts stable for 30 days | uptime ≥ 98%, action success ≥ 90% | Grafana 30-day window; action success rate from `attempt` rows   |
| A2  | Zero bans (or controlled)        | no platform ban in the window      | worker auth status; quarantined-account count stays flat         |
| A3  | Alert → Slack end-to-end         | alert fires and is acked           | Alertmanager log + Slack message                                 |
| A4  | MTTR worker failure              | < 5 min                            | reconciler restore timestamps                                    |
| A5  | DR drill success                 | restore < 30 min                   | `make drill` output (destructive — staging only)                 |
| A6  | Runbooks + severity matrix       | all alerts covered                 | `infra/runbooks/`, `docs/SEVERITY.md`                            |
| A7  | Security review sign-off         | granted                            | `docs/SECURITY_REVIEW.md` — granted, one deferred toolchain item |
| A8  | Load test                        | API p95 < 1500 ms                  | `make load-test`; see `docs/LOAD_TEST.md`                        |
| A9  | Scale at ~50 containers          | invariants hold                    | `docs/SCALE_TEST.md`                                             |

---

## 2. What is satisfied now (in code, on `main`)

These are done and are the reason the remaining gap is operational, not
engineering:

- **Scale / packing** — `TestPackScaleFiftyContainers` proves the ~50-container
  invariants for both the MVP quota (100 accounts / 2 platforms / quota 2) and
  the all-platform quota (350 / 7 / 7): exact fleet size, cap respected, one
  platform per container, no unpacked account, AUTO containers reaped on drain.
  `docs/SCALE_TEST.md`.
- **Security** — RBAC read/write split, `gitleaks` pre-commit hook,
  `govulncheck` clean on application deps (`go-redis` bumped v9.7.0 → v9.7.3 for
  GO-2025-3540). Sign-off granted in `docs/SECURITY_REVIEW.md`.
- **Load** — k6 load test, p95 **8.22 ms** at 10 VU / 20 s against a 1500 ms
  target, zero server errors. `docs/LOAD_TEST.md`.
- **Observability** — Prometheus/Grafana/Loki/Alertmanager tier behind the `obs`
  profile (`make up-obs`); API metrics wired through `internal/obs`.
- **Backup / restore / DR** — `make backup`, `make restore`, `make drill`
  (Postgres + WAL, MinIO, worker session PVCs).
- **Health & quarantine** — health score 0–100, auto-quarantine below threshold,
  anti-flapping cap; see `DEVELOPMENT_RULE.md`.
- **Runbooks** — `infra/runbooks/` (one per alert) with the severity matrix and
  on-call SLAs in `docs/SEVERITY.md`, and backup/DR mechanics in `docs/BACKUP.md`.

---

## 3. What remains (the operational gap)

### 3.1 The 30-day soak (A1, A2, A3, A4)

This is the substance of the ticket and it is calendar time, not code:

1. Deploy the current `main` to the production-like fleet.
2. Load the fleet to ~50 containers / 100 accounts following
   `docs/SCALE_TEST.md` §3.2.
3. Run the soak for 30 consecutive days. Collect, per week:
   - fleet uptime and worker restart / OOMKill count,
   - action success rate from `attempt` terminal statuses,
   - any platform ban or auth `FAILED` storm,
   - MTTR for any worker failure (reconciler restore time).
4. Verify at least one real Alertmanager → Slack delivery (A3). If the window
   produced no natural trigger, fire a synthetic alert and confirm delivery.

**Ramp-up:** do not start the soak at 100 accounts. Step up in waves (e.g.
25 → 50 → 100) so the first sign of a ban risk surfaces at low blast radius.
The per-platform kill-switch and auto-quarantine are the containment; the ramp
is how you avoid discovering them at full scale.

### 3.2 Toolchain patch (A7, deferred)

`govulncheck` reports **7 Go stdlib CVEs** reachable from the API
(GO-2026-6218 and others, fixed in **go1.26.6**). Local toolchain is 1.26.4;
the CI image is `golang:1.26-alpine`, whose `1.26` tag already tracks the
latest 1.26.x, so the fix is a **rebuild**, not a code change.

`go.mod` deliberately stays at `go 1.26.0` — bumping the directive to 1.26.6
breaks `go mod tidy` on older toolchains while gaining nothing, since
`GOTOOLCHAIN=auto` already resolves forward. See `docs/SECURITY_REVIEW.md`
§Toolchain.

```bash
# locally: install the 1.26.6 toolchain, then
cd apps/api && go mod edit -go=1.26.6 && go mod tidy
# in CI: rebuild the image; the 1.26 tag tracks the latest 1.26.x
```

This needs a network-available install, so it was **not** done during
development. It is a pre-GA blocker for A7 only if the soak environment is
exposed to the affected stdlib paths; record the decision either way.

### 3.3 Docs sync (A6)

Confirm `PRD.md`, `ERD.md`, `SYSTEM_DESIGN.md`, `DESIGN_SYSTEM.md`,
`DEVELOPMENT_RULE.md`, `TICKETS.md` and the ADRs still match what shipped.
Spot-check against `docs/PROGRESS.md`, which is the source of truth for
ticket → commit mapping.

---

## 4. Sign-off record

| Item                               | Status           | Note                                        |
| ---------------------------------- | ---------------- | ------------------------------------------- |
| Scale invariants at ~50 containers | PASS (in code)   | `TestPackScaleFiftyContainers`              |
| Security review                    | PASS, 1 deferred | `docs/SECURITY_REVIEW.md`; go1.26.6 rebuild |
| Load test                          | PASS             | p95 8.22 ms vs 1500 ms target               |
| Backup / restore / DR drill        | PASS (mechanism) | live drill must run in the soak (A5)        |
| Observability tier                 | PASS             | `make up-obs`                               |
| 30-day soak (A1–A4)                | **PENDING**      | calendar time; §3.1                         |
| Alert → Slack (A3)                 | **PENDING**      | needs a live delivery in the window         |
| Toolchain patch (A7)               | **PENDING**      | image rebuild, §3.2                         |

**GA is granted when every PENDING row above has dated evidence attached.**
Until then the build is feature-complete and green, but **not GA**.

---

## 5. References

- Ticket: `tickets/p5_10.md` · Exit criteria: `DEVELOPMENT_PHASE.md` → P5
- `docs/SCALE_TEST.md` (A9) · `docs/LOAD_TEST.md` (A8)
- `docs/SECURITY_REVIEW.md` (A7) · `infra/runbooks/` + `docs/SEVERITY.md` (A6)
  · `docs/BACKUP.md` (A5)
- Progress: `docs/PROGRESS.md`
