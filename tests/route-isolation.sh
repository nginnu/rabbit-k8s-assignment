#!/usr/bin/env bash
#
# Usecase: a console host is answered by its own backend, never by a
# storefront route that attached to it by accident.
#
# platform/manifests/apps/api/route.yaml and .../web/route.yaml declared no
# `hostnames`, so both routes matched every Host on the platform Gateway.
# Traefik ranks a match by path length first and barely weighs hostname, so
# /api/auth (any host) outranked Host("kiali.localhost") && PathPrefix("/") —
# measured priorities were 10904 vs 17. kiali.localhost/api/auth/info came
# back from auth-svc: 404, `server: istio-envoy`,
# `x-envoy-decorator-operation: auth-svc.api.svc.cluster.local:80/*`. Kiali's
# own answer for that path, read straight from the pod by port-forward before
# this test was written, is 200 with body {"sessionInfo":{},"strategy":
# "anonymous"}.
#
# tests/routing.sh already proves each storefront path reaches its own
# service on the storefront's own host. This file proves the same routes do
# NOT also answer on a console's host once `hostnames` scopes them there —
# scoping a route can break it as easily as fix it, so both directions are
# checked.
#
#   ./tests/route-isolation.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

# lib.sh's status()/body() are pinned to $BASE (https://localhost) — every
# check below needs a different Host, so this file talks to curl directly
# instead of routing around lib.sh's single-host helpers.

# code <url> [method] [data] — HTTP status only, "000" if the call did not
# complete. GET by default, same as lib.sh's status(); POST with a body is
# passed explicitly where a handler only answers on POST (auth/login below —
# a bare GET hits no route inside auth-svc and returns 404 of its own, which
# would misread as "the console answered" if this defaulted silently).
code() {
  local url="$1" method="${2:-GET}" data="${3:-}"
  local args=(-sS -o /dev/null -w '%{http_code}' --max-time 15 -X "$method")
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' -d "$data")
  curl "${args[@]}" "$url" 2>/dev/null
}

# header <name> <url> [method] [data] — one response header, unfolded and
# stripped of its name, empty if absent. Same mktemp/-D/grep/tr/sed shape
# o11y-stack.sh and o11y-journey.sh already use to read a header off a live
# response.
header() {
  local name="$1" url="$2" method="${3:-GET}" data="${4:-}" tmp val
  tmp=$(mktemp)
  local args=(-sS -o /dev/null -D "$tmp" --max-time 15 -X "$method")
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' -d "$data")
  curl "${args[@]}" "$url" 2>/dev/null
  val=$(grep -i "^$name:" "$tmp" | tr -d '\r' | sed 's/^[^:]*: *//')
  rm -f "$tmp"
  printf '%s' "$val"
}

section "the exact regression: kiali answers its own auth endpoint"

# 404 is the bug back — the request reached the gateway (not 000) but landed
# on auth-svc, which has no /api/auth/info handler and returns plain-text 404.
# A fixed route reaches Kiali's own handler instead.
expect "kiali.localhost/api/auth/info status" 200 "$(code https://kiali.localhost/api/auth/info)"

kiali_body=$(curl -sS --max-time 15 https://kiali.localhost/api/auth/info 2>/dev/null)
if printf '%s' "$kiali_body" | grep -q 'strategy'; then
  ok "kiali.localhost/api/auth/info body carries \"strategy\" — $kiali_body"
else
  bad "kiali.localhost/api/auth/info body has no \"strategy\" — $kiali_body"
fi

# The discriminator used everywhere below: istio only injects a sidecar into
# auth-svc, order-svc, payment-svc, payment-gateway and notification in the api
# namespace (tests/istio.sh). observability, argocd, kube-system and traefik
# carry no sidecar, so `server: istio-envoy` is never legitimate on a console
# response — only a storefront route answering in the console's place can
# produce it.
kiali_server=$(header server https://kiali.localhost/api/auth/info)
case "$kiali_server" in
  *istio-envoy*)
    bad "kiali.localhost/api/auth/info still carries server: istio-envoy — auth-svc answered, not kiali" ;;
  *)
    ok "kiali.localhost/api/auth/info carries no istio-envoy fingerprint — kiali answered" ;;
esac

section "the storefront still works on its own host"

# Scoping api/web-ui to hostnames: [localhost] can overcorrect as easily as
# fix the bug — a typo'd host or a dropped entry breaks the storefront the
# same way the missing hostnames hid it. routing.sh already checks this path
# status by status; this adds proof the response actually came from auth-svc,
# so a route that happens to return 400 for the wrong reason cannot pass
# silently.
expect "localhost/api/auth/login status " 400 "$(code https://localhost/api/auth/login POST '{}')"

login_decorator=$(header x-envoy-decorator-operation https://localhost/api/auth/login POST '{}')
case "$login_decorator" in
  auth-svc.*)
    ok "localhost/api/auth/login answered by $login_decorator" ;;
  "")
    bad "localhost/api/auth/login carries no x-envoy-decorator-operation — cannot confirm auth-svc answered" ;;
  *)
    bad "localhost/api/auth/login answered by $login_decorator, not auth-svc" ;;
esac

section "console hosts are not hijacked by the storefront prefixes"

# 5 console hosts x 5 storefront prefixes that can steal them. Only the
# istio-envoy fingerprint is checked, never a status code — what each console
# legitimately answers on a path it does not own (its own 404, its own auth
# challenge) was not observed ahead of writing this test, and guessing it
# here would make a false positive read as a pass. Kiali answering 404 for a
# path of its own, like /api/istio/certs, is not a hijack; the fingerprint is
# what tells the two apart.
HOSTS="kiali grafana argocd hubble traefik"
PREFIXES="/api/auth/info /api/products /api/orders /api/payments /notification"

for host in $HOSTS; do
  for prefix in $PREFIXES; do
    url="https://$host.localhost$prefix"
    c=$(code "$url")
    if [ "$c" = "000" ]; then
      bad "$host.localhost$prefix — no response, cannot tell who answered"
      continue
    fi
    server=$(header server "$url")
    case "$server" in
      *istio-envoy*)
        dec=$(header x-envoy-decorator-operation "$url")
        bad "$host.localhost$prefix — hijacked, $c from ${dec:-a meshed api pod}" ;;
      *)
        ok "$host.localhost$prefix — $c, no istio-envoy fingerprint, $host answered" ;;
    esac
  done
done

summary
