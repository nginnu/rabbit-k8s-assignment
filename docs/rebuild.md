# Rebuilding the cluster

Two commands.

```sh
make down      # deletes the cluster — the database goes with it
make up        # rebuilds everything
```

`make down` is not optional. The `cluster` target checks whether
`rabbit-k8s-test` already exists and skips creation if it does, so `make up` on
its own leaves the old cluster in place.

## What `make up` does

```
cluster ──→ gateway ──→ traefik-dashboard ──→ images ──→ observability ──→ rollouts ──→ argocd ──→ apps ──→ gitops-bootstrap ──→ verify
   │           │              │                                 │
   │           │              └─ dashboard route, after          └─ prometheus, loki, tempo,
   │           │                 gateway is Programmed and          alloy, grafana
   │           │                 traefik's chart exposes :8080
   │           └─ gateway-api CRDs
   │              traefik
   │              cert-manager + CA
   │              Gateway + routes
   │
   └─ kind create (3 nodes, hostPort 80/443)
```

`observability` runs before `apps` so the first requests are captured. The
services do not depend on it — the OTel SDK logs a failed export and carries on,
so a missing collector costs telemetry, not availability.

`gitops-bootstrap` hands `notification` to Argo CD after `apps`, never before —
applied first, Argo CD would create `notification`'s objects itself with no
`meta.helm.sh` ownership, and the `helm upgrade --install notification` in `apps`
would then refuse to adopt them.

`verify` runs at the end without being asked.

## Then test

```sh
make test              # every suite, cheapest first
```

Or one at a time:

| Command | Proves |
|---|---|
| `make test-resilience` | the data and the service survive losing a pod |
| `make test-o11y` | the stack is up and receiving |
| `make test-journey` | one purchase, followed from log to trace to metric |

## Two things to expect

**The first run after `make down` is slow.** Traefik, cert-manager, the Gateway
API CRDs and five LGTM charts come over the network, every image is rebuilt, and
the node image cache went away with the cluster.

**The TLS warning may come back.** `tls` loads a local CA into the cluster. If
mkcert generates a new one, the browser does not trust it until you do.
`make test-tls` catches this.

## What survives what

| Action | Database |
|---|---|
| `kubectl delete pod mariadb-0` | survives — the PVC is untouched |
| `kubectl delete sts mariadb` | survives — the PVC is left behind |
| `kubectl delete pvc data-mariadb-0` | gone |
| `make down` | gone |

The StatefulSet does not delete its PVC when the pod goes, which is why a pod
can be recreated without losing anything. `make down` is different: kind's
`standard` storageclass writes to local-path inside the node container, and
deleting the cluster deletes the disk under it.
