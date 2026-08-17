#!/usr/bin/env bash
#
# Preflight — is the platform assembled the way the repo says it is?
#
#   ./tests/preflight.sh
#   SKIP_HTTP=1 ./tests/preflight.sh     # config checks only, no traffic
#
# The other test files ask whether a feature works. This asks whether what is
# running is what was declared. Those come apart, and when they do every symptom
# points at the wrong layer: a Traefik below its pin ignores listener fields it
# does not understand, logs nothing, and leaves you debugging a Gateway that is
# correct in git and wrong in the cluster.
#
# The rule throughout: read the pin from the Makefile, read reality from the
# cluster, print both. A check that says "ok" without showing what it compared
# cannot be trusted the day it lies.
#
# Exit codes:  0 all passed   1 a check failed   2 could not run the checks

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MAKEFILE="$REPO_ROOT/Makefile"
KIND_CONFIG="$REPO_ROOT/platform/kind/cluster.yaml"

BASE="${BASE:-http://localhost}"
KUBECTL="${KUBECTL:-kubectl}"
SKIP_HTTP="${SKIP_HTTP:-}"
export PATH="/usr/local/bin:$PATH"

bold=$(tput bold 2>/dev/null || true)
dim=$(tput dim 2>/dev/null || true)
red=$(tput setaf 1 2>/dev/null || true)
green=$(tput setaf 2 2>/dev/null || true)
yellow=$(tput setaf 3 2>/dev/null || true)
reset=$(tput sgr0 2>/dev/null || true)

pass=0
fail=0
skip=0
declare -a FAILURES=()

step() { printf '\n%s%s%s\n' "$bold" "$1" "$reset"; }
ok()   { printf '  %s✓%s %s\n' "$green" "$reset" "$1"; pass=$((pass + 1)); }
bad()  { printf '  %s✗%s %s\n' "$red" "$reset" "$1"; fail=$((fail + 1)); FAILURES+=("$1"); }
warn() { printf '  %s!%s %s\n' "$yellow" "$reset" "$1"; skip=$((skip + 1)); }
note() { printf '    %s%s%s\n' "$dim" "$1" "$reset"; }

# The Makefile is the declaration of record. Parsing it means this script cannot
# drift from it the way a second hardcoded copy of a version would.
makevar() {
  sed -nE "s/^$1[[:space:]]*[:?]?=[[:space:]]*([^[:space:]#]+).*/\1/p" "$MAKEFILE" | head -1
}

compare() {
  local label="$1" declared="$2" actual="$3" hint="${4:-}"
  if [ -z "$actual" ]; then
    bad "$label — declared $declared, but nothing is installed"
    [ -n "$hint" ] && note "$hint"
    return
  fi
  if [ "$declared" = "$actual" ]; then
    ok "$label — $actual"
  else
    bad "$label — declared $declared, running $actual"
    [ -n "$hint" ] && note "$hint"
  fi
}

CLUSTER="${CLUSTER:-$(makevar CLUSTER)}"
DATA_NS="${DATA_NS:-$(makevar DATA_NS)}"
TRAEFIK_NS="${TRAEFIK_NS:-$(makevar TRAEFIK_NS)}"
APP_VERSION="${VERSION:-$(makevar VERSION)}"
# The application namespace split in two: web-ui lives in `web`; the four Go
# services, notification and platform-config live in `api`. Checks below that used to
# read one namespace now loop over both — a chart moved into the wrong one
# renders healthy and is only found by going looking for it.
APP_NAMESPACES="web api"

# ─── 0  tools and reachability ───────────────────────────────────────────────

step "Prerequisites"

for tool in kubectl helm kind docker; do
  if command -v "$tool" >/dev/null 2>&1; then
    ok "$tool found"
  else
    bad "$tool not on PATH"
    printf '\n%sCannot run the checks without %s.%s\n\n' "$red" "$tool" "$reset"
    exit 2
  fi
done

if ! $KUBECTL cluster-info >/dev/null 2>&1; then
  printf '\n%sNo reachable cluster. Is kind running?  make cluster%s\n\n' "$red" "$reset"
  exit 2
fi

ctx=$($KUBECTL config current-context 2>/dev/null)
if [ "$ctx" = "kind-$CLUSTER" ]; then
  ok "kubectl context is kind-$CLUSTER"
