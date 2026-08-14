#!/usr/bin/env bash
#
# Proves the CNI cutover (kindnet -> Cilium) is real, and that a pod with no
# allow-rule to the database is actually refused, not just that `kubectl get
# netpol` looks right — see TODO.md's Deferred entry for why that mattered.
#
# Allow-path routing is already proven end to end by tests/routing.sh,
# tests/checkout.sh, tests/auth.sh — not repeated here.
#
# Cilium here is CNI + NetworkPolicy only; sidecar injection and mesh mTLS
# are a later phase, see notes/09-service-mesh-plan.md.
#
#   ./tests/mesh.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

# web-ui is the one pod this file execs into directly. lib.sh only ever names
# api because every other suite talks to the cluster over BASE (HTTP); a
# NetworkPolicy is proven from inside the denied pod, not from outside it.
WEB_NS="${WEB_NS:-web}"

section "cilium is the CNI, not kindnet"

if kubectl -n kube-system get daemonset cilium >/dev/null 2>&1; then
  ok "daemonset/cilium exists in kube-system"
else
  bad "daemonset/cilium not found — the cutover has not happened, or the install step never ran"
fi

if kubectl -n kube-system rollout status daemonset/cilium --timeout=120s >/dev/null 2>&1; then
  ok "every cilium pod is ready"
else
  bad "cilium daemonset is not fully rolled out — some node has no working CNI"
fi

# daemonset/cilium existing doesn't rule out kindnet also running — two CNIs
# owning pod routing at once is undefined, and the check above can't see it.
if kubectl -n kube-system get daemonset kindnet >/dev/null 2>&1; then
  bad "daemonset/kindnet still exists — kindnet was never removed, two CNIs are live at once"
else
  ok "kindnet is gone — cilium is the only CNI"
fi

section "every node is Ready"

# Catches Cilium coming up after other manifests in the Makefile — a node
# left without CNI stays NotReady, and the daemonset checks above never look
# at node state.
total=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
ready=$(kubectl get nodes --no-headers 2>/dev/null | awk '$2 == "Ready"' | wc -l | tr -d ' ')
note "$ready/$total nodes Ready"
expect "all nodes Ready         " "$total" "$ready"

section "denied path: web-ui cannot reach mariadb"

# platform/manifests/local/data/netpol.yaml opens mariadb:3306 only to
# auth-svc, order-svc, payment-svc in api, named explicitly, not by
# namespace. charts/apps/web-ui/values.yaml's networkPolicyPeers.egress is
# [alloy], never mariadb — web-ui in web has no allow-rule to 3306.
# TODO.md's Deferred section is why this check exists.
pod=$(kubectl -n "$WEB_NS" get pods -l app.kubernetes.io/name=web-ui \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)

if [ -z "$pod" ]; then
  bad "no web-ui pod found in $WEB_NS — cannot attempt the denied path"
else
  note "exec into $pod ($WEB_NS) — connecting to mariadb.data.svc.cluster.local:3306"
  # node, not nc/curl: web-ui's alpine image ships neither, but node is
  # already the container's own entrypoint binary — no debug image or extra
  # tool this project does not otherwise carry.
  result=$(kubectl -n "$WEB_NS" exec "$pod" -- node -e '
    const net = require("net");
    const s = net.createConnection({ host: "mariadb.data.svc.cluster.local", port: 3306, timeout: 5000 });
    s.on("connect", () => { console.log("CONNECTED"); s.destroy(); process.exit(0); });
    s.on("timeout", () => { console.log("DENIED:TIMEOUT"); s.destroy(); process.exit(1); });
    s.on("error", (e) => { console.log("DENIED:" + e.code); process.exit(1); });
  ' 2>&1)

  if printf '%s\n' "$result" | grep -q '^CONNECTED'; then
    bad "web-ui reached mariadb:3306 — the default-deny policy did not block it"
  elif printf '%s\n' "$result" | grep -q '^DENIED:'; then
    ok "web-ui was refused — $result"
  else
    bad "could not tell what happened — $result"
  fi
fi

summary
