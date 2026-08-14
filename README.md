# rabbit-k8s-assignment

A reproducible Kubernetes deployment, built locally on kind.

```
                              https://localhost/
                                      │
                            ┌─────────▼─────────┐
                            │  Traefik Gateway  │   TLS, local CA
                            └─────────┬─────────┘
                                      │
        ┌──────────────┬──────────────┼──────────────┬──────────────┐
        ▼              ▼              ▼              ▼              ▼
   [ web-ui ]    [ auth-svc ]   [ order-svc ]  [ payment-svc ]  [ grafana ]
    ns: web            │              │              │               │
                       │              │              ▼               │
                       │              │       [ mock-payment ]       │
                       │              │        ns: api (all four)    │
                       └──────┬───────┘                              │
                              ▼                                      │
                     [ mariadb ] [ redis ]                           │
                     StatefulSet   sessions                          │
                       + PVC                                         │
                                                                     ▼
   every pod ───── OTLP :4317 ─────► [ alloy ] ─────► [ prometheus · loki · tempo ]

   delivery:  Argo Rollouts (canary)  ·  Argo CD (GitOps, https://localhost/argocd)
```

Istio is out of this stack — removed completely, no `istiod`, no `istio-system`.
Traefik owns ingress now (chart `41.2.0`, app `v3.7.10`, namespace `traefik`).
Istio is planned to come back at a later stage as a service mesh only (sidecar
injection), never again as the Gateway; that stage has not started.

`make up` ran end to end on 2026-08-13 against this tree: 3 kind nodes Ready,
every release installed into the namespace its chart declares, every pod
Running with 0 restarts. The full suite passed after — 51 checks across
routing, TLS, checkout, resilience and observability; see
[docs/testing.md](docs/testing.md) for the breakdown. That run was a rebuild
on a machine that had already built and loaded these images; `make up` on a
genuinely fresh clone is still unproven (tracked in [TODO.md](TODO.md)).

**What this is** — a shirt shop, deliberately small but split the way a real one
is: a Next.js frontend, three Go services that call each other, a MariaDB
StatefulSet and a Redis cache. A purchase touches four of them in five requests,
so there is something real to trace, break, and roll back.

Install the prerequisites first: [docs/install.md](docs/install.md). How to
verify it once it's up: [docs/testing.md](docs/testing.md). What the
assignment asks for: [docs/goals.md](docs/goals.md). Starting over from a
clean cluster: [docs/rebuild.md](docs/rebuild.md).

| | | |
|---|---|---|
| ![the shop](notes/screenshot/common/Screenshot%202569-08-10%20at%2020.31.57.png) | ![grafana](notes/screenshot/common/Screenshot%202569-08-11%20at%2001.02.01.png) | ![argocd](notes/screenshot/argocd/Screenshot%202569-08-10%20at%2016.06.32.png) |
| `https://localhost` — log in with `alice` / `password` (any of the seeded users, same password) | `https://localhost/grafana` — anonymous access, opens straight into Admin | `https://localhost/argocd` — user `admin`, password from the chart's generated secret |

```sh
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 -d
```

---

## Deployment use GitOps

```
[ Service Repo A ] ──┐
[ Service Repo B ] ──┤
[ Service Repo C ] ──┤
                     │ CI Build & Push Image
                     ▼
              [ Container Registry ]
                     │
                     │ Update Version
                     ▼
             [ GitOps Config Repo ]
             ─────────────────────
             app-a/values.yaml
             app-b/values.yaml
             app-c/values.yaml
                     ▲
                     │ PR / Auto Update
                     │
              [ IDP / Platform ]
              ── Golden Path ──
                     │
                     ▼
                  [ Argo CD ]
                     │
             Reconcile Desired State
                     │
                     ▼
              [ Argo Rollouts ]
             Progressive Delivery
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   [ App A ]     [ App B ]     [ App C ]
     Helm          Helm          Helm
```

**Why** — Why do we need it?

To eliminate Configuration Drift, reduce Deployment Inconsistency, and prevent
unauthorized changes in Production.

**How** — How does it work / Why is it better?

Git is the Desired State and Single Source of Truth. Every change must go
through a Git PR. Argo CD continuously reconciles the actual environment with
Git, while Argo Rollouts enables Progressive Delivery.

**What** — What do we get / What problem does it solve?

Standardized, Auditable, Reproducible deployments, fast Rollback, and safer
Releases with reduced Blast Radius.

**Key Control:**

Zero Direct Access — No direct changes to Production. All changes must go
through Git PRs.

