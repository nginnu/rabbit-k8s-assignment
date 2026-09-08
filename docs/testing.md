# Testing

```sh
make test            # every suite, cheapest first
make test-auth       # one suite
```

`make test` runs `tests/run-all.sh` and prints a table at the end: pass, fail,
time, result per suite. Nothing uses `set -e` — a suite records a failure and
keeps going, so one run reports every problem instead of the first.

## The suites

| Command | Suite | Proves |
|---|---|---|
| `make test-routing` | `routing.sh` | a request reaches the service that owns the path |
| `make test-route-isolation` | `route-isolation.sh` | a console host is answered by its own backend, never by the storefront |
| `make test-internal-routes` | `internal-routes.sh` | `/internal/*` is gone, not merely unrouted at the edge |
| `make test-mesh` | `mesh.sh` | Cilium is the CNI, and a pod with no allow-rule is refused the database |
| `make test-istio` | `istio.sh` | every api pod carries a ready sidecar; east-west mTLS is STRICT, not assumed |
| `make test-auth` | `auth.sh` | bad credentials are rejected, the token opens the API |
| `make test-checkout` | `checkout.sh` | a purchase completes end to end, notification leg included |
| `make test-o11y` | `o11y-stack.sh` | the observability stack is assembled and receiving |
| `make test-journey` | `o11y-journey.sh` | one purchase followed log → trace → metric |
| `make test-tls` | `tls-proof.sh` | tcpdump on the node: the http capture holds the password, the https capture does not |
| `make test-resilience` | `resilience.sh` | data and service survive a pod delete |
| `make test-unit` | `unit.sh` | **broken** — points at `../apps/…`; the Go sources live in `rabbit-api`, whose own CI already runs `go test -race` on both modules |

Order in `run-all.sh` is cheapest first. `resilience.sh` is last because it
deletes pods and takes minutes.

`routing.sh` fails one check by design mismatch, not by breakage: it expects
200 on `http://localhost/` and gets the 302 the Gateway is configured to
send.

## The gate that is not a suite

```sh
make test-preflight
```

`tests/preflight.sh` asks a different question from the rest: **is what is
running what the repo declared?** It reads each pinned version out of the
Makefile and the running value out of the cluster, and prints both. That is why
it is not in `run-all.sh` — run it when versions, routes or releases are the
thing in doubt.

Two of its checks fail on the script's own stale paths rather than on the
cluster: it looks for `platform/scripts/check-image-tags.sh`, which moved to
`rabbit-gitops/scripts/`, and for a Gateway listener named `websecure`, which
is named `https`.

## Load

No Make target. k6 reads its settings from the environment:

```sh
k6 run tests/k6-shop-load.js       # steady storefront traffic
k6 run tests/k6-canary-load.js     # traffic for a canary analysis window
```

Both default to `BASE=https://localhost`, user `alice`, password `password`.
Neither is pass/fail — they generate the traffic a rollout is judged on.
