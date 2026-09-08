# Running this from a fresh clone

Nine steps, in order. Every step has something to run that says whether it
worked. Stop at the first one that does not.

---

## 1. Host tools

| Tool | Needed for | Fails without it |
|---|---|---|
| Docker (running) | kind nodes are containers | `kind get clusters` errors before anything starts |
| kind | the cluster | — |
| kubectl | everything | — |
| Helm 3 | every addon and every app chart | — |
| mkcert + nss | the CA the browser trusts | `make tls` stops at `scripts/seed-ca.sh` |
| curl | the test suites and `make verify` | — |
| Go ≥ 1.26 | `tests/unit.sh` only | the cluster suites still run |
| k6 | `k6 run tests/k6-*.js` only | optional, not part of `make test` |

```sh
brew install kind kubectl helm mkcert nss go k6
```

Give Docker Desktop room for three kind nodes, Istio sidecars on every app pod
and five LGTM releases — the 4 GB default leaves pods `Pending` or OOMKilled.
8 GB / 4 CPUs is my own judgement, not a measured floor.

## 2. Trust the local CA

```sh
mkcert -install
```

Asks for your password. `make tls` copies `$(mkcert -CAROOT)/rootCA*.pem` into
the cluster as the `mkcert-ca` secret and cert-manager issues from it. The CA
has to be trusted by the machine, and nothing inside the cluster can do that —
skip this and every `https://` URL below shows a certificate warning.

## 3. Clone the four repos side by side

```
rabbit-assignment/
├── rabbit-k8s-assignment/   ← this repo, run make from here
├── rabbit-api/              ← Go services (shop-api, notification-api)
├── rabbit-web/              ← Next.js storefront
└── rabbit-gitops/           ← charts + Argo CD bootstrap
```

```sh
for r in rabbit-k8s-assignment rabbit-api rabbit-web rabbit-gitops; do
  git clone https://github.com/nginnu/$r.git
done
cd rabbit-k8s-assignment && make preflight
```

`make up` builds images from `../rabbit-api` and `../rabbit-web` and installs
charts from `../rabbit-gitops/charts`. Cloned somewhere else:

```sh
make up API_REPO=<path> WEB_REPO=<path> GITOPS=<path>
```

`make preflight` is what tells you this is wrong — it runs before the cluster is
created, so a missing sibling repo fails in a second instead of ten minutes in.

## 4. Free ports 80 and 443

kind maps them onto the control-plane node (`k8s-local/cluster.yaml`). Anything
already listening takes the cluster's place and every URL below answers from the
wrong process.

```sh
sudo lsof -nP -iTCP:80 -iTCP:443 -sTCP:LISTEN
```

## 5. `make up`

```sh
make up
```

```
preflight ─┬─ cluster ── cilium ─┬─ namespaces ─┬─ istio ── istio-gateway
           │   (3 nodes,         │   (+ default- │   (sidecar mesh, STRICT mTLS)
           │   hostPort 80/443)  │    deny netpol)│
           │                     │              ├─ observability ─┬─ kiali
           │                     │              │   (prom·loki·tempo·alloy·grafana)
           │                     │              ├─ rollouts
           │                     │              └─ argocd
           │                     │
           │                     └─ gateway-api ── networking ── traefik ── tls ── gateway ── routes
           │                          (CRDs)        (GatewayClass)            (mkcert CA)  (Programmed)
           │
           └─ images ──────────────────────────── apps ── verify
               (docker build + kind load)          │
                                                   └─ data (mariadb·redis·init job) + secrets
```

Order that is not obvious, and what breaks if it moves:

| Step | Why here |
|---|---|
| `cilium` right after `cluster` | `disableDefaultCNI: true` — no pod schedules anywhere until the CNI is in |
| `gateway-api` before `traefik` | the GatewayClass never registers, and the Gateway sits unprogrammed with no error |
| `tls` before `gateway` | a Gateway reads `certificateRefs` only from its own namespace — the cert is issued into `gateway` |
| `observability` before `apps` | the first requests are captured; a missing collector costs telemetry, not availability |
| `data` inside `apps` | `make up` waits on the `mariadb-init` Job, not just the StatefulSet — services would otherwise start against empty tables |

**The first run is slow.** Every image is built, three node images are pulled,
and around fifteen upstream charts come over the network.

`verify` runs last on its own and prints the nodes, the pods in `web` and `api`,
and three HTTP codes.

**Check it worked:**

```sh
kubectl get pods -A --no-headers | awk '$4!="Running" && $4!="Completed"'
make verify
```

The first prints nothing when the cluster is healthy — 64 pods across 9
namespaces here. `make verify` reprints the summary at any time; it changes
nothing.

Expected from `verify`: `http://localhost/ -> 302` (the redirect to https),
`https://localhost/ -> 200` with `ssl_verify_result 0` — 0 is the whole point,
it means curl validated the chain against the mkcert CA with no `-k`.

## 6. Look at it

