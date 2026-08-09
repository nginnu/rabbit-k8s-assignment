# rabbit-k8s-assignment

A reproducible Kubernetes deployment, built locally on kind.

## Prerequisites

Docker, [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation),
kubectl, and [Helm](https://helm.sh/docs/intro/install/) 3+.

```sh
brew install kind kubectl helm     # macOS
```

## Run it

```sh
make up       # create the cluster, build and load the image, deploy
make verify   # show what is running and check it responds
make down     # delete the cluster
make          # list every target
```

## Layout

```
apps/                        service sources and Dockerfiles
charts/
├── platform-service/        library chart — shared templates
└── apps/                    one chart per service; values only
platform/
├── kind/cluster.yaml        cluster definition
└── manifests/               namespaces
```

## Design notes

TODO
