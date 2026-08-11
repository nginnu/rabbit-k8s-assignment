# rabbit-k8s-assignment

A reproducible Kubernetes deployment, built locally on kind.

```
                              https://localhost/
                                      │
                            ┌─────────▼─────────┐
                            │   Istio Gateway   │   TLS, local CA
                            └─────────┬─────────┘
                                      │
        ┌──────────────┬──────────────┼──────────────┬──────────────┐
        ▼              ▼              ▼              ▼              ▼
   [ web-ui ]    [ auth-svc ]   [ order-svc ]  [ payment-svc ]  [ grafana ]
                       │              │              │               │
                       │              │              ▼               │
                       │              │       [ mock-payment ]       │
                       │              │                              │
                       └──────┬───────┘                              │
                              ▼                                      │
                     [ mariadb ] [ redis ]                           │
                     StatefulSet   sessions                          │
                       + PVC                                         │
                                                                     ▼
   every pod ───── OTLP :4317 ─────► [ alloy ] ─────► [ prometheus · loki · tempo ]

   delivery:  Argo Rollouts (canary)  ·  Argo CD (GitOps, https://localhost/argocd)
```

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
issuing the certificate to the Istio Gateway on port 443. In Production, I
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

## Current Assignment — Temporary

```
                              https://localhost/
                                      │
                            ┌─────────▼─────────┐
                            │   Istio Gateway   │  
                            └─────────┬─────────┘
                                      │
   ══════════════════════════ namespace: demo ═════════════════════════════════
   ║                                  │                                       ║
   ║        ┌──────────┬──────────────┼──────────────┬──────────┐            ║
   ║        ▼          ▼              ▼              ▼          ▼            ║
   ║   [ web-ui ]  [ auth-svc ]  [ order-svc ]  [ payment-svc ]  [ dummy ]    ║
   ║                                  │◄────────────►│                       ║
   ║                                  │   start/settle (declared, both ways) ║
   ║                                                  │                      ║
   ║                                                  ▼                      ║
   ║                                          [ mock-payment ]                ║
   ║                                          no gateway ingress declared     ║
   ║                                                                         ║
   ║   every pod in demo can reach every other pod in demo — the             ║
   ║   ingress/egress lists above are declared in values.yaml only           ║
   ╚═══════════════════════════════════════════════════════════════════════╝
                    │                                    │
      (declared: auth,order,payment → mariadb/redis)   (declared: all 6 → alloy)
                    │                                    │
   ═══════ namespace: data — NetworkPolicy LIVE ═══   ═══ namespace: observability — LIVE ═══
   ║                ▼                            ║   ║              ▼                      ║
   ║   ingress from named pods only:              ║   ║   alloy  ingress: whole demo ns     ║
   ║   [ mariadb :3306 ] ← auth, order, payment    ║   ║   grafana ingress: gateway only     ║
   ║   [ redis   :6379 ] ← auth, order (not payment)║  ║   prometheus/loki/tempo: internal   ║
   ║   egress: DNS only                            ║   ║   egress: DNS + each other's ports  ║
   ╚═══════════════════════════════════════════════╝   ╚══════════════════════════════════════╝
```

This is intentionally temporary. Option 1 requires application changes to
route backend calls through web-ui, while Option 2 requires no application
rewrite. Given the assignment time constraint, I chose Option 2 for
delivery. It should be replaced by Option 1 once the application
architecture is ready.

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

**Honest note:** due to the limited time available, this implementation
focuses primarily on the core Kubernetes setup and functionality rather than
detailed production-level refinement. Most of the main components are
implemented and working, although some areas may still contain bugs, errors,
or require further improvement and hardening.

---
