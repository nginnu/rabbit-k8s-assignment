# rabbit-k8s-assignment

A reproducible Kubernetes deployment, built locally on kind — a microservices
platform of six workloads simulating a shirt shop end to end: storefront UI,
auth, catalog, order, payment, notification. Security in transit throughout:
TLS at the edge, mTLS through the mesh.

This architecture is designed with production in mind, so moving from the local
environment to managed cloud services requires minimal changes to the
application architecture and configuration.

```
[ Browser ]
       │  HTTPS — mkcert CA trusted on the host
       ▼  host :80 / :443
┌────────────────────────────────────────────────────────┐
│ ns: gateway                                            │
│   Traefik — Gateway "external"                         │
│     :8000 http  → redirect https                       │
│     :8443 https → TLS terminate                        │
└───────────────────────────┬────────────────────────────┘
                            │  HTTPRoute
                            ▼
┌────────────────────────────────────────────────────────┐
│ ns: istio-system                                       │
│   Istio Ingress Gateway :80                            │
└───────────────────────────┬────────────────────────────┘
                            │
             ┌──────────────┴───────────────┐             
             ▼                              ▼             
┌─────────────────────────┐ ┌────────────────────────────┐
│ ns: web                 │ │ ns: api                    │
│   web-ui (Next.js BFF)  │ │   auth · catalog · order   │
│   :80 → :3000           │ │   :80 → :8080              │
│                         │ │     ├─► payment            │
│                         │ │     └─► notification       │
└────────────┬────────────┘ └────────────────┬───────────┘
             │                              │             
             └──────────────┬───────────────┘             
                            ▼
┌────────────────────────────────────────────────────────┐
│ ns: data                                               │
│   MariaDB :3306                                        │
│   Redis   :6379                                        │
└────────────────────────────────────────────────────────┘

   NetworkPolicy      — deny-all per ns, allow only declared peers (Cilium)
   PeerAuthentication — STRICT, mTLS between every meshed pod


┌────────────────────────────────────────────────────────┐
│ Observability Infrastructure                           │
└────────────────────────────────────────────────────────┘

  Application Pods (ns: web, api)
   ├─► Apps ──────────── push OTLP :4317 ────┐
   ├─► Envoy Sidecar ─── pull HTTP :15020 ───┼─► [ alloy ]
   └─► istiod ────────── pull HTTP :15014 ───┘      │
                                                    ├─► [ prometheus ] ───┐
                                                    │       ▲             │
                                                    │       │ metric query│
                                                    │  [ Argo Rollouts ]  ├─► [ grafana ]
                                                    │  (canary analysis)  │   [ kiali ]
                                                    ├─► [ loki ] ─────────┤
                                                    └─► [ tempo ] ────────┘
```

## URLs

- `https://localhost` — the storefront (web-ui)
- `https://grafana.localhost` — Grafana dashboards
- `https://kiali.localhost` — Kiali service-mesh console
- `https://argocd.localhost` — Argo CD (GitOps UI)
- `https://rollouts.localhost` — Argo Rollouts dashboard
- `https://traefik.localhost` — Traefik dashboard
- `https://hubble.localhost` — Hubble UI (Cilium flows)

**Repositories**