else
  bad "kubectl context is $ctx, expected kind-$CLUSTER"
  note "every check below inspects the wrong cluster until this is fixed"
fi

# ─── 1  versions: declared vs installed ──────────────────────────────────────

step "Versions — what the Makefile pins vs what is running"

helm_app_version() {
  helm list -n "$1" -o json 2>/dev/null | python3 -c "
import json,sys
try: rs = json.load(sys.stdin)
except Exception: rs = []
for r in rs:
    if r.get('name') == sys.argv[1]:
        print(r.get('app_version') or '')
        break
" "$2" 2>/dev/null
}

# Two pins, because they are two different strings: TRAEFIK_VERSION is the
# chart and TRAEFIK_APP_VERSION is the proxy image tag. helm list reports the
# chart's own declared appVersion, not what is actually running, so the image
# tag is read from the cluster directly — the same split the Makefile's own
# check-version.sh call makes at install time.
traefik_chart_declared=$(makevar TRAEFIK_VERSION)
traefik_chart_actual=$(helm list -n "$TRAEFIK_NS" -o json 2>/dev/null | python3 -c "
import json,sys
try: rs = json.load(sys.stdin)
except Exception: rs = []
for r in rs:
    if r.get('name') == 'traefik':
        print((r.get('chart') or '').rsplit('-', 1)[-1]); break
" 2>/dev/null)
compare "traefik chart" "$traefik_chart_declared" "$traefik_chart_actual" \
  "below the pin, listener fields the Gateway needs are dropped with nothing logged"

# The running image is checked separately from the Helm release: a release can
# report one version while a cached image runs another.
traefik_declared=$(makevar TRAEFIK_APP_VERSION)
traefik_image=$($KUBECTL -n "$TRAEFIK_NS" get deploy traefik \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)
if [ -n "$traefik_image" ]; then
  compare "traefik image tag" "$traefik_declared" "${traefik_image##*:}"
else
  bad "traefik deployment not found in $TRAEFIK_NS"
fi

compare "cert-manager" "$(makevar CERT_MANAGER_VERSION)" "$(helm_app_version cert-manager cert-manager)"

gwapi_actual=$($KUBECTL get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null)
compare "Gateway API CRDs" "$(makevar GATEWAY_API_VERSION)" "$gwapi_actual" \
  "CRDs below the pin drop fields at admission — the object applied is not the object stored"

# ─── 2  the gateway is reachable from the host ───────────────────────────────
#
# Three pieces have to line up for :80 to reach Traefik, and each fails
# silently on its own, so each is checked by itself.

step "Gateway plumbing — host :80 to Traefik"

# Traefik is the controller and the data path in one pod: there is no
# per-Gateway deployment, and nothing carries a gateway.networking.k8s.io
# label — that label belongs to the Gateway object, not the pod that serves
# it. The chart's own pod labels are the only handle here.
gw_pod_ports=$($KUBECTL -n "$TRAEFIK_NS" get pod \
  -l app.kubernetes.io/name=traefik \
  -o jsonpath='{.items[0].spec.containers[0].ports[*].hostPort}' 2>/dev/null)

if printf '%s' "$gw_pod_ports" | grep -qw 80; then
  ok "Traefik binds hostPort 80"
else
  bad "Traefik has no hostPort 80 — the ports: block in values.yaml never reached the pod spec"
fi

gw_node=$($KUBECTL -n "$TRAEFIK_NS" get pod \
  -l app.kubernetes.io/name=traefik \
  -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null)

# Parsed from the kind config rather than hardcoded, so renaming a node cannot
# make this check quietly wrong.
mapped_node="$(python3 - "$KIND_CONFIG" <<'PY' 2>/dev/null
import re, sys
try:
    text = open(sys.argv[1]).read()
except OSError:
    sys.exit(0)
for b in re.split(r'\n  - role: ', text)[1:]:
    if 'extraPortMappings' in b:
        print(b.split('\n')[0].strip())
        break
PY
)"
mapped_node="${mapped_node:-control-plane}"

if [ -n "$gw_node" ]; then
  case "$gw_node" in
    *"$mapped_node"*) ok "Traefik runs on $gw_node — the node kind mapped :80 to" ;;
    *)
      bad "Traefik runs on $gw_node, but kind maps :80 to the $mapped_node node"
      note "hostPort binds a port on a node nothing forwards to the host" ;;
  esac
