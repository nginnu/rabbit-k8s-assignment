"use client";

import { useState, useEffect, FormEvent, Suspense } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
  getToken,
  getUserId,
  getSessionId,
  createPayment,
  PaymentMethod,
  PaymentResult,
} from "@/lib/api";
import TraceBadge from "@/components/TraceBadge";

// Labels + copy for the four methods payment-svc accepts. "cod" carries a
// `failsAlways` flag so the form can call out that its failure is the
// gateway declining the method — not the chaos knobs below it, and not
// something the shopper did wrong.
const PAYMENT_METHODS: {
  value: PaymentMethod;
  label: string;
  icon: string;
  failsAlways?: boolean;
}[] = [
  { value: "card", label: "Card", icon: "💳" },
  { value: "truemoney", label: "TrueMoney", icon: "📱" },
  { value: "gwallet", label: "GWallet", icon: "👛" },
  { value: "cod", label: "Cash on Delivery", icon: "📦", failsAlways: true },
];

function PayForm() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const [orderId, setOrderId] = useState("");
  const [amount, setAmount] = useState("");
  const [productName, setProductName] = useState("");
  const [method, setMethod] = useState<PaymentMethod | "">("");
  const [errorRate, setErrorRate] = useState("0");
  const [latencyMs, setLatencyMs] = useState("0");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<PaymentResult | null>(null);
  const [traceId, setTraceId] = useState<string | null>(null);

  useEffect(() => {
    if (!getToken()) {
      router.replace("/login");
      return;
    }
    const oid = searchParams.get("order_id");
    const amt = searchParams.get("amount");
    const pname = searchParams.get("product_name");
    if (oid) setOrderId(oid);
    if (amt) setAmount(amt);
    if (pname) setProductName(pname);
  }, [router, searchParams]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!method) return; // guarded by the disabled submit button too
    setError("");
    setResult(null);
    setLoading(true);

    try {
      // amount is deliberately not sent — the server prices the order from
      // what it already knows (products.price via the order), never from a
      // number the browser hands it. The "amount" state here is purely the
      // figure this page displays back to the shopper before they submit.
      const res = await createPayment(
        Number(orderId),
        method,
        Number(errorRate),
        Number(latencyMs)
      );
      setTraceId(res.traceId);

      if (res.ok) {
        setResult(res.data);
      } else {
        setError(res.data.error || `Payment failed (${res.status})`);
        setResult(res.data);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Network error");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="grid md:grid-cols-5 gap-6">
      {/* Summary / context */}
      <div className="md:col-span-2 space-y-4">
        <div className="glass p-6">
          <div className="inline-flex items-center gap-2 rounded-full bg-sky-100/80 px-3 py-1 text-xs font-semibold text-sky-700 ring-1 ring-sky-200">
            🔒 Secure checkout
          </div>
          <h2 className="mt-3 text-2xl font-bold text-slate-800">
            Complete your payment
          </h2>
          <p className="mt-1 text-sm text-slate-600">
            Your order is held until payment confirms.
          </p>

          {orderId ? (
            <div className="mt-5 rounded-xl border border-white/60 bg-white/60 backdrop-blur p-4">
              <div className="text-xs text-slate-500">Order #{orderId}</div>
              {productName && (
                <div className="mt-1 font-semibold text-slate-800">
                  {productName}
                </div>
              )}
              <div className="mt-3 flex items-baseline justify-between">
                <span className="text-xs uppercase tracking-wider text-slate-500">
                  Total
                </span>
                {amount ? (
                  <span className="text-2xl font-bold text-sky-700">
                    ฿{Number(amount).toLocaleString()}
                  </span>
                ) : (
                  <span className="text-xs text-slate-500">
                    Priced by the server on submit
                  </span>
                )}
              </div>
            </div>
          ) : (
            <p className="mt-5 text-xs text-slate-500">
              No order selected — enter an order ID on the right.
            </p>
          )}
        </div>

        <TraceBadge
          traceId={traceId}
          orderId={orderId}
          userId={getUserId()}
          sessionId={getSessionId()}
        />
      </div>

      {/* Form */}
      <div className="md:col-span-3">
        <form onSubmit={handleSubmit} className="glass p-6 space-y-4">
          {/* Amount is never entered here — the server derives it from the
              order, so there is nothing for this field to submit. Only the
              order ID is ever manual input. */}
          {!productName && (
            <div>
              <label className="block text-xs font-semibold text-slate-600 mb-1.5">
                Order ID
              </label>
              <input
                type="number"
                value={orderId}
                onChange={(e) => setOrderId(e.target.value)}
                required
                min={1}
                className="input"
              />
            </div>
          )}

          <fieldset className="rounded-xl border border-slate-200 bg-white/70 p-4">
            <legend className="px-2 text-xs font-bold uppercase tracking-wider text-slate-600">
              Payment method
            </legend>
            <div className="grid grid-cols-2 gap-3 mt-1">
              {PAYMENT_METHODS.map((m) => (
                <label
                  key={m.value}
                  className={`flex items-center gap-2 rounded-xl border px-3 py-2.5 text-sm cursor-pointer transition ${
                    method === m.value
                      ? "border-sky-400 bg-sky-50 ring-1 ring-sky-300"
                      : "border-slate-200 bg-white hover:border-slate-300"
                  }`}
                >
                  <input
                    type="radio"
                    name="method"
                    value={m.value}
                    checked={method === m.value}
                    onChange={() => setMethod(m.value)}
                    required
                    className="accent-sky-600"
                  />
                  <span>{m.icon}</span>
                  <span className="font-medium text-slate-700">{m.label}</span>
                </label>
              ))}
            </div>
            {method === "cod" && (
              <p className="mt-3 rounded-lg bg-amber-50 border border-amber-200 px-3 py-2 text-xs text-amber-800">
                ⚠️ The gateway declines Cash on Delivery every time — that's
                this method, not a mistake on your end. It'll fail on submit
                so you can follow the trace id below into the logs.
              </p>
            )}
          </fieldset>

          <fieldset className="rounded-xl border border-dashed border-sky-300/70 bg-sky-50/50 p-4">
            <legend className="px-2 text-xs font-bold uppercase tracking-wider text-sky-700">
              🧪 Chaos Knobs{" "}
              <span className="font-normal text-slate-500 normal-case">
                (demo)
              </span>
            </legend>
            <div className="grid sm:grid-cols-2 gap-4 mt-1">
              <div>
                <label className="block text-xs font-semibold text-slate-600 mb-1.5">
                  Error Rate (0.0 – 1.0)
                </label>
                <input
                  type="number"
                  value={errorRate}
                  onChange={(e) => setErrorRate(e.target.value)}
                  min={0}
                  max={1}
                  step="0.1"
                  className="input"
                />
              </div>
              <div>
                <label className="block text-xs font-semibold text-slate-600 mb-1.5">
                  Latency (ms, 0 – 10000)
                </label>
                <input
                  type="number"
                  value={latencyMs}
                  onChange={(e) => setLatencyMs(e.target.value)}
                  min={0}
                  max={10000}
                  step="100"
                  className="input"
                />
              </div>
            </div>
          </fieldset>

          <button
            type="submit"
            disabled={loading || !method}
            className="btn-primary w-full text-base py-3"
          >
            {loading ? (
              <>
                <span className="h-4 w-4 rounded-full border-2 border-white/60 border-t-white animate-spin" />
                Processing…
              </>
            ) : !method ? (
              "Choose a payment method"
            ) : amount ? (
              <>
                Pay ฿{Number(amount).toLocaleString()}
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
                    d="M5 12l5 5L20 7"
                  />
                </svg>
              </>
            ) : (
              "Submit Payment"
            )}
          </button>
        </form>

        {error && (
          <div className="mt-4 rounded-xl bg-rose-50/80 border border-rose-200 px-4 py-3 text-sm text-rose-700">
            {error}
          </div>
        )}

        {result && (
          <div
            className={`mt-4 rounded-2xl border p-4 text-sm backdrop-blur ${
              result.error
                ? "bg-rose-50/70 border-rose-200 text-rose-800"
                : "bg-emerald-50/70 border-emerald-200 text-emerald-800"
            }`}
          >
            <div className="flex items-center gap-2 font-semibold">
              {result.error ? "✗ Payment failed" : "✓ Payment result"}
            </div>
            <pre className="mt-2 overflow-x-auto whitespace-pre-wrap font-mono text-[12px] leading-relaxed text-slate-700">
{JSON.stringify(result, null, 2)}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}

export default function PayPage() {
  return (
    <Suspense
      fallback={
        <div className="glass p-10 text-center text-slate-500">Loading…</div>
      }
    >
      <PayForm />
    </Suspense>
  );
}
