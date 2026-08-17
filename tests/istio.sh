#!/usr/bin/env bash
#
# Proves the mesh is on the workload, not just that istiod exists: every api
# service pod carries a ready istio-proxy, a request through the edge shows up
# in a sidecar's access log, and east-west mTLS is declared rather than
# assumed.
#
# What is deliberately absent: STRICT. Traefik is not in the mesh and its
# plaintext HTTP must keep reaching these pods, so no PeerAuthentication is
# applied and mTLS is asserted client-side by the DestinationRule — see
# notes/09-service-mesh-plan.md.
#
# The full request journey (checkout, auth, observability) is proven by the
# other suites; this file only proves the mesh layer.
#
#   ./tests/istio.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

ISTIO_NS="${ISTIO_NS:-istio-system}"

section "istiod is up"

if kubectl -n "$ISTIO_NS" get deployment istiod >/dev/null 2>&1; then
  ok "deployment/istiod exists"
else
  bad "deployment/istiod not found — run 'make istio' first"
fi

if kubectl -n "$ISTIO_NS" rollout status deployment/istiod --timeout=60s >/dev/null 2>&1; then
  ok "istiod is ready"
else
  bad "istiod is not ready — no sidecar can start or fetch a certificate"
fi

section "every api service pod carries a ready sidecar"

# Four services, deployed by `make apps`, turn meshed when their release rolls.
# notification is the exception: its chart is synced by Argo CD from main, so a
# sidecar appears only after the commit is pushed and Argo syncs — warned
# about below, not failed on, or every fresh clone is red over a push that has
# not happened yet.
for svc in auth catalog order-svc payment-svc payment; do
  pods=$(kubectl -n "$API_NS" get pods -l app.kubernetes.io/name="$svc" \
    -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)
  if [ -z "$pods" ]; then
    bad "$svc — no pods found in $API_NS"
    continue
  fi
  for pod in $pods; do
    # Verdict from python over the whole object, not a jsonpath filter: a
    # filter that fails to evaluate (or matches nothing) prints the empty
    # string, which reads identically to a pod with no sidecar — the first
    # version of this check failed every pod that way, including ones whose
    # access logs proved the proxy was forwarding. json() is lib.sh's helper,
    # the same one every other suite reads JSON through.
    #
    # Both lists, not just containerStatuses: this istiod injects the proxy as
    # a native sidecar — a restartable entry in initContainers, reported in
    # initContainerStatuses — so a containerStatuses-only check reports every
    # meshed pod as unmeshed while kubectl's READY column (which counts it)
    # says 2/2. Cost a long detour to learn; do not pay it twice.
    verdict=$(kubectl -n "$API_NS" get pod "$pod" -o json 2>/dev/null \
      | json 'next((("ready" if c["ready"] else "not-ready") for c in (d["status"].get("containerStatuses") or []) + (d["status"].get("initContainerStatuses") or []) if c["name"] == "istio-proxy"), "absent")')
    case "$verdict" in
      ready)
        ok "$pod — istio-proxy present and ready" ;;
      not-ready)
        bad "$pod — istio-proxy not ready; pod stuck at not-fully-ready. First suspect: the NetworkPolicy egress to istiod:15012" ;;
      absent)
        bad "$pod — no istio-proxy container; the sidecar.istio.io/inject label or the webhook is missing" ;;
      *)
        bad "$pod — could not read pod status" ;;
    esac
  done
done

# notification, warn-only — and reading both container lists for the same
# reason as above: a native sidecar never shows up in spec.containers.
notification_names=$(kubectl -n "$API_NS" get pods -l app.kubernetes.io/name=notification \
  -o jsonpath='{.items[*].spec.initContainers[*].name}{" "}{.items[*].spec.containers[*].name}' 2>/dev/null)
case "$notification_names" in
  *istio-proxy*)
    ok "notification pods carry a sidecar — Argo CD has synced the mesh opt-in" ;;
  "")
    note "no notification pods found in $API_NS" ;;
  *)
    note "notification pods have no sidecar yet — expected until the mesh opt-in commit is pushed and Argo CD syncs; not counted as a failure" ;;
esac

section "the sidecar is on the request path"

# The login probe, same as routing.sh: it needs no token (400 is the answer
# that proves arrival), writes nothing, and its target auth is meshed. If
# the access log shows the request the proxy forwarded it; if not, Traefik is
# reaching the app port past the mesh and the sidecars are decoration.
expect "POST /api/auth/login still answers 400" 400 "$(status POST /api/auth/login '{}')"

# auth runs two replicas, and the request lands on one of them — every
# pod's log has to be read before concluding the mesh was bypassed.
found=""
for pod in $(kubectl -n "$API_NS" get pods -l app.kubernetes.io/name=auth \
  -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
  # The edge rewrites /api/auth to /auth before it reaches the service, so the
  # access log records the rewritten path. Envoy writes access lines in
  # batches on its own schedule, so the grep gets a wide tail and three
  # attempts — reading once after two seconds flakes between runs.
  # grep -c, not -q: -q exits at the first match while kubectl is still
  # writing, the SIGPIPE makes kubectl die with 141, and under lib.sh's
  # pipefail that flips a found match into a failed check. -c reads the whole
  # stream, so the producer always finishes.
  for attempt in 1 2 3; do
    if kubectl -n "$API_NS" logs "$pod" -c istio-proxy --tail=200 2>/dev/null \
      | grep -c '"POST /auth' >/dev/null; then
      found="$pod"
      break 2
    fi
    [ "$attempt" = 3 ] || sleep 3
  done
done

if [ -n "$found" ]; then
  ok "$found istio-proxy access log shows the request — traffic flows through the sidecar"
else
  bad "no auth sidecar logged the request — the mesh is not on the request path"
fi

section "east-west mTLS is declared, not assumed"

# PERMISSIVE negotiates mTLS between sidecars by default; the DestinationRule
# pins that so it survives a default changing. Proving the bytes themselves is
# tls-proof.sh's method applied to an east-west hop — that capture belongs to
# the STRICT follow-up in notes/09, not to this suite.
mode=$(kubectl -n "$API_NS" get destinationrule east-west-mtls \
  -o jsonpath='{.spec.trafficPolicy.tls.mode}' 2>/dev/null)
if [ "$mode" = "ISTIO_MUTUAL" ]; then
  ok "destinationrule/east-west-mtls sets ISTIO_MUTUAL for *.api.svc.cluster.local"
else
  bad "destinationrule/east-west-mtls missing or not ISTIO_MUTUAL — run 'make istio', which applies the manifest"
fi

summary
