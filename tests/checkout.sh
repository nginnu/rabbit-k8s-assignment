#!/usr/bin/env bash
#
# Usecase: a customer buys a shirt.
#
# Five requests, each feeding the next, across five services. This is the one
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

section "the notification leg"
# payment-svc also POSTs /notify to the notification service once the payment
# settles. Nothing above this section would notice that leg missing: the call
# is deliberately non-fatal, so a purchase can fully succeed with it broken.

# 200, not the body's content, is the claim here — the route strip and the
# endpoint exist. notification registers exact paths, so /notification has to
# be rewritten to /notifications on the way in; an unstripped prefix matches
# no handler and this 404s.
expect "notifications endpoint  " 200 "$(status GET /notification/notifications)"

# The proof the call happened is the notification pod's own log line, not a
# read-back GET on the endpoint above: the history is in-memory and per-pod,
# five replicas sit behind one Service, and a GET lands on a random one — a
# miss would prove nothing about the pod that did receive this order. The
# line is written by another pod on another schedule, so the grep retries
# rather than racing it.
received=0
for _ in $(seq 1 10); do
  received=$(kubectl -n "$API_NS" logs -l app.kubernetes.io/name=notification --prefix --tail=-1 2>/dev/null \
    | grep '"notification received"' | grep "\"order_id\":$order_id," \
    | grep -c "\"payment_id\":$payment_id,")
  [ "${received:-0}" -ge 1 ] && break
  sleep 1
done
if [ "${received:-0}" -ge 1 ]; then
  ok "notification received order $order_id / payment $payment_id"
else
  bad "no \"notification received\" line for order $order_id — /notify was not called, or it failed non-fatally"
fi

# The caller's side of the same call. Either line alone could be stale from an
# earlier run; the pair — payment-svc saying sent, notification saying
# received, both carrying this payment's id — is what makes it proof.
sent=0
for _ in $(seq 1 10); do
  sent=$(kubectl -n "$API_NS" logs -l app.kubernetes.io/name=payment-svc --prefix --tail=-1 2>/dev/null \
    | grep '"notification sent"' | grep "\"order_id\":$order_id," \
    | grep -c "\"payment_id\":$payment_id,")
  [ "${sent:-0}" -ge 1 ] && break
  sleep 1
done
if [ "${sent:-0}" -ge 1 ]; then
  ok "payment-svc logged \"notification sent\" for payment $payment_id"
else
  bad "no \"notification sent\" line in payment-svc for payment $payment_id — the leg failed before it reached notification"
fi

section "failure simulation"
# /notify-failed is the manual failure simulator: five error lines and a 500,
# so a failing notification can be produced on demand and looked for in Loki.
# The 500 is the correct answer, not a suite failure — what has to be true is
# that the burst reaches the pod log, because that is what Alloy ships.
# The real order and payment ids, not zeros: the handler rejects order_id 0
# with a 400, and the ids make this burst greppably unique against the
# received line checked above.
fail_code=$(status POST /notification/notify-failed \
  "{\"order_id\":$order_id,\"payment_id\":$payment_id,\"amount\":$AMOUNT,\"user_id\":0}")
expect "notify-failed answers  " 500 "$fail_code"

burst=0
for _ in $(seq 1 10); do
  burst=$(kubectl -n "$API_NS" logs -l app.kubernetes.io/name=notification --prefix --tail=-1 2>/dev/null \
    | grep '"notification delivery failed"' | grep "\"order_id\":$order_id," \
    | grep -c "\"payment_id\":$payment_id,")
  [ "${burst:-0}" -ge 1 ] && break
  sleep 1
done
if [ "${burst:-0}" -ge 1 ]; then
  ok "the failure burst is in the notification log, where Alloy can ship it to Loki"
else
  bad "no \"notification delivery failed\" line in the notification log — the simulator's burst never reached the pod log Loki is fed from"
fi

summary
