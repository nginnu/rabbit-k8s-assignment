#!/usr/bin/env bash
#
# Check that every image tag a chart asks for is a tag the Makefile builds.
#
#   check-image-tags.sh <version>
#
# A chart pinned to a tag that was never built does not fail at deploy time. The
# release installs, the Deployment is created, and the pod sits in
# ErrImageNeverPull — pullPolicy is Never, so the kubelet does not even try to
# fetch it and there is no error until someone looks at the pod.
#
# Cheaper to compare two strings before any of that happens.

set -euo pipefail

version="$1"
root="$(cd "$(dirname "$0")/../.." && pwd)"

bad=0
for values in "$root"/charts/apps/*/values.yaml; do
  chart="$(basename "$(dirname "$values")")"

  # Charts that render no workload have no image to check.
  grep -q 'tag:' "$values" || continue

  tag="$(grep -m1 'tag:' "$values" | sed 's/.*tag: *//' | tr -d '"'"'"' ')"
  if [ "$tag" != "$version" ]; then
    echo "$chart: chart asks for $tag, make builds $version"
    bad=1
  fi
done

if [ "$bad" -ne 0 ]; then
  echo
  echo "Fix the tag in the chart, or build that version with VERSION=<tag>."
  exit 1
fi

echo "image tags $version"
