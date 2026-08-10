#!/usr/bin/env bash
#
# Preflight — is the platform assembled the way the repo says it is?
#
#   ./tests/preflight.sh
#   SKIP_HTTP=1 ./tests/preflight.sh     # config checks only, no traffic
#
# The other test files ask whether a feature works. This asks whether what is
# running is what was declared. Those come apart, and when they do every symptom
# points at the wrong layer: an Istio below its pin ignores fields it does not
# understand, logs nothing, and leaves you debugging a Gateway that is correct
# in git and wrong in the cluster.
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
APP_VERSION="${VERSION:-$(makevar VERSION)}"

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

istio_declared=$(makevar ISTIO_VERSION)
compare "istiod" "$istio_declared" "$(helm_app_version istio-system istiod)" \
  "below the pin, infrastructure.parametersRef on the Gateway is ignored with nothing logged"

# base owns the CRDs istiod validates against, so they must move together.
base_actual=$(helm_app_version istio-system istio-base)
istio_actual=$(helm_app_version istio-system istiod)
if [ -n "$base_actual" ] && [ -n "$istio_actual" ]; then
  if [ "$base_actual" = "$istio_actual" ]; then
    ok "istio-base matches istiod — $base_actual"
  else
    bad "istio-base is $base_actual but istiod is $istio_actual"
  fi
fi

# The running image is checked separately from the Helm release: a release can
# report one version while a cached image runs another.
pilot_image=$($KUBECTL -n istio-system get deploy istiod \
  -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null)
if [ -n "$pilot_image" ]; then
  compare "istiod image tag" "$istio_declared" "${pilot_image##*:}"
else
  bad "istiod deployment not found in istio-system"
fi

compare "cert-manager" "$(makevar CERT_MANAGER_VERSION)" "$(helm_app_version cert-manager cert-manager)"

gwapi_actual=$($KUBECTL get crd gateways.gateway.networking.k8s.io \
  -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null)
compare "Gateway API CRDs" "$(makevar GATEWAY_API_VERSION)" "$gwapi_actual" \
  "CRDs below the pin drop fields at admission — the object applied is not the object stored"

# ─── 2  the gateway is reachable from the host ───────────────────────────────
#
# Three pieces have to line up for :80 to reach Envoy, and each fails silently
# on its own, so each is checked by itself.

step "Gateway plumbing — host :80 to Envoy"

gw_pod_ports=$($KUBECTL -n istio-system get pod \
  -l gateway.networking.k8s.io/gateway-name=platform \
  -o jsonpath='{.items[0].spec.containers[0].ports[*].hostPort}' 2>/dev/null)

if printf '%s' "$gw_pod_ports" | grep -qw 80; then
  ok "Envoy binds hostPort 80"
else
  bad "Envoy has no hostPort 80 — the ConfigMap never reached the pod spec"
  note "this is the visible symptom when istiod ignores parametersRef"
fi

gw_node=$($KUBECTL -n istio-system get pod \
  -l gateway.networking.k8s.io/gateway-name=platform \
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
    *"$mapped_node"*) ok "Envoy runs on $gw_node — the node kind mapped :80 to" ;;
    *)
      bad "Envoy runs on $gw_node, but kind maps :80 to the $mapped_node node"
      note "hostPort binds a port on a node nothing forwards to the host" ;;
  esac
else
  bad "no Envoy pod found for gateway/platform"
fi

labelled=$($KUBECTL get nodes -l ingress-ready=true -o name 2>/dev/null | wc -l | tr -d ' ')
if [ "$labelled" = "1" ]; then
  ok "exactly one node carries ingress-ready=true"
else
  bad "$labelled nodes carry ingress-ready=true — kind config declares 1"
  note "with more than one, Envoy can schedule onto a node with no port mapping"
fi

# ─── 3  routing objects accepted, not merely present ─────────────────────────
#
# kubectl apply succeeding proves the YAML parsed, not that the controller
# accepted it. Status conditions are the real answer.

step "Gateway API objects"

prog=$($KUBECTL -n istio-system get gateway platform \
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

mismatched=$($KUBECTL -n demo get deploy -o json 2>/dev/null | python3 -c "
import json,sys
want = sys.argv[1]
try: d = json.load(sys.stdin)
except Exception: sys.exit(0)
for item in d.get('items', []):
    for c in item['spec']['template']['spec']['containers']:
        img = c['image']
        if ':' in img:
            tag = img.rsplit(':', 1)[1]
            # Only locally built images carry VERSION; upstream ones have their own.
            if tag.startswith('v') and tag != want:
                print(f\"      {item['metadata']['name']}  {tag}\")
" "$APP_VERSION" 2>/dev/null)

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
unmanaged=$($KUBECTL -n demo get deploy,svc -o json 2>/dev/null | python3 -c "
import json,sys
try: items = json.load(sys.stdin).get('items', [])
except Exception: sys.exit(0)
for i in items:
    m = i['metadata']
    if m.get('labels', {}).get('app.kubernetes.io/managed-by') != 'Helm':
        print(f\"      {i['kind']}/{m['name']}\")
" 2>/dev/null)

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

release_status=$(helm list -n demo -o json 2>/dev/null | python3 -c "
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
  ns="$DATA_NS"; [ "$s" = "app-secrets" ] && ns=demo
  if $KUBECTL -n "$ns" get secret "$s" >/dev/null 2>&1; then
    ok "secret $ns/$s exists"
  else
    bad "secret $ns/$s is missing — make secrets creates it"
  fi
done

# ─── 6  TLS ──────────────────────────────────────────────────────────────────
#
# Three things have to hold for a green padlock and each fails on its own: the
# certificate issued, the Gateway serving it, and the machine trusting the CA.
# The last cannot be fixed from inside the cluster.

step "TLS"

cert_ready=$($KUBECTL -n istio-system get certificate platform-tls \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
if [ "$cert_ready" = "True" ]; then
  ok "certificate platform-tls is issued"
else
  bad "certificate platform-tls is not Ready (${cert_ready:-missing})"
  note "make tls installs cert-manager and seeds the CA"
fi

https_listener=$($KUBECTL -n istio-system get gateway platform \
  -o jsonpath='{.status.listeners[?(@.name=="https")].conditions[?(@.type=="Programmed")].status}' 2>/dev/null)
if [ "$https_listener" = "True" ]; then
  ok "gateway serves the https listener"
else
  bad "gateway https listener is not Programmed (${https_listener:-missing})"
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
    inner=$(docker exec "${CLUSTER}-${mapped_node}" \
      curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://localhost:80/ 2>/dev/null)
    if [ "$inner" = "200" ]; then
      note "from inside the node it returns 200 — the app is fine, the host path is broken"
    else
      nodeport=$($KUBECTL -n istio-system get svc platform-istio \
        -o jsonpath='{.spec.ports[?(@.port==80)].nodePort}' 2>/dev/null)
      if [ -n "$nodeport" ]; then
        np=$(docker exec "${CLUSTER}-${mapped_node}" \
          curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://localhost:$nodeport/" 2>/dev/null)
        [ "$np" = "200" ] && note "NodePort $nodeport answers 200 — Envoy works, only the :80 binding is missing"
      fi
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
