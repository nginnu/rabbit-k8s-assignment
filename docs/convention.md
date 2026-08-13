# Convention

How this system is structured, and the reason for each rule — read before
designing the next piece, not while running the thing. Working model (who
owns what, the execution gate, commit rules) is [`CLAUDE.md`](../CLAUDE.md);
this file does not repeat it.

---

## 1. Namespaces

Current set: `web` (frontend) · `api` (Go services, mock-payment, dummy) ·
`data` · `traefik` · `cert-manager` · `observability` · `argocd` ·
`argo-rollouts`.

**Decision rule (this project's judgement, not upstream doctrine):** split a
namespace when at least one is true —

- a different party holds the rights to it
- a policy is written per-namespace rather than per-pod
- it is deleted on a different cycle

Being a different tier on its own is not a reason. `web` and `api` split
because a NetworkPolicy `podSelector` only ever matches inside its own
namespace — with the frontend and the services together, "the storefront may
not reach MariaDB" cannot be written without naming pods one by one; apart, it
is the default. See the comment block in
[`01-namespaces.yaml`](../platform/manifests/01-namespaces.yaml).

Upstream, for when a split looks tempting and isn't earning its keep:

> "It is not necessary to use multiple namespaces to separate slightly
> different resources, such as different versions of the same software: use
> labels to distinguish resources within the same namespace." — [Kubernetes:
> Namespaces](https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/)

> "Namespaces are intended for use in environments with many users spread
> across multiple teams, or projects." — same page

