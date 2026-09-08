import http from 'k6/http';
import { check, fail } from 'k6';
import { Rate } from 'k6/metrics';

const BASE = __ENV.BASE || 'https://localhost';
const SECRET = __ENV.SECRET || 'password';
const PRODUCT = __ENV.PRODUCT || 'LIV-H-24';
const DURATION = __ENV.DURATION || '300s';
const RATE = Number(__ENV.RATE || 4);
const VUS = Number(__ENV.VUS || 10);

const loginFailed = new Rate('login_failed');

export const options = {
  insecureSkipTLSVerify: true,
  scenarios: {
    shoppers: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: VUS,
      maxVUs: VUS * 3,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    login_failed: ['rate==0'],
  },
};

function accountFor(vu) {
  const n = vu % 2 === 1 ? (((vu - 1) / 2) % 20) + 1 : ((vu / 2) % 80) + 21;
  return 'user_' + String(n).padStart(3, '0');
}

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

let account = null;

function login() {
  account = accountFor(__VU);
  const res = http.post(
    `${BASE}/api/auth/login`,
    JSON.stringify({ username: account, password: SECRET }),
    JSON_HEADERS
  );
  const ok = check(res, { 'login succeeded': (r) => r.status === 200 });
  loginFailed.add(!ok);
  if (!ok) {
    fail(`login as ${account} failed — status ${res.status}, body ${res.body}`);
  }
}

export default function () {
  if (account === null) {
    login();
  }

  const products = http.get(`${BASE}/api/products`);
  check(products, { 'catalog listed': (r) => r.status === 200 });

  const order = http.post(
    `${BASE}/api/orders`,
    JSON.stringify({ product_id: PRODUCT }),
    JSON_HEADERS
  );
  const orderId = order.json('id');
  check(order, { 'order created': (r) => r.status < 300 && !!orderId });
  if (!orderId) return;

  const payment = http.post(
    `${BASE}/api/payments`,
    JSON.stringify({ order_id: orderId, method: 'card' }),
    JSON_HEADERS
  );
  const paymentId = payment.json('payment_id');
  check(payment, { 'payment charged': (r) => r.status < 300 && !!paymentId });
  if (!paymentId) return;

  const confirm = http.get(`${BASE}/api/payments/${paymentId}/success`);
  check(confirm, { 'checkout confirmed': (r) => r.status === 200 });
}
