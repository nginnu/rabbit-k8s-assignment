import http from 'k6/http';
import { check, fail, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';

const BASE = __ENV.BASE || 'https://localhost';
const SECRET = __ENV.SECRET || 'password';
const PRODUCT = __ENV.PRODUCT || 'LIV-H-24';
const VUS = Number(__ENV.VUS || 100);
const DURATION = __ENV.DURATION || '600s';
const THINK = Number(__ENV.THINK || 30);

const loginFailed = new Rate('login_failed');
const orderRequests = new Counter('order_requests');

export const options = {
  insecureSkipTLSVerify: true,
  noCookiesReset: true,
  scenarios: {
    shoppers: {
      executor: 'constant-vus',
      vus: VUS,
      duration: DURATION,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],
    login_failed: ['rate==0'],
  },
};

const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };
const METHODS = ['card', 'truemoney', 'gwallet'];

let account = null;

function login() {
  account = 'user_' + String(((__VU - 1) % 100) + 1).padStart(3, '0');
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
  orderRequests.add(1);
  const orderId = order.json('id');
  check(order, { 'order created': (r) => r.status < 300 && !!orderId });
  if (!orderId) {
    sleep(THINK);
    return;
  }

  const payment = http.post(
    `${BASE}/api/payments`,
    JSON.stringify({ order_id: orderId, method: METHODS[Math.floor(Math.random() * METHODS.length)] }),
    JSON_HEADERS
  );
  orderRequests.add(1);
  const paymentId = payment.json('payment_id');
  check(payment, { 'payment charged': (r) => r.status < 300 && !!paymentId });
  if (!paymentId) {
    sleep(THINK);
    return;
  }

  const confirm = http.get(`${BASE}/api/payments/${paymentId}/success`);
  orderRequests.add(1);
  check(confirm, { 'checkout confirmed': (r) => r.status === 200 });

  sleep(THINK);
}
