# Local platform — one command per thing you actually want to do.
#
# kind and helm live in /usr/local/bin, which a login shell does not always have
# on PATH.
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
	@# apply does not replace CRDs from an older bundle, and the fields Istio
	@# needs are then dropped at admission while the YAML still looks right.
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
	@# Older Istio ignores parametersRef on the Gateway and logs nothing: the
	@# nodeSelector and hostPort go missing and :80 fails several targets later.
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
	@# The CA has to come from mkcert on this machine: a browser only trusts a CA
	@# already in the system keychain, and nothing in the cluster can add one.
	@./platform/scripts/seed-ca.sh
	@kubectl apply -f $(MANIFESTS)/02-certificates.yaml
	@kubectl -n istio-system wait --for=condition=Ready --timeout=120s \
		certificate/platform-tls

## gateway: create the Gateway and routes
gateway: namespaces tls
	@kubectl apply -f $(MANIFESTS)/03-gateway.yaml
	@kubectl -n istio-system wait --for=condition=Programmed --timeout=180s gateway/platform
	@kubectl apply -f $(MANIFESTS)/04-routes.yaml

DATA_NS := data

## secrets: generate credentials into the cluster, never into git
secrets: namespaces
	@# Created once: regenerating would change the password out from under a
	@# database that still has the old one.
	@kubectl -n $(DATA_NS) get secret mariadb >/dev/null 2>&1 \
		&& echo "secret mariadb already exists — delete it to rotate" \
		|| kubectl -n $(DATA_NS) create secret generic mariadb \
			--from-literal=username=rabbitshop \
			--from-literal=password=$$(openssl rand -hex 16) \
			--from-literal=root-password=$$(openssl rand -hex 16)

## app-secrets: build the app credentials from the database ones
app-secrets: secrets
	@DBPASS=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.password}' | base64 -d); \
	DBUSER=$$(kubectl -n $(DATA_NS) get secret mariadb -o jsonpath='{.data.username}' | base64 -d); \
	kubectl -n demo get secret app-secrets >/dev/null 2>&1 \
		|| kubectl -n demo create secret generic app-secrets \
			--from-literal=mariadb-dsn="$$DBUSER:$$DBPASS@tcp(mariadb.$(DATA_NS).svc.cluster.local:3306)/rabbitshop?parseTime=true" \
			--from-literal=jwt-secret=$$(openssl rand -hex 32)

## sql: load the schema and seed into a ConfigMap
sql: namespaces
	@kubectl -n $(DATA_NS) create configmap mariadb-init-sql \
		--from-file=init.sql=storage/mariadb-init.sql \
		--dry-run=client -o yaml | kubectl apply -f -

## data: bring up MariaDB and Redis
data: secrets sql
	@kubectl apply -f $(MANIFESTS)/05-mariadb.yaml -f $(MANIFESTS)/06-redis.yaml
	@kubectl -n $(DATA_NS) rollout status statefulset/mariadb --timeout=180s
	@kubectl -n $(DATA_NS) rollout status deployment/redis --timeout=120s
	@# The schema Job runs separately from the database, so waiting on the
	@# StatefulSet alone would let a service start against empty tables.
	@kubectl -n $(DATA_NS) wait --for=condition=complete job/mariadb-init --timeout=180s

# One image per service out of apps/services, selected by the SVC build arg.
GO_SVCS := auth-svc order-svc payment-svc mock-payment

## images: build every image and load it into the cluster
images:
	@# pullPolicy is Never, so a chart pinned to an unbuilt tag does not fail at
	@# deploy time — the pod just sits in ErrImageNeverPull.
	@./platform/scripts/check-image-tags.sh $(VERSION)
	@docker build --build-arg VERSION=$(VERSION) -t dummy:$(VERSION) apps/dummy
	@for s in $(GO_SVCS); do \
		docker build --build-arg SVC=$$s --build-arg VERSION=$(VERSION) \
			-t $$s:$(VERSION) apps/services; \
	done
	@docker build -t web-ui:$(VERSION) apps/web-ui
	@# kind nodes have their own image store and no registry, so an image built on
	@# the host is invisible until loaded in.
	@kind load docker-image --name $(CLUSTER) \
		dummy:$(VERSION) web-ui:$(VERSION) \
		$(foreach s,$(GO_SVCS),$(s):$(VERSION))

# One release per service, so one can be rolled back without its siblings.
# platform-config first: a pod that mounts a ConfigMap which does not exist yet
# stays Pending rather than failing loudly.
APP_CHARTS := platform-config auth-svc order-svc payment-svc mock-payment web-ui dummy

# platform-config renders only a ConfigMap, so there is nothing to wait on.
APP_DEPLOYS := auth-svc order-svc payment-svc mock-payment web-ui dummy

## apps: deploy the application services from their charts
apps: data app-secrets
	@for c in $(APP_CHARTS); do \
		helm dependency update charts/apps/$$c >/dev/null; \
		helm upgrade --install $$c charts/apps/$$c -n demo; \
	done
	@for d in $(APP_DEPLOYS); do \
		kubectl -n demo rollout status deployment/$$d --timeout=180s; \
	done

