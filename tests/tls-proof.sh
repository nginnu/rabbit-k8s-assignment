#!/usr/bin/env bash
#
# Prove that TLS encrypts the traffic, by capturing it and looking.
#
#   tests/tls-proof.sh [cluster-name]
#
# The same credentials are sent twice, once over http and once over https, while
# tcpdump records the wire. The http capture must contain the password and the
# https capture must not.
#
# Sending it over http first is the control. Without it, "the password is not in
# the https capture" could equally mean the capture missed the traffic, and the
# test would pass on a cluster with TLS switched off.

set -euo pipefail

CLUSTER="${1:-rabbit-k8s-test}"
NODE="${CLUSTER}-control-plane"

# Traffic reaches the cluster through the control-plane node's host port
# mappings, so that is the interface the request crosses.

# A marker that cannot occur in the traffic by chance. Searching for a word that
# also appears in ordinary headers — "GET", "localhost" — matches for reasons
# unrelated to the body, and the http control then passes without proving the
# body was visible.
PASSWORD="hunter2-$(date +%s)-${RANDOM}${RANDOM}"
CARD="4111111111111111"
BODY="{\"username\":\"alice\",\"password\":\"${PASSWORD}\",\"card\":\"${CARD}\"}"

pass=0
fail=0

ok()   { printf '  \033[32mPASS\033[0m  %s\n' "$1"; pass=$((pass + 1)); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$1"; fail=$((fail + 1)); }

cleanup() {
  docker exec "$NODE" rm -f /tmp/tls-proof-http.pcap /tmp/tls-proof-https.pcap 2>/dev/null || true
}
trap cleanup EXIT

# --- preconditions --------------------------------------------------------

if ! docker ps --format '{{.Names}}' | grep -qx "$NODE"; then
  echo "node $NODE is not running — start the cluster with 'make up'"
  exit 1
fi

if ! docker exec "$NODE" which tcpdump >/dev/null 2>&1; then
  echo "installing tcpdump in $NODE"
  docker exec "$NODE" bash -c \
    'apt-get update -qq && apt-get install -y -qq tcpdump' >/dev/null 2>&1
fi

# --- capture --------------------------------------------------------------

# Returns the number of times the password appears in what crossed the wire.
#
# tcpdump is backgrounded, given a moment to attach, then signalled and waited
# on. Reading the file before tcpdump has flushed and closed it produces a
# truncated capture, and a truncated capture reads as an empty one — which would
# look exactly like encryption.
capture() {
  local port="$1" url="$2" file="/tmp/tls-proof-$3.pcap"

  docker exec -d "$NODE" \
    tcpdump -i any -n -s 0 -w "$file" "tcp port $port" >/dev/null 2>&1
  sleep 2

  curl -sS -o /dev/null --max-time 10 \
    -X POST "$url" -H 'Content-Type: application/json' -d "$BODY" || true
  sleep 1

  docker exec "$NODE" pkill -INT tcpdump >/dev/null 2>&1 || true
  sleep 1

  docker exec "$NODE" tcpdump -r "$file" -A -n 2>/dev/null \
    | grep -c "$PASSWORD" || true
}

# Packets, not bytes. An empty pcap is still ~264 bytes of file header, so a
# byte count above zero does not mean anything was recorded.
packets() {
  docker exec "$NODE" tcpdump -r "/tmp/tls-proof-$1.pcap" -n 2>/dev/null \
    | wc -l | tr -d ' '
}

echo "── sending credentials over http and https ───────"
echo "   password: $PASSWORD"
echo

http_hits=$(capture 80 http://localhost/login http)
https_hits=$(capture 443 https://localhost/login https)
http_packets=$(packets http)
https_packets=$(packets https)

# A TLS handshake plus the request is around 50 packets; a request that never
# reached this port is 0. Ten separates them without being brittle.
MIN_PACKETS=10

# --- results --------------------------------------------------------------

echo "── did the capture actually see traffic? ─────────"

# Guards against a false pass: an empty https capture proves nothing. If the
# request never crossed port 443, "no plaintext found" is a statement about an
# empty file, not about encryption.
if [ "$http_packets" -ge "$MIN_PACKETS" ] && [ "$https_packets" -ge "$MIN_PACKETS" ]; then
  ok "both captures recorded traffic (http ${http_packets} pkts, https ${https_packets} pkts)"
else
  bad "a capture is empty (http ${http_packets} pkts, https ${https_packets} pkts) — tcpdump missed the traffic"
fi

if [ "$http_hits" -gt 0 ]; then
  ok "http leaks the password in plaintext — $http_hits match(es), as expected"
else
  bad "http capture has no password — the test is not observing the request"
fi

# The https result below is only meaningful for a string that would show up when
# TLS is off. This is the same marker, matched in the http capture above, so a
# clean https capture is about encryption rather than about the marker.
echo "   marker searched: ${PASSWORD}"

echo
echo "── the actual check ──────────────────────────────"

if [ "$https_hits" -eq 0 ]; then
  ok "https carries no plaintext password"
else
  bad "https leaked the password — $https_hits match(es)"
fi

verify=$(curl -sS -o /dev/null -w '%{ssl_verify_result}' --max-time 10 https://localhost/ || echo 99)
if [ "$verify" = "0" ]; then
  ok "certificate chain verifies against the system trust store"
else
  bad "certificate did not verify (ssl_verify_result=$verify)"
fi

echo
if [ "$fail" -eq 0 ]; then
  printf '\033[32m%d passed\033[0m\n' "$pass"
  exit 0
fi
printf '\033[31m%d passed, %d failed\033[0m\n' "$pass" "$fail"
exit 1
