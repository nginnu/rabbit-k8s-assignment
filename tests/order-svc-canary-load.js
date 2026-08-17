// Steady traffic against order-svc through the gateway, for the duration of a
// canary analysis.
//
// This is a load generator, not a pass/fail journey test: run it in the
// background while an operator changes the order-svc pod template and the
// Rollout starts its canary. Without traffic in that window the
// order-svc-success-rate AnalysisTemplate divides 0/0, gets NaN, and fails by
// design (see charts/apps/order-svc/values.yaml) — proving the opposite of a
// healthy release. This script exists so there is something for the analysis
// query to measure.
//
//   BASE=https://localhost k6 run tests/order-svc-canary-load.js
//
// Same login flow, same test account, same gateway path as every other
// suite — see tests/lib.sh (status/body helpers) and tests/auth.sh.

import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

// A dedicated metric rather than reusing http_req_failed for this: a login
// failure in setup() means every request that follows is meaningless
// (401s from a missing token would just look like ordinary traffic
// failures), so it needs its own signal the threshold can key off.
const loginFailed = new Rate('login_failed');

const BASE = __ENV.BASE || 'https://localhost';
const ACCOUNT = __ENV.ACCOUNT || 'alice';
const SECRET = __ENV.SECRET || 'password';

// --- rate -------------------------------------------------------------
//
// The AnalysisTemplate's query is rate(...)[2m], and the comment in
// values.yaml already worked the failure mode: at ~10 requests in a 2m
// window, one 5xx alone is 9/10 = 0.90 — under the 0.95 threshold on a
// single stray error, on a service whose steady state is ~1.0. A rate
// generating only that many samples makes the analysis as noise-sensitive
// as the no-traffic case it's meant to be distinguished from.
//
// For one error to still clear 0.95 in a window of N requests:
//   (N-1)/N >= 0.95  =>  N >= 20
//
// 5 req/s sustained puts ~600 requests in any 2m window the analysis reads
// (5 * 120), so one flaky request is 599/600 = 0.9983 — nowhere near the
// line. That is a deliberate order of magnitude above the N>=20 floor: kind
// on a laptop can move a few req/s against a 2-pod, resource-limited
// service (order-svc requests 50m/96Mi) without the load itself becoming
// the thing under test.
const RATE = 5; // requests per second, constant arrival rate

// --- duration -----------------------------------------------------------
//
// The AnalysisRun is created when the canary step starts (setWeight: 50),
// and only then does initialDelay start counting:
//   initialDelay                    30s
//   interval(30s) * count(4)       120s
//   ---------------------------------------
//   minimum for a verdict           150s
//
// k6 does not know when the operator's change lands or when the Rollout
// actually creates the AnalysisRun, so traffic has to already be flowing
// before that point and keep flowing well past it. Duration below is set to
// 240s (4m) — 90s of margin over the 150s minimum, split so there is slack
// both for the operator to trigger the change after k6 starts and for pod
// scheduling/readiness (new ReplicaSet has to reach Ready before the step
// and its analysis even begin) before the window that matters starts.
const DURATION = '240s';
const PRE_ALLOCATED_VUS = 10;
const MAX_VUS = 30;

// --- TLS ------------------------------------------------------------------
//
// The gateway serves a cert from a mkcert-issued local CA. curl in the other
// suites verifies it because `mkcert -install` drops the CA into the host's
// system trust store, which curl (via the OS's TLS backend) reads. k6 is a
// static Go binary with its own certificate pool — it never consults the
// macOS keychain or NSS, so it has no way to see that CA and would reject
// the leaf cert as unknown. There is no k6 flag to add a CA to its pool
// (only --insecure-skip-tls-verify or an OS-level trust store), so the
// options below skip verification for this run only. This script tests
// that the canary serves healthy traffic, not that the gateway's TLS chain
// verifies — tests/tls-proof.sh already owns that proof, against curl,
// which does trust the CA.
export const options = {
  insecureSkipTLSVerify: true,
  scenarios: {
    canary_load: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: PRE_ALLOCATED_VUS,
      maxVUs: MAX_VUS,
    },
  },
  // A run against a broken canary must fail as a k6 run, not just log a
  // quiet drop in the pass rate — otherwise "the load script ran" and "the
  // release was healthy" look the same in CI output.
  thresholds: {
    http_req_failed: ['rate<0.01'], // >1% failures means the run itself was unhealthy
    checks: ['rate>0.99'],
    login_failed: ['rate==0'], // any login failure invalidates every request that follows
  },
};

const LOGIN_BODY = JSON.stringify({ username: ACCOUNT, password: SECRET });
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// setup() runs once, not once per VU or iteration — every VU shares the one
// token it returns. That's fine here: the analysis measures order-svc
// response codes, not per-request auth freshness. Logging in inside the
// default function instead would make every iteration pay for a second
// request and would mix login traffic into the same metrics as the
// products calls this script exists to generate.
export function setup() {
  const res = http.post(`${BASE}/api/auth/login`, LOGIN_BODY, JSON_HEADERS);
  const ok = check(res, {
    'login succeeded': (r) => r.status === 200,
    'login returned a token': (r) => !!r.json('token'),
  });
  loginFailed.add(!ok);
  if (!ok) {
    // Aborting setup fails the whole run immediately rather than sending
    // hundreds of unauthenticated requests that would all read as 401 and
    // bury the real problem (bad credentials, gateway down) in noise.
    throw new Error(
      `login as ${ACCOUNT} failed — status ${res.status}, body ${res.body}`
    );
  }
  return { token: res.json('token') };
}

export default function (data) {
  // GET /api/orders, not /api/products: the AnalysisTemplate measures
  // service="order-svc", and the product list moved to catalog-svc with the
  // split — pointing this generator at the catalog would leave the canary
  // measuring no traffic at all, which the query scores as NaN and fails by
  // design. Listing the user's own orders is the cheapest authed read
  // order-svc has.
  const res = http.get(`${BASE}/api/orders`, {
    headers: { Authorization: `Bearer ${data.token}` },
  });
  check(res, { 'orders request succeeded': (r) => r.status === 200 });
}
