#!/usr/bin/env bash
#
# Proves the mesh is on the workload, not just that istiod exists: every api
# service pod carries a ready istio-proxy, a request through the edge shows up
# in a sidecar's access log, and east-west mTLS is declared rather than
# assumed.
#
# STRICT is mesh-wide (notes/10-strict-mesh.md): every meshed service must
# refuse plaintext on its app port, and the only plaintext left in the
# cluster is Traefik -> istio-ingressgateway:80, kept alive by one
# port-level exception. Both halves are proven below — the refusal on the
# wire, and the edge still answering through the exception.
#
# The full request journey (checkout, auth, observability) is proven by the
# other suites; this file only proves the mesh layer.
#
#   ./tests/istio.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

ISTIO_NS="${ISTIO_NS:-istio-system}"
WEB_NS="${WEB_NS:-web}"

section "istiod is up"

if kubectl -n "$ISTIO_NS" get deployment istiod >/dev/null 2>&1; then
  ok "deployment/istiod exists"
else
  bad "deployment/istiod not found — run 'make istio' first"
fi

if kubectl -n "$ISTIO_NS" rollout status deployment istiod --timeout=60s >/dev/null 2>&1; then
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
for svc in auth catalog order payment; do
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

# STRICT on the servers is half the contract; the other half is each caller
# namespace naming ISTIO_MUTUAL, so the wire does not depend on auto-mTLS
# defaults. exportTo "*" is part of the assertion because the callers live
# outside the rules' own namespaces — web-ui (web) calls *.api, the gateway
# (istio-system) calls both — and a rule nobody can see protects nobody.
for ns in "$API_NS" "$WEB_NS"; do
  mode=$(kubectl -n "$ns" get destinationrule east-west-mtls \
    -o jsonpath='{.spec.trafficPolicy.tls.mode}' 2>/dev/null)
  vis=$(kubectl -n "$ns" get destinationrule east-west-mtls \
    -o jsonpath='{.spec.exportTo[0]}' 2>/dev/null)
  if [ "$mode" = "ISTIO_MUTUAL" ] && [ "$vis" = "*" ]; then
    ok "destinationrule/east-west-mtls in $ns — ISTIO_MUTUAL, visible to every namespace"
  else
    bad "destinationrule/east-west-mtls in $ns missing, not ISTIO_MUTUAL, or not exportTo * — run 'make istio', which applies the manifest"
  fi
done

section "each service carries its own mesh identity"

seen=""
for svc in auth catalog order payment notification; do
  sa=$(kubectl -n "$API_NS" get pod -l app.kubernetes.io/name="$svc" \
    -o jsonpath='{.items[0].spec.serviceAccountName}' 2>/dev/null)
  case "$sa" in
    "")
      bad "$svc — no pod, or no serviceAccountName on it" ;;
    default)
      bad "$svc — runs as sa/default; its certificate is spiffe://cluster.local/ns/$API_NS/sa/default, shared with every sibling, so no AuthorizationPolicy can name it" ;;
    *)
      case " $seen " in
        *" $sa "*)
          bad "$svc — shares serviceAccount $sa with another service; two workloads, one identity" ;;
        *)
          ok "$svc — sa/$sa"
          seen="$seen $sa" ;;
      esac ;;
  esac
done

pod=$(kubectl -n "$API_NS" get pod -l app.kubernetes.io/name=payment \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -z "$pod" ]; then
  bad "no payment pod — cannot read the certificate the sidecar was issued"
else
  uri=$(kubectl -n "$API_NS" exec "$pod" -c istio-proxy -- \
    pilot-agent request GET certs 2>/dev/null | grep -om1 'spiffe://[^"]*')
  case "$uri" in
    "")
      bad "payment — could not read a SPIFFE id from the sidecar certificate" ;;
    */sa/payment)
      ok "payment sidecar certificate is $uri" ;;
    *)
      bad "payment sidecar certificate is $uri — expected .../sa/payment; the pod predates the ServiceAccount change and has not rolled" ;;
  esac
fi

section "mesh-wide STRICT, one plaintext exception"

# The rule shape first, then the behaviour it produces. The mesh-level rule
# carries no selector, so it reaches every sidecar in the mesh; the gateway
# exception opens port 80 only — the hop where Traefik's plaintext arrives.
mode=$(kubectl -n "$ISTIO_NS" get peerauthentication default-strict \
  -o jsonpath='{.spec.mtls.mode}' 2>/dev/null)
sel=$(kubectl -n "$ISTIO_NS" get peerauthentication default-strict \
  -o jsonpath='{.spec.selector}' 2>/dev/null)
