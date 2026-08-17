# Testing

Two layers, ordered cheapest first: the Go modules' own unit tests run on the
host alone, then the shell suites under `tests/` (sourcing `tests/lib.sh`)
prove the deployed cluster. Both record failures and keep going rather than
`set -e` — one run reports every problem, not the first.

```sh
make test          # unit + every cluster suite, cheapest first
make test-unit     # go test both modules, no cluster required
make test-preflight  # does the running cluster match what the repo declares
```

`tests/run-all.sh` runs them in this order: `unit.sh`, `routing.sh`,
`route-isolation.sh`, `mesh.sh`, `istio.sh`, `auth.sh`, `checkout.sh`,
`o11y-stack.sh`, `o11y-journey.sh`, `tls-proof.sh`, `resilience.sh` —
`unit.sh` first because it needs no cluster and fails fastest;
`resilience.sh` last because it deletes pods and takes minutes.
`preflight.sh` is a gate, not a suite: it compares the running cluster
against what the repo declares (versions, releases, routes, netpols) and is
run via `make test-preflight` when that question is the one being asked.

| Requirement | Suite | Note |
|---|---|---|
| Unit behaviour | `make test-unit` | go test `apps/services` + `apps/notification` — saga order, non-fatal steps, cache-aside, handlers, gateways |
| Routing | `make test-routing` | each path reaches the service that owns it |
| Route isolation | `make test-route-isolation` | console hosts answered by their own backends, never a storefront route |
| Mesh CNI + netpol | `make test-mesh` | Cilium is the CNI; a pod with no rule is refused the database |
| Istio sidecars | `make test-istio` | every api pod carries a ready proxy; east-west mTLS |
| Auth | `make test-auth` | bad credentials rejected, token opens the API |
| Checkout | `make test-checkout` | a purchase completes end to end, including the notification leg |
| Resilience | `make test-resilience` | data and service survive a pod delete |
| TLS | `make test-tls` | [notes/01](../notes/01-ingress-tls.md) — packet capture shows no plaintext credentials |
| Observability | `make test-o11y`, `make test-journey` | [notes/07](../notes/07-observability.md) — log → trace → metric, one trace across four services |
| Cluster matches repo | `make test-preflight` | versions, releases (helm + Argo CD), routes, workloads |
| Canary load | `make load-test` | k6 generator for the analysis window — not pass/fail, run beside a rollout |

## What the unit tests pin down

The shell suites prove the deployed cluster; they cannot see the code inside
the pods. `tests/unit.sh` (and the `*_test.go` files it runs) cover what only
code-level tests can:

- **payment saga order** — validate → create pending → charge → update →
  mark paid → notify, asserted as a sequence, plus every failure turn: a
  declined charge marks `failed` and returns the 402 shape; MarkPaid and
  notify failures are non-fatal by contract
- **gateways** — wire shapes at the sender: chaos header forwarding, JSON
  bodies, the 3s notification timeout, status-code-to-error mapping
- **handlers** — 402 vs 500 vs 400 vs 409 mapping, auth through the real
  middleware with a signed token
- **auth** — one sentinel for every bad-credential path, token claims and
  session keyed by jti
- **order cache-aside** — hit skips the DB, miss writes back, corrupt cache
  and dead Redis both degrade instead of fail
- **notification service** — the store's cap and copy semantics, payload
  validation, the chaos control plane staying reachable under injected
  failure (run with `-race` via `go test -race`)

## What actually ran

| Run | Result |
|---|---|
| `go test ./...` — apps/services, apps/notification (2026-08-17) | all packages PASS, `-race` clean on notification |
| cluster suites (2026-08-13) | see the previous run record in git history — needs `make up` first |

The 2026-08-13 numbers predate the notification leg and the unit suite; run
`make test` against a fresh `make up` to regenerate them rather than trusting
stale counts.

## Notes with more detail

- [notes/01-ingress-tls.md](../notes/01-ingress-tls.md) — Gateway, TLS, packet capture
- [notes/03-services-and-data.md](../notes/03-services-and-data.md) — probes, resources, workloads
- [notes/04-canary-rollout.md](../notes/04-canary-rollout.md) — Argo Rollouts canary, proven both directions
- [notes/05-gitops-argocd.md](../notes/05-gitops-argocd.md) — ArgoCD sync, self-heal, drift
- [notes/07-observability.md](../notes/07-observability.md) — LGTM pipeline, correlation, verification detail
- [notes/08-traefik-netpol-migration.md](../notes/08-traefik-netpol-migration.md) — Istio → Traefik, `demo` → `web`/`api`, NetworkPolicy wired into `make up`
