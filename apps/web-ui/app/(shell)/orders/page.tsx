"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import {
  getToken,
  getUserId,
  getSessionId,
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

export default function OrdersPage() {
  const router = useRouter();
  const [products, setProducts] = useState<Product[]>([]);
  const [orders, setOrders] = useState<Order[]>([]);
  const [creating, setCreating] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [traceId, setTraceId] = useState<string | null>(null);

  useEffect(() => {
    if (!getToken()) {
      router.replace("/login");
      return;
    }
    fetchAll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const fetchAll = async () => {
    try {
      const [pRes, oRes] = await Promise.all([listProducts(), listOrders()]);
      setTraceId(oRes.traceId);
      if (pRes.ok && Array.isArray(pRes.data)) setProducts(pRes.data);
      if (oRes.ok && Array.isArray(oRes.data)) setOrders(oRes.data);
      if (oRes.status === 401) router.replace("/login");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load");
    }
  };

  const handleBuy = async (product: Product) => {
    setError("");
    setCreating(product.id);
    try {
      const res = await createOrder(product.id);
      setTraceId(res.traceId);
      if (res.ok) {
        const order = res.data as Order;
        router.push(
          `/pay?order_id=${order.id}` +
            `&amount=${product.price}` +
            `&product_name=${encodeURIComponent(product.name)}`
        );
        return;
      }
      const d = res.data as { error?: string };
      setError(d.error || `Create failed (${res.status})`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Network error");
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
            Official home kits for the 2024/25 season. Tap “Buy” to create an
            order — you&apos;ll head straight to secure checkout.
          </p>
        </div>
        <TraceBadge
          traceId={traceId}
          userId={getUserId()}
          sessionId={getSessionId()}
        />
      </section>

      {error && (
        <div className="rounded-xl bg-rose-50/80 border border-rose-200 px-4 py-3 text-sm text-rose-700">
          {error}
        </div>
      )}

      {/* Catalog grid */}
      <section>
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="text-lg font-semibold text-slate-800">Catalog</h3>
          <span className="text-xs text-slate-500">
            {products.length} item{products.length === 1 ? "" : "s"}
          </span>
        </div>

        {products.length === 0 ? (
          <div className="glass p-12 text-center text-slate-500">
            <div className="inline-flex items-center gap-2">
              <span className="h-2 w-2 rounded-full bg-sky-400 animate-pulse" />
              Loading products…
            </div>
          </div>
        ) : (
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
                  </div>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>

      {/* My orders */}
      <section>
        <div className="flex items-baseline justify-between mb-4">
          <h3 className="text-lg font-semibold text-slate-800">My Orders</h3>
          <span className="text-xs text-slate-500">
            {orders.length} record{orders.length === 1 ? "" : "s"}
          </span>
        </div>

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
                    <td className="px-4 py-3 text-slate-700">{o.product_id}</td>
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
      </section>
    </div>
  );
}
