#!/usr/bin/env bash
#
# Print the namespace a chart under charts/apps declares for itself.
#
#   chart-namespace.sh <chart>
#
# The chart's values.yaml is the only place this is written down. Every object
# the library chart renders takes its namespace from that key, and helm needs
# the same string a second time as -n, because release metadata is stored in the
# namespace of the flag and not in the namespace of the objects. The two
# disagreeing installs a release that `helm list -n api` cannot see and that
# `helm uninstall` will not clean up, while the pods themselves come up fine and
# nothing reports a problem.
#
# So the Makefile does not keep its own chart-to-namespace list. A list drifts
# the first time a service moves between namespaces and only one of the two
# files is edited.

set -euo pipefail

chart="$1"
root="$(cd "$(dirname "$0")/../.." && pwd)"
values="$root/charts/apps/$chart/values.yaml"

if [ ! -f "$values" ]; then
  echo "chart-namespace: $chart has no values.yaml at $values" >&2
  exit 1
fi

# Anchored to column 0. An indented `namespace:` belongs to some other block —
# taking it would hand helm a namespace the objects were never rendered into.
ns="$(awk '/^namespace:[[:space:]]/ { print $2; exit }' "$values")"
ns="${ns//[\"\']/}"

if [ -z "$ns" ]; then
  echo "chart-namespace: $values sets no top-level namespace: key" >&2
  echo "  the library chart requires it, so the render fails; without -n," >&2
  echo "  helm would install into whatever namespace the kubeconfig points at." >&2
  exit 1
fi

printf '%s\n' "$ns"
