#!/usr/bin/env bash
#
# Usecase: the platform survives losing a pod.
#
# Deletes the database and a stateless replica, and checks that the data is
# still there and the service still answers. Slower than the other files
# because it waits for real rollouts.
#
#   ./tests/resilience.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

ACCOUNT="${ACCOUNT:-alice}"
SECRET="${SECRET:-password}"
login="{\"username\":\"$ACCOUNT\",\"password\":\"$SECRET\"}"

section "before"
token=$(body POST /api/auth/login "$login" | json "d['token']")
if [ -z "$token" ]; then
  bad "cannot log in — is the stack up?"
  summary
fi

orders_before=$(mysql_q "SELECT COUNT(*) FROM orders")
if [ -n "$orders_before" ]; then
  ok "$orders_before orders in the database"
else
  bad "cannot read the database"
  summary
fi

section "the database pod is deleted"
kubectl -n "$DATA_NS" delete pod mariadb-0 --wait=true >/dev/null 2>&1
if kubectl -n "$DATA_NS" rollout status statefulset/mariadb --timeout=180s >/dev/null 2>&1; then
  ok "mariadb came back"
else
  bad "mariadb did not come back within 180s"
  summary
fi

orders_after=$(mysql_q "SELECT COUNT(*) FROM orders")
expect "orders survived        " "$orders_before" "$orders_after"

# The PVC is the reason. A pod without one comes back with an empty database
# and the count above would read 0 rather than failing outright.
pvc=$(kubectl -n "$DATA_NS" get pvc data-mariadb-0 -o jsonpath='{.status.phase}' 2>/dev/null)
expect "the volume is still bound" Bound "$pvc"

section "the services reconnect"
# gorm holds a pool of connections that were all just cut. A service that does
# not reconnect returns 500 here while looking healthy in kubectl.
recovered=""
for _ in $(seq 1 30); do
  if [ "$(status GET /api/products '' "$token")" = "200" ]; then
    recovered=yes
    break
  fi
  sleep 2
done

if [ -n "$recovered" ]; then
  ok "order-svc is serving again"
else
  bad "order-svc did not recover — the connection pool did not reconnect"
fi

section "a stateless replica is deleted"
# Two replicas and a PDB, so one going away should not be visible from outside.
victim=$(kubectl -n "$NS" get pods -l app.kubernetes.io/name=order-svc \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ -z "$victim" ]; then
  bad "no order-svc pod found"
  summary
fi

kubectl -n "$NS" delete pod "$victim" --wait=false >/dev/null 2>&1
note "deleted $victim"

# Ask immediately and repeatedly: the other replica should answer throughout.
failures=0
for _ in $(seq 1 10); do
  [ "$(status GET /api/products '' "$token")" = "200" ] || failures=$((failures + 1))
  sleep 1
done

if [ "$failures" -eq 0 ]; then
  ok "no failed requests while a replica was replaced"
else
  bad "$failures of 10 requests failed during the replacement"
fi

kubectl -n "$NS" rollout status deployment/order-svc --timeout=120s >/dev/null 2>&1
replicas=$(kubectl -n "$NS" get deployment order-svc -o jsonpath='{.status.readyReplicas}' 2>/dev/null)
expect "back to two replicas   " 2 "$replicas"

summary
