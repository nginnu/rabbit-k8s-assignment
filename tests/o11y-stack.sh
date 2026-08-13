#!/usr/bin/env bash
#
# Usecase: the observability stack is assembled and receiving.
#
# Checks the five components are up, that the alias every service resolves has
# endpoints, and that a purchase leaves traces, logs and metrics behind.
#
#   ./tests/o11y-stack.sh
#
# o11y-journey.sh covers the same ground from the other side: it follows one
# customer journey and prints Grafana links for it.

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

OBS_NS="${OBS_NS:-observability}"
ACCOUNT="${ACCOUNT:-alice}"
SECRET="${SECRET:-password}"
PRODUCT="${PRODUCT:-LIV-H-24}"
AMOUNT="${AMOUNT:-3290}"
# Telemetry is batched, so it is not queryable the instant a request returns.
# Waiting is honest; retrying until it appears would hide a pipeline that had
# genuinely stopped.
SETTLE="${SETTLE:-25}"

# Query a component over a port-forward.
#
# Not `kubectl exec ... wget`: none of these images ship wget or curl, and the
# exec fails with "executable file not found" on stderr while stdout stays
# empty — which reads exactly like "the component returned nothing" and had this
# file reporting a working pipeline as broken.
#
#   obs_get <service> <local-port> <path> [service-port]
#
# The service port defaults to the local one, but prometheus-server listens on
# 80 while its API is conventionally reached on 9090.
obs_get() {
  local svc="$1" port="$2" path="$3" svcport="${4:-$2}" out=""
  kubectl -n "$OBS_NS" port-forward "svc/$svc" "$port:$svcport" >/dev/null 2>&1 &
  local pf=$!
  # Wait for the tunnel rather than sleeping a fixed amount.
  for _ in $(seq 1 20); do
    curl -sS --max-time 2 -o /dev/null "http://localhost:$port/" 2>/dev/null && break
    sleep 0.5
  done
  out=$(curl -sS --max-time 15 "http://localhost:$port$path" 2>/dev/null)
  kill "$pf" 2>/dev/null
  wait "$pf" 2>/dev/null
  printf '%s' "$out"
}

section "the stack is up"

# The charts disagree about workload kind: loki and tempo are StatefulSets,
# the rest Deployments.
for entry in deploy/alloy deploy/grafana deploy/prometheus-server \
             statefulset/loki statefulset/tempo; do
  kind="${entry%%/*}"; name="${entry##*/}"
  ready=$(kubectl -n "$OBS_NS" get "$kind" "$name" \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
  expect "$(printf '%-26s' "$entry")" 1 "${ready:-0}"
done

# The applications resolve otel-collector, not the chart's own service name.
# With no endpoints they keep serving and silently record nothing.
eps=$(kubectl -n "$OBS_NS" get endpoints otel-collector \
  -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null)
if [ -n "$eps" ]; then
  ok "otel-collector has endpoints — $eps"
else
  bad "otel-collector has no endpoints — telemetry is being dropped"
  summary
fi

section "buy something to generate telemetry"

login="{\"username\":\"$ACCOUNT\",\"password\":\"$SECRET\"}"
token=$(body POST /api/auth/login "$login" | json "d['token']")
if [ -z "$token" ]; then
  bad "cannot log in"
  summary
fi

order=$(body POST /api/orders "{\"product_id\":\"$PRODUCT\"}" "$token")
order_id=$(printf '%s' "$order" | json "d['id']")
if [ -z "$order_id" ]; then
  bad "cannot place an order"
  summary
fi

# The trace id comes back in a response header, which is what makes a specific
# request findable later rather than searching by time.
headers=$(mktemp)
curl -sS -o /dev/null -D "$headers" --max-time 15 \
  -X POST "$BASE/api/payments" \
  -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d "{\"order_id\":$order_id,\"amount\":$AMOUNT}" 2>/dev/null
trace=$(grep -i '^traceresponse:' "$headers" | tr -d '\r' | sed 's/.*: *//' | cut -d- -f2)
rm -f "$headers"

if [ -n "$trace" ]; then
  ok "order $order_id paid, trace $trace"
else
  bad "no traceresponse header — the middleware is not returning the trace id"
  summary
fi

note "waiting ${SETTLE}s for telemetry to land"
sleep "$SETTLE"

section "traces reached tempo"

trace_json=$(obs_get tempo 3200 "/api/traces/$trace")
services=$(printf '%s' "$trace_json" | python3 -c "
import json, sys
try: d = json.load(sys.stdin)
except Exception: print(''); sys.exit()
out = set()
for b in d.get('batches', []):
    at = {a['key']: list(a['value'].values())[0] for a in b['resource']['attributes']}
    out.add(at.get('service.name') or '?')
print(' '.join(sorted(out)))" 2>/dev/null)

# No service mesh sits on this path — that returns in a later stage. Nothing
# propagates trace context for the application, so it has to pass traceparent
# itself. Three services in one trace is the evidence that it does.
missing=""
for svc in platform-local-payment-svc platform-local-order-svc platform-local-mock-payment; do
  case " $services " in
    *" $svc "*) : ;;
    *) missing="$missing ${svc#platform-local-}" ;;
  esac
