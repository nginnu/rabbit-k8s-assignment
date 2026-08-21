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

tally=$(mktemp)
trap 'rm -f "$tally"' EXIT

failed=""
for suite in $SUITES; do
  printf '\n\033[1m═══ %s ═══\033[0m\n' "$suite"
  out=$(mktemp)
  start=$(date +%s)
  ./"$suite" 2>&1 | tee "$out"
  rc=${PIPESTATUS[0]}
  elapsed=$(( $(date +%s) - start ))

  # Counts come from the line lib.sh's summary() prints, so a suite that dies
  # before reaching it reports "-" rather than a fabricated zero.
  line=$(grep -oE '[0-9]+ passed(, [0-9]+ failed)?' "$out" | tail -1)
  pass=$(printf '%s' "$line" | grep -oE '^[0-9]+')
  fail=$(printf '%s' "$line" | grep -oE '[0-9]+ failed' | grep -oE '^[0-9]+')
  [ -n "$pass" ] || pass="-"
  [ -n "$fail" ] || { [ "$pass" = "-" ] && fail="-" || fail=0; }

  if [ "$rc" -eq 0 ]; then
    result="ok"
  elif [ "$pass" = "-" ]; then
    # No "N passed" line at all: the suite died before its own summary. The exit
    # code is the only thing left that says why, so it goes in the table rather
    # than being thrown away with the captured output.
    result="no summary (exit $rc)"
    cp "$out" "/tmp/run-all-$suite.log"
    failed="$failed $suite"
  else
    result="FAILED"
    failed="$failed $suite"
  fi

  printf '%s\t%s\t%s\t%s\t%s\n' "$suite" "$pass" "$fail" "${elapsed}s" "$result" >>"$tally"
  rm -f "$out"
done

printf '\n\033[1m═══ summary ═══\033[0m\n\n'
printf '  %-20s %6s %6s %7s  %s\n' "suite" "pass" "fail" "time" "result"
printf '  %-20s %6s %6s %7s  %s\n' "────────────────────" "──────" "──────" "───────" "──────"
while IFS=$'\t' read -r s p f t r; do
  if [ "$r" = "ok" ]; then
    printf '  %-20s %6s %6s %7s  \033[32m%s\033[0m\n' "$s" "$p" "$f" "$t" "$r"
  else
    printf '  %-20s %6s %6s %7s  \033[31m%s\033[0m\n' "$s" "$p" "$f" "$t" "$r"
  fi
done <"$tally"

tp=$(awk -F'\t' '$2 ~ /^[0-9]+$/ {n += $2} END {print n + 0}' "$tally")
tf=$(awk -F'\t' '$3 ~ /^[0-9]+$/ {n += $3} END {print n + 0}' "$tally")
tt=$(awk -F'\t' '{gsub(/s$/, "", $4); n += $4} END {print n + 0}' "$tally")
printf '  %-20s %6s %6s %7s\n' "────────────────────" "──────" "──────" "───────"
printf '  %-20s %6s %6s %7s\n\n' "total" "$tp" "$tf" "${tt}s"

if [ -z "$failed" ]; then
  printf '\033[32mall suites passed\033[0m\n'
  exit 0
fi
printf '\033[31mfailed:%s\033[0m\n' "$failed"
for suite in $failed; do
  [ -f "/tmp/run-all-$suite.log" ] && printf '  output kept: /tmp/run-all-%s.log\n' "$suite"
done
exit 1
