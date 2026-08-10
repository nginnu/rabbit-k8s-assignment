#!/usr/bin/env bash
#
# One customer journey, followed through the telemetry.
#
# Buys a shirt in five requests, then checks that the platform can tell you what
# happened. The checks matter more than the purchase: an order that succeeds
# while its trace is missing is a platform nobody can operate.
#
#   ./tests/o11y-journey.sh
#
# Ends with Grafana links for the run that just happened. o11y-stack.sh asks
# whether the stack is assembled correctly; this one asks whether a real request
# can be followed afterwards.
#
# Everything goes through the gateway, the same path a browser takes. The
# telemetry is on emptyDir with short retention, so data from an hour ago is
# gone — re-run it whenever you want something to look at.

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

OBS_NS="${OBS_NS:-observability}"
ACCOUNT="${ACCOUNT:-alice}"
SECRET="${SECRET:-password}"
PRODUCT="${PRODUCT:-LIV-H-24}"
AMOUNT="${AMOUNT:-3290}"
SETTLE="${SETTLE:-30}"

# Queried over a port-forward. None of these images ship wget or curl, so
# `kubectl exec ... wget` fails on stderr and returns an empty stdout — which is
# indistinguishable from "the component had no data".
# prometheus-server listens on 80, so the service port is passed separately.
obs_get() {
  local svc="$1" port="$2" path="$3" svcport="${4:-$2}" out=""
  kubectl -n "$OBS_NS" port-forward "svc/$svc" "$port:$svcport" >/dev/null 2>&1 &
  local pf=$!
  for _ in $(seq 1 20); do
    curl -sS --max-time 2 -o /dev/null "http://localhost:$port/" 2>/dev/null && break
    sleep 0.5
  done
  out=$(curl -sS --max-time 15 "http://localhost:$port$path" 2>/dev/null)
  kill "$pf" 2>/dev/null
  wait "$pf" 2>/dev/null
  printf '%s' "$out"
}

tempo() { obs_get tempo 3200 "$1"; }
loki()  { obs_get loki 3100 "$1"; }
prom()  { obs_get prometheus-server 9090 "/api/v1/query?query=$(
            python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$1")" 80; }

section "Buying a shirt"

login=$(body POST /api/auth/login "{\"username\":\"$ACCOUNT\",\"password\":\"$SECRET\"}")
token=$(printf '%s' "$login" | json "d['token']")
if [ -z "$token" ]; then
  bad "cannot log in as $ACCOUNT"
  summary
fi
note "1. logged in as $ACCOUNT"

# The session id lives inside the JWT, so every request carrying this token
# shares it. It is what ties four separate traces into one journey.
session=$(printf '%s' "$token" | cut -d. -f2 | python3 -c "
import sys, base64, json
p = sys.stdin.read().strip(); p += '=' * (-len(p) % 4)
print(json.loads(base64.urlsafe_b64decode(p)).get('sid',''))" 2>/dev/null)
note "   session $session"

count=$(body GET /api/products '' "$token" | json "len(d)")
note "2. $count products listed"

order=$(body POST /api/orders "{\"product_id\":\"$PRODUCT\"}" "$token")
order_id=$(printf '%s' "$order" | json "d['id']")
if [ -z "$order_id" ]; then
  bad "cannot place an order"
  summary
fi
note "3. order $order_id created for $PRODUCT"

# The trace id comes back in a header, which is what makes this specific request
# findable later instead of searching by time.
headers=$(mktemp)
payment=$(curl -sS --max-time 15 -D "$headers" \
  -X POST "$BASE/api/payments" \
  -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
  -d "{\"order_id\":$order_id,\"amount\":$AMOUNT}" 2>/dev/null)
payment_id=$(printf '%s' "$payment" | json "d['payment_id']")
trace=$(grep -i '^traceresponse:' "$headers" | tr -d '\r' | sed 's/.*: *//' | cut -d- -f2)
rm -f "$headers"
note "4. payment $payment_id charged"

confirm=$(body GET "/api/payments/$payment_id/success" '' "$token")
state=$(printf '%s' "$confirm" | json "d['status']")
note "5. checkout confirmed"

section "Checks"

expect "order $order_id settled  " paid "$state"

if [ -n "$trace" ]; then
  ok "the response carried a trace id — $trace"
else
  bad "no traceresponse header; the middleware is not returning the trace id"
  summary
fi

# Telemetry is pushed and batched, so it is not queryable the instant the
# request returns. Waiting is honest; retrying until it appears would hide a
# pipeline that had genuinely stopped.
note "waiting ${SETTLE}s for telemetry to land"
sleep "$SETTLE"

trace_json=$(tempo "/api/traces/$trace")

services=$(printf '%s' "$trace_json" | python3 -c "
import json, sys
try: d = json.load(sys.stdin)
except Exception: print(''); sys.exit()
out = set()
for b in d.get('batches', []):
    at = {a['key']: list(a['value'].values())[0] for a in b['resource']['attributes']}
    out.add((at.get('service.name') or '?').replace('platform-local-', ''))
print(' '.join(sorted(out)))" 2>/dev/null)

# Payment calls orders to validate, the bank to charge, then orders again to
# mark it paid. All three in one trace is the thing worth proving: Istio does
# not propagate context for you — the application passes traceparent itself.
missing=""
for svc in payment-svc order-svc mock-payment; do
  case " $services " in
    *" $svc "*) : ;;
    *) missing="$missing $svc" ;;
  esac
done
if [ -z "$missing" ]; then
  ok "one trace spans: $services"
else
  bad "trace is missing$missing — context is not propagating"
  note "saw: ${services:-nothing}"
fi

db=$(printf '%s' "$trace_json" | python3 -c "
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

if [ "${db:-0}" -gt 0 ]; then
  ok "$db database spans — the data layer is instrumented, not assumed"
else
  bad "no database spans; gorm and redis instrumentation is not reaching the collector"
fi

start=$(( $(date +%s) - 900 ))
log_json=$(loki "/loki/api/v1/query_range?query=%7Bservice_name%3D~%22.%2B%22%7D%20%7C%3D%20%22$trace%22&limit=50&start=${start}000000000")

read -r lines log_services <<EOF
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
  ok "$lines log lines across $log_services services carry this trace id"
else
  bad "no logs found for this trace — logs and traces are not correlated"
fi

metrics=$(prom 'sum by (service) (http_server_request_duration_seconds_count)' | python3 -c "
import json, sys
try: rs = json.load(sys.stdin)['data']['result']
except Exception: rs = []
print(' '.join(f\"{r['metric'].get('service','?')}={r['value'][1]}\"
      for r in sorted(rs, key=lambda r: r['metric'].get('service',''))))" 2>/dev/null)

if [ -n "$metrics" ]; then
  ok "request counts per service — $metrics"
else
  bad "no request metrics; check the remote-write receiver and resource_to_telemetry_conversion"
fi

section "In Grafana"

printf '  this purchase\n    %s/grafana/explore?left=%%7B%%22datasource%%22:%%22tempo%%22,%%22queries%%22:%%5B%%7B%%22query%%22:%%22%s%%22,%%22queryType%%22:%%22traceql%%22%%7D%%5D,%%22range%%22:%%7B%%22from%%22:%%22now-30m%%22,%%22to%%22:%%22now%%22%%7D%%7D\n' \
  "$BASE" "$trace"
printf '\n  the whole journey — four traces, one session\n    TraceQL:  %s\n' \
  "{ .session.id = \"$session\" }"
printf '\n  its logs\n    LogQL:    %s\n' \
  "{service_name=~\".+\"} |= \"$trace\""

summary
