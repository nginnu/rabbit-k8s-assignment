"use client";

import { useState } from "react";

interface TraceBadgeProps {
  traceId: string | null;
  orderId?: number | string | null;
  userId?: number | string | null;
  sessionId?: string | null;
}

// Same-origin and relative, for the reason api.ts is: NEXT_PUBLIC_* is inlined
// at build time, so an absolute URL bakes one environment's hostname into the
// image and stops a digest from being promoted unchanged. Grafana is reached
// through the same gateway as everything else, under /grafana (61-grafana-route
// strips the prefix; GF_SERVER_ROOT_URL carries it into generated links).
const GRAFANA_BASE = "/grafana";

// Build Grafana Explore URL for Tempo TraceQL query
function tempoUrl(query: string): string {
  return `${GRAFANA_BASE}/explore?left=${encodeURIComponent(
    JSON.stringify({
      datasource: "tempo",
      queries: [{ query, queryType: "traceql" }],
      range: { from: "now-1h", to: "now" },
    })
  )}`;
}

// Build Grafana Explore URL for Loki query
function lokiUrl(expr: string): string {
  return `${GRAFANA_BASE}/explore?left=${encodeURIComponent(
    JSON.stringify({
      datasource: "loki",
      queries: [{ expr, refId: "A" }],
      range: { from: "now-1h", to: "now" },
    })
  )}`;
}

export default function TraceBadge({
  traceId,
  orderId,
  userId,
  sessionId,
}: TraceBadgeProps) {
  const [copied, setCopied] = useState<string | null>(null);

  if (!traceId && !orderId && !userId && !sessionId) return null;

  const handleCopy = async (label: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(label);
      setTimeout(() => setCopied(null), 2000);
    } catch {
      prompt(`Copy ${label}:`, value);
    }
  };

  const short = (v: string) => (v.length > 12 ? v.slice(0, 12) + "…" : v);

  type Row = { label: string; value: string };

  const rows: Row[] = [];
  if (traceId) rows.push({ label: "trace_id", value: traceId });
  if (orderId !== null && orderId !== undefined && orderId !== "") {
    rows.push({ label: "order_id", value: String(orderId) });
  }
  if (userId !== null && userId !== undefined && userId !== "") {
    rows.push({ label: "user_id", value: String(userId) });
  }
  if (sessionId) rows.push({ label: "session_id", value: sessionId });

  // Header link scope: session_id covers the whole journey (login → browse →
  // order → pay); trace_id only covers this one request. Prefer the wider
  // scope when we have it — fall back to trace_id on pages before login sets
  // a session (e.g. the login page itself).
  //
  // The trace_id fallback links straight to Tempo's trace-by-ID view rather
  // than an Explore TraceQL query — jumping directly to a known trace ID is
  // a plain URL, not a query, so there's no TraceQL syntax to get wrong.
  const primaryTempoUrl = sessionId
    ? tempoUrl(`{ span.session.id = "${sessionId}" }`)
    : traceId
    ? `${GRAFANA_BASE}/explore?left=${encodeURIComponent(
        JSON.stringify({
          datasource: "tempo",
          queries: [{ query: traceId, queryType: "traceql" }],
          range: { from: "now-1h", to: "now" },
        })
      )}`
    : null;
  const primaryLokiExpr = sessionId
    ? `{env="local"} | json | session_id="${sessionId}"`
    : traceId
    ? `{env="local"} | json | trace_id="${traceId}"`
    : null;

  return (
    <div
      className="inline-flex flex-col gap-1.5 rounded-xl
                 bg-slate-900/85 backdrop-blur-md border border-white/10
                 px-3 py-2 font-mono text-[12px] text-cyan-300 shadow-glass-sm"
    >
      <div className="flex items-center justify-between gap-2 pb-1 mb-0.5 border-b border-white/10">
        <span className="text-slate-400 text-[10px] uppercase tracking-widest">
          Observability
        </span>
        {primaryTempoUrl && (
          <div className="flex gap-1.5">
            <a
              href={primaryTempoUrl}
              target="_blank"
              rel="noopener noreferrer"
              className="rounded-md bg-cyan-400 px-1.5 py-0.5 text-[10px] font-semibold
                         text-slate-900 hover:bg-cyan-300 transition"
              title={sessionId ? `Tempo: session ${sessionId}` : `Tempo: trace ${traceId}`}
            >
              🔍 Tempo
            </a>
            {primaryLokiExpr && (
              <a
                href={lokiUrl(primaryLokiExpr)}
                target="_blank"
                rel="noopener noreferrer"
                className="rounded-md bg-emerald-400 px-1.5 py-0.5 text-[10px] font-semibold
                           text-slate-900 hover:bg-emerald-300 transition"
                title={`Loki: ${primaryLokiExpr}`}
              >
                📝 Loki
              </a>
            )}
          </div>
        )}
      </div>

      {rows.map((row) => (
        <div key={row.label} className="flex flex-wrap items-center gap-2">
          <span className="text-slate-400 text-[10px] uppercase tracking-widest min-w-[72px]">
            {row.label}
          </span>
          <span
            onClick={() => handleCopy(row.label, row.value)}
            title={`Full: ${row.value}\nClick to copy`}
            className="cursor-pointer select-all hover:text-cyan-200 transition"
          >
            {short(row.value)}
          </span>
          <button
            onClick={() => handleCopy(row.label, row.value)}
            className="rounded-md border border-cyan-400/60 px-1.5 py-0.5 text-[10px]
                       text-cyan-300 hover:bg-cyan-400/10 transition"
          >
            {copied === row.label ? "✓" : "copy"}
          </button>
        </div>
      ))}
    </div>
  );
}
