// Same-origin, relative. NEXT_PUBLIC_* is inlined at build time, so absolute
// per-environment URLs would bake an environment into the image and stop one
// digest from being promoted unchanged from local to production.
//
// The prefix is required rather than cosmetic: the UI serves its own /orders
// page, which would collide with the order API's /orders on the same origin.
// Route handlers under app/api own these paths and forward in-cluster, so
// the backends keep the paths they serve:
//
//   /api/auth/login  → auth         /auth/login
//   /api/products    → catalog      /products   (public, no credentials)
//   /api/orders      → order-svc    /orders
//   /api/payments    → order-svc    /payments
//
// One constant, not one per backend: the browser only ever sees this single
// same-origin prefix — which service answers behind the BFF is a routing
// decision, not something the frontend distinguishes between call sites.
//
// The JWT never reaches this file's callers: the BFF keeps it in an HttpOnly
// cookie and attaches it server-side, so scripts on the page cannot read it.
const API_BASE = "/api";

// ── Trace ID extraction ──────────────────────────────────────
// traceresponse header format: 00-<trace_id>-<span_id>-<flags>
export function extractTraceId(headers: Headers): string | null {
  const tr = headers.get("traceresponse");
  if (!tr) return null;
  const parts = tr.split("-");
  return parts.length >= 2 ? parts[1] : tr;
}

// ── Generic fetch wrapper ────────────────────────────────────
export interface ApiResult<T = unknown> {
  ok: boolean;
  status: number;
  data: T;
  traceId: string | null;
}

async function apiFetch<T = unknown>(
  url: string,
  init: RequestInit = {}
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init.headers as Record<string, string>),
  };

  const res = await fetch(url, { ...init, headers });
  const traceId = extractTraceId(res.headers);

  if (traceId) {
    console.log(`[trace_id] ${traceId}`);
  }

  let data: T;
  try {
    data = await res.json();
  } catch {
    data = {} as T;
  }

  return { ok: res.ok, status: res.status, data, traceId };
}

// ── Auth ─────────────────────────────────────────────────────
export async function login(username: string, password: string) {
  return apiFetch<{
    token?: string;
    session_id?: string;
    expires_at?: string;
    error?: string;
  }>(`${API_BASE}/auth/login`, {
    method: "POST",
    body: JSON.stringify({ username, password }),
  });
}

export interface Me {
  user_id: number;
  username: string | null;
  session_id: string | null;
  expires_at: string | null;
}

export async function me() {
  return apiFetch<Me | { error?: string }>(`${API_BASE}/auth/me`);
}

export async function logout() {
  return apiFetch<{ error?: string }>(`${API_BASE}/auth/logout`, {
    method: "POST",
  });
}

// ── Products ─────────────────────────────────────────────────
export interface Product {
  id: string;
  name: string;
  price: number;
  created_at: string;
}

export async function listProducts() {
  return apiFetch<Product[] | { error?: string }>(`${API_BASE}/products`);
}

// ── Orders ───────────────────────────────────────────────────
// user_id and created_at are marked optional, not required: the backend
// (order/domain/domain.go) never sends either field today. Declaring them
// required typed a lie — every read of order.user_id or order.created_at
// was `undefined` at runtime while TypeScript insisted it couldn't be.
//
// amount is optional for the same reason in reverse: it is new and
// additive, present on the create-order 201, but not necessarily on every
// row from GET /orders (the list endpoint's shape didn't change).
export interface Order {
  id: number;
  user_id?: number;
  product_id: string;
  status: string;
  created_at?: string;
  amount?: number;
}

export async function listOrders() {
  return apiFetch<Order[] | { error?: string }>(`${API_BASE}/orders`);
}

export async function createOrder(productId: string) {
  return apiFetch<Order | { error?: string }>(`${API_BASE}/orders`, {
    method: "POST",
    body: JSON.stringify({ product_id: productId }),
  });
}

// ── Payments ─────────────────────────────────────────────────
export interface PaymentResult {
  payment_id?: number;
  status?: string;
  gateway_ref?: string;
  error?: string;
}

// The four methods payment-svc accepts. "cod" is not a bug bucket — the
// gateway declines it on every call, on purpose, so there's a failure path
// that reproduces 100% of the time without touching the chaos headers below.
export type PaymentMethod = "card" | "truemoney" | "gwallet" | "cod";

// amount is not a parameter: the server derives it from the order (joined on
// products.price) and charges that, never a client-supplied figure. Sending
// one here — even a correct one — would keep the door open for the next
// caller to send an incorrect one instead. method is different: the shopper
// picks it, so it travels as-is — it just never carries a price with it.
export async function createPayment(
  orderId: number,
  method: PaymentMethod,
  chaosErrorRate?: number,
  chaosLatencyMs?: number
) {
  const headers: Record<string, string> = {};
  if (chaosErrorRate !== undefined && chaosErrorRate > 0) {
    headers["X-Chaos-Error-Rate"] = String(chaosErrorRate);
  }
  if (chaosLatencyMs !== undefined && chaosLatencyMs > 0) {
    headers["X-Chaos-Latency-Ms"] = String(chaosLatencyMs);
  }

  return apiFetch<PaymentResult>(`${API_BASE}/payments`, {
    method: "POST",
    body: JSON.stringify({ order_id: orderId, method }),
    headers,
  });
}
