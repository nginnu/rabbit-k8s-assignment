#!/usr/bin/env bash
#
# Copy the developer's mkcert CA into the cluster so cert-manager can issue
# from it.
#
#   ./platform/scripts/seed-ca.sh
#
# The CA has to come from outside the cluster. A browser only trusts a CA that
# is already in the machine's trust store, which is what `mkcert -install`
# does. Nothing running in the cluster can add one, so the CA is created on the
# machine and copied in; cert-manager issues the certificate from it.
#
# The private key is read from `mkcert -CAROOT` at run time and written into a
# Secret. It never goes into git.

set -euo pipefail

PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
NAMESPACE="${NAMESPACE:-cert-manager}"
SECRET="${SECRET:-mkcert-ca}"

if ! command -v mkcert >/dev/null 2>&1; then
  echo "mkcert not installed — run: brew install mkcert nss && mkcert -install"
  exit 1
fi

CAROOT="$(mkcert -CAROOT)"

if [ ! -f "$CAROOT/rootCA.pem" ] || [ ! -f "$CAROOT/rootCA-key.pem" ]; then
  echo "no CA at $CAROOT — run: mkcert -install"
  exit 1
fi

kubectl -n "$NAMESPACE" create secret tls "$SECRET" \
  --cert="$CAROOT/rootCA.pem" \
  --key="$CAROOT/rootCA-key.pem" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "seeded $SECRET in $NAMESPACE from $CAROOT"
