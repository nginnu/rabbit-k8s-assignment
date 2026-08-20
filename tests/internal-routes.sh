#!/usr/bin/env bash
#
# Usecase: /internal/orders/:id is really gone, not just unrouted at the
# gateway.
#
# The payment-svc merge deleted this route from order (see
# apps/shop-api/internal/payment/gateway/order_adapter.go — GetOrder and
# UpdateOrderStatus now call the usecase in-process instead of GET/PATCH
# /internal/orders/:id over HTTP). Nothing on the gateway ever routed
# /internal — HTTPRoute only ever matched /api/... prefixes — so hitting this
# path through $BASE would 404 whether or not order's own mux still answers
# it, and that 404 would prove nothing about whether the route was actually
# removed from the service. Only a request that reaches order's mux directly,
# bypassing the gateway, can tell the two apart.
#
#   ./tests/internal-routes.sh
#
# route-isolation.sh proves the gateway routes each path to the right
# service; this proves a path removed from a service's own mux stays removed,
# which the gateway is never in a position to see either way.

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

# order's own port (services.order.port in charts/apps/order/values.yaml),
# not 80: there is no gateway between this exec and order, so the
# Service's own targetPort is what has to be named.
ORDER_PORT="${ORDER_PORT:-9002}"

section "/internal/orders/:id is gone from order's own mux"

# Any api pod with a shell works as the launch point; order's own image ships
# no curl or shell to exec into (see apps/shop-api/Dockerfile — wget is the
# only HTTP client baked in, and there is no shell to pipe a heredoc through
# for the PATCH body). auth is picked because every suite already assumes it
# is up before this one runs (istio.sh, checkout.sh) — no new dependency.
pod=$(kubectl -n "$API_NS" get pods -l app.kubernetes.io/name=auth \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ -z "$pod" ]; then
  bad "no auth pod found in $API_NS — cannot launch the in-cluster request"
  summary
fi
note "exec into $pod ($API_NS) — curling order.$API_NS.svc.cluster.local:$ORDER_PORT directly, gateway bypassed"

# wget, not curl: alpine's runtime image installs wget (ca-certificates
# tzdata wget in the Dockerfile), never curl. --spider still issues a GET
# against the target path (it just discards the body), which is enough to
# read the status line back with -S; a HEAD would answer differently on a
# gin route that only registers GET.
get_code=$(kubectl -n "$API_NS" exec "$pod" -- sh -c \
  "wget -S -O /dev/null http://order.$API_NS.svc.cluster.local:$ORDER_PORT/internal/orders/1 2>&1" \
  | grep -oE 'HTTP/[0-9.]+ [0-9]+' | head -1 | awk '{print $2}')
expect "GET  /internal/orders/1 " 404 "${get_code:-000}"

# --method is a busybox wget extension (present on alpine, not on every
# wget build) — the PATCH leg needs it, since --spider only ever sends GET.
patch_code=$(kubectl -n "$API_NS" exec "$pod" -- sh -c \
  "wget -S -O /dev/null --method=PATCH --body-data='{\"status\":\"paid\"}' --header='Content-Type: application/json' http://order.$API_NS.svc.cluster.local:$ORDER_PORT/internal/orders/1 2>&1" \
  | grep -oE 'HTTP/[0-9.]+ [0-9]+' | head -1 | awk '{print $2}')
expect "PATCH /internal/orders/1 " 404 "${patch_code:-000}"

summary
