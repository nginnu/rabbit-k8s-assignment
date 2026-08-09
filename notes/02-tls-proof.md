# Proving TLS actually encrypts

**Date:** 2026-08-09
**Status:** done — `tests/tls-proof.sh`, 4 checks

---

## What it does

The same credentials go out twice, over http and over https, while tcpdump
records the wire on the control-plane node. Then it greps both captures.

| # | Check | Passes when |
|---|---|---|
| 1 | both captures have packets | http ≥ 10, https ≥ 10 |
| 2 | http capture contains the password | ≥ 1 match |
| 3 | https capture contains the password | 0 matches |
| 4 | `ssl_verify_result` | 0 |

```sh
./tests/tls-proof.sh
```

---

## Why checks 1 and 2 exist

Check 3 on its own is worthless. "The password is not in the https capture" is
also true when tcpdump attached to the wrong port, when the request failed, and
when the file was read before tcpdump flushed it. All three produce an empty
capture, and an empty capture looks exactly like a working one.

Check 2 sends the same string over http first. If it is not found there, the
test is not observing the request at all and says so.

---

## Two things that made it pass when it should not have

**Byte count instead of packet count.** The first version accepted any capture
over 0 bytes. An empty pcap is still ~264 bytes of file header, so a capture
that recorded nothing scored 319 bytes and passed. A real https exchange is
~9 KB and about 52 packets. Counting packets separates them; counting bytes
does not.

**A marker that appears in ordinary traffic.** Searching for `GET` matched 4
times in the http capture, so check 2 passed without proving the body was
readable — `GET` is in the request line, not the body. Over HTTP/2 there is no
ASCII `GET` on the wire at all, so check 3 was clean for a reason unrelated to
encryption. The marker is now `hunter2-<epoch>-<random>`, which can only come
from the body.

Both were found by running the test against a setup where it should fail.

---

## Result

```
both captures recorded traffic (http 20 pkts, https 55 pkts)   PASS
http leaks the password in plaintext — 2 match(es)             PASS
https carries no plaintext password                            PASS
certificate chain verifies against the system trust store      PASS
```

Deliberately broken runs, both correctly failing:

| Broken how | Which check catches it |
|---|---|
| capture port 443 while sending over http | 1 — empty capture |
| grep the http capture as if it were the https one | 3 — password found |

---

## Notes

tcpdump is not in the kind node image; the script installs it on first run.

Capture happens inside the node, not on the Mac. Docker Desktop runs a Linux
VM, and the traffic is inside it — capturing on macOS `lo0` shows the hop to
the proxy rather than what crosses the cluster network.
