#!/usr/bin/env bash
#
# Runs every test file and reports which ones failed.
#
#   ./tests/run-all.sh
#
# Ordered cheapest first: unit.sh runs before everything else because it needs
# no cluster — pure host-side go test, so it fails fast before anything touches
# the cluster. Then routing before checkout, and resilience last because it
# deletes pods and takes minutes.

cd "$(dirname "$0")" || exit 1

SUITES="unit.sh routing.sh route-isolation.sh internal-routes.sh mesh.sh istio.sh auth.sh checkout.sh o11y-stack.sh o11y-journey.sh tls-proof.sh resilience.sh"

failed=""
for suite in $SUITES; do
  printf '\n\033[1m═══ %s ═══\033[0m\n' "$suite"
  if ./"$suite"; then
    :
  else
    failed="$failed $suite"
  fi
done

printf '\n\033[1m═══ summary ═══\033[0m\n'
if [ -z "$failed" ]; then
  printf '\033[32mall suites passed\033[0m\n'
  exit 0
fi
printf '\033[31mfailed:%s\033[0m\n' "$failed"
exit 1
