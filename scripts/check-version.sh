#!/usr/bin/env bash
#
# Compare a pinned version against what is actually installed.
#
#   check-version.sh <label> <expected> <actual>
#
# Installing a component does not mean you got the version you asked for. An
# older Istio ignores fields on the Gateway without logging anything, and the
# failure shows up much later as an unreachable port.
#
# This lives in a script because the same `if` inside a Makefile recipe gets
# mangled before it reaches the shell.

set -euo pipefail

label="$1"
expected="$2"
actual="$3"

if [ "$actual" != "$expected" ]; then
  echo "$label: pinned $expected, running ${actual:-nothing}"
  exit 1
fi

echo "$label $actual"