Details: [GitOps with Argo CD](notes/05-gitops-argocd.md) · [Canary rollout](notes/04-canary-rollout.md) 

---

## Observability with LGTM

```
                    [ User ]
                       │
                       ▼
                    [ Web ]
                       │
                trace_id / session_id/order_id
                       │
          ┌────────────┼─────────────┐
          ▼            ▼             ▼
       [ Auth ]    [ Product ]    [ Order ]
                                      │
                                      ▼
                                  [ Payment ]

                       │
                       ▼

                [ Observability ]
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       [ Logs ]     [ Metrics ]   [ Traces ]
          │            │             │
          └────────────┼─────────────┘
                       ▼
            [ Grafana / Loki / Tempo
                  / Prometheus ]


        ─────── Optional Trace Propagation ───────
        trace_id can be propagated across
        Microservices when distributed tracing
        is required.
```

**Correlation:**

```
  session_id + user_id → User Journey / Session-level analysis
  trace_id              → Request / Distributed tracing
```

I use session_id and user_id for user-journey correlation. Trace ID
propagation across services is also supported when distributed tracing is
required — see [Correlation](notes/07-observability.md#correlation--the-part-that-makes-it-usable)
in [notes/07 — observability](notes/07-observability.md).

---

## TLS Encryption

**Why** — Why do we need it?

To protect data in transit by encrypting communication and preventing
unauthorized parties from reading or modifying traffic.

**How** — How does it work / Why is it better?

For local development, I use mkcert as a trusted local CA, with cert-manager
issuing the certificate to the Traefik Gateway on port 443. In Production, I
would terminate TLS at the Edge/Load Balancer and use Gateway API for
standardized traffic routing.

**What** — What do we get / What problem does it solve?

Encrypted communication at the Edge, with a clear path to mTLS for
service-to-service communication to provide workload identity and prevent
unauthorized service-to-service access.

**Production Extension:**

mTLS Service-to-Service — to authenticate workloads and encrypt internal
traffic, not just protect client-to-edge communication.

**Scope Note:**

This implementation is intentionally simplified for a local environment. The
Production design would extend the same security model with Edge TLS and
service-to-service mTLS.

```
                    Production Should Be

[ Client ]
    │
    │ HTTPS / TLS
    ▼
[ Edge / Load Balancer ]
    │
    │ TLS
    ▼
[ Gateway API ]
    │
    │ Routing
    ├───────────────┐
    ▼               ▼
[ Service A ] ──mTLS──► [ Service B ]
    │                     │
    └──────── mTLS ───────┘
```

Local proof — packet capture showing no plaintext credential crosses the
wire once TLS is on: [ingress + TLS proof](notes/01-ingress-tls.md#result).
That capture ran against the Istio Gateway, before the Traefik switch. Re-run
against Traefik on 2026-08-13 (`tls-proof.sh`, 4/4 PASS): chain still verifies
against the system trust store, http still leaks the password, https still
carries none — see [docs/testing.md](docs/testing.md).

---

## NetworkPolicy

Production should be —

```
                         PUBLIC / INTERNET
                               │
                               │ North-South
                               ▼
                    ┌─────────────────────┐
                    │      [ Gateway ]    │
                    │   Public Entry Point │
                    └──────────┬──────────┘
                               │
                 ┌─────────────┼─────────────┐
                 │             │             │
                 ▼             ▼             ▼
             [ web-ui ]    [ grafana ]   [ argocd ]
              Internal      Internal       Internal
                 │
                 │
          ┌──────┴──────────────────────────────┐
          │        EAST-WEST / INTERNAL          │
          │                                      │
          ▼              ▼               ▼       │
      [ auth ]        [ order ]      [ payment ] │
          │              │               │       │
          └──────────────┼───────────────┘       │
                         │                       │
                         ▼                       │
                  [ mariadb / redis ]            │
                         │                       │
                         └── Internal ───────────┘


      ─────────────── OBSERVABILITY ─────────────────

             [ every pod ]
                  │
                  │ OTLP
                  ▼
               [ alloy ]
                  │
        ┌─────────┼─────────┐
        ▼         ▼         ▼
 [ prometheus ] [ loki ] [ tempo ]
```

**NetworkPolicy principle:**

The Gateway is the single north-south entry point. Backend services are not
directly exposed through the Gateway. East-west traffic is explicitly
allowed only between required services, and the data tier is isolated from
the Gateway.

## Current Assignment — where this stands

`demo` is gone. The frontend and the six backend workloads are split into two
namespaces so a NetworkPolicy peer can be written by namespace instead of by
naming every pod (`kubernetes.io/metadata.name` on the namespace, set by the
API server, not a hand-applied label — see `platform/manifests/apps/web/namespace.yaml`).

```
                              https://localhost/
                                      │
                            ┌─────────▼─────────┐
                            │  Traefik Gateway  │
                            └─────────┬─────────┘
                                      │
   ═══ namespace: web ═══                ═══════════ namespace: api ═══════════
   ║        │           ║                ║        │                          ║
   ║        ▼           ║                ║   ┌────┼────────┬─────────┬─────┐ ║
   ║   [ web-ui ]        ║                ║   ▼    ▼        ▼         ▼     ▼ ║
   ║   ingress: gateway  ║                ║ [auth][order]◄►[payment][dummy]  ║
   ║   egress: alloy     ║                ║              start/settle,      ║
   ╚═════════════════════╝                ║              both ways          ║
                                           ║                  │              ║
                                           ║                  ▼              ║
                                           ║           [ mock-payment ]      ║
                                           ║           no gateway ingress    ║
                                           ╚══════════════════════════════════╝
              default-deny both directions on all six — every peer above is a
              named service or namespace in the chart's networkPolicyPeers,
              not an open namespace
                    │                                    │
      (auth,order,payment → mariadb/redis)        (all six → alloy)
                    │                                    │
   ═══════ namespace: data ═══════════════════   ═══ namespace: observability ═══
   ║                ▼                        ║   ║              ▼               ║
   ║   ingress from named pods only:          ║   ║   alloy ingress: api ns     ║
   ║   [ mariadb :3306 ] ← auth, order, payment║  ║   grafana ingress: gateway  ║
   ║   [ redis :6379 ] ← auth, order (not payment)║ prometheus/loki/tempo: internal║
   ║   egress: DNS only                        ║  ║   egress: DNS + each other  ║
   ╚═══════════════════════════════════════════╝  ╚═══════════════════════════════╝
```

Every service chart under `charts/apps/` now sets `networkPolicy: true` — this
replaces an earlier version of this project where the app tier had the peers
declared in `values.yaml` but no enforcement, so every pod in one shared
namespace could reach every other regardless of what was declared. That was
wrong to leave standing once `data` and `observability` had real enforcement
next to it.

Run on 2026-08-13: `make up` applies all of it (`local/data/netpol.yaml` and
`addons/observability/netpol.yaml` are wired into the `data` and `observability`
targets), `kubectl get netpol -A` showed policies in every namespace above,
and the full 51-check suite — which exercises every allowed path in the
diagram — passed with 0 pod restarts. That proves the allow rules are not
blocking traffic that should get through. It does not prove the default-deny
half of the claim: no suite here sends traffic down a path this policy should
reject. `TODO.md` tracks the deny-path test as still open.

In a cloud-native environment, the same principle applies at the network
layer: separate public and private subnets appropriately, with Security
Group / Firewall rules between them, exposing only the required entry points
and keeping backend and data tiers private.

---

## Notes

| Doc | How to |
|---|---|
| [docs/install.md](docs/install.md) | install the prerequisites |
| [docs/rebuild.md](docs/rebuild.md) | tear down and rebuild the cluster |
| [docs/testing.md](docs/testing.md) | verify it once it's up |

| Note | What it covers |
|---|---|
| [notes/01 — ingress + TLS](notes/01-ingress-tls.md) | Gateway, mkcert CA, cert-manager, packet-capture proof |
| [notes/03 — services and data](notes/03-services-and-data.md) | workloads, probes, resources, MariaDB + Redis |
| [notes/04 — canary rollout](notes/04-canary-rollout.md) | Argo Rollouts on order-svc, proven both directions |
| [notes/05 — GitOps with Argo CD](notes/05-gitops-argocd.md) | auto-sync and selfHeal on the dummy service |
| [notes/07 — observability](notes/07-observability.md) | LGTM pipeline, correlation, verification detail |
| [notes/08 — Traefik + NetworkPolicy migration](notes/08-traefik-netpol-migration.md) | Istio → Traefik, `demo` → `web`/`api`, NetworkPolicy wired into `make up` |

**Honest note:** due to the limited time available, this implementation
focuses primarily on the core Kubernetes setup and functionality rather than
detailed production-level refinement. Most of the main components are
implemented and working, although some areas may still contain bugs, errors,
or require further improvement and hardening.

---