> "Many Kubernetes security policies are scoped to namespaces. For example,
> RBAC Roles and Network Policies are namespace-scoped resources." —
> [Kubernetes: Multi-tenancy](https://kubernetes.io/docs/concepts/security/multi-tenancy/)

### What actually reads a namespace name, ranked by how quietly it fails

| Mechanism | How it fails if the namespace is wrong | What breaks |
|---|---|---|
| NetworkPolicy peer (`namespaceSelector`) | Silent — renders a valid policy that matches no pod | Traffic dropped, nothing in the object says why |
| Gateway `certificateRefs` | Half silent — resolves same-namespace only, needs a `ReferenceGrant` to cross | Listener resolves no cert, `:443` answers with the controller's self-signed default |
| HTTPRoute `parentRefs` + listener `allowedRoutes.namespaces` | Half silent — shows in Gateway status if you look | Route stays unattached, no error at the browser |
| Secret | Loud | Pod does not start |
| Service DNS (short name vs FQDN) | Loud from the wrong namespace | `no such host` |
| Namespace delete | Loud, but blast radius is everything inside | Deleting `data` takes MariaDB and Redis with it |
| Sidecar/injection labels | Per-namespace, silent if forgotten | Pod runs unmeshed with no error |

### How namespaces get created here — three ways, not one

| Namespace | Created by | Why not the other way |
|---|---|---|
| `web`, `api`, `data` | [`01-namespaces.yaml`](../platform/manifests/01-namespaces.yaml), first manifest applied | Nothing else needs to exist before these do |
| `observability`, `argocd` | Declared inline in the feature manifest that needs it (`07-observability.yaml`, `09-argocd-route.yaml`) | `01` runs long before Argo CD or the observability stack is installed; the route has to land in a namespace that doesn't exist yet at `01` time |
| `traefik`, `cert-manager`, `argo-rollouts` | `helm --create-namespace` | The chart is the only thing that ever needs the namespace to exist, so there's no reason to declare it earlier and have it sit empty |

`argocd` is a case where two of these overlap: it's declared in
`09-argocd-route.yaml` *and* `helm --create-namespace` creates it too.
`--create-namespace` only creates if missing and does not own it, so whichever
runs first wins and neither conflicts — see the comment in
[`09-argocd-route.yaml`](../platform/manifests/09-argocd-route.yaml).

The label `app.kubernetes.io/part-of: platform` on the hand-written
namespaces is **not** load-bearing. NetworkPolicy selects on
`kubernetes.io/metadata.name`, which the API server sets on every namespace —
including `traefik`, which `helm --create-namespace` creates with no labels
at all. See [`_networkpolicy.tpl`](../charts/platform-service/templates/_networkpolicy.tpl).

---

## 2. Charts

- Templates live only in [`charts/platform-service/`](../charts/platform-service).
  A chart under `charts/apps/<svc>/` is `values.yaml` and nothing else.
- One chart → one release → one rollback unit. Two services never share a
  release, because the point of the split is rolling one back without its
  siblings.

`namespace` is `required` with no default —
[`_helpers.tpl`](../charts/platform-service/templates/_helpers.tpl):

```
{{- define "platform-service.namespace" -}}
{{ required "namespace is required in charts/apps/<svc>/values.yaml — ..." .Values.namespace }}
{{- end -}}
```

| Choice | What breaks if reversed |
|---|---|
| No default namespace | A default would make a values file that forgot the key render a complete, healthy-looking release into whichever namespace the default named — surfaces several targets later as objects nobody can find |
| Chart's own `namespace:` key is the source of truth for `-n` (read back by [`chart-namespace.sh`](../platform/scripts/chart-namespace.sh)) | A second list in the Makefile drifts the first time a service moves namespace and only one of the two files gets edited. Worse: if `-n` and the rendered `metadata.namespace` disagree, `helm list -n api` cannot see the release and `helm uninstall` will not clean it up, while the pods come up fine and nothing reports it |

---

## 3. Ingress and routing

Gateway API is the portable layer; the controller underneath is swappable —
proved by replacing Istio with Traefik without changing a single route's URL.
The old Istio Gateway is kept at
[`03-gateway.yaml.istio-old`](../platform/manifests/03-gateway.yaml.istio-old)
for the comparison; it is not applied.

### Three port layers, and why they don't collapse to one

| Layer | Value | Source |
|---|---|---|
| Host | 80 / 443 | kind maps these onto the control-plane node ([`platform/kind/cluster.yaml`](../platform/kind/cluster.yaml)) |
| Container | 8000 / 8443 | [`traefik/local/values.yaml`](../platform/addons/traefik/local/values.yaml) `ports.web/websecure.hostPort` |
| Gateway listener | must name the containerPort, not the host port | same file, `gateway.listeners` |

Reason: the Traefik pod runs `runAsUser: 65532` with
`capabilities: drop: [ALL]` (chart default,
`helm show values traefik/traefik`), so it cannot bind below 1024. Writing
80/443 into the listener fails the render outright ("port is not declared in
`ports`") rather than producing a listener that never binds — see the
comment in the values file.

A Gateway resolves `certificateRefs` from its own namespace only, unless a
`ReferenceGrant` exists for the cross-namespace case. That's why
`platform-tls` is issued into `traefik`, not into a namespace of its own —
see [`02-certificates.yaml`](../platform/manifests/02-certificates.yaml).
The same rule applies the other way for `backendRefs`: an HTTPRoute's
backend has to be in the route's own namespace or it needs a
`ReferenceGrant`, and without one the route attaches (Gateway still reports
`Accepted`) and answers 500 — see the comment in
[`04-routes.yaml`](../platform/manifests/04-routes.yaml).

Traefik's Gateway API conformance — checked before relying on
`HTTPRoutePathRewrite` / `HTTPRouteHostRewrite`:
[report, v1.3.0](https://github.com/kubernetes-sigs/gateway-api/tree/main/conformance/reports/v1.3.0/traefik-traefik),
extended: 13 passed / 0 failed.

---

## 4. NetworkPolicy

Peers in `networkPolicyPeers.ingress` / `.egress` are names
(`gateway`, `dns`, `alloy`, `mariadb`, `redis`, `svc:<name>`), not raw
selectors. See the reasoning already written in
[`_networkpolicy.tpl`](../charts/platform-service/templates/_networkpolicy.tpl):
every tier labels itself differently, so a values file holding raw selectors
would repeat four label conventions across six charts, and a wrong selector
doesn't fail — it renders a valid policy that matches no pod, and the
traffic is dropped with nothing in the object to look at.

The other trap the same file calls out: inside one `from` entry, a
`namespaceSelector` and a `podSelector` at the same list-item level mean AND
(those pods, in that namespace); split across two list entries they mean OR
(all pods in that namespace, or those pods anywhere). One dash is the
difference between reaching `mariadb` and reaching every pod in `data`.

- DNS is injected into every service's egress list, never declared per
  service — a values file that forgot it would produce a pod that can't
  resolve its own database and reports it as a connection failure, not a DNS
  failure.
- `svc:<name>` peers are namespace-local by construction — there is no
  cross-namespace form of that peer.

### Enforcement depends on the CNI — not confirmed here

| Cluster | What the upstream doc says |
|---|---|
| GKE | "If no network plugin is configured, network policies aren't enforced. If you don't use a network plugin to enforce network policies, we recommend removing all network policies from the cluster." — [GKE network policy](https://cloud.google.com/kubernetes-engine/docs/how-to/network-policy) |
| EKS | Requires VPC CNI 1.21.0+, EC2 Linux nodes only, not Fargate — [EKS network policy](https://docs.aws.amazon.com/eks/latest/userguide/cni-network-policy.html) |

The objects here were written and tested against kind's CNI. Whether they'd
enforce on a real EKS cluster was **not checked** — it depends on the add-on
version at cluster-create time, which this repo does not control. The reason
this matters more than most gaps: the objects apply cleanly and
`kubectl get netpol` looks correct either way, so a passing `test-resilience`
run proves nothing about enforcement on a CNI that was never verified.

---

## 5. Images and versions

- Built locally, `kind load`ed into the cluster, `pullPolicy: Never`. No
  registry — there is nowhere else for an image to come from.
- [`check-image-tags.sh`](../platform/scripts/check-image-tags.sh) exists
  because the alternative is silent: the release installs, the Deployment is
  created, and the pod sits in `ErrImageNeverPull` — several `make` targets
  after the actual mistake, since `pullPolicy: Never` means the kubelet
  never even tries the pull and never logs why.
- Upstream charts are pinned in the Makefile (`TRAEFIK_VERSION`,
  `LOKI_VERSION`, …) and `check-version.sh` checks what's pinned against
  what's **running**. Chart version and app version are different strings —
  `TRAEFIK_VERSION := 41.2.0` is the chart; `TRAEFIK_APP_VERSION := v3.7.10`
  is the proxy image tag actually pinned by `--version`, and it's the only
  one of the two observable from a running cluster (the chart version isn't
  in any object). A drift check against the wrong string would pass while
  the wrong proxy image ran.

---

## 6. Secrets

Generated into the cluster by `make secrets` / `make app-secrets`, never
committed. Created once, guarded by an existence check — regenerating would
rotate the password out from under a database that still has the old one.

---

## 7. Environments and portability

`platform/addons/` does not use one directory shape today —
[`traefik/local/values.yaml`](../platform/addons/traefik/local/values.yaml)
and `istio/local/values.yaml` sit under `<addon>/local/`, while
[`argocd/values/argocd.yaml`](../platform/addons/argocd/values) and the
observability stack sit under `<addon>/values/<name>.yaml`. That's an
inconsistency this repo has today, not a rule — noted here so it isn't
mistaken for a convention when the next addon is added.

### What's environment-specific in the Traefik values vs what any environment would share

| Field | Local-only | Any environment |
|---|---|---|
| `deployment.replicas: 1` | yes — tied to kind's single ingress-tainted node | |
| `nodeSelector` / `tolerations` (`ingress-ready`) | yes — kind-specific taint | |
| `service.spec.type: ClusterIP` | yes — no cloud LB controller on kind | |
| `ports.*.hostPort` | yes — traffic arrives on the node's hostPort, not a Service | |
| `gateway.listeners.*.statusAddress.hostname: localhost` | yes | |
| `providers.kubernetesGateway.enabled`, `kubernetesIngress`/`kubernetesCRD` disabled | | yes — Gateway API is the only routing mechanism regardless of cluster |
| `gateway.name`, listener names/ports (container-side), `certificateRefs` | | yes — these describe the route topology, not the node |

### What a move to EKS/GKE would need — none of this exists yet

- An infra layer this repo does not have: `platform/infra/<cloud>/`.
- A real registry and `pullPolicy` other than `Never` — `kind load` has no
  cloud equivalent.
- `service.type: LoadBalancer` in place of `hostPort` — there is a cloud LB
  controller to fill in the address that kind leaves `<pending>` forever.
- ACME in place of the mkcert `ClusterIssuer` — mkcert's CA is only trusted
  on the machine that generated it.
- Object storage for Loki/Tempo in place of whatever local path they use now.
- `make up` splitting into an infra stage and a deploy stage, because
  provisioning a cluster and deploying onto one don't share a lifecycle —
  tearing down a deploy should not touch the infra under it.

One piece already ports without change: the MariaDB PVC in
[`05-mariadb.yaml`](../platform/manifests/05-mariadb.yaml) sets no
`storageClassName`, so it takes the cluster default — `local-path` on kind,
`pd-balanced` on GKE — with nothing to edit at the manifest.

---

## 8. Comments

Comments explain the failure that would happen without the line, not what
the line does. Full rule and example: [`CLAUDE.md`](../CLAUDE.md#comments-in-makefile--manifests--scripts).
