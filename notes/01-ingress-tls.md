# Ingress + TLS

**Date:** 2026-08-09
**Status:** done — `https://localhost/` returns 200 with a trusted certificate,
`tests/tls-proof.sh` (4 checks) proves it actually encrypts

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

![certificate valid in the browser](screenshot/tls/Screenshot%202569-08-08%20at%2021.14.53.png)

---

## Proving it actually encrypts

`ssl_verify_result 0` says the chain is trusted. It says nothing about what's
on the wire. `tests/tls-proof.sh` sends the same credentials over http and
https while tcpdump records both, then greps each capture for the password.

![tcpdump capturing http and https on the wire](screenshot/tls/Screenshot%202569-08-09%20at%2023.24.11.png)

| Check | Passes when |
|---|---|
| both captures have packets | http ≥ 10, https ≥ 10 — rules out an empty/misattached capture |
| http capture contains the password | ≥ 1 match — proves the test is observing real traffic |
| https capture contains the password | 0 matches — the actual claim |
| `ssl_verify_result` | 0 |

Capture happens inside the kind node, not on the Mac — Docker Desktop runs a
Linux VM, and capturing macOS `lo0` shows the hop to the proxy, not what
crosses the cluster network.

---

## Files

| File | What |
|---|---|
| `platform/manifests/02-certificates.yaml` | ClusterIssuer + Certificate |
| `platform/manifests/03-gateway.yaml` | Gateway with :80 and :443 listeners |
| `platform/manifests/04-routes.yaml` | HTTPRoute → dummy |
| `platform/scripts/seed-ca.sh` | copies the mkcert CA into a Secret |
| `platform/addons/istio/local/values.yaml` | istiod sizing for kind |
| `tests/tls-proof.sh` | the packet-capture proof, 4 checks |

---

## Result

```
http://localhost/   -> HTTP 200
https://localhost/  -> HTTP 200  (ssl_verify_result 0)

issuer   mkcert development CA
SAN      DNS:localhost, IP:127.0.0.1
TLS      1.3, HTTP/2

both captures recorded traffic (http 20 pkts, https 55 pkts)   PASS
http leaks the password in plaintext — 2 match(es)             PASS
https carries no plaintext password                            PASS
certificate chain verifies against the system trust store      PASS
```

Port 80 stays open — it is the plaintext path the proof compares against.

---

## Things that cost time

**`exit` inside a Makefile recipe.** A multi-line `if ... exit 1; fi` got
mangled into `it 1` before it reached the shell. Moved the version checks into
`platform/scripts/check-version.sh`.

**Version pins are worth checking after install.** Istio below the pinned
version ignores `infrastructure.parametersRef` on the Gateway and logs
nothing. The Envoy pod loses its nodeSelector and hostPort, lands on the wrong
node, and `localhost` refuses the connection with everything looking correct
in git.

**Byte count instead of packet count.** The first version of the proof
accepted any capture over 0 bytes — an empty pcap is still ~264 bytes of file
header. A real https exchange is ~9 KB, ~52 packets. Counting packets, not
bytes, is what actually separates a working capture from a broken one.

**A marker that appears in ordinary traffic.** Searching for `GET` matched the
request line, not the body, and over HTTP/2 there's no ASCII `GET` on the wire
at all — so the check passed for a reason unrelated to encryption. The marker
is now `hunter2-<epoch>-<random>`, which can only come from the body. Both
traps were found by running the test against a setup where it should fail.