if [ "$mode" = "STRICT" ] && [ -z "$sel" ]; then
  ok "peerauthentication/default-strict is selector-less STRICT — mesh-wide"
else
  bad "peerauthentication/default-strict missing, not STRICT, or carrying a selector — run 'make istio'"
fi

port80=$(kubectl -n "$ISTIO_NS" get peerauthentication ingress-gateway-plain-80 \
  -o json 2>/dev/null \
  | json 'd["spec"].get("portLevelMtls", {}).get("80", {}).get("mode", "absent")')
gwl=$(kubectl -n "$ISTIO_NS" get peerauthentication ingress-gateway-plain-80 \
  -o jsonpath='{.spec.selector.matchLabels.istio}' 2>/dev/null)
if [ "$port80" = "DISABLE" ] && [ "$gwl" = "ingressgateway" ]; then
  ok "peerauthentication/ingress-gateway-plain-80 disables mTLS on the gateway's port 80 only"
else
  bad "peerauthentication/ingress-gateway-plain-80 missing or not a port-80 DISABLE on the gateway — run 'make istio'"
fi

# The superseded workload rules must be gone: a leftover payment/notification
# STRICT would still work, but a stale half-description of the mesh is what
# the next change gets designed against.
leftover=$(kubectl -n "$API_NS" get peerauthentication payment notification \
  -o name 2>/dev/null)
if [ -z "$leftover" ]; then
  ok "no workload-scoped PeerAuthentication left in $API_NS — superseded, not duplicated"
else
  bad "leftover workload PeerAuthentication: $leftover — run 'make istio', which deletes them"
fi

# The exception exists to keep the edge alive; if it stopped matching, the
# storefront would die with it. Same probe routing.sh uses, restated here so
# the mesh layer proves its own blast radius.
expect "storefront / still answers 200 through Traefik -> gateway:80 -> web-ui" 200 "$(status GET /)"

section "every meshed service refuses plaintext on its app port"

# STRICT as behaviour, not declaration: the proxy's inbound config for the
# app port must have no raw_buffer (plaintext) filter chain left. All six
# meshed services, not just payment and notification — that pair led the
# migration (notes/09), the rest moved with the mesh-wide rule (notes/10).
for entry in "$API_NS auth 9001" "$API_NS catalog 9004" "$API_NS order 9002" \
             "$API_NS payment 7000" "$API_NS notification 8080" "$WEB_NS web-ui 3000"; do
  set -- $entry
  ns="$1"; svc="$2"; port="$3"

  pod=$(kubectl -n "$ns" get pod -l app.kubernetes.io/name="$svc" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  if [ -z "$pod" ]; then
    bad "$svc — no pod to read the inbound listener from"
    continue
  fi

  plain=$(kubectl -n "$ns" exec "$pod" -c istio-proxy -- \
    pilot-agent request GET config_dump 2>/dev/null \
    | PORT="$port" json '
sum(1
    for c in d["configs"] if c["@type"].endswith("ListenersConfigDump")
    for l in c.get("dynamic_listeners", [])
    for fc in l.get("active_state", {}).get("listener", {}).get("filter_chains", [])
    if fc.get("filter_chain_match", {}).get("destination_port") == int(__import__("os").environ["PORT"])
    and fc.get("filter_chain_match", {}).get("transport_protocol") == "raw_buffer")')

  case "$plain" in
    0) ok "$svc :$port has no raw_buffer filter chain — a plaintext connection is rejected by the proxy" ;;
    "") bad "$svc — could not read the inbound listener config" ;;
    *)  bad "$svc :$port still has $plain raw_buffer filter chain(s) — the proxy accepts plaintext, STRICT has not taken effect" ;;
  esac
done

section "a plaintext connection on the wire is actually refused"

