#!/usr/bin/env bash
#
# Usecase: a request reaches the service that owns the path.
#
# The edge only — that each prefix goes to the right backend, that /api is
# rewritten before it arrives, and that the catch-all does not swallow the API.
#
#   ./tests/routing.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

section "the gateway is listening"
# BASE is https, so the http listener has to be asked for by name — otherwise
# both lines test the same port and one of them is decoration.
expect "http  /                " 200 "$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 http://localhost/ 2>/dev/null)"
expect "https /                " 200 "$(status GET /)"

section "each path reaches its own service"
# 401 rather than 200: these need a token. It is still the right answer, and it
# proves the request reached the service instead of stopping at the gateway —
# a missing route gives 404 from Traefik, not 401 from the app.
expect "/api/products          " 401 "$(status GET /api/products)"
expect "/api/orders            " 401 "$(status GET /api/orders)"
expect "/api/payments          " 401 "$(status POST /api/payments '{}')"
expect "/dummy                 " 200 "$(status GET /dummy)"

# auth/login is the one API path that takes no token, so it answers properly
# without one and shows the rewrite landed on the right handler.
expect "/api/auth/login        " 400 "$(status POST /api/auth/login '{}')"

section "the ui is served"
if body GET / | grep -qi '<!doctype html\|<html'; then
  ok "/ returned an HTML page"
else
  bad "/ did not return HTML — web-ui is not behind the catch-all"
fi

section "the catch-all does not shadow the api"
# / is a PathPrefix match and would swallow everything if the longest-prefix
# rule were not applied. JSON on an API path is the evidence.
if body GET /api/products | grep -q '^{\|^\['; then
  ok "/api/products returned JSON, not the UI page"
else
  bad "/api/products returned something else — the catch-all is shadowing it"
fi

section "an unknown path is refused"
# 404 from the gateway, because no route matches under /api.
expect "/api/nothing-here      " 404 "$(status GET /api/nothing-here)"

summary
