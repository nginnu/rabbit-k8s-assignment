"use client";

import { useState, useEffect, useCallback } from "react";
import { useRouter } from "next/navigation";
import {
  getToken,
  getUserId,
  getSessionId,
  clearToken,
  listProducts,
  listOrders,
  createOrder,
  Product,
  Order,
} from "@/lib/api";
import TraceBadge from "@/components/TraceBadge";
import JerseyArt from "@/components/JerseyArt";

// Soft background tints per club — pairs with JerseyArt colours.
const CARD_TINT: Record<string, string> = {
  "LIV-H-24": "from-rose-100/80 to-red-50/40",
  "MCI-H-24": "from-sky-100/90 to-cyan-50/50",
  "ARS-H-24": "from-rose-100/80 to-red-50/40",
  "MUN-H-24": "from-orange-100/80 to-red-50/40",
  "CHE-H-24": "from-indigo-100/80 to-blue-50/60",
  "TOT-H-24": "from-slate-100/90 to-indigo-50/60",
};

// Loading / failed / empty are three different states, not one. Collapsing
// them into "products.length === 0" — the previous shape — means a 401, a
// 500, and a genuinely empty catalog all render the same "Loading…"
// placeholder forever, because nothing ever flips it away.
type FetchState = "loading" | "ok" | "error";