else
  bad "no Traefik pod found in $TRAEFIK_NS"
fi

labelled=$($KUBECTL get nodes -l ingress-ready=true -o name 2>/dev/null | wc -l | tr -d ' ')
if [ "$labelled" = "1" ]; then
  ok "exactly one node carries ingress-ready=true"
else
  bad "$labelled nodes carry ingress-ready=true — kind config declares 1"
  note "with more than one, Traefik can schedule onto a node with no port mapping"
fi

# ─── 3  routing objects accepted, not merely present ─────────────────────────
#
# kubectl apply succeeding proves the YAML parsed, not that the controller
# accepted it. Status conditions are the real answer.

step "Gateway API objects"

prog=$($KUBECTL -n "$TRAEFIK_NS" get gateway platform \
  -o jsonpath='{.status.conditions[?(@.type=="Programmed")].status}' 2>/dev/null)
if [ "$prog" = "True" ]; then
  ok "gateway/platform is Programmed"
else
  bad "gateway/platform is not Programmed (status: ${prog:-missing})"
fi

routes_seen=0
while read -r ns name; do
  [ -z "$name" ] && continue
  routes_seen=1
  accepted=$($KUBECTL -n "$ns" get httproute "$name" \
    -o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}' 2>/dev/null)
  resolved=$($KUBECTL -n "$ns" get httproute "$name" \
    -o jsonpath='{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' 2>/dev/null)
  if [ "$accepted" = "True" ] && [ "$resolved" = "True" ]; then
    ok "httproute $ns/$name — accepted, refs resolved"
  else
    bad "httproute $ns/$name — Accepted=${accepted:-?} ResolvedRefs=${resolved:-?}"
    note "ResolvedRefs=False usually means the backend Service name or port is wrong"
  fi
done < <($KUBECTL get httproute -A --no-headers 2>/dev/null | awk '{print $1, $2}')

[ "$routes_seen" = "0" ] && bad "no HTTPRoutes exist — nothing is exposed through the gateway"

# ─── 4  workloads ────────────────────────────────────────────────────────────
#
# Reported per pod rather than as one count. "3 of 14 not ready" sends you
# looking at 14 pods; naming the three sends you to the right one.

step "Workloads"

# Job-owned pods are judged by status only, never by the ready column: a
# Completed job pod reads 0/1 because its container has exited, which is success
# rather than a fault. A job that genuinely failed still appears, because its
# pod leaves Running for Error or CrashLoopBackOff.
job_pods=$($KUBECTL get pods -A \
  -o jsonpath='{range .items[?(@.metadata.ownerReferences[0].kind=="Job")]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}' 2>/dev/null)

