// Same-origin, relative. NEXT_PUBLIC_* is inlined at build time, so absolute
// per-environment URLs would bake an environment into the image and stop one
// digest from being promoted unchanged from local to production.
//
// The prefix is required rather than cosmetic: the UI serves its own /orders
// page, which would collide with order-svc's /orders API on the same origin.
// The gateway strips /api before forwarding, so backend paths are untouched:
//
//   /api/auth/login  → auth         /auth/login
//   /api/products    → catalog      /products   (public, no Authorization)
//   /api/orders      → order-svc    /orders
//   /api/payments    → payment-svc  /payments
//
// One constant, not one per backend: the browser only ever sees this single
// same-origin prefix — which service answers behind the gateway is a routing
// decision, not something the frontend distinguishes between call sites. A
// name like ORDER_URL used to build the catalog URL lied about that.
const API_BASE = "/api";

// ── JWT + session helpers ────────────────────────────────────
export function getToken(): string | null {
  if (typeof window === "undefined") return null;
  return sessionStorage.getItem("jwt");
}

export function setToken(token: string) {
  sessionStorage.setItem("jwt", token);
}

export function clearToken() {
  sessionStorage.removeItem("jwt");
  sessionStorage.removeItem("session_id");
}

export function getSessionId(): string | null {
  if (typeof window === "undefined") return null;
  return sessionStorage.getItem("session_id");
}

export function setSessionId(sessionId: string) {
  sessionStorage.setItem("session_id", sessionId);
}

/** Decode JWT payload (base64url) — no signature verification, display only */
interface JwtPayload {
  uid?: number;
  usr?: string;
  sid?: string;
  exp?: number;
}
function decodeJwt(token: string): JwtPayload | null {
  try {
    const part = token.split(".")[1];
    if (!part) return null;
    const b64 = part.replace(/-/g, "+").replace(/_/g, "/");
    const pad = b64.length % 4 ? 4 - (b64.length % 4) : 0;
    return JSON.parse(atob(b64 + "=".repeat(pad)));
  } catch {
    return null;
  }
}

export function getUserId(): number | null {
  const t = getToken();
  if (!t) return null;
  return decodeJwt(t)?.uid ?? null;
}

export function getUsername(): string | null {
  const t = getToken();
  if (!t) return null;
  return decodeJwt(t)?.usr ?? null;
}

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
  const token = getToken();
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(init.headers as Record<string, string>),
  };
  if (token) {
    headers["Authorization"] = `Bearer ${token}`;
  }

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
