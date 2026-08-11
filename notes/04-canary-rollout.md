# Argo Rollouts canary on order-svc

**Date:** 2026-08-10
**Status:** happy path + bad-release rollback both observed (see below)

---

## Setup

| Component | Version |
|---|---|
| argo-rollouts controller | chart 2.41.1, app v1.9.1 |
| kubectl-argo-rollouts plugin | v1.9.1 |
| k6 | v1.3.0 |

Strategy: `setWeight 50` → `analysis` → `setWeight 100`. 2 replicas, no Istio
traffic split, so 50% is the only weight the cluster can produce — asking for
10% would still land 50/50 and the analysis would be grading a lie.

Analysis: success rate over a 2m window, `successCondition: result[0] >= 0.95`,
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

5 req/s, not fewer: the analysis reads a 2m rate() window; at low volume one
stray 5xx alone can drop below the 0.95 line on a build with no real problem.

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

![kubectl argo rollouts get rollout (Degraded, revision 3 aborted) beside the k6 run that tripped it — 91.85% success, 8.15% failed, both thresholds crossed](screenshot/argorollout/Screenshot%202569-08-10%20at%2013.49.21.png)

Two consecutive AnalysisRun readings under the 0.95 line tripped
`failureLimit: 1`; canary scaled down, traffic never left the stable
ReplicaSet. Rollback here means "stopped advancing, stable stays serving" —
nothing resets `FAIL_RATE`; that's a manual follow-up.
