"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";
import { login, setSessionId, setToken } from "@/lib/api";
import TraceBadge from "@/components/TraceBadge";

export default function LoginPage() {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [traceId, setTraceId] = useState<string | null>(null);

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setLoading(true);

    try {
      const res = await login(username, password);
      setTraceId(res.traceId);

      if (res.ok && res.data.token) {
        setToken(res.data.token);
        if (res.data.session_id) setSessionId(res.data.session_id);
        router.push("/orders");
      } else {
        setError(res.data.error || `Login failed (${res.status})`);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Network error");
    } finally {
      setLoading(false);
    }
  };

  return (
    <section className="mx-auto flex min-h-[calc(100vh-7.5rem)] w-full max-w-5xl items-center justify-center py-8">
      <div className="grid w-full overflow-hidden border border-slate-200 bg-white shadow-2xl shadow-slate-200/60 md:grid-cols-2">
        <div className="bg-slate-950 px-8 py-10 text-white md:px-12 md:py-14">
          <div className="inline-flex items-center gap-2 text-xs font-bold uppercase tracking-[0.22em] text-sky-300">
            <span className="text-base">⚽</span> Season 2024/25
          </div>
          <h1 className="mt-7 text-4xl font-black leading-[0.95] tracking-tight md:text-6xl">
            Shop the latest
            <span className="mt-2 block text-sky-400">Premier League kits</span>
          </h1>
          <p className="mt-7 max-w-md text-sm leading-7 text-slate-300 md:text-base">
            Sign in with your demo account to browse jerseys, place an order,
            and complete your checkout.
          </p>
          <div className="mt-10 space-y-4 border-t border-white/15 pt-6 text-sm text-slate-300">
            <p>
              Try <code className="rounded bg-white/10 px-2 py-1 font-semibold text-white">alice</code>{" "}
              / <code className="rounded bg-white/10 px-2 py-1 font-semibold text-white">password</code>
            </p>
            <p>Each request carries a <code className="text-sky-300">trace_id</code></p>
            <p>Jump straight to Grafana Tempo to explore traces</p>
          </div>
        </div>

        <div className="flex flex-col justify-center px-8 py-10 md:px-12 md:py-14">
          <p className="text-xs font-bold uppercase tracking-[0.2em] text-sky-700">Member access</p>
          <h2 className="mt-3 text-3xl font-bold tracking-tight text-slate-900">Welcome back</h2>
          <p className="mt-2 text-sm text-slate-500">Sign in to continue shopping.</p>

          <form onSubmit={handleSubmit} className="mt-8 space-y-5">
            <div>
              <label className="mb-1.5 block text-xs font-semibold text-slate-600">Username</label>
              <input
                type="text"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                required
                className="input"
                placeholder="e.g. alice, bob, hana"
                autoComplete="username"
              />
            </div>
            <div>
              <label className="mb-1.5 block text-xs font-semibold text-slate-600">Password</label>
              <input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
                className="input"
                placeholder="••••••••"
                autoComplete="current-password"
              />
            </div>
            <button type="submit" disabled={loading} className="btn-primary w-full py-3">
              {loading ? "Signing in…" : "Sign in"}
            </button>
          </form>

          {error && <div className="mt-5 border border-rose-200 bg-rose-50 px-4 py-3 text-sm text-rose-700">{error}</div>}
          <div className="mt-5"><TraceBadge traceId={traceId} /></div>
        </div>
      </div>
    </section>
  );
}
