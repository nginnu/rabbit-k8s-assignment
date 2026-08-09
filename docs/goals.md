# Goals

Build a Kubernetes deployment that anyone can run from a fresh clone.

Work order in [TODO.md](../TODO.md).

## What to build

A small multi-service app: frontend, backend services, and a datastore.

## Requirements

| # | Requirement | Done when |
|---|---|---|
| 1 | Deployments, StatefulSets, Services, ConfigMaps, Secrets | data survives a restart; no passwords in git |
| 2 | Liveness / readiness probes | an unhealthy pod stops receiving traffic |
| 3 | Resource requests and limits | set on every container |
| 4 | Ingress with TLS | `https://` with no browser warning, and a test checks for plaintext credentials |
| 5 | Metrics, dashboards, centralised logs | follow one request from log to trace to metric |
| 6 | Canary or blue-green | a bad release rolls back automatically |
| 7 | NetworkPolicy | a blocked pod cannot reach the database |

## Deliverables

- **Repo** — all manifests and charts
- **README** — how to deploy and how to reach it
- **Design notes** — the decisions and the trade-offs

## How I am working

- One command to build everything, so nothing depends on steps I did by hand.
- Every item above has something to run and something to look at. No item is
  marked done because it should work.
