# Service mesh: Cilium vs Istio, and the boundary with Traefik

> **Status: DRAFT — planning only, nothing in this note is implemented or
> tested yet. Written 2026-08-14, ahead of the usual notes/ convention (see
> CLAUDE.md) at the user's explicit request. Revise or fold into a normal
> after-the-fact note once the mesh choice is implemented.**

---

## Already decided (not up for debate here)

- Istio is out as ingress, permanently. Traefik owns the Gateway. See
  `CLAUDE.md` — "it will come back later as a mesh (sidecar injection) only,
  never again as ingress."
- Still open, and this note does not close it: **Cilium or Istio for the
  mesh itself.** What follows is the investigation, not the call.
- Working boundary, true regardless of which mesh wins: **Traefik owns
  Gateway/ingress (north-south) completely. The mesh owns only CNI +
  east-west (pod-to-pod). The mesh must never take over routing, TLS
  termination, or path rewriting that Traefik already does.**

---

## Cilium vs Istio — architecture

| | Cilium | Istio |
|---|---|---|
| L3/L4 | eBPF in kernel | iptables/eBPF via sidecar or ztunnel |
| L7 proxy | per-node Envoy (cilium-agent embedded or `cilium-envoy` DaemonSet); traffic reaches it via eBPF+TPROXY redirect at the Service port, transparent to the pod | sidecar: Envoy injected per pod (mutating webhook) — or ambient: `ztunnel` per node (L4) + optional waypoint proxy per namespace (L7) |
| Ambient mode | n/a | GA as of Istio 1.30, sidecar-free — postdates the "sidecar injection" wording already in `CLAUDE.md`, worth flagging as new information, not a silent override |
| mTLS | supported, identity-based, **beta** | mature — SPIFFE identity (`spiffe://trust-domain/path`), SVID as X.509 or JWT, istiod issues off the pod's ServiceAccount |
| Resource scaling | proxy count scales with **node** count | sidecar: scales with **pod** count; ambient: scales with node count like Cilium |

Cilium is **not** zero-proxy — the proxy moved from per-pod to per-node.
Trade-off: smaller footprint, but one node's Envoy failing or overloading
hits every pod on that node, not just the one workload. Noisy-neighbor risk
between pods sharing a node's proxy.

---

## Verifying sidecar injection is actually happening (checklist for later, not run yet)

| Check | Sidecar present | Sidecar absent (Cilium / ambient) |
|---|---|---|
| `kubectl get pod <p> -n <ns> -o jsonpath='{.spec.containers[*].name}'` | 2 names (app + `istio-proxy`) | 1 name |
| `kubectl get pod` READY column | `2/2` | `1/1` |
| `kubectl get mutatingwebhookconfigurations istio-sidecar-injector -o yaml` | webhook exists, check `namespaceSelector` | n/a |
| `kubectl exec <p> -c istio-proxy -- ps aux` | real `envoy` process | container doesn't exist |
| `istioctl proxy-status` | mesh-wide view | n/a |
| Cilium case | — | envoy process lives on the `cilium-agent`/`cilium-envoy` DaemonSet on the node, not inside the app pod |

---

## Cilium's L7 ceiling — verified against [docs.cilium.io](https://docs.cilium.io), not from memory

