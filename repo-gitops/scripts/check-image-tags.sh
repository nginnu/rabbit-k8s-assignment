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
# The charts are one level up, in this repo, and nothing outside it is named —
# so a clone of repo-gitops on its own still finds them. Taken from the script's
# own location rather than $PWD: make runs it from the tree above and a test
# runs it from anywhere, and a bare `charts/*` would scan nothing in both cases.
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

bad=0
seen=0
for values in "$repo"/charts/*/values.yaml; do
  # A glob that matches nothing is left literal by the shell, so this loop still
  # runs once, with a path containing a `*` that does not exist. Without this
  # line the counter below reaches 1 and the empty-scan guard passes having
  # checked no chart at all — the precise failure the guard is for.
  [ -f "$values" ] || continue

  chart="$(basename "$(dirname "$values")")"

  # The library chart lives beside the service charts now rather than in a tree
  # of its own, so the glob reaches it. Its values.yaml carries a commented-out
  # `tag: v0.1.0` as documentation, which the grep below reads as a real pin —
  # left in, it fails the build against any VERSION other than the one that
  # happens to be written in the example.
  [ "$chart" = "_template" ] && continue

  seen=$((seen + 1))

  # Charts that render no workload have no image to check.
  grep -q 'tag:' "$values" || continue

  tag="$(grep -m1 'tag:' "$values" | sed 's/.*tag: *//' | tr -d '"'"'"' ')"
  if [ "$tag" != "$version" ]; then
    echo "$chart: chart asks for $tag, make builds $version"
    bad=1
  fi
done

# A glob that matches nothing expands to nothing, the loop never runs, and this
# script exits 0 having checked no charts at all — the guard is gone and nothing
# says so until a pod sits in ErrImageNeverPull several targets later. That is
# the exact failure this file exists to prevent, so a scan of zero charts is
# itself a failure.
if [ "$seen" -eq 0 ]; then
  echo "check-image-tags: no charts found under $repo/charts/*/values.yaml"
  echo "  the tag guard did not run. Fix the path before trusting this build."
  exit 1
fi

if [ "$bad" -ne 0 ]; then
  echo
  echo "Fix the tag in the chart, or build that version with VERSION=<tag>."
  exit 1
fi

echo "image tags $version ($seen charts)"