# The config above says the proxy should refuse; this proves it does. The
# probe curls a pod IP from inside a sidecar's istio-proxy container: traffic
# from the proxy's own UID is exempt from the iptables redirect, so the
# request leaves as real plaintext and lands on the destination's inbound
# listener. The senders are chosen for what NetworkPolicy lets through —
# order reaches payment and notification, web-ui reaches auth, catalog and
# order — so a reset means STRICT refused the connection, not the netpol.
refuses_plaintext() { # <sender-ns> <sender-svc> <dst-ns> <svc> <port>
  sender_ns="$1" sender_svc="$2" dst_ns="$3" svc="$4" port="$5"

  sender=$(kubectl -n "$sender_ns" get pod -l app.kubernetes.io/name="$sender_svc" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
  if [ -z "$sender" ]; then
    bad "$sender_svc — no pod to send the plaintext probe from"
    return
  fi

  ip=$(kubectl -n "$dst_ns" get pod -l app.kubernetes.io/name="$svc" \
    -o jsonpath='{.items[0].status.podIP}' 2>/dev/null)
  if [ -z "$ip" ]; then
    bad "$svc — no pod IP to connect to"
    return
  fi

  kubectl -n "$sender_ns" exec "$sender" -c istio-proxy -- \
    curl -sS -o /dev/null --max-time 8 "http://$ip:$port/healthz" >/dev/null 2>&1
  rc=$?

  case "$rc" in
    56|52|35)
      ok "$svc :$port reset the plaintext connection (curl $rc) — STRICT is enforced on the wire" ;;
    0)
      bad "$svc :$port answered a plaintext request — STRICT is declared but not enforced" ;;
    28)
      bad "$svc :$port timed out — the packet never arrived, so this proves NetworkPolicy, not STRICT" ;;
    *)
      bad "$svc :$port — curl exited $rc, cannot tell whether the proxy refused" ;;
  esac
}

refuses_plaintext "$API_NS" order "$API_NS" payment      7000
refuses_plaintext "$API_NS" order "$API_NS" notification 8080
refuses_plaintext "$WEB_NS" web-ui "$API_NS" auth        9001
refuses_plaintext "$WEB_NS" web-ui "$API_NS" catalog     9004
refuses_plaintext "$WEB_NS" web-ui "$API_NS" order       9002

section "web-ui is in the mesh, so nothing reaches api unencrypted"

pods=$(kubectl -n "$WEB_NS" get pods -l app.kubernetes.io/name=web-ui \
  -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)

if [ -z "$pods" ]; then
  bad "no web-ui pod found in $WEB_NS"
else
  for pod in $pods; do
    verdict=$(kubectl -n "$WEB_NS" get pod "$pod" -o json 2>/dev/null \
      | json 'next((("ready" if c["ready"] else "not-ready") for c in (d["status"].get("containerStatuses") or []) + (d["status"].get("initContainerStatuses") or []) if c["name"] == "istio-proxy"), "absent")')
    case "$verdict" in
      ready)     ok "$pod — istio-proxy present and ready" ;;
      not-ready) bad "$pod — istio-proxy not ready; first suspect is the NetworkPolicy egress to istiod:15012" ;;
      absent)    bad "$pod — no istio-proxy; mesh: true is not in charts/web-ui/values.yaml, or Argo CD has not synced it" ;;
      *)         bad "$pod — could not read pod status" ;;
    esac
  done

  first=$(printf '%s\n' "$pods" | awk '{print $1}')
  sa=$(kubectl -n "$WEB_NS" get pod "$first" -o jsonpath='{.spec.serviceAccountName}' 2>/dev/null)
  expect "web-ui runs under its own ServiceAccount" "web-ui" "$sa"

  # Under mesh-wide STRICT a plaintext request cannot land at all, so this
  # delta check is now the early-warning tripwire: a counter that moves means
  # some path stopped speaking mTLS (a lost sidecar, a rule that stopped
  # matching) before anything else notices.
  #
  # A delta, not a presence check: istio_requests_total never resets, so the
  # plaintext requests catalog served before web-ui was meshed stay in the
  # counter for the life of the pod. Asserting that "none" is absent fails
  # forever on a cluster that has already been fixed.
  cat_pod=$(kubectl -n "$API_NS" get pod -l app.kubernetes.io/name=catalog \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

  plain_count() {
    kubectl -n "$API_NS" exec "$cat_pod" -c istio-proxy -- \
      pilot-agent request GET stats/prometheus 2>/dev/null \
      | grep -E '^istio_requests_total.*reporter="destination".*connection_security_policy="none"' \
      | sed -E 's/.*\} ([0-9.]+)$/\1/' \
      | awk '{n += $1} END {printf "%d", n + 0}'
  }

  if [ -z "$cat_pod" ]; then
    bad "no catalog pod — cannot tell whether web-ui still reaches it in plaintext"
  else
    before=$(plain_count)
    for _ in 1 2 3 4 5; do
      curl -sS -o /dev/null --max-time 15 "$BASE/api/products" 2>/dev/null
    done
    after=$(plain_count)

    if [ "$after" = "$before" ]; then
      ok "catalog served five storefront requests without one plaintext connection — none stayed at $before"
    else
      bad "catalog's plaintext counter rose $before -> $after — web-ui is still calling it unencrypted"
    fi
  fi
fi

summary
