# Network isolation — two options, and which one I shipped

**Date:** 2026-08-10

- **Shipped:** option 2
- **Production needs:** option 1
- **The gap:** a decision, not an oversight

---

## Option 1 — the target

Backends sit behind `web-ui`. The gateway has one door.

```
[ Browser ] ──► [ Gateway ] ──┬──► [ web-ui ] ──east-west──► [ auth / order / payment ]
                              │                                    │        │
                              │                                    │        ▼
                              │                                    │  [ mock-payment ]
                              │                                    ▼
                              │                          [ mariadb / redis ]
                              │
                              ├──► [ grafana ] ──► [ prometheus / loki / tempo ]
                              │                               ▲
                              │                               │
                              │                         [ alloy ] ◄── OTLP from every pod
                              └──► [ argocd ]
```

- gateway ingress: `web-ui` and `grafana` only
- backend services take traffic from `web-ui` and each other — not the gateway
- data tier takes the four services; the gateway has no route to it at all

## Option 2 — what runs today

The browser calls `/api/*` itself, so the gateway is a second front door.

```
[ Browser ] ──► [ Gateway ] ──┬──► [ web-ui ]
                              │
                              ├──► [ auth / order / payment ]   ← extra doors
                              │            │        │
                              │            │        ▼
                              │            │  [ mock-payment ]
                              │            ▼
                              │    [ mariadb / redis ]
                              │
                              ├──► [ dummy ]
                              │
                              ├──► [ grafana ] ──► [ prometheus / loki / tempo ]
                              │                              ▲
                              │                              │
                              │                        [ alloy ] ◄── OTLP from every pod
                              └──► [ argocd ]
```

Five routes. `dummy`, `grafana` and `argocd` are edge-facing by design — the
three backend services are the only deviation, and everything below the edge is
identical to option 1.

---

## Why option 2 — temporary, for this delivery only

**Option 1 needs application code rewritten. Option 2 needs nothing rewritten.
That is the whole reason, and it expires the moment the code is written** —
alongside the fixed time this assignment allowed.

| | |
|---|---|
| **Code, not config** | `web-ui/lib/api.ts` fetches from the browser. Option 1 needs Next.js route handlers first — application work, not a manifest change. |
| **Seven suites plus k6** | every test calls the gateway directly. Option 1 means rewriting all of them before anything can be demonstrated at all. |
| **Not a security position** | option 2 is a scheduling decision. Nothing about it is safer, simpler, or preferable — it is only faster to deliver. |

## What it costs

| Deviation | Cost | What still guards it |
|---|---|---|
| `/api/*` at the gateway | three services at the edge instead of one | JWT — `/api/*` returns 401 with no token |
| grafana anonymous admin | anyone reaching the gateway is an admin | nothing; laptop-scoped |
| canary has no canary-only label | measures blast radius, not the canary | caught `FAIL_RATE=0.5` anyway (notes/04) |

---

## The isolation model itself

Both options share this. Only the top row changes.

| Tier | Pods | Ingress from |
|---|---|---|
| edge | web-ui, dummy, grafana, argocd | gateway |
| service | auth, order, payment | web-ui *(option 2: gateway)* |
| internal | mock-payment | payment-svc |
| data | mariadb, redis | the four services |
| telemetry | alloy | every demo pod |
| storage | loki, tempo, prometheus | alloy, grafana |

Data and storage get egress to DNS only — they are destinations, never callers.

**Three traps**

| Trap | Symptom |
|---|---|
| no egress to `kube-dns:53` | timeout to mariadb, reads as a database fault |
| `order-svc ⇄ payment-svc` is a cycle | write one direction, the callback fails |
| egress without matching ingress | caller looks right, destination drops it |

---

## To reach option 1

1. Route handlers in `web-ui` — proxy the backend calls server-side
2. Gateway route cut to `/`
3. Rewrite the seven suites and the k6 script
4. Drop the gateway from backend ingress, then `networkPolicy: true`

**Option 1 is the one that matters in production.** The edge is one hop; the
interior is every hop after it, and that is where a compromised pod moves.
