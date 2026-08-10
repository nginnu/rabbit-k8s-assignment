#!/usr/bin/env bash
#
# Usecase: a customer buys a shirt.
#
# Five requests, each feeding the next, across four services. This is the one
# that matters — if it passes, routing, auth, the service-to-service calls and
# MariaDB all worked together.
#
#   ./tests/checkout.sh

cd "$(dirname "$0")" || exit 1
. ./lib.sh

require_cluster

ACCOUNT="${ACCOUNT:-alice}"
SECRET="${SECRET:-password}"
PRODUCT="${PRODUCT:-LIV-H-24}"
AMOUNT="${AMOUNT:-3290}"

login="{\"username\":\"$ACCOUNT\",\"password\":\"$SECRET\"}"

section "1. log in"
token=$(body POST /api/auth/login "$login" | json "d['token']")
if [ -z "$token" ]; then
  bad "cannot log in as $ACCOUNT — the rest of the journey needs a token"
  summary
fi
ok "logged in as $ACCOUNT"

section "2. browse the catalog"
products=$(body GET /api/products '' "$token")
count=$(printf '%s' "$products" | json "len(d)")

if [ "${count:-0}" -gt 0 ]; then
  ok "$count products listed"
else
  bad "no products — the seed did not load, or order-svc cannot reach MariaDB"
  summary
fi

if printf '%s' "$products" | grep -q "$PRODUCT"; then
  ok "$PRODUCT is in the catalog"
else
  bad "$PRODUCT is not in the catalog"
fi

section "3. place an order"
order=$(body POST /api/orders "{\"product_id\":\"$PRODUCT\"}" "$token")
order_id=$(printf '%s' "$order" | json "d['id']")
order_status=$(printf '%s' "$order" | json "d['status']")

if [ -n "$order_id" ]; then
  ok "order $order_id created"
else
  bad "no order id returned"
  summary
fi
expect "starts pending         " pending "$order_status"

section "4. pay"
# payment-svc calls order-svc to validate, then the bank, then order-svc again
# to mark it paid. One request, three services.
payment=$(body POST /api/payments "{\"order_id\":$order_id,\"amount\":$AMOUNT}" "$token")
payment_id=$(printf '%s' "$payment" | json "d['payment_id']")
gateway_ref=$(printf '%s' "$payment" | json "d['gateway_ref']")

if [ -n "$payment_id" ]; then
  ok "payment $payment_id charged"
else
  bad "payment failed — $payment"
  summary
fi

if [ -n "$gateway_ref" ]; then
  ok "the bank returned a reference"
  note "$gateway_ref"
else
  bad "no gateway reference — mock-payment was not reached"
fi

section "5. confirm"
confirm=$(body GET "/api/payments/$payment_id/success" '' "$token")
final=$(printf '%s' "$confirm" | json "d['status']")
expect "order is paid          " paid "$final"

section "the order is really in the database"
# Reading the row directly rather than trusting the API that just wrote it.
# A handler can return 200 on a transaction that rolled back.
db_status=$(mysql_q "SELECT status FROM orders WHERE id = $order_id")
expect "orders row says paid   " paid "$db_status"

db_payment=$(mysql_q "SELECT COUNT(*) FROM payments WHERE order_id = $order_id")
expect "one payment recorded   " 1 "$db_payment"

summary
