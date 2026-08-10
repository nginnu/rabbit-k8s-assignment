# Argo Rollouts canary on order-svc

**Date:** 2026-08-10
**Status:** happy path proven; bad-release rollback observed mid-note (see below) — TODO box stays unticked, no NetworkPolicy-style repeat run yet

---

## Order matters

| # | Piece | Why |
|---|---|---|
| 1 | `make rollouts` | controller has to exist before a Rollout object means anything |
| 2 | `workloadKind: Rollout` in values | the library chart's `_deployment-rollout.tpl` already branches on this — no template change needed |
| 3 | k6 load, started first | analysis window needs traffic already flowing before `initialDelay` starts counting |
| 4 | `make apps` | ships the pod-template change that triggers the canary |

---

## Setup

| Component | Version |
|---|---|
| argo-rollouts controller | chart 2.41.1, app v1.9.1 |
| kubectl-argo-rollouts plugin | v1.9.1 |
| k6 | v1.3.0 |

Strategy (`charts/apps/order-svc/values.yaml`): `setWeight 50` → `analysis` →
`setWeight 100`. 2 replicas, no Istio traffic split, so 50% is the only weight
the cluster can produce — asking for 10% would still land 50/50 and the
analysis would be grading a lie.

Analysis query: success rate over a 2m window, `sum(rate(...status!~"5.."))
/ sum(rate(...))`, `interval: 30s`, `count: 4`, `initialDelay: 30s`,
`successCondition: len(result) > 0 && !isNaN(result[0]) && result[0] >= 0.95`,
`failureLimit: 1`, `consecutiveErrorLimit: 2`.

---

## Triggering a canary

Image tag change does **not** work: `pullPolicy: Never`, kind nodes never see
a tag that was not `kind load`ed. Used `FAIL_RATE` env var on order-svc
instead — a pod-template diff that both triggers the canary step and doubles
as the bad-release switch (0 = healthy build, 0.5 = half its business
requests 500).

```
terminal 1   BASE=https://localhost k6 run tests/order-svc-canary-load.js   (5 req/s, 4m)
terminal 2   make apps
terminal 3   kubectl argo rollouts get rollout order-svc -n demo --watch
```

5 req/s, not fewer: the analysis reads a 2m rate() window; at ~10 requests in
that window one stray 5xx alone is 9/10 = 0.90, under the 0.95 line, on a
build with no real problem. `N >= 20` clears one flaky request; 5 req/s gives
~600 in any 2m window k6 covers.

---

## Result — good release (FAIL_RATE unchanged, revision 2)

```
revision 1  →  first revision, canary skipped, straight to 100%   (nothing to compare against)
revision 2  →  Step 1/3  SetWeight 50   ActualWeight 50   (1 canary pod + 1 stable pod)
               AnalysisRun  ✔ Successful  ✔ 4/4
               Step 3/3  SetWeight 100   Status ✔ Healthy
               revision 1 ReplicaSet ScaledDown
```

k6: 550 iterations at exactly 5.00/s, 0 interrupted.

## Result — bad release (FAIL_RATE=0.5, revision 3)

```
kubectl argo rollouts get rollout order-svc -n demo

Status:   ✖ Degraded
Message:  RolloutAborted: Rollout aborted update to revision 3: Step-based
          analysis phase error/failed: Metric "success-rate" assessed Failed
          due to failed (2) > failureLimit (1)
Step:     0/3   SetWeight: 0   ActualWeight: 0

revision 3   canary ReplicaSet ScaledDown, AnalysisRun ✖ Failed  ✖ 2
revision 2   stable ReplicaSet ✔ Healthy — both pods still on the last good build
```

AnalysisRun measurements: `0.8359891061216561`, then `0.8071591158771856` —
two consecutive readings under the 0.95 line, `failureLimit: 1` tripped after
the second, canary scaled down, traffic never left the stable ReplicaSet.

---

## Traps

| # | Trap | Cost |
|---|---|---|
| 1 | `kubectl wait --for=jsonpath='{...}'` with no `=<value>` | rejected at arg-parse, never touches the cluster — broke `make up` from scratch, moved detection into tests |
| 2 | Argo Rollouts does not adopt an existing Deployment with the same selector | `kubectl delete deployment order-svc -n demo --cascade=orphan` required first; a plain delete drops both pods |
| 3 | setWeight must match what the cluster can produce | at replicas 2, no Istio split, 10% would still be graded as 50/50 |
| 4 | no traffic during analysis → `NaN` (0/0), not an empty vector; an empty vector throws inside `expr` and scores as a provider Error, not a failure | both guards in `successCondition` are load-bearing |
| 5 | no Istio → no canary-only label | the query measures the service as a whole — blast radius, not canary isolation |

---

## Not yet done

- No repeat of the bad-release run captured with fresh timestamps in this
  note beyond the one above — worth re-running once section 6 verification
  is written up formally, so the TODO box has its own dedicated proof rather
  than borrowing this note's.
- Rollback here means "stopped advancing, stable stays serving" — nothing in
  this stage removes the failed ReplicaSet or resets `FAIL_RATE`; that is a
  manual follow-up (`kubectl argo rollouts abort` / undo + values edit), not
  something the canary does on its own.
