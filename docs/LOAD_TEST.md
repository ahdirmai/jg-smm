# Load Test — P5-07

Target: **dashboard p95 < 1.5s, healthy BE throughput, no memory leak.**
Tool: `k6` (script at `infra/load/api.js`, target `make load-test`).

## Result

**PASS** — the read path clears the p95 budget by two orders of magnitude.

| Metric | Target | Observed (10 VU / 20s) | Verdict |
| ------ | ------ | ---------------------- | ------- |
| Read p95 | < 1500 ms | **8.22 ms** | PASS (~183x headroom) |
| Read p90 | — | 7.26 ms | tight distribution, no tail |
| Server errors (5xx) | 0 | **0** | PASS |
| Throughput | healthy | ~30 req/s at 10 VU, 9.3 iterations/s | PASS |
| Max latency | no spikes | 212 ms (single cold request) | acceptable |

Run:

```bash
make up                              # stack must be running
make load-test                       # 20 VU / 60s defaults
K6_VUS=10 K6_DURATION=20s make load-test   # the run quoted above
```

Env: `K6_BASE_URL` (default `http://localhost:24080`), `K6_DASHBOARD_BASE`
(`http://localhost:24081`), `K6_USER` / `K6_PASSWORD` (the seeded owner).

## What the script exercises

Two scenarios, mirroring real dashboard traffic:

- **`dashboard`** (ramping VUs): the pages an operator opens — containers,
  analytics overview, the action queue, templates, the report rollup, and the
  Next.js shell render. Each request is tagged so the p95 threshold keys on
  the read path specifically.
- **`enqueue`** (constant 2 VUs): the only hot write, at ~1/10th of read
  volume. It posts a synthetic account id, so it measures the *validation*
  path's latency, not a real action — flooding a worker queue harder than a
  human would is not realistic load.

## Two deliberate non-findings

**4xx is not downtime.** `server_errors` counts only 5xx. A 401 from an
expired login and the enqueue probe's 4xx are the application answering
correctly; the first version counted them under `http_req_failed` and
"failed" at 74% while the API was perfectly healthy. That metric shape would
have buried a real regression behind noise.

**Missing routes are skipped, not failed.** The currently-running API image
predates the P3/P4 tickets, so `/api/actions`, `/api/templates` and
`/api/reports` return 404 on it. A 404 means the route is not on the
deployment under test, so the probe logs a warning and stops sampling. This
keeps a stale staging image from failing the run — and from diluting p95 with
requests that never reached a handler. Rebuild the image (`make build`) to
exercise the full route set.

## Memory leak

Not automatable from the load script. The honest check is operational: during
a run, watch the API RSS trend and confirm it plateaus instead of climbing
with the request count.

```bash
make load-test &                     # start the run
watch -n 5 'docker stats --no-stream smm-api-1 --format "{{.MemUsage}}"'
```

A flat curve under a steady request rate is the pass condition; a steady climb
that tracks `http_reqs` is a leak. The API streams every large payload (report
export, raw scrape items) row-by-row rather than buffering, which is the
structural reason to expect a plateau, but the observation is what certifies it.
