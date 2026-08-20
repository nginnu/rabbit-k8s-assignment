#!/usr/bin/env bash
#
# Usecase: the Go modules' own unit tests pass on the host.
#
# Why this exists: apps/notification-api and apps/shop-api carry Go unit tests the
# shell suites cannot see — routing/auth/checkout prove the deployed cluster,
# not the code inside the pods. This is the cheapest suite (pure `go test`, no
# cluster), so run-all.sh runs it first and fails fast before anything touches
# the cluster.
#
# No require_cluster: `go test` compiles and runs on the host alone; gating it
# on a reachable cluster would stop the one suite that needs no cluster from
# running at all.
#
#   ./tests/unit.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

# go_unit <module-dir> — one check per module. -count=1 because a cached PASS
# from a previous run would make this suite lie about the current tree. Output
# is captured so a failure is printed whole — one run reports every failing
# package and test, not just the first.
go_unit() {
  local module="$1"
  if (cd "$module" && go test ./... -count=1) >"$out" 2>&1; then
    ok "$(basename "$module") — go test ./... passed"
  else
    bad "$(basename "$module") — go test ./... failed"
    sed 's/^/    /' "$out"
  fi
}

section "apps/notification-api"
go_unit ../apps/notification-api

section "apps/shop-api"
go_unit ../apps/shop-api

summary
