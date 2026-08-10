// Same-origin, relative. NEXT_PUBLIC_* is inlined at build time, so absolute
// per-environment URLs would bake an environment into the image and stop one
// digest from being promoted unchanged from local to production.
//
// The prefix is required rather than cosmetic: the UI serves its own /orders
// page, which would collide with order-svc's /orders API on the same origin.
// The gateway strips /api before forwarding, so backend paths are untouched:
//
//   /api/auth/login  → auth-svc     /auth/login
//   /api/products    → order-svc    /products
//   /api/orders      → order-svc    /orders
//   /api/payments    → payment-svc  /payments
const API_BASE = "/api";

const AUTH_URL = API_BASE;
const ORDER_URL = API_BASE;
const PAYMENT_URL = API_BASE;

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
  }>(`${AUTH_URL}/auth/login`, {
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
  return apiFetch<Product[] | { error?: string }>(`${ORDER_URL}/products`);
}

// ── Orders ───────────────────────────────────────────────────
export interface Order {
  id: number;
  user_id: number;
  product_id: string;
  status: string;
  created_at: string;
}

export async function listOrders() {
  return apiFetch<Order[] | { error?: string }>(`${ORDER_URL}/orders`);
}

export async function createOrder(productId: string) {
  return apiFetch<Order | { error?: string }>(`${ORDER_URL}/orders`, {
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

export async function createPayment(
  orderId: number,
  amount: number,
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

  return apiFetch<PaymentResult>(`${PAYMENT_URL}/payments`, {
    method: "POST",
    body: JSON.stringify({ order_id: orderId, amount }),
    headers,
  });
}