# Upstream charts. We own the values files and nothing else.
#
# prometheus-community/prometheus rather than kube-prometheus-stack: the stack
# pulls Grafana in as a dependency, which collides with the grafana release that
# owns our datasources, and brings an Operator, CRDs, Alertmanager and exporters
# that nothing here reads. Our Prometheus receives remote-write and scrapes
# nothing.
PROMETHEUS_VERSION := 29.23.0
LOKI_VERSION       := 7.2.0
TEMPO_VERSION      := 1.24.4
ALLOY_VERSION      := 1.11.1
GRAFANA_VERSION    := 10.5.15

OBS_VALUES := platform/addons/observability/values

## observability: prometheus, loki, tempo, alloy and grafana
observability: namespaces
	@kubectl apply -f $(MANIFESTS)/07-observability.yaml
	@helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo add grafana https://grafana.github.io/helm-charts >/dev/null 2>&1 || true
	@helm repo update prometheus-community grafana >/dev/null
	@# The dashboard is a file rather than a values blob so it stays valid JSON
	@# that Grafana can export back into.
	@kubectl -n observability create configmap grafana-dashboards \
		--from-file=platform.json=platform/addons/observability/dashboard.json \
		--dry-run=client -o yaml | kubectl apply -f -
	@helm upgrade --install prometheus prometheus-community/prometheus \
		-n observability --version $(PROMETHEUS_VERSION) -f $(OBS_VALUES)/prometheus.yaml
	@helm upgrade --install loki grafana/loki \
		-n observability --version $(LOKI_VERSION) -f $(OBS_VALUES)/loki.yaml
	@helm upgrade --install tempo grafana/tempo \
		-n observability --version $(TEMPO_VERSION) -f $(OBS_VALUES)/tempo.yaml
	@helm upgrade --install alloy grafana/alloy \
		-n observability --version $(ALLOY_VERSION) -f $(OBS_VALUES)/alloy.yaml
	@helm upgrade --install grafana grafana/grafana \
		-n observability --version $(GRAFANA_VERSION) -f $(OBS_VALUES)/grafana.yaml
	@kubectl -n observability rollout status deployment/alloy --timeout=180s
	@kubectl -n observability rollout status deployment/grafana --timeout=180s
	@kubectl -n observability rollout status statefulset/loki --timeout=180s
	@kubectl apply -f $(MANIFESTS)/08-grafana-route.yaml

ROLLOUTS_VERSION     := 2.41.1
# The controller image tag, which is what a running cluster reports. The chart
# version is not recoverable from the Deployment.
ROLLOUTS_APP_VERSION := v1.9.1

## rollouts: install the Argo Rollouts controller
rollouts: namespaces
	@helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
	@helm repo update argo >/dev/null
	@helm upgrade --install argo-rollouts argo/argo-rollouts \
		-n argo-rollouts --create-namespace --version $(ROLLOUTS_VERSION)
	@kubectl -n argo-rollouts rollout status deployment/argo-rollouts --timeout=180s
	@# Helm does not upgrade CRDs that already exist, so a cluster carrying an
	@# older bundle drops canary and analysis fields at admission and the Rollout
	@# still looks correct in git.
	@./platform/scripts/check-version.sh argo-rollouts $(ROLLOUTS_APP_VERSION) \
		"$$(kubectl -n argo-rollouts get deploy argo-rollouts \
			-o jsonpath='{.spec.template.spec.containers[0].image}' | sed 's/.*://')"

## up: cluster, gateway, images, observability, rollouts, apps
up: cluster gateway images
	@# Observability first only so the first requests are captured. The services
	@# do not depend on it: the OTel SDK logs an export failure and carries on,
	@# so a missing collector costs telemetry, not availability.
	@$(MAKE) --no-print-directory observability
	@# Before apps, not after: once a service is a Rollout, its chart fails to
	@# render against a cluster where the CRD is not registered yet.
	@$(MAKE) --no-print-directory rollouts
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
	@echo "── through every service ─────────────────────────"
	@# 401 without a token is the correct answer, and it proves the request
	@# reached auth-svc rather than stopping at the gateway.
	@curl -sS -o /dev/null -w '  /api/products       -> HTTP %{http_code}  (401 = auth is enforced)\n' --max-time 10 https://localhost/api/products
	@curl -sS -o /dev/null -w '  /dummy              -> HTTP %{http_code}\n' --max-time 10 https://localhost/dummy
	@echo "   run 'make test' to buy something end to end"

## test: run every test suite
test:
	@./tests/run-all.sh

## test-routing: each path reaches the service that owns it
test-routing:
	@./tests/routing.sh

## test-auth: credentials are checked and the token opens the api
test-auth:
	@./tests/auth.sh

## test-checkout: a customer buys a shirt, end to end
test-checkout:
	@./tests/checkout.sh

## test-resilience: the data and the service survive losing a pod
test-resilience:
	@./tests/resilience.sh

## test-o11y: the observability stack is up and receiving
test-o11y:
	@./tests/o11y-stack.sh

## test-journey: follow one purchase from log to trace to metric
test-journey:
	@./tests/o11y-journey.sh

## test-tls: capture the traffic and prove https encrypts it
test-tls:
	@./tests/tls-proof.sh $(CLUSTER)

## down: delete the cluster
down:
	@kind delete cluster --name $(CLUSTER)

.PHONY: help cluster namespaces gateway-api istio tls gateway secrets app-secrets \
        sql data images observability rollouts apps up verify down \
        test test-routing test-auth test-checkout test-resilience test-tls \
        test-o11y test-journey