not_ready=$($KUBECTL get pods -A --no-headers 2>/dev/null | awk -v jobs="$job_pods" '
  BEGIN { n = split(jobs, j, "\n"); for (i = 1; i <= n; i++) if (j[i] != "") isjob[j[i]] = 1 }
  {
    id = $1 "/" $2
    if ($4 != "Running" && $4 != "Completed") { print "      " id "  " $4; next }
    if (isjob[id]) next
    split($3, r, "/")
    if (r[1] != r[2]) print "      " id "  " $3 " ready"
  }')

if [ -z "$not_ready" ]; then
  ok "every pod is Running and ready"
else
  bad "pods not ready:"$'\n'"$not_ready"
fi

# web-ui and notification/auth-svc/order-svc/payment-svc/mock-payment/platform-config
# split across two namespaces now, so this loops rather than reading one.
mismatched=""
for ns in $APP_NAMESPACES; do
  out=$($KUBECTL -n "$ns" get deploy -o json 2>/dev/null | python3 -c "
import json,sys
ns, want = sys.argv[1], sys.argv[2]
try: d = json.load(sys.stdin)
except Exception: sys.exit(0)
for item in d.get('items', []):
    for c in item['spec']['template']['spec']['containers']:
        img = c['image']
        if ':' in img:
            tag = img.rsplit(':', 1)[1]
            # Only locally built images carry VERSION; upstream ones have their own.
            if tag.startswith('v') and tag != want:
                print(f\"      {ns}/{item['metadata']['name']}  {tag}\")
" "$ns" "$APP_VERSION" 2>/dev/null)
  [ -n "$out" ] && mismatched="${mismatched}${out}"$'\n'
done

if [ -z "$mismatched" ]; then
  ok "app images match VERSION $APP_VERSION"
else
  bad "images not on VERSION $APP_VERSION:"
  printf '%s\n' "$mismatched"
  note "make images rebuilds and reloads them"
fi

# A tag make images never builds fails as ErrImageNeverPull: the pod stays down
# and nothing logs why, because pullPolicy Never means no attempt is made.
if "$REPO_ROOT/platform/scripts/check-image-tags.sh" "$APP_VERSION" >/dev/null 2>&1; then
  ok "chart values all reference VERSION $APP_VERSION"
else
  bad "chart values pin a tag make images never builds:"
  "$REPO_ROOT/platform/scripts/check-image-tags.sh" "$APP_VERSION" 2>&1 | sed 's/^/      /'
fi

# A resource left behind by kubectl looks identical in kubectl get and is
# invisible to helm — never upgraded, never rolled back, never deleted.
unmanaged=""
for ns in $APP_NAMESPACES; do
  out=$($KUBECTL -n "$ns" get deploy,svc -o json 2>/dev/null | python3 -c "
import json,sys
ns = sys.argv[1]
try: items = json.load(sys.stdin).get('items', [])
except Exception: sys.exit(0)
for i in items:
    m = i['metadata']
    if m.get('labels', {}).get('app.kubernetes.io/managed-by') != 'Helm':
        print(f\"      {ns}/{i['kind']}/{m['name']}\")
" "$ns" 2>/dev/null)
  [ -n "$out" ] && unmanaged="${unmanaged}${out}"$'\n'
done

if [ -z "$unmanaged" ]; then
  ok "every app workload and Service is Helm-managed"
else
  bad "not managed by Helm — kubectl created these, helm cannot upgrade them:"
  printf '%s\n' "$unmanaged"
fi

# Nothing else here notices a release that is simply gone: the workload checks
# only inspect what exists, so a service that was never installed leaves no
# failing check behind it.
expected_releases=$(sed -nE 's/^APP_CHARTS[[:space:]]*:?=[[:space:]]*(.*)/\1/p' "$MAKEFILE" | head -1)

# -A rather than a single -n: web-ui's release lives in `web`, the rest in
# `api`, and matching by name only does not need to know which is which.
release_status=$(helm list -A -o json 2>/dev/null | python3 -c "
import json,sys
try: rs = json.load(sys.stdin)
except Exception: rs = []
print('\n'.join(f\"{r['name']} {r['status']}\" for r in rs))
" 2>/dev/null)

missing_releases=""
for r in $expected_releases; do
  line=$(printf '%s\n' "$release_status" | awk -v n="$r" '$1==n {print $2}')
  if [ -z "$line" ]; then
    missing_releases="${missing_releases}      $r  (no release — never installed, or uninstalled)"$'\n'
  elif [ "$line" != "deployed" ]; then
    missing_releases="${missing_releases}      $r  (status: $line)"$'\n'
  fi
done
missing_releases=$(printf '%s' "$missing_releases" | grep -v '^$' || true)

if [ -z "$missing_releases" ]; then
  ok "all $(printf '%s\n' $expected_releases | wc -w | tr -d ' ') helm releases are deployed"
else
  bad "helm releases missing or not deployed:"
  printf '%s\n' "$missing_releases"
fi

# The gitops charts are invisible to the check above — APP_CHARTS deliberately
# excludes them — so they need their own: the release exists (installed once
# by gitops-bootstrap), and Argo CD owns it now (Application Healthy + Synced).
gitops_charts=$(sed -nE 's/^GITOPS_CHARTS[[:space:]]*:?=[[:space:]]*(.*)/\1/p' "$MAKEFILE" | head -1)

for g in $gitops_charts; do
  gstatus=$(printf '%s\n' "$release_status" | awk -v n="$g" '$1==n {print $2}')
  compare "helm release $g     " deployed "${gstatus:-missing}" \
    "gitops-bootstrap installed it once; without the release Argo CD has nothing to manage"

  app=$($KUBECTL -n argocd get application "$g" -o json 2>/dev/null | python3 -c "
import json,sys
try: d = json.load(sys.stdin)
except Exception: sys.exit()
print(d.get('status', {}).get('health', {}).get('status', ''),
      d.get('status', {}).get('sync', {}).get('status', ''))" 2>/dev/null)
  health=$(printf '%s' "$app" | awk '{print $1}')
  sync=$(printf '%s' "$app" | awk '{print $2}')
  if [ "$health" = "Healthy" ] && [ "$sync" = "Synced" ]; then
    ok "argocd application $g is Healthy/Synced"
  else
    bad "argocd application $g: health=${health:-none} sync=${sync:-none}"
    note "the Application exists but has not converged — check the argocd UI at /argocd"
  fi
done

# ─── 5  data layer ───────────────────────────────────────────────────────────
#
# The services report ready before the schema exists, so a missing seed shows up
# as an empty catalog rather than a failure.

step "Data layer"

for sts in mariadb; do
  ready=$($KUBECTL -n "$DATA_NS" get statefulset "$sts" \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
  if [ "${ready:-0}" -ge 1 ]; then
    ok "statefulset/$sts is ready"
  else
    bad "statefulset/$sts has ${ready:-0} ready replicas"
  fi
done

pvc=$($KUBECTL -n "$DATA_NS" get pvc data-mariadb-0 \
  -o jsonpath='{.status.phase}' 2>/dev/null)
compare "mariadb volume" "Bound" "${pvc:-missing}" \
  "without a bound PVC the database starts empty every time the pod moves"

init=$($KUBECTL -n "$DATA_NS" get job mariadb-init \
  -o jsonpath='{.status.succeeded}' 2>/dev/null)
if [ "${init:-0}" -ge 1 ]; then
  ok "schema job completed"
else
  # ttlSecondsAfterFinished removes the Job, so its absence is not a failure if
  # the tables are there. Ask the database instead.
  tables=$($KUBECTL -n "$DATA_NS" exec statefulset/mariadb -- sh -c \
    'mariadb -uroot -p"$MARIADB_ROOT_PASSWORD" rabbitshop -N -B -e "SHOW TABLES"' 2>/dev/null | wc -l | tr -d ' ')
  if [ "${tables:-0}" -gt 0 ]; then
    ok "schema is present — $tables tables (the init Job has already expired)"
  else
    bad "no schema in the database and no completed init Job"
    note "make sql loads it into a ConfigMap; make data runs the Job"
  fi
fi

for s in mariadb app-secrets; do
  ns="$DATA_NS"; [ "$s" = "app-secrets" ] && ns=api
  if $KUBECTL -n "$ns" get secret "$s" >/dev/null 2>&1; then
    ok "secret $ns/$s exists"
  else
    bad "secret $ns/$s is missing — make secrets creates it"
  fi
done

# ─── 6  observability ────────────────────────────────────────────────────────
#
# The chart versions are pinned in the Makefile like every other component, and
# the alias Service is the one thing the charts do not create for us.

step "Observability"

OBS_NS="${OBS_NS:-observability}"

for pin in prometheus:PROMETHEUS_VERSION loki:LOKI_VERSION tempo:TEMPO_VERSION \
           alloy:ALLOY_VERSION grafana:GRAFANA_VERSION; do
  rel="${pin%%:*}"; var="${pin##*:}"
  compare "$rel chart" "$(makevar "$var")" \
    "$(helm list -n "$OBS_NS" -o json 2>/dev/null | python3 -c "
import json,sys
try: rs = json.load(sys.stdin)
except Exception: rs = []
for r in rs:
    if r.get('name') == sys.argv[1]:
        print((r.get('chart') or '').rsplit('-', 1)[-1]); break
" "$rel" 2>/dev/null)"
done

# Empty endpoints costs telemetry, not availability: the OTel SDK logs the
# failed export and the services keep serving. That is what makes it worth a
# check — nothing else reports it.
otel_eps=$($KUBECTL -n "$OBS_NS" get endpoints otel-collector \
  -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null)
if [ -n "$otel_eps" ]; then
  ok "otel-collector resolves to an alloy pod — $otel_eps"
else
  bad "otel-collector has no endpoints"
  note "the alias selector does not match the alloy pods; every signal is being dropped"
fi

grafana_route=$($KUBECTL -n "$OBS_NS" get httproute grafana \
  -o jsonpath='{.status.parents[0].conditions[?(@.type=="Accepted")].status}' 2>/dev/null)
if [ "$grafana_route" = "True" ]; then
  ok "httproute observability/grafana — accepted"
else
  bad "httproute observability/grafana is not accepted (${grafana_route:-missing})"
fi

# ─── 6  TLS ──────────────────────────────────────────────────────────────────
#
# Three things have to hold for a green padlock and each fails on its own: the
# certificate issued, the Gateway serving it, and the machine trusting the CA.
# The last cannot be fixed from inside the cluster.

step "TLS"

# The Gateway now comes from the Traefik release, so the certificate it reads
# has to live in the same namespace — a Secret left in a different one needs a
# ReferenceGrant beside it, or the listener resolves no certificate and :443
# falls back to Traefik's self-signed default.
cert_ready=$($KUBECTL -n "$TRAEFIK_NS" get certificate platform-tls \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
if [ "$cert_ready" = "True" ]; then
  ok "certificate platform-tls is issued"
else
  bad "certificate platform-tls is not Ready (${cert_ready:-missing})"
  note "make tls installs cert-manager and seeds the CA"
fi

# websecure, not https: the Traefik chart names its listeners web/websecure,
# not http/https. Asking for the old name returns an empty jsonpath match
# rather than an error, and a check that reads empty as "not yet Programmed"
# instead of "wrong listener name" sends you to the wrong fix.
https_listener=$($KUBECTL -n "$TRAEFIK_NS" get gateway platform \
  -o jsonpath='{.status.listeners[?(@.name=="websecure")].conditions[?(@.type=="Programmed")].status}' 2>/dev/null)
if [ "$https_listener" = "True" ]; then
  ok "gateway serves the websecure listener"
else
  bad "gateway websecure listener is not Programmed (${https_listener:-missing})"
fi

if [ -n "$SKIP_HTTP" ]; then
  warn "TLS request skipped (SKIP_HTTP set)"
else
  # Deliberately no -k. Passing without it is the proof: curl validated the
  # chain against the system trust store, the same judgement a browser makes.
  tls_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 https://localhost/ 2>/dev/null)
  if [ "$tls_code" = "200" ]; then
    ok "GET https://localhost/ returns 200 with a trusted chain (no -k)"
  else
    bad "GET https://localhost/ returns ${tls_code:-no response}"
    insecure=$(curl -sk -o /dev/null -w '%{http_code}' --max-time 10 https://localhost/ 2>/dev/null)
    [ "$insecure" = "200" ] && \
      note "works with -k — TLS serves, but this machine does not trust the CA: run mkcert -install"
  fi
fi

# ─── 7  the promise, end to end ──────────────────────────────────────────────
#
# Subsumes section 2: if this passes the plumbing works whatever the pieces
# looked like. It runs last so that when it fails, the checks above have already
# named the reason.

step "The promise — a browser reaches the store on :80"

if [ -n "$SKIP_HTTP" ]; then
  warn "skipped (SKIP_HTTP set)"
else
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$BASE/" 2>/dev/null)
  if [ "$code" = "200" ]; then
    ok "GET $BASE/ returns 200"
  else
    bad "GET $BASE/ returns ${code:-no response}"

    # Separating "the app is broken" from "the host cannot reach it" is the
    # whole diagnosis, and asking from inside the node answers it in one line.
    # No NodePort fallback below this: traefik/traefik is a ClusterIP Service,
    # and traffic only ever arrives through the hostPort binding checked in
    # section 2 — there is no second path left to try.
    inner=$(docker exec "${CLUSTER}-${mapped_node}" \
      curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://localhost:80/ 2>/dev/null)
    if [ "$inner" = "200" ]; then
      note "from inside the node it returns 200 — the app is fine, the host path is broken"
    fi
  fi
fi

# ─── result ──────────────────────────────────────────────────────────────────

step "Result"
printf '  %s passed, %s failed' "$pass" "$fail"
[ "$skip" -gt 0 ] && printf ', %s skipped' "$skip"
printf '\n'

if [ "$fail" -gt 0 ]; then
  printf '\n  %sFix before running the other suites:%s\n' "$bold" "$reset"
  for f in "${FAILURES[@]}"; do printf '    · %s\n' "$f"; done
  printf '\n'
  exit 1
fi

printf '\n  %sReady.%s  make test\n\n' "$green" "$reset"