| URL | Login |
|---|---|
| `https://localhost` | `alice` / `password` (seeded, bcrypt, lab only) |
| `https://grafana.localhost` | none — anonymous Admin is on |
| `https://argocd.localhost` | `admin` / the secret below |
| `https://kiali.localhost` | none |
| `https://rollouts.localhost` | none |
| `https://traefik.localhost` | none |
| `https://hubble.localhost` | none |

```sh
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

`*.localhost` resolves to 127.0.0.1 on macOS with no `/etc/hosts` entry. On
Linux that depends on the resolver — systemd-resolved does it, plain glibc does
not; add the names to `/etc/hosts` if they do not resolve.

Other seeded accounts: `bob` `hana` `felix` `sara` `lily` (admin), `user_001`
… `user_020` (qa, the canary cohort), `user_021` … `user_100` (member). Same
password.

## 7. Test it

```sh
make test          # every suite, cheapest first
make test-auth     # or one at a time
```

Details and the per-suite table: [testing.md](testing.md).

Known red before you start, so a failure here is not something you caused.
This is the last full-suite run on this tree, not a fresh one:

| Suite | Why it fails today |
|---|---|
| `unit.sh` | points at `../apps/…`; the Go sources moved to `rabbit-api` |
| `preflight.sh` | reads `platform/kind/cluster.yaml`; the layout is `k8s-local/` |
| `routing.sh` | expects 200 on `http://localhost`, gets the 302 to https |
| `route-isolation.sh` | asserts `x-envoy-decorator-operation`, which is gone |
| `mesh.sh` | cilium is not enforcing web-ui's default-deny to mariadb |
| `o11y-stack.sh` | grafana answers 308 |
| `o11y-journey.sh` | payment, notification and gorm spans missing from the trace |

Green at that same baseline: `istio` (39/39), `auth`, `checkout`, `tls-proof`,
`resilience`.

## 8. GitOps — separate, and only if you own the git repo

`make up` does **not** run it. `make apps` installs the charts with Helm
directly; `make gitops` hands the same charts to Argo CD.

```sh
make gitops
```

It applies `rabbit-gitops/bootstrap/bootstrap.yaml`, waits for one Application
per chart, then for all of them to be Synced and Healthy. Run it after
`make up`, never before: applied first, Argo CD creates the objects itself
with no `meta.helm.sh` ownership, and the `helm upgrade --install` inside
`make apps` then refuses to adopt them.

Every rebuild needs it again — see step 9.

**Check it worked:**

```sh
kubectl -n argocd get applications.argoproj.io
```

Eight Applications — `auth catalog order payment notification web-ui
platform-config bootstrap` — every one `Synced` and `Healthy`. `make gitops`
already waits for exactly that and exits 1 otherwise, so a target that returned
0 has proven it; the command above is for looking again later.

`Synced` alone is not enough. An Application can be Synced and `Degraded` at
the same time — git was applied, the workload underneath it is not healthy.

Both files point at `https://github.com/nginnu/rabbit-gitops.git`, branch
`main`. Argo CD polls **GitHub, not your working tree** — a local commit that is
not pushed changes nothing. Working from a fork means editing `repoURL` in
`bootstrap/bootstrap.yaml` and `bootstrap/applicationset.yaml` and pushing that
first, or every Application syncs someone else's charts.

## 9. Tear down and rebuild

```sh
make down      # deletes the cluster — the database goes with it
make up
make gitops    # step 8 again — a new cluster has no Argo CD Applications
```

`make down` is not optional before a rebuild: `cluster` skips creation when
`rabbit-k8s-test` already exists, so `make up` alone leaves the old one in
place.

`make gitops` is not optional either, unless you mean to run without GitOps.
It is not part of `make up`, and `make down` took the Applications with the
cluster — rebuild without it and Argo CD is installed but managing nothing.

| Action | Database |
|---|---|
| `kubectl delete pod mariadb-0` | survives — the PVC is untouched |
| `kubectl delete sts mariadb` | survives — the PVC is left behind |
| `kubectl delete pvc data-mariadb-0` | gone |
| `make down` | gone — kind's `standard` storageclass writes inside the node container |

---

## Traps

| Trap | What you see |
|---|---|
| `mkcert -install` skipped | browser warning on every URL; `make test-tls` catches it |
| mkcert generated a new CA after `make up` | same warning — re-run `make tls` |
| A chart pins a tag that was not built | `ErrImageNeverPull`, several targets after the real mistake — `check-image-tags.sh` fails the build first |
| `kubectl apply` over CRDs from an older bundle | fields dropped at admission, the YAML still looks right |
| Argo CD "Synced" but nothing changed | the commit was never pushed |
| Docker was restarted with a cluster already built | the three node containers sit `Exited`; `make up` stops at `kubernetes: pinned v1.36.1, running nothing`. `docker start rabbit-k8s-test-control-plane rabbit-k8s-test-worker rabbit-k8s-test-worker2`, then wait until no pod reads `Unknown` — about a minute here |
