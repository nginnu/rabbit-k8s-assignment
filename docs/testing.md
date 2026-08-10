# Testing

Every suite is a `.sh` file under `tests/`, sourcing `tests/lib.sh`. They
record failures and keep going rather than `set -e` — one run reports every
problem, not the first.

```sh
make test     # every suite, cheapest first
```

`tests/run-all.sh` runs them in this order: `routing.sh`, `auth.sh`,
`checkout.sh`, `o11y-stack.sh`, `o11y-journey.sh`, `tls-proof.sh`,
`resilience.sh` — cheapest first, `resilience.sh` last because it deletes
pods and takes minutes.

| Requirement | Suite | Note |
|---|---|---|
| Routing | `make test-routing` | each path reaches the service that owns it |
| Auth | `make test-auth` | bad credentials rejected, token opens the API |
| Checkout | `make test-checkout` | a purchase completes end to end |
| Resilience | `make test-resilience` | data and service survive a pod delete |
| TLS | `make test-tls` | [notes/01](../notes/01-ingress-tls.md) — packet capture shows no plaintext credentials |
| Observability | `make test-o11y`, `make test-journey` | [notes/07](../notes/07-observability.md) — log → trace → metric |

## What actually ran

Verified against the live cluster on this machine — not assumed to pass.

| Suite | Result | What it proved |
|---|---|---|
| `routing.sh` | 10/10 PASS | every path reaches its own service; `/api/nothing-here` → 404 |
| `auth.sh` | 10/10 PASS | wrong password → 401, valid JWT → 200, junk token → 401 |
| `checkout.sh` | 10/10 PASS | order placed, paid, and confirmed `paid` in the database |
| `o11y-stack.sh` | 14/14 PASS | one trace spans payment-svc → order-svc → mock-payment; logs carry the trace id; metrics labelled by service; Grafana reachable |
| `tls-proof.sh` | 4/4 PASS | http leaked the password (2 matches, as expected); https carried none; cert chain verified |

`o11y-journey.sh` and `resilience.sh` were not run in this pass —
`resilience.sh` deletes pods on a live cluster and needs a deliberate
go-ahead, not a side effect of writing docs.

## Notes with more detail

- [notes/01-ingress-tls.md](../notes/01-ingress-tls.md) — Gateway, TLS, packet capture
- [notes/03-services-and-data.md](../notes/03-services-and-data.md) — probes, resources, workloads
- [notes/04-canary-rollout.md](../notes/04-canary-rollout.md) — Argo Rollouts canary, proven both directions
- [notes/06-network-isolation.md](../notes/06-network-isolation.md) — NetworkPolicy, what's applied vs not
- [notes/07-observability.md](../notes/07-observability.md) — LGTM pipeline, correlation, verification detail