done
if [ -z "$missing" ]; then
  ok "one trace spans payment-svc, order-svc and mock-payment"
else
  bad "trace is missing:$missing — context is not propagating"
  note "saw: ${services:-nothing}"
fi

db_spans=$(printf '%s' "$trace_json" | python3 -c "
import json, sys
try: d = json.load(sys.stdin)
except Exception: print(0); sys.exit()
n = 0
for b in d.get('batches', []):
    for ss in b.get('instrumentationLibrarySpans', []) + b.get('scopeSpans', []):
        for s in ss.get('spans', []):
            if any(a['key'] == 'db.system' for a in s.get('attributes', [])):
                n += 1
print(n)" 2>/dev/null)

if [ "${db_spans:-0}" -gt 0 ]; then
  ok "$db_spans database spans — the data layer is instrumented, not assumed"
else
  bad "no database spans; gorm instrumentation is not reaching the collector"
fi

section "logs reached loki and carry the trace id"

start=$(( $(date +%s) - 900 ))
log_json=$(obs_get loki 3100 \
  "/loki/api/v1/query_range?query=%7Bservice_name%3D~%22.%2B%22%7D%20%7C%3D%20%22$trace%22&limit=50&start=${start}000000000")

read -r lines log_svcs <<EOF
$(printf '%s' "$log_json" | python3 -c "
import json, sys
try: d = json.load(sys.stdin)
except Exception: print(0, 0); sys.exit()
n, svcs = 0, set()
for r in d.get('data', {}).get('result', []):
    svcs.add(r['stream'].get('service_name', '?'))
    n += len(r.get('values', []))
print(n, len(svcs))" 2>/dev/null)
EOF

if [ "${lines:-0}" -gt 0 ]; then
  ok "$lines log lines across $log_svcs services carry this trace id"
else
  bad "no logs found for this trace — logs and traces are not correlated"
fi

section "metrics reached prometheus"

# The label only exists because Alloy converts the resource attribute into one.
# Querying by it proves resource_to_telemetry_conversion is on.
metrics=$(obs_get prometheus-server 9090 \
  "/api/v1/query?query=sum%20by%20(service)%20(http_server_request_duration_seconds_count)" 80)

svc_count=$(printf '%s' "$metrics" | python3 -c "
import json, sys
try: rs = json.load(sys.stdin)['data']['result']
except Exception: print(0); sys.exit()
print(len([r for r in rs if r['metric'].get('service')]))" 2>/dev/null)

if [ "${svc_count:-0}" -gt 0 ]; then
  ok "$svc_count services report request counts, labelled by service"
else
  bad "no request metrics with a service label"
  note "either remote-write is not arriving, or resource_to_telemetry_conversion is off"
fi

section "grafana is reachable"

expect "GET /grafana           " 200 "$(status GET /grafana/)"

# root_url and serve_from_sub_path both adding the prefix produces
# /grafana/grafana on every asset. Anchored to a quote so it does not match
# github.com/grafana/grafana in Grafana's own error page.
if body GET /grafana/ | grep -qE '"/grafana/grafana|=/grafana/grafana'; then
  bad "assets are served from /grafana/grafana"
  note "serve_from_sub_path is on as well as root_url; only root_url should carry the prefix"
else
  ok "assets are not double-prefixed"
fi

# The config that produces it, checked directly — the page above only shows the
# symptom.
sub_path=$(kubectl -n "$OBS_NS" get cm grafana \
  -o jsonpath='{.data.grafana\.ini}' 2>/dev/null | grep -c 'serve_from_sub_path = false')
if [ "${sub_path:-0}" -ge 1 ]; then
  ok "serve_from_sub_path is off, root_url carries the prefix"
else
  bad "serve_from_sub_path is not explicitly false"
fi

summary
