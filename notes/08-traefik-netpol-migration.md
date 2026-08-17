# Traefik, `web`/`api`, NetworkPolicy

**Date:** 2026-08-13
**Status:** done — `make up` observed end to end, full suite 51/51 PASS

---

## What changed, as one move

| Piece | Before | After |
|---|---|---|
| Gateway | Istio (`istiod`, `istio-system`) | Traefik `41.2.0` / `v3.7.10`, namespace `traefik` |
| App namespace | one `demo` | `web` (frontend) + `api` (six backend workloads) |
| NetworkPolicy | declared in chart values, not enforced | `networkPolicy: true` on all six app charts, enforced |

Bundled because the NetworkPolicy split needs the namespace split to be
expressible, and the namespace split was done at the same time as the gateway
swap rather than as a second migration.

---

## Order

| # | Step | Why it has to come first |
|---|---|---|
| 1 | `gateway-api` CRDs | Traefik's `kubernetesGateway` provider needs the CRDs to register a GatewayClass, same trap as Istio |
| 2 | `01-namespaces.yaml` (`web`, `api`) | HTTPRoutes and NetworkPolicy peers reference these by name |
| 3 | Traefik chart, `gateway.enabled: true` | creates the GatewayClass and the Gateway object together — one release, one rollback unit |
| 4 | `02-certificates.yaml` into `traefik` ns | Gateway `certificateRefs` only reads Secrets in its own namespace |
| 5 | `04-routes.yaml` | HTTPRoutes in `web`/`api`, `parentRefs` → `platform` Gateway in `traefik` |
| 6 | `11-netpol-data.yaml` with/before `data` release | egress from app pods reaches nothing without the matching ingress already in place |
| 7 | `12-netpol-observability.yaml` with `observability` release | same reasoning, alloy/grafana/prometheus/loki/tempo side |

---

## Flow

```
                              https://localhost/
                                      │
                            ┌─────────▼─────────┐
                            │  Traefik Gateway  │   ns: traefik, hostPort 80/8000, 443/8443
                            └─────────┬─────────┘
                                      │
   ═══ namespace: web ═══                ═══════════ namespace: api ═══════════
   ║   [ web-ui ]        ║                ║ [auth][order]◄►[payment][dummy]  ║
   ║   ingress: gateway  ║                ║              → [payment-gateway]    ║
   ╚═════════════════════╝                ╚══════════════════════════════════╝
              default-deny both directions on all six, allow rules named per
              service/namespace — proven both ways only for allow (see below)
                    │                                    │
      (auth,order,payment → mariadb/redis)        (all six → alloy)
   ═══════ namespace: data ══════════════            ═══ namespace: observability ═══
```

---

## Traps that cost time

| Trap | What happens | Cost |
|---|---|---|
| Traefik pod runs `runAsUser: 65532`, `capabilities: drop: [ALL]` | cannot bind < 1024; listener `port:` must name the container port (8000/8443), not 80/443 | writing 80 fails the chart render outright, not a runtime error |
| Gateway `certificateRefs` is namespace-local | a Secret in another namespace needs a `ReferenceGrant`, none exists here | Certificate had to move to `traefik`, not stay where TLS notes originally put it |
| library chart used to default `namespace: demo` | a default renders valid YAML into the wrong namespace, no error | `namespace` is `required` now, no default — every `values.yaml` under `charts/apps/` must set it or the render fails loudly |
| `11-netpol-data.yaml` / `12-netpol-observability.yaml` applied by no target | harmless while every service had `networkPolicy: false` | the moment allow rules turned on, an app-side allow with no matching data-side ingress means every query drops silently, reading as a database outage — fixed by wiring both files into `make data` / `make observability` |

---

## What is proven, what is not

- 51/51 suite checks pass with `networkPolicy: true` live on all six app
  charts and both tier policies applied — every **allowed** path in the
  diagram above was exercised: routing, TLS, checkout, resilience,
  observability. 0 pod restarts.
- Whether kubelet probes survive a default-deny **ingress** policy could not
  be settled from documentation — kubelet-originated probe traffic is not
  always exempt by spec, implementations differ. Settled here by observation
  only: 0 restarts on every pod, under kindnet, on this cluster. Not a
  general claim about other CNIs.
- **Not proven**: that the default-deny actually denies. No suite sends
  traffic down a path this policy should reject — every check above is an
  allow-path proof, not a block-path one. Tracked in `TODO.md`.

---

## Files

| File | What |
|---|---|
| `platform/addons/traefik/local/values.yaml` | Gateway, listeners, hostPorts, resources |
| `platform/manifests/01-namespaces.yaml` | `web` / `api` split, reasoning in the file header |
| `platform/manifests/04-routes.yaml` | HTTPRoutes, `parentRefs` → `traefik` |
| `platform/manifests/11-netpol-data.yaml` | data-tier ingress allow, applied by `make data` |
| `platform/manifests/12-netpol-observability.yaml` | observability-tier ingress allow, applied by `make observability` |
| `charts/platform-service/templates/_helpers.tpl` | `namespace` now `required`, no default |
| `charts/platform-service/templates/_networkpolicy.tpl` | peer catalogue shared by all six app charts |
