#!/usr/bin/env bash
#
# Usecase: only a user with valid credentials gets in, and the token is what
# opens the rest of the API.
#
#   ./tests/auth.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

# Not USER or PASS — the shell already exports USER, and a test that reads it
# silently logs in as whoever is running the script.
ACCOUNT="${ACCOUNT:-alice}"
SECRET="${SECRET:-password}"

# Payloads are built into variables rather than inlined into the expect call.
# A JSON literal written inside `expect "..." 200 "$(status ... "{...}")"` gets
# word-split on the comma, and curl is handed two half-objects: the request
# fails with 400 and the test looks like a broken service.
good="{\"username\":\"$ACCOUNT\",\"password\":\"$SECRET\"}"
wrong_pass="{\"username\":\"$ACCOUNT\",\"password\":\"nope\"}"
unknown='{"username":"nobody","password":"password"}'

section "logging in"
expect "correct password       " 200 "$(status POST /api/auth/login "$good")"
expect "wrong password         " 401 "$(status POST /api/auth/login "$wrong_pass")"
expect "unknown user           " 401 "$(status POST /api/auth/login "$unknown")"
expect "empty body             " 400 "$(status POST /api/auth/login '{}')"

section "the token"
token=$(body POST /api/auth/login "$good" | json "d['token']")

if [ -n "$token" ]; then
  ok "login returned a token"
else
  bad "login returned no token — nothing below can pass"
  summary
fi

# A JWT is three dot-separated parts. Checking the shape catches a handler that
# returns a session id or an opaque string where a JWT is expected.
if [ "$(printf '%s' "$token" | awk -F. '{print NF}')" = "3" ]; then
  ok "the token is a JWT — three parts"
else
  bad "the token is not a JWT"
fi

# The session id inside the token is what ties one user's requests together
# across services. Without it the journey cannot be followed later.
sid=$(printf '%s' "$token" | cut -d. -f2 | python3 -c "
import sys, base64, json
p = sys.stdin.read().strip(); p += '=' * (-len(p) % 4)
print(json.loads(base64.urlsafe_b64decode(p)).get('sid', ''))" 2>/dev/null)

if [ -n "$sid" ]; then
  ok "the token carries a session id"
  note "sid $sid"
else
  bad "no session id in the token — requests cannot be correlated"
fi

section "the token is what opens the api"
# /api/orders, not /api/products: the catalog went public, so a 401-then-200
# against /api/products would now pass on every request regardless of the
# token — this suite would stay green while proving nothing. /api/orders
# stays behind middleware.Auth, so 401/401/200 here is still contingent on
# the token actually being valid.
expect "no token               " 401 "$(status GET /api/orders)"
expect "junk token             " 401 "$(status GET /api/orders '' 'not-a-real-token')"
expect "valid token            " 200 "$(status GET /api/orders '' "$token")"

summary
