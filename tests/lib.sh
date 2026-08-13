#!/usr/bin/env bash
#
# Shared helpers for the test scripts.
#
# Sourced, not executed. Each test file declares its own checks and calls
# `summary` at the end; the counting, colour and exit codes live here so the
# files stay about the behaviour being tested.

# Not `set -e`: a failing check must be recorded and the run continue. A test
# that stops at the first failure only reports one problem per run.
set -uo pipefail

export PATH="/usr/local/bin:$PATH"

BASE="${BASE:-https://localhost}"
# One application namespace split into two: web-ui lives in `web`, the four Go
# services plus dummy and platform-config live in `api`. One variable, not
# two: no suite here runs kubectl against a web-ui pod directly, only through
# BASE, so api is the only namespace anything below needs to name.
API_NS="${API_NS:-api}"
DATA_NS="${DATA_NS:-data}"
CLUSTER="${CLUSTER:-rabbit-k8s-test}"

_pass=0
_fail=0

ok()  { printf '  \033[32mPASS\033[0m  %s\n' "$1"; _pass=$((_pass + 1)); }
bad() { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; _fail=$((_fail + 1)); }
note() { printf '  \033[2m%s\033[0m\n' "$1"; }

section() { printf '\n\033[1m%s\033[0m\n' "$1"; }

# expect <label> <expected> <actual>
expect() {
  if [ "$3" = "$2" ]; then
    ok "$1 — $3"
  else
    bad "$1 — expected $2, got $3"
  fi
}

# status <method> <path> [data] [token] — the HTTP code, or 000 if the call did
# not complete.
status() {
  local method="$1" path="$2" data="${3:-}" token="${4:-}"
  local args=(-sS -o /dev/null -w '%{http_code}' --max-time 15 -X "$method")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  if [ -n "$data" ]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  curl "${args[@]}" "$BASE$path" 2>/dev/null
}

# body <method> <path> [data] [token]
body() {
  local method="$1" path="$2" data="${3:-}" token="${4:-}"
  local args=(-sS --max-time 15 -X "$method")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  if [ -n "$data" ]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  curl "${args[@]}" "$BASE$path" 2>/dev/null
}

# json <expression> — reads stdin. python3 rather than jq, which is not always
# installed.
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)" 2>/dev/null; }

mysql_q() {
  kubectl -n "$DATA_NS" exec statefulset/mariadb -- sh -c \
    "mariadb -uroot -p\"\$MARIADB_ROOT_PASSWORD\" rabbitshop -N -B -e '$1'" 2>/dev/null | tr -d '\r'
}

# Fails early when the cluster is not up. Without this a whole file reports
# failures that all mean "nothing is running".
require_cluster() {
  if ! kubectl -n "$API_NS" get pods >/dev/null 2>&1; then
    echo "cluster $CLUSTER is not reachable — run 'make up' first"
    exit 1
  fi
}

summary() {
  echo
  if [ "$_fail" -eq 0 ]; then
    printf '\033[32m%d passed\033[0m\n' "$_pass"
    exit 0
  fi
  printf '\033[31m%d passed, %d failed\033[0m\n' "$_pass" "$_fail"
  exit 1
}