| Feature | Status | Detail | What breaks if assumed otherwise |
|---|---|---|---|
| Circuit breaking | supported | via `CiliumEnvoyConfig`/`CiliumClusterwideEnvoyConfig` — raw Envoy Cluster fields (`max_pending_requests`, `max_requests`, `outlier_detection`) typed in directly, no Cilium-native simplified field | **trap** — these Envoy resources are not validated by Kubernetes at admission; `kubectl apply` succeeds on a broken config, the error only shows up later in `cilium-agent` logs. Same shape as the CRD-drop trap already in `CLAUDE.md`'s trap table |
| Retry | partial | only a global `cilium-agent` flag `--http-retry-count` (default 3) — no per-route policy. Per-route retry needs a hand-written raw Envoy `retry_policy` inside a `CiliumEnvoyConfig`, no dedicated field like Istio's `VirtualService.retries` | assuming per-route control exists and configuring it per-service silently falls back to the global default |
| Traffic shifting / canary | supported, documented | [L7 Traffic Shifting](https://docs.cilium.io/en/stable/network/servicemesh/l7-traffic-management/), example is a 50/50 weighted split via `CiliumEnvoyConfig` | — |
| Rate limiting (app traffic) | **not available** | see below | assuming it exists and skipping a rate-limit layer at Traefik leaves nothing enforcing it anywhere |
| Gateway API controller | supported, unused here | `gatewayClassName: cilium`, Gateway API v1.6.1 Core conformance passed (HTTPRoute, GRPCRoute, TLSRoute; TCPRoute/UDPRoute need extra install). Makefile already pins `GATEWAY_API_VERSION := v1.6.1` — exact match, no CRD version risk if this were ever used | recorded so nobody re-discovers this later and wonders why it's not wired up — **this project keeps Traefik as the only GatewayClass controller** |

**Trap that cost time — rate limiting.** Three things sound like the answer
and aren't:

1. **Bandwidth Manager** — byte/sec bandwidth shaping via eBPF+EDT, not
   request-rate limiting.
2. **"API Rate Limiting"** in Cilium's own docs — a real name trap. This page
   throttles calls to `cilium-agent`'s internal management API, nothing to
   do with application traffic.
3. A community ask for HTTPRoute-level rate limiting
   ([cilium/cilium#33500](https://github.com/cilium/cilium/issues/33500)) was
   closed as "not planned."

In theory the raw Envoy `local_ratelimit` filter could be wired through a
`CiliumEnvoyConfig` the same way circuit breaking is — unverified,
undocumented, marked speculative, not a supported path.

**Trap that cost time — URLRewrite.** Relevant because
`platform/manifests/addons/traefik/route.yaml` and
`platform/manifests/apps/api/route.yaml` both depend on URLRewrite for
Traefik's own routing today. It's a Gateway API **Extended**-conformance
filter, not Core. Cilium had a real bug here
([cilium/cilium#27954](https://github.com/cilium/cilium/issues/27954),
"HttpRoute URLRewrite not working"), addressed in PR #27472. Since this
project doesn't plan to move routing to Cilium this is background, not a
blocker — but if that boundary ever moves, re-verify against the current
release, don't assume the doc is still accurate.

---

## Cloud fit (recorded for later — cloud deployment itself stays out of scope per `CLAUDE.md`)

| | GKE | EKS |
|---|---|---|
| Cilium | Dataplane V2 is Cilium natively, `--enable-dataplane-v2` at cluster **creation only**, can't toggle on an existing cluster | not native — default is VPC CNI; NetworkPolicy needs `enableNetworkPolicy: true` on the VPC CNI add-on (≥1.21, EC2 Linux nodes only, not Fargate/Windows) plus `NETWORK_POLICY_ENFORCING_MODE=strict` for default-deny (EKS defaults to allow-all until policies sync — opposite of this project's posture). Cilium as a CNI replacement is a well-documented self-managed path, not an AWS-native add-on |
| Istio | Google Cloud Service Mesh — fully managed control + data plane | entirely self-managed, no AWS-native product (App Mesh is gone from current docs) |

Both mesh choices have a turnkey path on GKE; neither does on EKS.

---

## No-overlap checklist — what to watch if scope ever grows

| # | Concern | Risk if both layers configure it | Which layer owns it |
|---|---|---|---|
| 1 | Retry | Traefik retry × mesh retry stacking = N×M actual attempts, a retry storm | split by hop — Traefik owns edge retries, mesh owns internal service-to-service retries, never the same path twice |
| 2 | Circuit breaker | two layers tripping on different observed error rates, confusing to debug | mesh — closer to the backend, more accurate signal |
| 3 | TLS/mTLS boundary | undefined handoff between Traefik's edge TLS termination and mesh mTLS could leave a plaintext gap between the Traefik pod and the first backend pod, undermining what `tests/tls-proof.sh` already proves | needs an explicit diagram of where each handshake starts/ends before implementing, not assumed |
| 4 | Path rewrite/routing | Traefik already rewrites (`/api/auth` → `/auth`, see `platform/manifests/apps/api/route.yaml`); a mesh re-rewrite on the same path is a silent double-rewrite | Traefik owns all north-south rewriting, full stop |
| 5 | Health checks | three independent probers can hit the same pod — kubelet, Traefik active health check, mesh outlier detection — all three need to clear the default-deny NetworkPolicy, and `notes/08` only verified kubelet | decide explicitly whether mesh outlier detection replaces or supplements Traefik's active health check, before turning both on |
| 6 | Rate limit | moot for now — Cilium has no app-traffic rate limiting feature at all (see above) | Traefik, regardless of which mesh gets picked |
| 7 | L3/L4 allow/deny | Traefik middleware IP allowlist + CiliumNetworkPolicy = two sources of truth for "who can reach what," hand-synced and drifting | CiliumNetworkPolicy is the single source of truth for L3/L4; Traefik middleware stays L7-only |
| 8 | Trace context | `traceparent` generated at Traefik must propagate unbroken into the mesh, or the log→trace→metric chain `test-o11y`/`test-journey` prove breaks exactly at this seam | — (propagation correctness, not an ownership question) |
| 9 | Manifest ownership | mixing Traefik and mesh config in one folder means debugging one concern requires reading both | keep `platform/manifests/addons/traefik/` and the mesh's addon folder (e.g. `platform/manifests/addons/cilium/`) strictly separate |

**Not a list of active conflicts.** Every Cilium feature that could actually
overlap with Traefik — Gateway API controller, `CiliumEnvoyConfig` traffic
management — is opt-in and off by default. Running Cilium at CNI +
NetworkPolicy + mTLS only, without touching those knobs, has zero overlap
surface with Traefik by construction. The table above is what to keep
watching if scope ever grows past that, not a problem that exists today.

---

## Traefik's current responsibilities (baseline — pulled from the files, not restated from memory)

| File | Owns |
|---|---|
| `platform/addons/traefik/local/values.yaml` | sole GatewayClass controller (`kubernetesGateway.enabled: true`; `kubernetesIngress`/`kubernetesCRD` both off), `Gateway` object `platform` in `traefik` ns with `web` (:8000) and `websecure` (:8443, TLS terminate) listeners, single replica pinned to the control-plane node (hostPort constraint), `ClusterIP` Service (no cloud LB on kind), access logging on, `ingressClass.enabled: false` |
| `platform/manifests/addons/traefik/certificate.yaml` | TLS for `localhost`, `grafana.localhost`, `argocd.localhost`, `traefik.localhost`, off the `mkcert-ca` ClusterIssuer |
| `platform/manifests/apps/web/route.yaml` | host+path routing to web-ui |
| `platform/manifests/apps/api/route.yaml` | routing to the four backend services + dummy, URLRewrite filters |
| `platform/manifests/addons/observability/route.yaml` | routing to Grafana |
| `platform/manifests/addons/argocd/route.yaml` | routing to ArgoCD |
| `platform/manifests/addons/traefik/route.yaml` | routing to Traefik's own dashboard, URLRewrite filter |

None of this moves to the mesh under the working boundary above.
