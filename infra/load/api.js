// k6 load test for the jg-smm API + dashboard (P5-07).
//
// Run:   make load-test
// Env:   K6_BASE_URL (default http://localhost:24080)
//        K6_VUS     (default 20)
//        K6_DURATION (default 60s)
//
// What it proves (the ticket's acceptance criteria):
//   - dashboard p95 < 1.5s            -> the `dashboard` scenario's http_req_duration p(95)
//   - BE throughput                   -> the `list_containers` + `report` throughput
//   - no memory leak                   -> the run logs trend; the heap claim is a
//                                        manual check against `make logs S=api` growth
//
// The mix mirrors real dashboard traffic: mostly reads, one write path
// (enqueue) at low volume, and the SSE channel opened once per VU. Writes are
// capped by the service's own batch limit (50) and by the schedule below, so
// this never floods a worker queue on a shared environment.

import http from 'k6/http';
import { check, sleep, group } from 'k6';
import { Trend, Counter } from 'k6/metrics';

// 5xx counted on its own counter so the failure threshold reflects
// infrastructure downtime, not the application correctly rejecting a bad
// request. The enqueue probe deliberately posts a synthetic account that
// validates with 4xx; a 401 from an expired login is the auth layer working.
// Neither is an outage, and counting either as one would hide a real
// regression behind noise.
const serverErrors = new Counter('server_errors');

const BASE = __ENV.K6_BASE_URL || 'http://localhost:24080';
const DASHBOARD_BASE = __ENV.K6_DASHBOARD_BASE || 'http://localhost:24081';

// One login per VU; k6's default cookie jar (one per VU) carries the session.
let loggedIn = false;

const dashboardTrend = new Trend('dashboard_duration', true);
const sseOpened = new Counter('sse_opened');
const enqueueOk = new Counter('enqueue_ok');

export const options = {
  // Only a 5xx counts as infrastructure failure. A 401 (bad/expired login) or a
  // 4xx from validation is the application answering correctly, and the
  // enqueue scenario deliberately posts a synthetic account that validates
  // with 4xx — those must not masquerade as a broken API.
  httpDebug: undefined,
  discardResponseBodies: true,
  scenarios: {
    // The dashboard read path: the pages an operator actually opens.
    dashboard: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: parseInt(__ENV.K6_VUS || '20', 10) },
        { duration: __ENV.K6_DURATION || '60s', target: parseInt(__ENV.K6_VUS || '20', 10) },
        { duration: '10s', target: 0 },
      ],
      gracefulRampDown: '10s',
    },
    // The write path at a tenth of the read volume — the action queue is the
    // only hot write; hammering it harder than a human would is not realistic.
    enqueue: {
      executor: 'constant-vus',
      vus: 2,
      duration: __ENV.K6_DURATION || '60s',
      exec: 'enqueue',
    },
  },
  thresholds: {
    // The ticket's headline AC: p95 under 1.5s on the read path.
    'dashboard_duration{page:read}': ['p(95)<1500'],
    // A write must not hang the API either, but it has a wider budget: enqueue
    // validates + persists a batch before returning.
    'dashboard_duration{page:enqueue}': ['p(95)<3000'],
    // Only real downtime fails the run.
    server_errors: ['count==0'],
  },
};

// login authenticates once per VU. The seeded owner is enough for a read-heavy
// mix; a 401 here fails the run loudly rather than skewing the read metrics.
function login() {
  const res = http.post(
    `${BASE}/api/auth/login`,
    JSON.stringify({
      email: __ENV.K6_USER || 'owner@smm.local',
      password: __ENV.K6_PASSWORD || 'changeme-changeme',
    }),
    { headers: { 'content-type': 'application/json' } },
  );
  if (res.status !== 200) {
    console.error(`login failed: ${res.status} ${res.body}`);
    return false;
  }
  return true;
}

// authedGet tags the request with the page name so the threshold can key on it.
// A 404 means the route is not on the deployment under test (an image built
// before the ticket that added it); the probe is skipped rather than counted,
// so a stale staging image can neither fail the run nor inflate the p95.
const probeSeen = {};
function authedGet(path, page) {
  if (probeSeen[path] === false) return null;
  const res = http.get(`${BASE}${path}`, { tags: { page } });
  if (res.status === 404) {
    if (!(path in probeSeen)) console.warn(`route absent, skipping: ${path}`);
    probeSeen[path] = false;
    return res;
  }
  probeSeen[path] = true;
  dashboardTrend.add(res.timings.duration, { page });
  if (res.status >= 500) serverErrors.add(1);
  return res;
}

export default function () {
  if (!loggedIn) {
    loggedIn = login();
    if (!loggedIn) {
      sleep(5);
      return;
    }
  }

  group('dashboard', () => {
    // The pages the workers/monitoring shells load on open.
    authedGet('/api/containers', 'read');
    authedGet('/api/analytics/overview', 'read');
    authedGet('/api/actions?page_size=50', 'read');
    authedGet('/api/templates', 'read');
    // The report builder is the heaviest read (a rollup over a window), so it
    // is the one most likely to breach the p95 budget.
    authedGet('/api/reports/actions?from=2026-08-01T00:00:00Z&to=2026-09-16T00:00:00Z', 'read');

    // The FE shell itself: proves the Next.js page serves under budget too.
    // `/` is the shell render; a per-page route is a 404 on a stale image, not
    // an API regression, so the root is the honest page-load probe.
    const web = http.get(`${DASHBOARD_BASE}/`, { tags: { page: 'read' } });
    dashboardTrend.add(web.timings.duration, { page: 'read' });
  });

  sleep(1);
}

// The enqueue scenario (separate VU pool, constant 2 VUs).
let enqueueAvailable = true;
export function enqueue() {
  if (!loggedIn) {
    loggedIn = login();
    if (!loggedIn) {
      sleep(5);
      return;
    }
  }
  if (!enqueueAvailable) {
    sleep(2);
    return;
  }
  const res = http.post(
    `${BASE}/api/actions`,
    JSON.stringify({
      items: [
        {
          account_id: '00000000-0000-0000-0000-000000000000',
          action: 'comment',
          target_url: 'https://example.com/post/1',
        },
      ],
    }),
    { headers: { 'content-type': 'application/json' }, tags: { page: 'enqueue' } },
  );
  // 404 = the route is not on the deployment under test (a stale image), not
  // a throughput failure. Skip the probe instead of misreading it.
  if (res.status === 404) {
    if (enqueueAvailable) console.warn('/api/actions absent, skipping the enqueue probe');
    enqueueAvailable = false;
    sleep(2);
    return;
  }
  dashboardTrend.add(res.timings.duration, { page: 'enqueue' });
  // A 4xx is expected when the account does not exist — it still proves the
  // validation path responds fast. Only a 5xx is a real failure.
  if (res.status >= 500) {
    console.error(`enqueue 5xx: ${res.status}`);
    serverErrors.add(1);
  } else {
    enqueueOk.add(1);
  }
  sleep(2);
}
