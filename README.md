# rabbit-k8s-assignment

A reproducible Kubernetes deployment, built locally on kind.

## Prerequisites

Docker, [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation),
kubectl, [Helm](https://helm.sh/docs/intro/install/) 3+, and
[mkcert](https://github.com/FiloSottile/mkcert) for the local TLS certificate.

```sh
brew install kind kubectl helm mkcert nss
mkcert -install     # adds the local CA to the system keychain
```

`mkcert -install` asks for your password. Without it the browser shows a
certificate warning — the CA has to be trusted by the machine, and nothing
inside the cluster can do that.

## Run it

```sh
make up       # create the cluster, install the gateway, build and deploy
make verify   # show what is running and check it responds
make down     # delete the cluster
make          # list every target
```

Then open **https://localhost/**

```sh
curl https://localhost/
# {"path":"/","service":"dummy","version":"v0.1.0"}
```

## Layout

```
apps/                        service sources and Dockerfiles
charts/
├── platform-service/        library chart — shared templates
└── apps/                    one chart per service; values only
platform/
├── kind/cluster.yaml        cluster definition
├── addons/istio/            istiod values
├── manifests/               namespaces, certificates, gateway, routes
└── scripts/                 helpers called from the Makefile
```

## Design notes

TODO
