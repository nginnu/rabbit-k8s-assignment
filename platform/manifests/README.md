# manifests

One folder = one namespace = one unit `kubectl delete ns <name>` removes
completely. Every namespace folder sits at depth 2 — `apps/`, `addons/`,
`local/` — so a single glob (`*/*/namespace.yaml`) reaches all of them.
Filenames are standardised: `namespace.yaml`, `route.yaml`, `netpol.yaml`,
`certificate.yaml`. Workloads keep their real name (`mariadb.yaml`,
`redis.yaml`).

The Makefile owns apply order by naming folders and files in its targets —
nothing here encodes order a second time.

## Depth-1 groupings

| folder | what it means |
|---|---|
| `apps/` | ours — `web`, `api`. Workloads come from `charts/`; these folders hold only the namespace and the routes |
| `addons/` | objects that accompany an upstream Helm release — `traefik`, `observability`, `argocd`. Mirrors `platform/addons/`, which holds the values for the same three names: values in one tree, objects in the other |
| `local/` | stand-ins for managed services — `data` (MariaDB, Redis). See "Data tier — why MariaDB and Redis aren't charts" in `docs/convention.md` |
| `cluster/` | cluster-scoped objects, deliberately at depth 1: a ClusterIssuer has no namespace to be a unit of, and depth 1 keeps it out of the namespace glob |
| `_archive/` | kept, never applied — no Make target names `_archive/`, and none of the `kubectl apply -f <folder>` targets recurse into subfolders they weren't given |

## Folder → Make target

| folder | target | what runs |
|---|---|---|
| `apps/web`, `apps/api` | `namespaces` | `namespace.yaml` only, via the depth-2 glob |
| `apps/web`, `apps/api` | `gateway` | `route.yaml` from both, in one `kubectl apply -f ... -f ...` |
| `addons/traefik` | `tls` | `certificate.yaml` |
| `addons/traefik` | `traefik-dashboard` | `route.yaml`, after `gateway` so the Gateway is Programmed first |
| `addons/observability` | `namespaces` | `namespace.yaml` |
| `addons/observability` | `observability` | `collector-service.yaml` and `netpol.yaml` by name, before the Helm installs; `route.yaml` by name, after them |
| `addons/argocd` | `namespaces` | `namespace.yaml` |
| `addons/argocd` | `argocd` | `namespace.yaml` and `route.yaml` by name, after the Helm install |
| `addons/argocd` | `gitops-bootstrap` | `applications.yaml` — a separate target, run after `apps` |
| `local/data` | `namespaces` | `namespace.yaml` |
| `local/data` | `data` | the whole folder in one `kubectl apply -f`, workloads and `netpol.yaml` together |
| `cluster/` | `tls` | `clusterissuer.yaml`, before the certificate that names it |

## Two folders that cannot be applied in one shot

Both for the same reason: one file in the folder belongs to a later moment
than the rest of it.

| folder | file held back | why |
|---|---|---|
| `addons/observability/` | `route.yaml` | applied after the Helm installs, not before — a route attached to nothing sits orphaned with no error; `collector-service.yaml` and `netpol.yaml` go on *before* the installs so a wrong label selector surfaces in the rollout waits instead of as empty dashboards |
| `addons/argocd/` | `applications.yaml` | applied by `gitops-bootstrap`, after `apps`. Applied earlier, Argo CD creates `dummy`'s objects itself with no `meta.helm.sh` ownership, and `helm upgrade --install dummy` then aborts with `invalid ownership metadata … missing key "app.kubernetes.io/managed-by"` — observed, not theoretical |

## The one folder that breaks the rule

`addons/traefik/` has no `namespace.yaml`. The `traefik` namespace is created
by `helm --create-namespace` inside the `traefik` target, not by anything in
this tree. The folder is still one delete unit, but it cannot restore
itself — `make traefik` has to run before anything in `addons/traefik/` can be
applied.

## `_archive/`

`gateway.istio-old.yaml` — the Istio Gateway this replaced. Kept to diff
against the Traefik Gateway, not to be reapplied.
