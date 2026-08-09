# Ingress + TLS

**Date:** 2026-08-09
**Status:** done — `https://localhost/` returns 200 with a trusted certificate

---

## Order matters

| # | Target | Why it has to come first |
|---|---|---|
| 1 | `gateway-api` | Istio only registers the GatewayClass if the CRDs exist at startup |
| 2 | `istio` | the Gateway object is what makes Istio create the Envoy deployment |
| 3 | `tls` | the Certificate needs cert-manager, and the Gateway needs the Secret |
| 4 | `gateway` | Gateway + HTTPRoute |

Installing Istio before the CRDs means the GatewayClass never appears and the
Gateway sits unprogrammed with no useful error.

---

## Where the certificate comes from

```
mkcert on this machine
  └── rootCA.pem + rootCA-key.pem      already trusted by the system keychain
        │  seed-ca.sh copies it into a Secret
        ▼
  cert-manager  ClusterIssuer "mkcert-ca"
        │  issues from that CA
        ▼
  Certificate "platform-tls"  →  Secret in istio-system
        │
        ▼
  Gateway listener :443
```

The CA has to come from outside. A browser only trusts a CA that is already in
the system keychain, and nothing running inside the cluster can put one there.
cert-manager issues the leaf certificate from it — the same flow as Let's
Encrypt, so only the issuer changes on a real domain.

---

## Files

| File | What |
|---|---|
| `platform/manifests/02-certificates.yaml` | ClusterIssuer + Certificate |
| `platform/manifests/03-gateway.yaml` | Gateway with :80 and :443 listeners |
| `platform/manifests/04-routes.yaml` | HTTPRoute → dummy |
| `platform/scripts/seed-ca.sh` | copies the mkcert CA into a Secret |
| `platform/addons/istio/local/values.yaml` | istiod sizing for kind |

---

## Result

```
http://localhost/   -> HTTP 200
https://localhost/  -> HTTP 200  (ssl_verify_result 0)

issuer   mkcert development CA
SAN      DNS:localhost, IP:127.0.0.1
TLS      1.3, HTTP/2
```

`ssl_verify_result 0` with no `-k` means curl checked the chain against the
system trust store and it passed — the same check a browser makes.

Port 80 stays open. It is the plaintext path to compare against when testing
that TLS actually encrypts.

---

## Things that cost time

**`exit` inside a Makefile recipe.** A multi-line `if ... exit 1; fi` got
mangled into `it 1` before it reached the shell. Moved the version checks into
`platform/scripts/check-version.sh`.

**Version pins are worth checking after install.** Istio below the pinned
version ignores `infrastructure.parametersRef` on the Gateway and logs nothing.
The Envoy pod loses its nodeSelector and hostPort, lands on the wrong node, and
`localhost` refuses the connection with everything looking correct in git.
