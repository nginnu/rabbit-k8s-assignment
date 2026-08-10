# ArgoCD — auto-sync and selfHeal on dummy

**Date:** 2026-08-10
**Status:** two cases proven (auto-sync, selfHeal); dummy only — TODO box stays
unticked, App of Apps and the other five services not started

---

## Setup

| Component | Version |
|---|---|
| argo-cd chart | 10.3.2, app v3.5.0 |

**Reached through the Gateway, not port-forward** — `https://localhost/argocd`

- `server.insecure`, `server.basehref`, `server.rootpath` in
  `platform/addons/argocd/values/argocd.yaml` make Argo CD serve itself at
  `/argocd` instead of `/`
- `09-argocd-route.yaml` carries **no** URLRewrite filter, unlike the Grafana
  route — rewriting to `/` would leave the server looking for `/argocd` in a
  request that no longer has it

**Scope — one Application** (`platform/manifests/10-argocd-apps.yaml`)

- `dummy` only, pointed at `charts/apps/dummy` on branch `main`
- `automated.prune: true`, `automated.selfHeal: true`
- nothing calls dummy, so a bad sync takes out a workload nothing depends on
- the other five services stay on `make apps` until this one proves the loop

**Adoption from Helm**

- `dummy` was already a Helm release from `make apps` before ArgoCD existed
- `ServerSideApply=true` let ArgoCD adopt the objects instead of failing on
  `meta.helm.sh` ownership annotations
- pods kept their prior AGE across the adoption — no restart

**Poll interval — up to ~3 minutes**, no webhook configured. From `argocd-cm`:

```
timeout.hard.reconciliation: 0s
timeout.reconciliation:      120s
timeout.reconciliation.jitter: 60s
```

![Applications list — one app, dummy, Healthy and Synced](screenshot/argocd/Screenshot%202569-08-10%20at%2016.06.32.png)

![ArgoCD resource tree before the push — Synced to 0ba6d79, two pods](screenshot/argocd/Screenshot%202569-08-10%20at%2016.02.36.png)

---

## Case 1 — auto-sync: git reaches the cluster without make

| Step | Detail |
|---|---|
| Edit | `replicas: 2 → 5` in `charts/apps/dummy/values.yaml` |
| Commit | `d075d36` |
| Push | `https://github.com/nginnu/rabbit-k8s-assignment`, branch `main` |
| Ran afterwards | nothing — no `make`, no `helm`, no `kubectl apply` |

```
kubectl -n demo get deploy dummy
before   2/2
after    5/5
```

![the commit — only values.yaml staged, replicas 2 to 5](screenshot/argocd/Screenshot%202569-08-10%20at%2015.52.49.png)

- one of five changed files was staged; the other four were the ArgoCD install
  itself, still uncommitted — so the replica count was all ArgoCD saw
- Deployment AGE did not reset: the object was patched, not recreated
- events confirm the same ReplicaSet scaled in place —
  `Scaled up replica set dummy-664f864bfb from 2 to 5`

**Trap — "Synced" said nothing until the push**

- before `git push`, the Application already read `Synced to main (0ba6d79)`,
  the commit *before* the change
- the edit was committed locally but not pushed; ArgoCD polls GitHub, never the
  working tree

![Synced to d075d36 — five pods, three of them a minute old](screenshot/argocd/Screenshot%202569-08-10%20at%2016.06.50.png)

The three pods aged `a minute` next to two aged `5 hours` are the canary-free
version of the same evidence: ArgoCD added replicas to the existing ReplicaSet.

![Deployment going 2/2 to 5/5 with AGE unchanged](screenshot/argocd/Screenshot%202569-08-10%20at%2016.07.31.png)

---

## Case 2 — selfHeal: manual drift pulled back

### 2a — hand-edit a live value

```
kubectl -n demo scale deploy dummy --replicas=1
```

`kubectl -n demo describe deploy dummy` events, verbatim:

```
Normal  ScalingReplicaSet  10m   deployment-controller  Scaled up replica set dummy-664f864bfb from 2 to 5
Normal  ScalingReplicaSet  69s   deployment-controller  Scaled down replica set dummy-664f864bfb from 5 to 1
Normal  ScalingReplicaSet  69s   deployment-controller  Scaled up replica set dummy-664f864bfb from 1 to 5
```

- scale-down and scale-back are both stamped `69s` — the same second
- selfHeal reconciled back to git's `replicas: 5` faster than `kubectl get -w`
  could catch the deployment at `1/1`

### 2b — delete the object outright

```
kubectl -n demo delete svc dummy
```

Polled every 2s:

```
before   dummy   ClusterIP   10.96.13.238   80/TCP   5m18s
delete   service "dummy" deleted
+2s      dummy   ClusterIP   10.96.94.192   80/TCP   1s
+20s     dummy   ClusterIP   10.96.94.192   80/TCP   20s
```

- CLUSTER-IP changed: `10.96.13.238` → `10.96.94.192`
- AGE reset to `1s`

Both together rule out a delete that silently failed — Kubernetes only issues a
new ClusterIP when it creates a new object.

![scale to 1 and the describe events showing 5 to 1 and 1 to 5 in the same second](screenshot/argocd/Screenshot%202569-08-10%20at%2016.17.35.png)

![delete svc dummy, then the Service back with a new CLUSTER-IP at AGE 8s](screenshot/argocd/Screenshot%202569-08-10%20at%2016.22.10.png)

---

## Not yet tested

- prune (removing a template and watching the object disappear)
- rollback via the ArgoCD UI, App of Apps, the remaining five services under
  GitOps
