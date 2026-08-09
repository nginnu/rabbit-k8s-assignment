# TODO

Each stage works on its own before the next one starts.
Acceptance criteria in [docs/goals.md](docs/goals.md).

## 1. Foundation ✅

- [x] kind cluster, three nodes, host ports 80/443
- [x] Helm library chart — one template, values per service
- [x] one workload deployed and reachable through its Service
- [x] `make up` runs end to end

## 2. Data layer

- [ ] database as a StatefulSet with a PVC
- [ ] cache
- [ ] credentials generated into a Secret, never committed
- [ ] schema and seed data loaded on first start
- [ ] verify: data survives a pod restart

## 3. Application services

- [ ] backend services — one chart, one release each
- [ ] frontend
- [ ] shared config in a ConfigMap
- [ ] probes and resource limits on every container
- [ ] verify: a request goes through every service and comes back

## 4. Ingress + TLS

- [x] gateway on 80/443
- [x] cert-manager issuing from a local CA
- [x] one route per service
- [x] verify: `https://` with no browser warning
- [ ] verify: a test checks the traffic has no plaintext credentials

## 5. Observability — LGTM + OpenTelemetry

- [ ] OpenTelemetry SDK in each service, OTLP to a collector
- [ ] Prometheus (metrics), Loki (logs), Tempo (traces), Grafana (dashboards)
- [ ] trace_id on every log line
- [ ] verify: follow one request from log to trace to metric

## 6. Progressive delivery

- [ ] Argo Rollouts installed
- [ ] canary steps with analysis on a real metric
- [ ] verify: a good release shifts traffic step by step
- [ ] verify: a bad release rolls back automatically

## 7. Isolation

- [ ] default-deny NetworkPolicy
- [ ] explicit allow rules between services that talk to each other
- [ ] verify: a blocked pod cannot reach the database

## 8. Deliverables

- [ ] README — how to deploy and how to reach it
- [ ] design notes — the decisions and the trade-offs
- [ ] `make up` works on a machine that has never run this project

## Deferred

- GitOps (ArgoCD) — not asked for
- Deploying to a cloud provider — the brief asks for reproducible, not hosted