export default function OrdersPage() {
  const router = useRouter();

  const [loggedIn, setLoggedIn] = useState(false);

  const [products, setProducts] = useState<Product[]>([]);
  const [productsState, setProductsState] = useState<FetchState>("loading");
  const [productsError, setProductsError] = useState("");

  const [orders, setOrders] = useState<Order[]>([]);
  const [ordersState, setOrdersState] = useState<FetchState>("loading");
  const [ordersError, setOrdersError] = useState("");

  const [creating, setCreating] = useState<string | null>(null);
  const [buyError, setBuyError] = useState("");
  const [traceId, setTraceId] = useState<string | null>(null);

  // /api/products needs no Authorization any more — it is fetched
  // unconditionally, logged in or not. /api/orders is genuinely per-user, so
  // it is only fetched (and only rendered) when a token is present.
  const fetchProducts = useCallback(async () => {
    setProductsState("loading");
    try {
      const res = await listProducts();
      setTraceId(res.traceId);
      if (res.ok && Array.isArray(res.data)) {
        setProducts(res.data);
        setProductsState("ok");
      } else {
        const d = res.data as { error?: string };
        setProductsError(d?.error || `Couldn't load the catalog (${res.status})`);
        setProductsState("error");
      }
    } catch (err) {
      setProductsError(err instanceof Error ? err.message : "Network error");
      setProductsState("error");
    }
  }, []);

  const fetchOrders = useCallback(async () => {
    setOrdersState("loading");
    try {
      const res = await listOrders();
      setTraceId(res.traceId);
      if (res.ok && Array.isArray(res.data)) {
        setOrders(res.data);
        setOrdersState("ok");
        return;
      }
      if (res.status === 401) {
        // The token this page started with is gone or expired. Order history
        // is the only part of this page that requires it, so drop back to
        // the anonymous view instead of a hard redirect — the catalog above
        // is still public and shouldn't disappear along with the session.
        clearToken();
        setLoggedIn(false);
        return;
      }
      const d = res.data as { error?: string };
      setOrdersError(d?.error || `Couldn't load your orders (${res.status})`);
      setOrdersState("error");
    } catch (err) {
      setOrdersError(err instanceof Error ? err.message : "Network error");
      setOrdersState("error");
    }
  }, []);

  useEffect(() => {
    const hasToken = !!getToken();
    setLoggedIn(hasToken);
    // Independent requests, not Promise.all: a products failure used to
    // discard a perfectly good orders response (and vice versa) because
    // Promise.all rejects/resolves as one unit. Firing them separately means
    // one endpoint being down never hides data the other successfully
    // returned.
    fetchProducts();
    if (hasToken) fetchOrders();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleBuy = async (product: Product) => {
    if (!loggedIn) {
      router.push("/login");
      return;
    }
    setBuyError("");
    setCreating(product.id);
    try {
      const res = await createOrder(product.id);
      setTraceId(res.traceId);
      if (res.ok) {
        const order = res.data as Order;
        // The amount comes from the order the server just created — it
        // already priced the product server-side — never from the
        // client-held product.price. That price never leaves this page.
        const amountParam =
          typeof order.amount === "number" ? `&amount=${order.amount}` : "";
        router.push(
          `/pay?order_id=${order.id}` +
            amountParam +
            `&product_name=${encodeURIComponent(product.name)}`
        );
        return;
      }
      const d = res.data as { error?: string };
      setBuyError(d.error || `Create failed (${res.status})`);
    } catch (err) {
      setBuyError(err instanceof Error ? err.message : "Network error");
    } finally {
      setCreating(null);
    }
  };

  const statusPill = (status: string) => {
    const cls =
      status === "paid"
        ? "pill pill-paid"
        : status === "cancelled"
        ? "pill pill-cancel"
        : "pill pill-pending";
    return <span className={cls}>{status}</span>;
  };

  return (
    <div className="mx-auto max-w-7xl space-y-10">
      {/* Hero */}
      <section className="glass p-6 md:p-8 flex flex-col md:flex-row md:items-center md:justify-between gap-4">
        <div>
          <div className="inline-flex items-center gap-2 rounded-full bg-sky-100/80 px-3 py-1 text-xs font-semibold text-sky-700 ring-1 ring-sky-200">
            ⚡ Fresh drop · 2024/25
          </div>
          <h2 className="mt-3 text-3xl md:text-4xl font-bold tracking-tight text-slate-800">
            Premier League <span className="text-sky-600">Jerseys</span>
          </h2>
          <p className="mt-2 text-sm text-slate-600 max-w-xl">
            {loggedIn
              ? "Official home kits for the 2024/25 season. Tap “Buy” to create an order — you'll head straight to secure checkout."
              : "Official home kits for the 2024/25 season. Sign in to buy — browsing the catalog is open to everyone."}
          </p>
        </div>
        <TraceBadge
          traceId={traceId}
          userId={getUserId()}
          sessionId={getSessionId()}
        />
      </section>

      {buyError && (
        <div className="rounded-xl bg-rose-50/80 border border-rose-200 px-4 py-3 text-sm text-rose-700">
          {buyError}
        </div>
      )}

      {/* Catalog grid — public: no login required to view or browse */}
      <section>
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="text-lg font-semibold text-slate-800">Catalog</h3>
          {productsState === "ok" && (
            <span className="text-xs text-slate-500">
              {products.length} item{products.length === 1 ? "" : "s"}
            </span>
          )}
        </div>

        {productsState === "loading" && (
          <div className="glass p-12 text-center text-slate-500">
            <div className="inline-flex items-center gap-2">
              <span className="h-2 w-2 rounded-full bg-sky-400 animate-pulse" />
              Loading products…
            </div>
          </div>
        )}

        {productsState === "error" && (
          <div className="glass p-12 text-center">
            <p className="text-sm text-rose-700">{productsError}</p>
            <button
              onClick={fetchProducts}
              className="btn-primary mt-4 text-sm px-4 py-2"
            >
              Retry
            </button>
          </div>
        )}

        {productsState === "ok" && products.length === 0 && (
          <div className="glass p-12 text-center text-slate-500">
            No products available right now.
          </div>
        )}

        {productsState === "ok" && products.length > 0 && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
            {products.map((p) => (
              <article
                key={p.id}
                className="group glass-sm overflow-hidden hover:shadow-glass hover:-translate-y-1 transition-all duration-300"
              >
                {/* Jersey visual */}
                <div
                  className={`relative aspect-[4/3] bg-gradient-to-br ${
                    CARD_TINT[p.id] ?? "from-sky-100 to-sky-50"
                  } p-4`}
                >
                  <div className="absolute top-3 left-3 text-[10px] font-mono tracking-wider text-slate-500/80 bg-white/70 backdrop-blur px-2 py-0.5 rounded-md">
                    {p.id}
                  </div>
                  <JerseyArt
                    productId={p.id}
                    className="h-full w-full drop-shadow-[0_10px_20px_rgba(2,132,199,0.25)] group-hover:scale-105 transition-transform duration-500"
                  />
                </div>

                {/* Details */}
                <div className="p-4">
                  <h4 className="font-semibold text-slate-800 leading-tight">
                    {p.name}
                  </h4>
                  <div className="mt-1 flex items-end justify-between">
                    <div>
                      <div className="text-[11px] text-slate-400 leading-none">
                        Price
                      </div>
                      <div className="text-xl font-bold text-sky-700">
                        ฿{Number(p.price).toLocaleString()}
                      </div>
                    </div>
                    {loggedIn ? (
                      <button
                        onClick={() => handleBuy(p)}
                        disabled={creating !== null}
                        className="btn-primary text-sm px-4 py-2"
                      >
                        {creating === p.id ? (
                          <>
                            <span className="h-3 w-3 rounded-full border-2 border-white/60 border-t-white animate-spin" />
                            …
                          </>
                        ) : (
                          <>
                            Buy
                            <svg
                              viewBox="0 0 24 24"
                              className="h-4 w-4"
                              fill="none"
                              stroke="currentColor"
                              strokeWidth={2.5}
                            >
                              <path
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                d="M13 5l7 7-7 7M5 12h15"
                              />
                            </svg>
                          </>
                        )}
                      </button>
                    ) : (
                      <a
                        href="/login"
                        className="btn-primary text-sm px-4 py-2 inline-flex items-center gap-1.5"
                      >
                        🔒 Sign in to buy
                      </a>
                    )}
                  </div>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>

      {/* My orders — genuinely per-user, stays behind login */}
      <section>
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="text-lg font-semibold text-slate-800">My Orders</h3>
          {loggedIn && ordersState === "ok" && (
            <span className="text-xs text-slate-500">
              {orders.length} record{orders.length === 1 ? "" : "s"}
            </span>
          )}
        </div>

        {!loggedIn && (
          <div className="glass p-8 text-center text-slate-500">
            <p>Sign in to see your order history.</p>
            <a href="/login" className="btn-primary mt-4 inline-flex text-sm px-4 py-2">
              Sign in
            </a>
          </div>
        )}

        {loggedIn && ordersState === "loading" && (
          <div className="glass p-8 text-center text-slate-500">
            <div className="inline-flex items-center gap-2">
              <span className="h-2 w-2 rounded-full bg-sky-400 animate-pulse" />
              Loading orders…
            </div>
          </div>
        )}

        {loggedIn && ordersState === "error" && (
          <div className="glass p-8 text-center">
            <p className="text-sm text-rose-700">{ordersError}</p>
            <button
              onClick={fetchOrders}
              className="btn-primary mt-4 text-sm px-4 py-2"
            >
              Retry
            </button>
          </div>
        )}

        {loggedIn && ordersState === "ok" && (
          <div className="glass overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-white/50">
                <tr className="text-left text-xs font-semibold uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-3">ID</th>
                  <th className="px-4 py-3">Product</th>
                  <th className="px-4 py-3">Status</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-white/60">
                {orders.length === 0 ? (
                  <tr>
                    <td
                      colSpan={4}
                      className="px-4 py-6 text-center text-slate-400"
                    >
                      No orders yet — pick a jersey above to get started.
                    </td>
                  </tr>
                ) : (
                  orders.map((o) => (
                    <tr key={o.id} className="hover:bg-white/40 transition">
                      <td className="px-4 py-3 font-mono text-slate-600">
                        #{o.id}
                      </td>
                      <td className="px-4 py-3 text-slate-700">
                        {o.product_id}
                      </td>
                      <td className="px-4 py-3">{statusPill(o.status)}</td>
                      <td className="px-4 py-3 text-right">
                        {o.status !== "paid" ? (
                          <a
                            href={`/pay?order_id=${o.id}`}
                            className="inline-flex items-center gap-1 text-sky-700 hover:text-sky-900 font-medium"
                          >
                            Pay
                            <svg
                              viewBox="0 0 24 24"
                              className="h-3.5 w-3.5"
                              fill="none"
                              stroke="currentColor"
                              strokeWidth={2.5}
                            >
                              <path
                                strokeLinecap="round"
                                strokeLinejoin="round"
                                d="M13 5l7 7-7 7M5 12h15"
                              />
                            </svg>
                          </a>
                        ) : (
                          <span className="text-slate-400">—</span>
                        )}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
