# ArgoCD — auto-sync and selfHeal on dummy

**Date:** 2026-08-10
**Status:** two cases proven (auto-sync, selfHeal); dummy only — TODO box stays
unticked, App of Apps and the other five services not started

---

## Setup

| Component | Version |
|---|---|
| argo-cd chart | 10.3.2, app v3.5.0 |

Reachable at `https://localhost/argocd` through the Istio Gateway — no
port-forward. `server.insecure`, `server.basehref`, `server.rootpath` all set
in `platform/addons/argocd/values/argocd.yaml` so Argo CD serves itself at
`/argocd` instead of `/`; `09-argocd-route.yaml` deliberately carries no
URLRewrite filter, unlike the Grafana route — rewriting to `/` here would leave
the server looking for `/argocd` under a request that no longer has it.

`platform/manifests/10-argocd-apps.yaml` defines exactly one Application,
`dummy`, pointed at `charts/apps/dummy` on branch `main`, with
`automated.prune: true` and `automated.selfHeal: true`. Nothing calls dummy —
if a sync goes wrong it takes out a workload nothing depends on. The other
five services stay on `make apps` until this one has proven the loop.

`dummy` was already a Helm release from `make apps` before ArgoCD existed.
`ServerSideApply=true` let ArgoCD adopt the existing objects instead of
failing on `meta.helm.sh` ownership annotations — pods kept their prior AGE
across the adoption, no restart.

Poll interval: `timeout.reconciliation: 120s` +
`timeout.reconciliation.jitter: 60s` in `argocd-cm` — up to ~3 minutes with no
webhook configured, confirmed live:

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

Only `charts/apps/dummy/values.yaml` was staged out of five changed files. The
other four were the ArgoCD install itself, still uncommitted at that point —
which is why the only thing ArgoCD saw was the replica count.

Deployment AGE did not reset — the existing object was patched, not recreated.
`kubectl -n demo describe deploy dummy` events confirm the same ReplicaSet
scaled in place: `Scaled up replica set dummy-664f864bfb from 2 to 5`.

**Trap.** Before the push, the Application already reported
`Synced to main (0ba6d79)` — the commit *before* the change. The edit existed
locally but had not been pushed yet; ArgoCD polls GitHub, not the working
tree, so "synced" meant nothing until `git push` actually ran.

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

Scale-down and scale-back are stamped the same second (`69s` both). ArgoCD's
selfHeal reconciled the manual change back to git's `replicas: 5` fast enough
that `kubectl get -w` never caught the deployment sitting at `1/1`.

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

CLUSTER-IP changed (`10.96.13.238` → `10.96.94.192`) and AGE reset to `1s` —
a new object recreated from git, not a delete that silently failed.

![scale to 1 and the describe events showing 5 to 1 and 1 to 5 in the same second](screenshot/argocd/Screenshot%202569-08-10%20at%2016.17.35.png)

![delete svc dummy, then the Service back with a new CLUSTER-IP at AGE 8s](screenshot/argocd/Screenshot%202569-08-10%20at%2016.22.10.png)

---

## Not yet tested

- prune (removing a template and watching the object disappear)
- rollback via the ArgoCD UI, App of Apps, the remaining five services under
  GitOps
