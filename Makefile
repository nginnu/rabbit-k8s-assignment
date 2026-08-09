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
	@kubectl apply -f $(MANIFESTS)/01-namespaces.yaml

GATEWAY_API_VERSION := v1.6.1

## gateway-api: install Gateway API CRDs — must run before Istio
gateway-api: cluster
	@kubectl apply --server-side --force-conflicts -f \
		https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml
	@kubectl wait --for=condition=established --timeout=60s \
		crd/gateways.gateway.networking.k8s.io \
		crd/httproutes.gateway.networking.k8s.io \
		crd/gatewayclasses.gateway.networking.k8s.io
	@# apply does not replace CRDs from an older bundle. The fields Istio needs
	@# would be dropped at admission time while the YAML still looks right, so
	@# check the version instead of assuming.
	@./platform/scripts/check-version.sh gateway-api $(GATEWAY_API_VERSION) \
		"$$(kubectl get crd gateways.gateway.networking.k8s.io \
			-o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}')"

ISTIO_VERSION := 1.30.3

## istio: install the control plane
istio: gateway-api
	@helm repo add istio https://istio-release.storage.googleapis.com/charts >/dev/null 2>&1 || true
	@helm repo update istio >/dev/null
	@helm upgrade --install istio-base istio/base -n istio-system --create-namespace \
		--version $(ISTIO_VERSION) --wait
	@helm upgrade --install istiod istio/istiod -n istio-system \
		--version $(ISTIO_VERSION) -f platform/addons/istio/local/values.yaml --wait --timeout 5m
	@# Older Istio ignores infrastructure.parametersRef on the Gateway without
	@# logging anything. The nodeSelector and hostPort go missing and :80 is
	@# unreachable several targets later, so check the version here.
	@./platform/scripts/check-version.sh istio $(ISTIO_VERSION) \
		"$$(kubectl -n istio-system get deploy istiod \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

CERT_MANAGER_VERSION := v1.21.1

## tls: install cert-manager and load the local CA it issues from
tls: istio
	@helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
	@helm repo update jetstack >/dev/null
	@helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager \
		--create-namespace --version $(CERT_MANAGER_VERSION) \
		--set crds.enabled=true --wait --timeout 5m
	@# The CA comes from mkcert on this machine, because the browser only trusts
	@# a CA that is already in the system keychain. Nothing inside the cluster
	@# can add one. cert-manager then issues the certificate from it.
	@./platform/scripts/seed-ca.sh
	@kubectl apply -f $(MANIFESTS)/02-certificates.yaml
	@kubectl -n istio-system wait --for=condition=Ready --timeout=120s \
		certificate/platform-tls

## gateway: create the Gateway and routes
gateway: namespaces tls
	@kubectl apply -f $(MANIFESTS)/03-gateway.yaml
	@kubectl -n istio-system wait --for=condition=Programmed --timeout=180s gateway/platform
	@kubectl apply -f $(MANIFESTS)/04-routes.yaml

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

## up: cluster, gateway, images, apps
up: cluster gateway images
	@$(MAKE) --no-print-directory apps
	@$(MAKE) --no-print-directory verify

## verify: prove the stack is actually working, not merely present
verify:
	@echo "── nodes ─────────────────────────────────────────"
	@kubectl get nodes -o custom-columns='NAME:.metadata.name,STATUS:.status.conditions[-1].type'
	@echo "── workloads ─────────────────────────────────────"
	@kubectl -n demo get pods \
		-o custom-columns='POD:.metadata.name,NODE:.spec.nodeName,STATUS:.status.phase'
	@echo "── from the browser ──────────────────────────────"
	@# No -k on the https call. Passing without it means curl checked the chain
	@# against the system trust store, which is the same thing a browser does
	@# before showing a padlock.
	@curl -sS -o /dev/null -w '  http://localhost/   -> HTTP %{http_code}\n' --max-time 10 http://localhost/
	@curl -sS -o /dev/null -w '  https://localhost/  -> HTTP %{http_code}  (%{ssl_verify_result} = verified)\n' --max-time 10 https://localhost/

## down: delete the cluster
down:
	@kind delete cluster --name $(CLUSTER)

.PHONY: help cluster namespaces gateway-api istio tls gateway images apps up verify down
