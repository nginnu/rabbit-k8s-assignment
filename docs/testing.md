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

Run on 2026-08-13, against the current tree — Traefik gateway, `web`/`api`
namespace split, `networkPolicy: true` on all six app charts. `make up`
completed first: 3 kind nodes Ready, every release installed into the
namespace its chart declares, every pod Running with 0 restarts.

| Suite | Result | What it proved |
|---|---|---|
| `routing.sh` | 10/10 PASS | every path reaches the service that owns it |
| `tls-proof.sh` | 4/4 PASS | chain verifies against the system trust store with no `-k`; http leaked the marker password in plaintext, https carried none |
| `checkout.sh` | 10/10 PASS | order placed, paid, and confirmed `paid` in the database |
| `resilience.sh` | 7/7 PASS | MariaDB pod deleted, came back with orders intact and the PVC still Bound |
| `o11y-stack.sh` | 14/14 PASS | one trace spans payment-svc, order-svc and mock-payment with 5 database spans; 10 log lines across 3 services carry the trace id |
| `o11y-journey.sh` | 6/6 PASS | `/grafana` returns 200, assets not double-prefixed |

51 checks total, all passing. `kubectl -n traefik get gateway` showed
`platform · CLASS traefik · ADDRESS localhost · PROGRAMMED True`;
`kubectl get netpol -A` showed policies in `api` (5), `web` (1), `data` (3),
`observability` (5), `argocd` (4).

No suite here tries a **denied** path — every check above exercises traffic
the NetworkPolicy allows. `tests/*netpol*` proving a blocked pod is actually
blocked does not exist yet; see [TODO.md](../TODO.md) Deferred.

## Notes with more detail

- [notes/01-ingress-tls.md](../notes/01-ingress-tls.md) — Gateway, TLS, packet capture
- [notes/03-services-and-data.md](../notes/03-services-and-data.md) — probes, resources, workloads
- [notes/04-canary-rollout.md](../notes/04-canary-rollout.md) — Argo Rollouts canary, proven both directions
- [notes/05-gitops-argocd.md](../notes/05-gitops-argocd.md) — ArgoCD sync, self-heal, drift
- [notes/07-observability.md](../notes/07-observability.md) — LGTM pipeline, correlation, verification detail
- [notes/08-traefik-netpol-migration.md](../notes/08-traefik-netpol-migration.md) — Istio → Traefik, `demo` → `web`/`api`, NetworkPolicy wired into `make up`