- [rabbit-api](https://github.com/nginnu/rabbit-api) — Go services: auth · catalog · order · payment · notification
- [rabbit-web](https://github.com/nginnu/rabbit-web) — Next.js storefront (web-ui)
- [rabbit-gitops](https://github.com/nginnu/rabbit-gitops) — GitOps config repo: one Helm chart per service · Argo CD ApplicationSet
- [rabbit-k8s-assignment](https://github.com/nginnu/rabbit-k8s-assignment) — this repo: cluster · addons · OPA policy

---

## Deployment use GitOps

```
 APP CODE                CI / BUILD                    RELEASE & DEPLOYMENT
 ───────────────┐
  rabbit-api    │
    auth        │              ┌──────────────────┐
    catalog     │  git push    │  GitHub Actions  │   push     [ Docker Hub ]
    order       │ ───────────► │  build · test    │ ─────────►  nginnu/rabbit-*
    payment     │              │  image           │             tag = git sha
    notification│              └────────┬─────────┘
  rabbit-web    │                       │
    web-ui      │                       │ promote — bump values-ci.yaml
 ───────────────┘                       │
                                        │
                  ┌─────────────────────┴─────────────────────┐
                  ▼ dev                                       ▼ prod
        [ auto-promote on merge ]                ┌─────────────────────────┐
                  │                              │  IDP Portal — planned   │
                  │                              │  release orchestration  │
                  │                              │   · schedule date       │
                  │                              │   · grouping & deps     │
                  │                              └────────────┬────────────┘
                  │                                           │
                  └─────────────────────┬─────────────────────┘
                                        ▼
                       ╔════════════════════════════════════╗
                       ║          rabbit-gitops             ║
                       ║  one Helm chart per service        ║
                       ║  ApplicationSet                    ║
                       ╚════════════════╤═══════════════════╝
                                        │ validate: gitleaks · helm template
                                        │           kubeconform · conftest (OPA)
                                        ▼
                                   [ Argo CD ]
                             one Application per service
                              auto-sync: prune · selfHeal
                                        │
                                        ▼
                                [ Argo Rollouts ]
                            canary · analysis · rollback
                                        │
                                        ▼
                                 [ kind cluster ]
                            ≈ GKE — no cloud-specific manifests

 [ rabbit-k8s-assignment ]  platform repo — cluster · addons · OPA policy
        │ make up (kind + addons)        │ policy/ → conftest above
        └──────────► kind cluster ◄──────┘
```

Git is the single source of truth — Argo CD reconciles the cluster to it,
Argo Rollouts releases step by step and rolls back on metric failure. Zero
direct access: nothing reaches the cluster except through Git. The result is
standardized, auditable, reproducible deployments — fast rollback, safer
releases, reduced blast radius.

**For more detail** → [Notion — design decisions, test methodology & video demo](https://app.notion.com/p/Rabbit-k8s-Test-3c917d7441dc804087cef2bf311f7686)

## Stack

| | Role | Key Impact |
|---|---|---|
| **kind** | Multi-node Kubernetes | 3-node cluster on a single machine for production-like testing |
| **Cilium** | CNI + NetworkPolicy | Enforce network policies with eBPF; model closely aligned with GKE Dataplane V2 |
| **Hubble** | Network Observability | Real-time visibility into network flows and policy enforcement |
| **Traefik** | Gateway API Controller | Edge HTTPS termination and routing with an easy-to-use monitoring dashboard |
| **cert-manager** | TLS | Automated TLS certificates for a production-like local environment |
| **Istio Ingress Gateway** | Mesh Entry Point | Controlled entry into the service mesh from the edge |
| **Istio Service Mesh** | Sidecar Mode · mTLS + Traffic Split | Enforce strict mTLS and fine-grained Canary traffic splitting with Argo Rollouts |
| **Istio DestinationRule** | Circuit Breaker + Retry | Isolate unhealthy upstreams and handle transient failures at the mesh layer |
| **Argo CD** | GitOps | Git as the Single Source of Truth with automated cluster sync |
| **Argo Rollouts** | Progressive Delivery | Automated Canary rollout, metric-based analysis, and rollback |
| **Prometheus** | Metrics | Business metrics as the deployment gate |
| **Loki** | Logs | Centralized application and infrastructure logs |
| **Tempo** | Tracing | Distributed tracing across services |
| **Alloy** | Telemetry Collector | Centralized OTLP collection and routing without backend coupling |
| **Grafana** | Observability | Unified view of metrics, logs, and traces |
| **Kiali** | Service Mesh Observability | Real-time service topology and mTLS visibility |
| **MariaDB** | Database | Persistent application data |
| **Redis** | Session + Cache | Persistent session and cache layer independent of application pods |
| **Go × 5** | Backend Services | Independent `auth`, `catalog`, `order`, `payment`, and `notification` services |
| **Next.js** | Web UI + BFF | BFF layer keeps backend services inaccessible directly from the browser |

---

**For a deeper dive:**

- [Blog — the build, decision by decision (Notion)](https://app.notion.com/p/Rabbit-k8s-Test-3c917d7441dc804087cef2bf311f7686)
- [Video — presentation & demo](https://app.notion.com/p/Rabbit-k8s-Test-3c917d7441dc804087cef2bf311f7686)

<br><br><br><br><br><br><br><br>

## Notes

| Doc | How to |
|---|---|
| [docs/install.md](docs/install.md) | run it from a fresh clone — nine steps, in order |
| [docs/testing.md](docs/testing.md) | verify it once it's up |
