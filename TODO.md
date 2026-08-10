# TODO

Each stage works on its own before the next one starts.
Acceptance criteria in [docs/goals.md](docs/goals.md).

## 1. Foundation ✅

- [x] kind cluster, three nodes, host ports 80/443
- [x] Helm library chart — one template, values per service
- [x] one workload deployed and reachable through its Service
- [x] `make up` runs end to end

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
- [x] verify: a blocked pod cannot reach the database

## 9. Deliverables ✅

- [x] README — how to deploy and how to reach it
- [x] design notes — the decisions and the trade-offs
- [x] `make up` works on a machine that has never run this project

## Deferred

- Deploying to a cloud provider — the brief asks for reproducible, not hosted
- Canary bad-release rollback — proven once mid-note in
  [notes/04](notes/04-canary-rollout.md), not repeated as a standalone run the
  way the NetworkPolicy checks were
- App of Apps — proven on `dummy` only in
  [notes/05](notes/05-gitops-argocd.md); the other five services are not yet
  under ArgoCD management
- NetworkPolicy — applied to the `data` and `observability` tiers; the `demo`
  tier ships with `networkPolicy: false` (see the README's NetworkPolicy
  section for why)
