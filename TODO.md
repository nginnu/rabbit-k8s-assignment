# TODO

Each stage works on its own before the next one starts.
Acceptance criteria in [docs/goals.md](docs/goals.md).

## 1. Foundation ⚠️ needs re-verification

- [x] kind cluster, three nodes, host ports 80/443
- [x] Helm library chart — one template, values per service
- [x] one workload deployed and reachable through its Service
- [ ] `make up` runs end to end — ran clean on 2026-08-13, before
  `platform/manifests/` moved from the flat numbered layout to per-namespace
  folders and `gitops-bootstrap` was added as a new stage in `up`. That run no
  longer describes the current tree. As of 2026-08-14 `make up` is failing
  partway through on this tree and no run against the new layout has been
  observed passing — see Known issues

## 2. Data layer ✅

- [x] database as a StatefulSet with a PVC
- [x] cache
- [x] credentials generated into a Secret, never committed
- [x] schema and seed data loaded on first start
- [x] verify: data survives a pod restart

## 3. Application services ✅

- [x] backend services — one chart, one release each
- [x] frontend
- [x] shared config in a ConfigMap
- [x] probes and resource limits on every container
- [x] verify: a request goes through every service and comes back

## 4. Ingress + TLS ✅

- [x] gateway on 80/443
- [x] cert-manager issuing from a local CA
- [x] one route per service
- [x] verify: `https://` with no browser warning
- [x] verify: a test checks the traffic has no plaintext credentials

## 5. Observability — LGTM + OpenTelemetry ✅

- [x] OpenTelemetry SDK in each service, OTLP to a collector
- [x] Prometheus (metrics), Loki (logs), Tempo (traces), Grafana (dashboards)
- [x] trace_id on every log line
- [x] verify: follow one request from log to trace to metric

## 6. Progressive delivery ✅

- [x] Argo Rollouts installed
- [x] canary steps with analysis on a real metric
- [x] verify: a good release shifts traffic step by step
- [x] verify: a bad release rolls back automatically

## 7. GitOps ✅

Argo Rollouts and ArgoCD are separate projects; Rollouts needs no ArgoCD.
Section 6 does not depend on this one.

- [x] ArgoCD installed
- [x] one Application per service, App of Apps pointing at charts/apps/
- [x] `make apps` stays as a bootstrap and escape hatch
- [x] verify: a change committed to git reaches the cluster without make

## 8. Isolation ✅

- [x] default-deny NetworkPolicy
- [x] explicit allow rules between services that talk to each other
- [ ] verify: a blocked pod cannot reach the database — `make up` on
  2026-08-13 exercised every *allowed* path (all 51 suite checks passed with
  the policies enforced), but no suite attempts a denied path; see Deferred

## 9. Deliverables ⚠️ unverified

- [x] README — how to deploy and how to reach it
- [x] design notes — the decisions and the trade-offs
- [ ] `make up` works on a machine that has never run this project — what ran
  on 2026-08-13 was a rebuild on this machine, which has built and loaded
  these images before; a clone on a machine that has never seen the repo is
  still unproven

## Deferred

- Deploying to a cloud provider — the brief asks for reproducible, not hosted
- Canary bad-release rollback — proven once mid-note in
  [notes/04](notes/04-canary-rollout.md), not repeated as a standalone run the
  way the NetworkPolicy checks were
- App of Apps — proven on `dummy` only in
  [notes/05](notes/05-gitops-argocd.md); the other five services are not yet
  under ArgoCD management
- NetworkPolicy deny-path proof — enablement itself is no longer deferred:
  all six app charts set `networkPolicy: true` (`web`/`api`, replacing the old
  shared `demo` namespace), and `local/data/netpol.yaml` / `addons/
  observability/netpol.yaml` are applied by `make up` (from the `data` and
  `observability` targets respectively, not a manual step). `make up` on
  2026-08-13 exercised every *allowed* path across all 51 suite checks with 0
  pod restarts — proof the allow rules do not block traffic that should pass.
  What is not proven is the other half of the claim in section 8 ("a blocked
  pod cannot reach the database"): no `tests/*netpol*` script tries a denied
  path. Stays here until one exists and passes.

## Known issues (senior-devops)

- `Makefile:223` waits on `kubectl rollout status deployment/$$d` for every
  name in `APP_DEPLOYS`, but `order-svc` renders as a `Rollout`
  (`workloadKind: Rollout` in `charts/apps/order-svc/values.yaml`), not a
  `Deployment`. `make up` prints `Error from server (NotFound): deployments.apps
  "order-svc" not found` for that one name and moves on without ever waiting
  for it — pre-existing, confirmed still present on 2026-08-13, not
  introduced by the Traefik/netpol change.
