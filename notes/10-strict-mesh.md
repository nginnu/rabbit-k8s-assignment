# Mesh-wide STRICT mTLS, and the one exception that makes it possible

> **Status: decision made 2026-08-26 — mesh-wide STRICT with a port-level
> exception on the chained gateway. Verified live: `tests/istio.sh` 39/39
> against the running kind cluster, and the full `make test` A/B'd before and
> after (details below). Supersedes the "Deferred: STRICT" call at the end of
> [notes/09](09-service-mesh-plan.md) — that deferral was correct when it was
> written; what changed is the subject of this note.**

---

## What changed since notes/09 deferred STRICT

Notes/09's deferral rested on one fact: *Traefik is not meshed and cannot
speak mTLS to the backends*, so any server-side STRICT would cut every edge
route at once. True then — Traefik's plaintext HTTP reached the backend pods
directly.

Then the chained ingress gateway landed (`2966d76`): edge traffic now runs
Traefik (TLS terminates) → **istio-ingressgateway:80** → meshed pods. The
plaintext surface shrank from "every backend pod" to **one port on one
workload**. And PeerAuthentication has a construct for exactly that shape:
a port-level DISABLE scoped to the gateway workload. The blocker didn't get
easier — it got a name and an API.

## The rule set

`k8s-local/istio/peer-authentication.yaml` (applied by `make istio`):

| Rule | Scope | Effect |
|---|---|---|
| `default-strict` (istio-system, no selector) | every sidecar in the mesh | STRICT — plaintext refused on all inbound app ports |
| `ingress-gateway-plain-80` (selector `istio: ingressgateway`) | the chained gateway | STRICT, except port 80 → DISABLE, where Traefik's plaintext arrives |

The workload-scoped STRICT rules for payment and notification (the notes/09
leading edge) are deleted — superseded, and a stale half-description of the
mesh is what the next change gets designed against. `make istio` deletes
them explicitly, because `kubectl apply` cannot delete objects.

**API trap, paid for once:** the port-level field is `portLevelMtls` — a
**map** keyed by port string — in `security.istio.io/v1` on Istio 1.30. The
documented-everywhere `portLevelSettings` **list** (port + mtls pairs) is the
old v1beta1 shape and the v1 CRD rejects it with
`strict decoding error: unknown field`. If a copy-paste from Istio docs fails
to apply, check the field name first, not the indentation.

## The trap that almost shipped: exact-match DRs beat wildcards

The port-80 exception alone was **not** enough. On the running cluster, the
gateway's Envoy clusters for `web-ui` and `order` were still `PLAINTEXT`
transport after the STRICT rules applied — because DestinationRule matching
is most-specific-wins, and the canary charts render an **exact-match** DR for
each Rollout (`order`, `web-ui` — subsets only, no TLS), which beat the
namespace wildcard `east-west-mtls` rules. Same for web-ui's own calls to
order. Under PERMISSIVE those paths silently ran plaintext — the exact
failure mode STRICT exists to expose, caught one hop before it cut the
storefront.

Fix: the shared chart's DestinationRule template
(`rabbit-gitops/charts/_template/templates/_destinationrule.tpl`) now renders
`trafficPolicy.tls.mode: ISTIO_MUTUAL` whenever `mesh: true`. After `make
apps`, the gateway's config_dump shows `envoy.transport_sockets.tls` for
both clusters. Lesson generalized: **a wildcard DR is a default, not a
guarantee — any exact-match DR silently shadows it, TLS policy included.**

Why not one mesh-wide DR (`*.local`, ISTIO_MUTUAL)? The data tier — mariadb,
redis — has no sidecars, and mTLS towards them breaks `order→mariadb` at
once. The declarations stay per namespace: `*.api` and `*.web`, both
`exportTo: ["*"]` so web-ui (ns `web`) and the gateway (ns `istio-system`)
can see them — the api rule previously exported only to `.`, which left
web-ui relying on auto-mTLS.

## What STRICT actually bought

Nothing changed on the wire for the paths that were already mTLS — that is
the point. STRICT converts mTLS from a preference (PERMISSIVE accepts both)
into the only way in:

- a pod that loses its sidecar, or a caller that never had one, gets a
  connection **reset** instead of a silent plaintext fallback;
- every inbound connection now carries a SPIFFE identity, so
  AuthorizationPolicy on principals (not just IPs) becomes meaningful —
  this is the prerequisite for identity-based authz, not a nice-to-have;
- the answer to "is east-west encrypted?" is a policy, provable by
  `tests/istio.sh`, instead of a claim about defaults.

## Proof — what `tests/istio.sh` now asserts

- the rule shape: selector-less STRICT exists, the gateway exception is a
  port-80-only DISABLE, and no workload-scoped PeerAuthentication remains;
- the edge survives its own exception: `GET /` still 200 through
  Traefik → gateway:80 → web-ui;
- every meshed service (auth, catalog, order, payment, notification — all
  :8080, **web-ui 3000**) has **no `raw_buffer` filter chain**
  on its app port — the Envoy config cannot accept plaintext;
- plaintext on the wire is refused: probes from a sidecar's own
  istio-proxy container (exempt from the iptables redirect, so the request
  leaves as real plaintext) to all five api services get connection reset,
  with senders chosen so NetworkPolicy — not STRICT — is never the blocker;
- catalog's `connection_security_policy="none"` counter does not move over
  five storefront requests — now a tripwire: under STRICT a rising counter
  means some path *stopped* speaking mTLS (lost sidecar, shadowed DR).

Full-suite A/B, same cluster, `git stash` as the switch: `istio` (39/0),
`auth`, `checkout`, `tls-proof`, `resilience` green under STRICT; the seven
red suites (`unit`, `routing`, `route-isolation`, `internal-routes`, `mesh`,
`o11y-stack`, `o11y-journey`) fail **identically** at the pre-STRICT
baseline — every one pre-existing, none caused by this change. The known
pre-existing set, for whoever picks them up: missing `../apps` sources for
`unit.sh`, the http→https 302 vs routing.sh's 200, the vanished
`x-envoy-decorator-operation` header, cilium not enforcing web-ui's
default-deny to mariadb, payment/notification/gorm spans absent from traces,
and grafana answering 308.

## Residual plaintext, accepted

The exception is honest about what it costs: **Traefik → gateway:80 is still
plain HTTP on the pod network.** On this kind cluster (one host) that is
nothing; on a real multi-node cluster it is plaintext between nodes. Closing
it needs the notes/09 follow-up — a sidecar on the Traefik pod with its
hostPorts excluded from inbound capture — which stays deferred, now as a
scoped gap with a document instead of a mesh-wide deferral.
