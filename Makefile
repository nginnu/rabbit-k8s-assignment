# Local platform — one command per thing you actually want to do.
#
# kind and helm live in /usr/local/bin, which is not always on a login shell's
# PATH; prepend it so `make` works regardless of how the shell was started.
export PATH := /usr/local/bin:$(PATH)

CLUSTER   ?= rabbit-k8s-test
MANIFESTS := platform/manifests
VERSION   ?= v0.1.0

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F: '{printf "  \033[36m%-16s\033[0m%s\n", $$1, $$2}'

## cluster: create the kind cluster
cluster:
	@kind get clusters | grep -qx $(CLUSTER) \
		&& echo "cluster $(CLUSTER) already exists" \
		|| kind create cluster --name $(CLUSTER) \
			--config platform/kind/cluster.yaml --wait 120s

## namespaces: create the namespaces everything else lands in
namespaces: cluster
	@kubectl apply -f $(MANIFESTS)/00-namespaces.yaml

## images: build every image and load it into the cluster
images:
	@docker build --build-arg VERSION=$(VERSION) -t dummy:$(VERSION) apps/dummy
	@# kind nodes have their own image store and no registry to pull from, so an
	@# image built on the host is invisible until it is loaded in.
	@kind load docker-image --name $(CLUSTER) dummy:$(VERSION)

# One chart per service, one release per service: a service is upgraded or
# rolled back without touching its siblings.
APP_CHARTS := dummy

## apps: deploy the application services from their charts
apps: namespaces
	@for c in $(APP_CHARTS); do \
		helm dependency update charts/apps/$$c >/dev/null; \
		helm upgrade --install $$c charts/apps/$$c -n demo; \
	done
	@for d in $(APP_CHARTS); do \
		kubectl -n demo rollout status deployment/$$d --timeout=180s; \
	done

## up: cluster, images, apps
up: cluster images
	@$(MAKE) --no-print-directory apps
	@$(MAKE) --no-print-directory verify

## verify: prove the stack is actually working, not merely present
verify:
	@echo "── nodes ─────────────────────────────────────────"
	@kubectl get nodes -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[-1].type'
	@echo "── workloads ─────────────────────────────────────"
	@kubectl -n demo get pods \
		-o custom-columns='POD:.metadata.name,NODE:.spec.nodeName,STATUS:.status.phase'
	@echo "── the app answers ───────────────────────────────"
	@# Run detached and read the logs rather than `--rm -i`: the attached form
	@# swallows the container's stdout often enough that a passing check prints
	@# nothing, which is worse than a failing one.
	@kubectl -n demo delete pod curl-check --ignore-not-found >/dev/null 2>&1
	@kubectl -n demo run curl-check --restart=Never --image=curlimages/curl:8.11.1 -- \
		curl -sS -o /dev/null -w 'GET /healthz -> HTTP %{http_code}' \
		http://dummy.demo.svc.cluster.local/healthz >/dev/null
	@kubectl -n demo wait --for=jsonpath='{.status.phase}'=Succeeded pod/curl-check --timeout=60s >/dev/null
	@kubectl -n demo logs curl-check
	@echo
	@kubectl -n demo delete pod curl-check >/dev/null

## down: delete the cluster
down:
	@kind delete cluster --name $(CLUSTER)

.PHONY: help cluster namespaces images apps up verify down
